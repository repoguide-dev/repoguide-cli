package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

var authHTTPClient = &http.Client{Timeout: time.Minute}

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
	baseURL, err := validatedBackendURL()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := postJSON(baseURL+"/api/auth/login", bytes.NewReader(body))
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
	if updated, changed := refreshedAuthToken(baseURL, clientauth.Token{Token: r.Token, Email: email}); changed {
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
	return runDeviceLogin(mode)
}

// deviceLogin runs the device-code auth flow and saves credentials, without launching any TUI after.
func deviceLogin(mode string) error {
	baseURL, err := validatedBackendURL()
	if err != nil {
		return err
	}

	resp, err := postJSON(baseURL+"/api/auth/device/start?mode="+mode, bytes.NewBufferString("{}"))
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

var runDeviceLogin = deviceLogin

func ensureActiveLogin() error {
	baseURL, err := validatedBackendURL()
	if err != nil {
		return err
	}

	token, ok := clientauth.Load()
	if !ok || strings.TrimSpace(token.Token) == "" {
		fmt.Println("No active session. Starting login...")
		return runDeviceLogin("login")
	}

	client := sessionimport.CloudClient{BaseURL: baseURL, Token: token.Token}
	if _, err := client.GetMe(); err != nil {
		if !loginRequiredError(err) {
			return err
		}
		clientauth.Delete()
		fmt.Println("Saved session is invalid. Starting login...")
		return runDeviceLogin("login")
	}

	if updated, changed := refreshedAuthToken(baseURL, token); changed {
		_ = clientauth.Save(updated)
	}
	return nil
}

func loginRequiredError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid token") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "(401") ||
		strings.Contains(msg, " 401")
}

type tokenResp struct {
	Token string `json:"token"`
	Email string `json:"email"`
	Error string `json:"error"`
}

func pollDeviceToken(baseURL, deviceCode string) (tokenResp, bool, error) {
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	resp, err := postJSON(baseURL+"/api/auth/device/token", bytes.NewReader(body))
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
		// empty title arg: cmd.exe's "start" treats the first quoted arg as the
		// window title, so without it a quoted URL could be misread.
		cmd, args = "cmd", []string{"/c", "start", "", url}
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

func postJSON(url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return authHTTPClient.Do(req)
}

func validatedBackendURL() (string, error) {
	raw := getBackendURL()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid backend URL %q", raw)
	}
	if u.Scheme == "https" {
		return raw, nil
	}
	if u.Scheme == "http" && isLoopbackBackendHost(u.Hostname()) {
		return raw, nil
	}
	return "", fmt.Errorf("backend URL must use https, except loopback http for local development")
}

func isLoopbackBackendHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
