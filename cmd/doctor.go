package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/config"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/sqlitestore"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(doctorCmd)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check local AI coding agent data",
	Run:   runDoctor,
}

var (
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	stylePartial = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	styleMissing = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func runDoctor(_ *cobra.Command, _ []string) {
	_, hasToken := clientauth.Load()
	printLocalSetup(repopkg.DetectLocalSetup(), hasToken)

	if !hasToken {
		printLocalModeSection()
	}

	agents := RunWithSpinner("Scanning agents...", internal.DetectWithProgress)

	fmt.Println()
	for _, a := range agents {
		printAgent(a)
	}
}

func printLocalModeSection() {
	fmt.Printf("%s %s\n", styleOK.Render("i"), styleOK.Render("local mode"))

	// API key status
	apiKeyStatus := ""
	apiKey := ""
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		apiKeyStatus = "set (env)"
		apiKey = v
	} else if v := config.AnthropicAPIKey(); v != "" {
		apiKeyStatus = "set (config)"
		apiKey = v
	} else {
		apiKeyStatus = "missing"
	}
	fmt.Printf("  %s %s\n", styleDim.Render("ANTHROPIC_API_KEY:"), apiKeyStatus)

	// Validate the key if set
	if apiKey != "" {
		fmt.Printf("  %s", styleDim.Render("API key: "))
		valid, errMsg := validateAnthropicKey(apiKey)
		if valid {
			fmt.Printf("valid (calling Anthropic API)\n")
		} else {
			fmt.Printf("invalid (%s)\n", errMsg)
		}
	}

	// SQLite DB info
	dbPath := internal.RepoGuideDir() + "/repoguide.db"
	if info, err := os.Stat(dbPath); err == nil {
		fmt.Printf("  %s %s (%s)\n", styleDim.Render("SQLite DB:"), renderPath(dbPath), internal.FormatBytes(info.Size()))
	} else {
		fmt.Printf("  %s %s (not created yet)\n", styleDim.Render("SQLite DB:"), renderPath(dbPath))
	}

	// Repos tracked
	repos, _ := repopkg.ListConfiguredRepos()
	fmt.Printf("  %s %d\n", styleDim.Render("Repos tracked:"), len(repos))
	for _, r := range repos {
		name := r.RepoRoot
		if idx := strings.LastIndex(r.RepoRoot, "/"); idx >= 0 {
			name = r.RepoRoot[idx+1:]
		}
		fmt.Printf("    %s  %s\n", name, styleDim.Render(renderPath(r.RepoRoot)))
	}

	// Token usage + agent info from SQLite
	if st, err := sqlitestore.Open(dbPath); err == nil {
		defer st.Close()
		if ls, err := st.LocalStats(context.Background()); err == nil {
			if len(ls.AgentVersions) > 0 {
				fmt.Printf("  %s %s\n", styleDim.Render("Agent(s):"), strings.Join(ls.AgentVersions, ", "))
			}
			if ls.SessionCount > 0 {
				fmt.Printf("  %s %d sessions  in %s  out %s\n",
					styleDim.Render("Tokens:"),
					ls.SessionCount,
					formatTokens(ls.InputTokens),
					formatTokens(ls.OutputTokens),
				)
			} else {
				fmt.Printf("  %s\n", styleDim.Render("Tokens: no sessions tracked yet"))
			}
		}
	}

	fmt.Println()
}

func formatTokens(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func validateAnthropicKey(key string) (bool, string) {
	body, _ := json.Marshal(map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 529 {
		return true, ""
	}
	return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

func printLocalSetup(status repopkg.LocalSetupStatus, hasToken bool) {
	token, _ := clientauth.Load()

	modeLabel := "Local Mode [Pro Plan]"
	if !status.IsLocalMode {
		modeLabel = "Online Mode [Pro Plan]"
	}

	if !status.InGitRepo {
		fmt.Printf("%s %s\n", stylePartial.Render("?"), stylePartial.Render(modeLabel))
		fmt.Printf("  %s\n", localStorageLine(status))
		fmt.Printf("  %s\n\n", styleDim.Render("Run doctor inside a Git repository to verify init status"))
		return
	}

	if status.Initialized {
		fmt.Printf("%s %s\n", styleOK.Render("✓"), styleOK.Render(modeLabel))
		if !status.IsLocalMode && hasToken && token.Email != "" {
			fmt.Printf("  %s\n", styleDim.Render("Logged in as: "+token.Email))
		}
		if status.IsLocalMode {
			fmt.Printf("  %s\n", styleDim.Render("No communication with RepoGuide servers - only calls to Claude"))
		}
		fmt.Printf("  %s\n", styleDim.Render("Repo initialized"))
		fmt.Printf("  %s\n", styleDim.Render("Repo ID: "+status.RepoID))
		fmt.Printf("  %s\n", styleDim.Render("Store: ")+renderPath(status.StoreDir))
		fmt.Printf("  %s\n\n", localStorageLine(status))
		return
	}

	fmt.Printf("%s %s\n", stylePartial.Render("?"), stylePartial.Render(modeLabel))
	fmt.Printf("  %s\n", styleDim.Render("Git repo detected"))
	fmt.Printf("  %s\n", localStorageLine(status))
	fmt.Printf("  %s\n\n", styleDim.Render("Run `repoguide repo init` to complete setup"))
}

func localStorageLine(status repopkg.LocalSetupStatus) string {
	if status.LocalStorageError != nil {
		return styleDim.Render("Local storage: ") + renderPath(status.RepoGuideDir) + styleDim.Render(" (unavailable)")
	}
	if !status.LocalStorageFound {
		return styleDim.Render("Local storage: ") + renderPath(status.RepoGuideDir) + styleDim.Render(" (not created yet)")
	}
	return styleDim.Render("Local storage: ") + renderPath(status.RepoGuideDir) + styleDim.Render(" ("+internal.FormatBytes(status.LocalStorageBytes)+")")
}

func printAgent(a internal.Agent) {
	var icon string
	var s lipgloss.Style
	switch a.Status {
	case internal.OK:
		icon, s = "✓", styleOK
	case internal.Partial:
		icon, s = "?", stylePartial
	default:
		icon, s = "✗", styleMissing
	}
	fmt.Printf("%s %s\n", s.Render(icon), s.Render(a.Name))
	if a.Status == internal.Missing {
		fmt.Println()
		return
	}
	for _, d := range a.Details {
		fmt.Printf("  %s\n", renderAgentDetail(d))
	}
	fmt.Println()
}

func renderAgentDetail(detail string) string {
	const pathPrefix = "Path: "
	if value, ok := strings.CutPrefix(detail, pathPrefix); ok {
		return styleDim.Render(pathPrefix) + renderPathText(value)
	}
	return styleDim.Render(detail)
}
