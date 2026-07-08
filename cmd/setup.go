package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	setupCmd.Flags().Bool("offline", false, "Initialize in offline mode (no backend sync)")
	setupCmd.Flags().Bool("no-hooks", false, "Skip installing Claude Code and Codex hooks (still installs the plugin/MCP registration; for benchmark/test environments)")
	setupCmd.Flags().Bool("ci", false, "")
	_ = setupCmd.Flags().MarkHidden("ci")
	root.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Choose a repo, initialize RepoGuide, and install the MCP",
	RunE:  runSetup,
}

func runSetup(cmd *cobra.Command, _ []string) error {
	ciMode, _ := cmd.Flags().GetBool("ci")
	if ciMode {
		return runCILogin()
	}

	offlineMode, _ := cmd.Flags().GetBool("offline")

	if !offlineMode {
		if err := ensureActiveLogin(); err != nil {
			return err
		}
	}

	var discoverErr error
	repos := RunWithSpinner("Discovering Git repositories", func(_ func(current, total int, label string)) []string {
		repos, err := sessionimport.DiscoverGitRepoRoots()
		discoverErr = err
		return repos
	})
	if discoverErr != nil {
		return discoverErr
	}
	if len(repos) == 0 {
		fmt.Println("No Git repositories found.")
		return nil
	}

	repoRoot, err := selectSetupRepo(repos)
	if err != nil {
		return err
	}
	if repoRoot == "" {
		return nil
	}

	previousDir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(repoRoot); err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(previousDir)
	}()

	noHooks, _ := cmd.Flags().GetBool("no-hooks")

	_ = initCmd.Flags().Set("offline", fmt.Sprintf("%t", offlineMode))
	_ = initCmd.Flags().Set("force", "false")
	_ = initCmd.Flags().Set("approve", "false")
	if err := runInitWithoutNext(initCmd); err != nil {
		return err
	}
	if !repopkg.IsRepoInitialized(repoRoot) {
		return nil
	}

	_ = mcpInstallCmd.Flags().Set("no-hooks", fmt.Sprintf("%t", noHooks))
	return runMCPInstall(mcpInstallCmd, nil)
}

func runCILogin() error {
	ciToken := os.Getenv("REPOGUIDE_CI_TOKEN")
	if ciToken == "" {
		return fmt.Errorf("REPOGUIDE_CI_TOKEN is not set")
	}
	baseURL, err := validatedBackendURL()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/auth/ci/exchange", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ciToken)
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach backend at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("CI exchange failed (HTTP %d)", resp.StatusCode)
	}
	var r struct {
		Token string `json:"token"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	if err := clientauth.Save(clientauth.Token{Token: r.Token, Email: r.Email}); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}
	fmt.Printf("Logged in as %s\n", r.Email)
	return nil
}

func runInitWithoutNext(cmd *cobra.Command) error {
	previous := skipInitNextPrompt
	skipInitNextPrompt = true
	defer func() {
		skipInitNextPrompt = previous
	}()
	return runInit(cmd, nil)
}

func selectSetupRepo(repos []string) (string, error) {
	if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
		for _, repo := range repos {
			fmt.Println(repo)
		}
		return "", nil
	}

	currentRepo, _ := internal.CurrentRepoRoot()
	final, err := tea.NewProgram(newSetupRepoModel(repos, currentRepo), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	m := final.(setupRepoModel)
	if m.quit || m.selected < 0 || m.selected >= len(m.repos) {
		return "", nil
	}
	return m.repos[m.selected], nil
}

type setupRepoModel struct {
	repos    []string
	cursor   int
	offset   int
	height   int
	selected int
	quit     bool
}

func newSetupRepoModel(repos []string, currentRepo string) setupRepoModel {
	m := setupRepoModel{repos: repos, selected: -1}
	currentRepo = normalizeSetupRepoPath(currentRepo)
	for i, repo := range repos {
		if normalizeSetupRepoPath(repo) == currentRepo {
			m.cursor = i
			break
		}
	}
	m.ensureCursorVisible()
	return m
}

func (m setupRepoModel) Init() tea.Cmd { return nil }

func (m setupRepoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.ensureCursorVisible()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.repos)-1 {
				m.cursor++
			}
		case "pgup", "b":
			m.cursor = max(0, m.cursor-m.visibleRows())
		case "pgdown", "f":
			m.cursor = setupMin(len(m.repos)-1, m.cursor+m.visibleRows())
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.repos) - 1
		case "enter":
			m.selected = m.cursor
			return m, tea.Quit
		}
		m.ensureCursorVisible()
	}
	return m, nil
}

func (m setupRepoModel) View() string {
	lines := []string{titleStyle.Render("RepoGuide setup"), "", headStyle.Render("Choose a repository:"), ""}
	visible := m.visibleRows()
	start := m.offset
	end := setupMin(len(m.repos), start+visible)
	for i := start; i < end; i++ {
		repo := m.repos[i]
		line := fmt.Sprintf("%s  %s", filepath.Base(repo), muted.Render(shortSetupRepoPath(repo)))
		if repopkg.IsRepoInitialized(repo) {
			line += muted.Render("  initialized")
		}
		if i == m.cursor {
			lines = append(lines, selected.Render("› "+line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	if len(m.repos) > visible {
		lines = append(lines, "", muted.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.repos))))
	}
	lines = append(lines, "", muted.Render("↑↓ navigate  •  pgup/pgdn jump  •  enter select  •  q quit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

func (m setupRepoModel) visibleRows() int {
	if m.height <= 0 {
		return len(m.repos)
	}
	return max(3, m.height-8)
}

func (m *setupRepoModel) ensureCursorVisible() {
	if len(m.repos) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.repos) {
		m.cursor = len(m.repos) - 1
	}
	visible := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	maxOffset := max(0, len(m.repos)-visible)
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func normalizeSetupRepoPath(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return path
}

func shortSetupRepoPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

func setupMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
