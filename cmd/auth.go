package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	loginCmd.Flags().Bool("ci", false, "")
	_ = loginCmd.Flags().MarkHidden("ci")
	loginCmd.Flags().String("email", "", "Email for password login (scripting; skips device flow)")
	loginCmd.Flags().String("password", "", "Password for password login (scripting; skips device flow)")
	root.AddCommand(loginCmd)
	root.AddCommand(logoutCmd)
	root.AddCommand(registerCmd)
	root.AddCommand(authCmd)
	authCmd.AddCommand(authStatusCmd)
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE:  runAuthStatus,
}

func runAuthStatus(_ *cobra.Command, _ []string) error {
	token, ok := clientauth.Load()
	if !ok {
		return fmt.Errorf("not logged in; run: repoguide setup --ci")
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
	me, err := client.GetMe()
	if err != nil {
		fmt.Printf("authenticated as %s (offline)\n", token.Email)
		return nil
	}
	plan := me.Plan
	if plan == "" {
		plan = "FREE"
	}
	fmt.Printf("authenticated as %s (plan: %s)\n", me.Email, plan)
	return nil
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to RepoGuide",
	RunE: func(cmd *cobra.Command, _ []string) error {
		email, _ := cmd.Flags().GetString("email")
		password, _ := cmd.Flags().GetString("password")
		if email != "" || password != "" {
			return passwordLogin(email, password)
		}
		if ci, _ := cmd.Flags().GetBool("ci"); ci {
			return runCILogin()
		}
		return deviceFlow("login")
	},
}

// passwordLogin authenticates against POST /api/auth/login and saves the token, without any TUI.
func passwordLogin(email, password string) error {
	if email == "" || password == "" {
		return fmt.Errorf("--email and --password are both required")
	}
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(getBackendURL()+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot reach backend: %w", err)
	}
	defer resp.Body.Close()

	var r tokenResp
	json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode != 200 {
		if r.Error == "" {
			r.Error = "login failed"
		}
		return fmt.Errorf("%s", r.Error)
	}
	if err := clientauth.Save(clientauth.Token{Token: r.Token, Email: email}); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}
	if updated, changed := refreshedAuthToken(getBackendURL(), clientauth.Token{Token: r.Token, Email: email}); changed {
		_ = clientauth.Save(updated)
	}
	fmt.Printf("Logged in as %s\n", email)
	return nil
}

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Create a RepoGuide account",
	RunE:  func(_ *cobra.Command, _ []string) error { return deviceFlow("register") },
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Sign out of RepoGuide",
	Run: func(_ *cobra.Command, _ []string) {
		clientauth.Delete()
		fmt.Println("Logged out.")
	},
}

func deviceFlow(mode string) error {
	if err := deviceLogin(mode); err != nil {
		return err
	}
	model := newSessionsModel("", sessionsModelOptions{repoFilter: detectCwdGitRoot()})
	_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

// deviceLogin runs the device-code auth flow and saves credentials, without launching any TUI after.
func deviceLogin(mode string) error {
	baseURL := getBackendURL()

	resp, err := http.Post(baseURL+"/api/auth/device/start?mode="+mode, "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		return fmt.Errorf("cannot reach backend at %s: %w\n\nSet --backend-url or REPOGUIDE_BACKEND_URL", baseURL, err)
	}
	defer resp.Body.Close()

	var device struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil {
		return fmt.Errorf("invalid backend response: %w", err)
	}

	fmt.Printf("\nOpen this URL in your browser:\n\n  %s\n\n", device.VerificationURIComplete)
	fmt.Printf("Your code: %s\n\n", device.UserCode)
	openBrowser(device.VerificationURIComplete)
	fmt.Print("Waiting for authentication")

	interval := device.Interval
	if interval <= 0 {
		interval = 5
	}

	for {
		time.Sleep(time.Duration(interval) * time.Second)
		fmt.Print(".")

		t, done, err := pollDeviceToken(baseURL, device.DeviceCode)
		if err != nil {
			continue // authorization_pending - keep polling
		}
		if done {
			fmt.Println()
			saved := clientauth.Token{Token: t.Token, Email: t.Email}
			if updated, changed := refreshedAuthToken(baseURL, saved); changed {
				saved = updated
			}
			if err := clientauth.Save(saved); err != nil {
				return fmt.Errorf("failed to save credentials: %w", err)
			}
			fmt.Printf("Logged in as %s\n\n", t.Email)
			return nil
		}
	}
}

type tokenResp struct {
	Token string `json:"token"`
	Email string `json:"email"`
	Error string `json:"error"`
}

func pollDeviceToken(baseURL, deviceCode string) (tokenResp, bool, error) {
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	resp, err := http.Post(baseURL+"/api/auth/device/token", "application/json", bytes.NewReader(body))
	if err != nil {
		return tokenResp{}, false, err
	}
	defer resp.Body.Close()
	var r tokenResp
	json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode == 200 {
		return r, true, nil
	}
	return r, false, fmt.Errorf("%s", r.Error)
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "linux":
		cmd, args = "xdg-open", []string{url}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", url}
	default:
		return
	}
	exec.Command(cmd, args...).Start()
}

func getBackendURL() string {
	if u, _ := root.PersistentFlags().GetString("backend-url"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8082"
}
