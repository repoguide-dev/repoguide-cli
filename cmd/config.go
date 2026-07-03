package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/config"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	repoConfigCmd.Flags().BoolP("delete", "d", false, "Remove RepoGuide tracking for this repo")
	repoConfigCmd.Flags().Bool("enable-hooks", false, "Enable managed commit hooks for this repo")
	repoConfigCmd.Flags().Bool("disable-hooks", false, "Disable managed commit hooks for this repo")
	repoCmd.AddCommand(repoConfigCmd)
}

var repoConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "View or change AI backend settings for local mode",
	RunE:  runRepoConfig,
}

func runRepoConfig(cmd *cobra.Command, _ []string) error {
	del, _ := cmd.Flags().GetBool("delete")
	enableHooks, _ := cmd.Flags().GetBool("enable-hooks")
	disableHooks, _ := cmd.Flags().GetBool("disable-hooks")
	if del {
		return runRemove(cmd, nil)
	}
	if enableHooks && disableHooks {
		return fmt.Errorf("choose only one of --enable-hooks or --disable-hooks")
	}

	status := repopkg.DetectLocalSetup()
	if (enableHooks || disableHooks) && (!status.InGitRepo || !status.Initialized) {
		return fmt.Errorf("no initialized repoguide repo in the current directory")
	}
	if enableHooks || disableHooks {
		hooks, err := internal.SetManagedCommitHooks(status.StoreDir, status.RepoRoot, enableHooks)
		if err != nil {
			return err
		}
		freshStatus := repopkg.DetectLocalSetup()
		status.Hooks = hooks
		status.HooksPath = freshStatus.HooksPath
		status.HooksPathCustom = freshStatus.HooksPathCustom
		status.CommitHooksEnabled = enableHooks
		printCommitHookStatus(status)
		return nil
	}
	nonInteractive := !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd())

	if nonInteractive {
		if status.IsLocalMode {
			printConfigStatus(status)
		} else {
			printOnlineModeStatus(status)
		}
		if status.Initialized {
			printCommitHookStatus(status)
		}
		if status.Initialized {
			printHintFilesStatus(status)
		}
		return nil
	}

	// Interactive: AI backend (local mode only)
	if status.IsLocalMode {
		m := newConfigModel(status)
		final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		if err != nil {
			return err
		}
		result := final.(configModel)
		if result.quit || !result.applied {
			fmt.Println("No changes made.")
		} else if err := applyConfigModel(status, result); err != nil {
			return err
		}
	} else {
		printOnlineModeStatus(status)
	}
	printCommitHookStatus(status)

	// Interactive: hook toggle
	if status.Initialized {
		anyInstalled := false
		for _, h := range status.Hooks {
			if h.Installed {
				anyInstalled = true
				break
			}
		}
		if anyInstalled {
			if status.CommitHooksEnabled {
				fmt.Print("  Disable commit hooks? [y/N]: ")
			} else {
				fmt.Print("  Enable commit hooks? [y/N]: ")
			}
			var answer string
			fmt.Scanln(&answer)
			if strings.ToLower(strings.TrimSpace(answer)) == "y" {
				if _, err := internal.SetManagedCommitHooks(status.StoreDir, status.RepoRoot, !status.CommitHooksEnabled); err != nil {
					fmt.Printf("  Error: %v\n", err)
				} else {
					action := "enabled"
					if status.CommitHooksEnabled {
						action = "disabled"
					}
					fmt.Printf("  Commit hooks %s.\n", action)
				}
			}
			fmt.Println()
		}
	}

	// Interactive: hint files (any mode)
	if !status.Initialized {
		return nil
	}
	return runHintSelect(status)
}

func printHintFilesStatus(status repopkg.LocalSetupStatus) {
	cfg, err := internal.LoadRepoConfigFile(status.StoreDir)
	if err != nil {
		return
	}
	fmt.Println(renderSectionTitle("Hint files"))
	if len(cfg.HintFiles) == 0 {
		fmt.Println("  None configured - using telemetry only.")
	} else {
		for _, f := range cfg.HintFiles {
			fmt.Printf("  %s\n", f)
		}
	}
}

func runHintSelect(status repopkg.LocalSetupStatus) error {
	cfg, err := internal.LoadRepoConfigFile(status.StoreDir)
	if err != nil {
		return err
	}
	found := internal.ScanHintFiles(status.RepoRoot)
	if len(found) == 0 {
		fmt.Println("No known guidance files found in this repo.")
		fmt.Println("RepoGuide looks for: AGENTS.md, CLAUDE.md, README.md, STRUCTURE.md, CONTRIBUTING.md")
		return nil
	}
	m := newHintSelectModel(found, cfg.HintFiles)
	prog := tea.NewProgram(m)
	result, err := prog.Run()
	if err != nil {
		return err
	}
	final := result.(hintSelectModel)
	if final.cancelled {
		return nil
	}
	cfg.HintFiles = final.selected()
	if err := internal.SaveRepoConfigFile(status.StoreDir, cfg); err != nil {
		return err
	}
	if len(cfg.HintFiles) == 0 {
		fmt.Println("Hint files cleared - using telemetry only.")
	} else {
		fmt.Printf("Saved: %s\n", strings.Join(cfg.HintFiles, ", "))
	}
	return nil
}

// ── hint file selector ───────────────────────────────────────────────────────

type hintSelectModel struct {
	files     []string
	checks    []bool
	cursor    int
	cancelled bool
}

func newHintSelectModel(found, current []string) hintSelectModel {
	currentSet := make(map[string]bool, len(current))
	for _, f := range current {
		currentSet[f] = true
	}
	checks := make([]bool, len(found))
	for i, f := range found {
		checks[i] = currentSet[f]
	}
	return hintSelectModel{files: found, checks: checks}
}

func (m hintSelectModel) selected() []string {
	var out []string
	for i, f := range m.files {
		if m.checks[i] {
			out = append(out, f)
		}
	}
	return out
}

func (m hintSelectModel) Init() tea.Cmd { return nil }

func (m hintSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.files)-1 {
				m.cursor++
			}
		case " ":
			m.checks[m.cursor] = !m.checks[m.cursor]
		case "enter":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m hintSelectModel) View() string {
	title := titleStyle.Render("Hint files")
	lines := []string{title, ""}
	checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	for i, f := range m.files {
		cursor := "  "
		mark := "[ ]"
		style := lipgloss.NewStyle()
		if m.checks[i] {
			mark = checkStyle.Render("[✓]")
		}
		if i == m.cursor {
			cursor = "› "
			style = selected
		}
		lines = append(lines, style.Render(cursor+mark+" "+f))
	}
	lines = append(lines, "", muted.Render(footerHint("space toggle", "enter save", "esc cancel")))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

// ── config model ─────────────────────────────────────────────────────────────

type configBackend int

const (
	backendCLI configBackend = iota
	backendAPIKey
)

type configModel struct {
	backend  configBackend
	keyInput textinput.Model
	cursor   int // 0 = CLI row, 1 = API key row
	applied  bool
	quit     bool
	err      string
}

func newConfigModel(status repopkg.LocalSetupStatus) configModel {
	ti := textinput.New()
	ti.Placeholder = "sk-ant-..."
	ti.EchoMode = textinput.EchoPassword
	ti.CharLimit = 256

	m := configModel{
		backend:  backendCLI,
		keyInput: ti,
	}
	if cfg, err := internal.LoadRepoConfigFile(status.StoreDir); err == nil {
		switch cfg.LocalAIBackend {
		case internal.LocalAIBackendAPI:
			m.backend = backendAPIKey
			m.cursor = 1
		case internal.LocalAIBackendClaudeCLI:
			m.backend = backendCLI
			m.cursor = 0
		}
	}
	if m.backend == backendCLI && m.cursor == 0 {
		if key := config.AnthropicAPIKey(); key != "" && !config.UseClaudeCLI() {
			m.backend = backendAPIKey
			m.cursor = 1
		}
	}
	if key := config.AnthropicAPIKey(); key != "" {
		ti.SetValue(key)
		m.keyInput = ti
	}
	if m.backend == backendCLI && config.UseClaudeCLI() {
		m.backend = backendCLI
		m.cursor = 0
	}
	return m
}

func (m configModel) Init() tea.Cmd {
	return nil
}

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quit = true
			return m, tea.Quit
		case "enter":
			if m.backend == backendAPIKey && m.keyInput.Value() == "" {
				m.err = "API key required"
				return m, nil
			}
			m.applied = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.backend = configBackend(m.cursor)
				if m.backend == backendCLI {
					m.keyInput.Blur()
				} else {
					return m, m.keyInput.Focus()
				}
			}
		case "down", "j":
			if m.cursor < 1 {
				m.cursor++
				m.backend = configBackend(m.cursor)
				if m.backend == backendAPIKey {
					return m, m.keyInput.Focus()
				}
				m.keyInput.Blur()
			}
		case "tab":
			m.cursor = 1 - m.cursor
			m.backend = configBackend(m.cursor)
			if m.backend == backendAPIKey {
				return m, m.keyInput.Focus()
			}
			m.keyInput.Blur()
		}
	}

	if m.backend == backendAPIKey {
		var cmd tea.Cmd
		m.keyInput, cmd = m.keyInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

var (
	configSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	configMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	configError    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func (m configModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("  Local mode AI backend") + "\n\n")

	// Row 0: claude CLI
	cli := "  ○  claude CLI"
	if m.cursor == 0 {
		cli = configSelected.Render("  ›  claude CLI")
	}
	b.WriteString(cli + "\n")
	b.WriteString(configMuted.Render("     Use the `claude` executable - no API key needed") + "\n\n")

	// Row 1: API key
	apiRow := "  ○  Anthropic API key"
	if m.cursor == 1 {
		apiRow = configSelected.Render("  ›  Anthropic API key")
	}
	b.WriteString(apiRow + "\n")
	if m.backend == backendAPIKey {
		b.WriteString("     " + m.keyInput.View() + "\n")
	} else {
		b.WriteString(configMuted.Render("     sk-ant-...") + "\n")
	}
	b.WriteString("\n")

	if m.err != "" {
		b.WriteString(configError.Render("  "+m.err) + "\n\n")
	}

	b.WriteString(configMuted.Render("  ↑↓ / tab select  •  enter apply  •  q quit") + "\n")
	return b.String()
}

// ── apply ─────────────────────────────────────────────────────────────────────

func applyConfigModel(status repopkg.LocalSetupStatus, m configModel) error {
	cfg, err := internal.LoadRepoConfigFile(status.StoreDir)
	if err != nil {
		return fmt.Errorf("load repo config: %w", err)
	}
	switch m.backend {
	case backendCLI:
		cfg.LocalAIBackend = internal.LocalAIBackendClaudeCLI
		if err := internal.SaveRepoConfigFile(status.StoreDir, cfg); err != nil {
			return fmt.Errorf("save repo config: %w", err)
		}
		fmt.Println("Switched to claude CLI backend.")
	case backendAPIKey:
		key := m.keyInput.Value()
		if err := config.SetAnthropicAPIKey(key); err != nil {
			return fmt.Errorf("save API key: %w", err)
		}
		cfg.LocalAIBackend = internal.LocalAIBackendAPI
		if err := internal.SaveRepoConfigFile(status.StoreDir, cfg); err != nil {
			return fmt.Errorf("save repo config: %w", err)
		}
		_ = os.Setenv("ANTHROPIC_API_KEY", key)
		fmt.Println("API key saved. Local mode will use the Anthropic API.")
	}
	return nil
}

// ── non-interactive fallback ──────────────────────────────────────────────────

func printConfigStatus(status repopkg.LocalSetupStatus) {
	apiKey := config.AnthropicAPIKey()
	repoCfg, _ := internal.LoadRepoConfigFile(status.StoreDir)
	fmt.Println(renderSectionTitle("Local mode AI backend"))
	switch {
	case repoCfg.LocalAIBackend == internal.LocalAIBackendAPI:
		if apiKey != "" {
			fmt.Printf("  Backend:  Anthropic API key (%s)\n", maskKey(apiKey))
		} else {
			fmt.Println("  Backend:  Anthropic API key (missing)")
		}
	case repoCfg.LocalAIBackend == internal.LocalAIBackendClaudeCLI:
		fmt.Println("  Backend:  claude CLI")
	case apiKey != "":
		fmt.Printf("  Backend:  Anthropic API key (%s)\n", maskKey(apiKey))
	case config.UseClaudeCLI():
		fmt.Println("  Backend:  claude CLI")
	default:
		fmt.Println("  Backend:  not configured")
	}
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

func printOnlineModeStatus(status repopkg.LocalSetupStatus) {
	if !status.InGitRepo || !status.Initialized {
		fmt.Println("No RepoGuide repo detected. Run `repoguide repo init` to get started.")
		return
	}
	fmt.Println(renderSectionTitle("RepoGuide - online mode"))
	fmt.Printf("  Repo:   %s\n", renderRepoPath(status.RepoRoot))
	fmt.Printf("  Repo ID: %s\n", status.RepoID)

	_, loggedIn := clientauth.Load()
	if loggedIn {
		fmt.Println("  Server: connected to RepoGuide backend")
	} else {
		fmt.Println("  Server: not logged in (run `repoguide logins`)")
	}

	cfg, err := internal.LoadRepoConfigFile(status.StoreDir)
	if err == nil && cfg.LastSyncedAt != nil {
		since := time.Since(*cfg.LastSyncedAt).Round(time.Minute)
		fmt.Printf("  Last sync: %s ago\n", since)
	} else {
		fmt.Println("  Last sync: never")
	}
	fmt.Println()
	fmt.Println("  To switch to offline mode: repoguide repo init --offline --force")
}

func printCommitHookStatus(status repopkg.LocalSetupStatus) {
	if !status.InGitRepo || !status.Initialized {
		return
	}
	fmt.Println(renderSectionTitle("Managed commit hooks"))
	switch {
	case !status.CommitHooksEnabled:
		fmt.Println("  Status:   disabled")
	case status.HooksPathCustom:
		fmt.Printf("  Status:   enabled in config, deferred by custom hooks path (%s)\n", renderPath(status.HooksPath))
	default:
		fmt.Println("  Status:   enabled")
	}
	for _, hook := range status.Hooks {
		state := "not installed"
		if hook.Installed {
			state = "installed"
		}
		fmt.Printf("  %-11s %s\n", hook.Name+":", state)
	}
	fmt.Println()
}
