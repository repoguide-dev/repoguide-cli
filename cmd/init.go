package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/config"
	"github.com/repoguide/repoguide-cli/internal/localrunner"
	"github.com/repoguide/repoguide-cli/internal/runtime"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/repoguide/repoguide-cli/internal/sqlitestore"
	"github.com/spf13/cobra"
)

var errInitAborted = errors.New("init aborted")
var skipInitNextPrompt bool

func init() {
	initCmd.Flags().Bool("force", false, "Re-write local RepoGuide config for this repo")
	initCmd.Flags().Bool("approve", false, "Approve all prompts non-interactively (for scripts)")
	initCmd.Flags().Bool("offline", false, "Initialize in offline mode (no backend sync)")
	repoCmd.AddCommand(initCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize RepoGuide for the current Git repository",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, _ []string) error {
	force, _ := cmd.Flags().GetBool("force")
	approve, _ := cmd.Flags().GetBool("approve")
	offlineMode, _ := cmd.Flags().GetBool("offline")

	// Wizard gate: resolve AI backend before touching anything on disk.
	var localCfg runtime.Config
	if offlineMode {
		localCfg = resolveLocalConfig(approve)
		if localCfg.AnthropicAPIKey == "" && !localCfg.UseClaudeCLI && localCfg.CLIBackend == "" {
			fmt.Println("No AI backend configured. Set ANTHROPIC_API_KEY, or configure an AI CLI executable.")
			return nil
		}
	} else {
		if err := ensureActiveLogin(); err != nil {
			return err
		}
	}

	var (
		result internal.InitResult
		err    error
	)
	mode := "online"
	if offlineMode {
		mode = "local"
	}
	result = RunWithSpinner("Initializing RepoGuide", func(_ func(current, total int, label string)) internal.InitResult {
		result, err = internal.InitRepo(internal.InitOptions{
			Force: force,
			Mode:  mode,
		})
		return result
	})
	if err != nil {
		if errors.Is(err, internal.ErrNotGitRepo) {
			if !approve {
				fmt.Print("This folder is not a Git repository. Run `git init` here and continue? [y/N]: ")
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(strings.TrimSpace(answer)) != "y" {
					return nil
				}
			}
			if out, gitErr := exec.Command("git", "init").CombinedOutput(); gitErr != nil {
				return fmt.Errorf("git init failed: %w\n%s", gitErr, out)
			}
			result = RunWithSpinner("Initializing RepoGuide", func(_ func(current, total int, label string)) internal.InitResult {
				result, err = internal.InitRepo(internal.InitOptions{
					Force: force,
					Mode:  mode,
				})
				return result
			})
		}
		if err != nil {
			return err
		}
	}

	if result.AlreadyInit {
		fmt.Println("RepoGuide is already initialized for this repo.")
		return nil
	}

	if !result.AlreadyInit {
		if foundFiles := internal.ScanHintFiles(result.RepoRoot); len(foundFiles) > 0 {
			var selected []string
			switch {
			case approve:
				selected = foundFiles
			case isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()):
				var hintErr error
				selected, hintErr = runHintFilesSelect(foundFiles)
				if errors.Is(hintErr, errInitAborted) {
					return nil
				}
				if hintErr != nil {
					return hintErr
				}
			}
			if len(selected) > 0 {
				if cfg, loadErr := internal.LoadRepoConfigFile(result.StoreDir); loadErr == nil {
					cfg.HintFiles = selected
					_ = internal.SaveRepoConfigFile(result.StoreDir, cfg)
				}
			}
		}
	}

	var (
		warmResults     []sessionimport.SessionIndexWarmResult
		analysisResults []sessionimport.SessionArtifactWarmResult
	)
	RunWithSpinner("Indexing sessions", func(progress func(current, total int, label string)) struct{} {
		warmResults, err = sessionimport.WarmSessionCaches(sessionimport.SessionLoadOptions{
			Progress: progress,
		})
		if err != nil {
			return struct{}{}
		}
		analysisResults, err = sessionimport.WarmRepoSessionArtifacts(result.RepoRoot, progress)
		return struct{}{}
	})
	if err != nil {
		return err
	}
	if !offlineMode {
		token, _ := clientauth.Load()
		c := sessionimport.CloudClient{
			BaseURL: getBackendURL(),
			Token:   token.Token,
		}
		if err := c.RegisterRepo(result.RepoID, result.RepoRoot); err != nil {
			return fmt.Errorf("repo initialized locally, but backend repo registration failed: %w", err)
		}
		if err := c.UploadRepoEvents(result.RepoID, result.RepoRoot); err != nil {
			return fmt.Errorf("repo initialized locally, but backend repo events upload failed: %w", err)
		}
		RunWithSpinner("Analyzing repo", func(_ func(int, int, string)) struct{} {
			if analyzeErr := c.TriggerAnalyze(result.RepoID, false); analyzeErr != nil {
				msg := analyzeErr.Error()
				switch {
				case strings.Contains(msg, "limit reached"):
					fmt.Println("\nRepo analysis limit reached for your plan.")
					fmt.Println("Upgrade at https://repoguide.dev/upgrade")
				case strings.Contains(msg, "already analyzed"), strings.Contains(msg, "409"):
					// already analyzed on a previous init — fine
				}
			}
			return struct{}{}
		})
	} else {
		// Local mode: apiKey already collected in wizard gate above.
		if cfg, loadErr := internal.LoadRepoConfigFile(result.StoreDir); loadErr == nil {
			cfg.LocalAIBackend = internal.LocalAIBackendFromRuntimeConfig(localCfg)
			_ = internal.SaveRepoConfigFile(result.StoreDir, cfg)
		}
		summary := initSummaryData{
			result: result,

			localMode:       true,
			warmResults:     warmResults,
			analysisResults: analysisResults,
		}
		err = RunWithStatusScreen(func(spinnerView string) string {
			return renderInitProcessingScreen(summary, spinnerView)
		}, func() error {
			dbPath, dbErr := internal.RepoGuideDBPath()
			if dbErr != nil {
				return dbErr
			}
			st, openErr := sqlitestore.Open(dbPath)
			if openErr != nil {
				return openErr
			}
			defer st.Close()
			rt := localrunner.New(st, localCfg)
			ctx := context.Background()
			_ = localrunner.EnsureLocalRepo(ctx, rt.Services, result.RepoID, result.RepoRoot, filepath.Base(result.RepoRoot))
			events, docs, parseErr := sessionimport.ParseRepoEventsForLocal(result.RepoID, result.RepoRoot)
			if parseErr != nil {
				return fmt.Errorf("parse sessions: %w", parseErr)
			}
			if len(events) > 0 {
				_ = rt.Services.Sessions.PutEvents(ctx, result.RepoID, events)
			}
			if len(docs) > 0 {
				_ = rt.Services.Sessions.PutDocs(ctx, result.RepoID, docs)
			}
			if err := rt.Services.Topics.BuildTopics(ctx, result.RepoID); err != nil {
				return fmt.Errorf("build topics: %w", err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("repo initialized locally, but local analysis failed: %w", err)
		}
	}

	printInitSummary(initSummaryData{
		result:          result,
		localMode:       offlineMode,
		warmResults:     warmResults,
		analysisResults: analysisResults,
	})

	if approve || skipInitNextPrompt {
		return nil
	}
	return promptNextSelection(cmd, initNextCommandOptions(cmd)...)
}

type initSummaryData struct {
	result          internal.InitResult
	localMode       bool
	warmResults     []sessionimport.SessionIndexWarmResult
	analysisResults []sessionimport.SessionArtifactWarmResult
}

func printInitSummary(data initSummaryData) {
	fmt.Print(renderInitSummary(data))
}

func renderInitSummary(data initSummaryData) string {
	var b strings.Builder
	writeSection(&b, "✓ Git repo detected", renderRepoPath(data.result.RepoRoot))
	writeSection(&b, "✓ Repo tracking initialized", data.result.RepoID)
	writeSection(&b, "✓ Local store created", renderPath(data.result.StoreDir))

	writeHookSummary(&b, data.result)
	writeAgentSummary(&b, data.result.Agents, data.localMode)
	writeWarmSummary(&b, data.warmResults)
	writeAnalysisWarmSummary(&b, data.analysisResults)
	writeSection(&b, "Privacy defaults",
		"File contents     never stored",
		"Upload mode       sync on init when signed in",
	)
	if data.localMode {
		writeSection(&b, "How it works",
			"Sessions are analyzed locally using your Anthropic API key or claude cli.",
			"No data is sent to RepoGuide servers.",
			"Run `repoguide mcp install` to activate the RepoGuide MCP for your agents in this repository.",
		)
	} else {
		writeSection(&b, "Why sync",
			"Init syncs your analytics with RepoGuide.",
			"That powers repo improvement recommendations.",
			"It also lets you activate the RepoGuide MCP for your agents.",
		)
	}
	return b.String()
}

func renderInitProcessingScreen(data initSummaryData, spinnerView string) string {
	var b strings.Builder
	b.WriteString("\n")
	// Indent title
	lines := strings.Split(titleStyle.Render("RepoGuide init"), "\n")
	for _, line := range lines {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n")
	// Indent summary
	summaryLines := strings.Split(renderInitSummary(data), "\n")
	for _, line := range summaryLines {
		if line != "" {
			b.WriteString("  " + line + "\n")
		} else {
			b.WriteString("\n")
		}
	}
	// Indent status section
	statusLines := strings.Split(renderSectionTitle("Status"), "\n")
	for _, line := range statusLines {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString(fmt.Sprintf("  %s Processing data\n", spinnerView))
	b.WriteString("\n")
	return b.String()
}

func initNextCommandOptions(cmd *cobra.Command) []nextCommandOption {
	return []nextCommandOption{
		{
			command:  "repoguide mcp install",
			selected: true,
			enabled:  true,
			run: func(_ *cobra.Command) error {
				return runMCPInstall(cmd, nil)
			},
		},
		{
			command: "repoguide sessions",
			enabled: true,
			run: func(_ *cobra.Command) error {
				return runSessions(cmd, nil)
			},
		},
		{
			command: "repoguide stats",
			enabled: true,
			run: func(_ *cobra.Command) error {
				return runStats(cmd, nil)
			},
		},
		{
			command: "repoguide files",
			enabled: true,
			run: func(_ *cobra.Command) error {
				return runFiles(cmd, nil)
			},
		},
	}
}

// initChoiceModel is a single-select Bubble Tea prompt for init flow screens.
type initChoiceModel struct {
	title   string
	body    []string
	options []string
	cursor  int
	done    bool
	quit    bool
}

func (m initChoiceModel) Init() tea.Cmd { return nil }

func (m initChoiceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "ctrl+c", "q", "esc":
		m.quit, m.done = true, true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.options)-1 {
			m.cursor++
		}
	case "enter":
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m initChoiceModel) View() string {
	parts := []string{m.title, ""}
	for _, line := range m.body {
		parts = append(parts, line)
	}
	if len(m.body) > 0 {
		parts = append(parts, "")
	}
	for i, opt := range m.options {
		if i == m.cursor {
			parts = append(parts, selected.Render("› "+opt))
		} else {
			parts = append(parts, "  "+opt)
		}
	}
	parts = append(parts, "", muted.Render("↑↓ navigate  •  enter select  •  q quit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(parts, "\n"))
}

func (m initChoiceModel) choice() int {
	if m.quit || !m.done {
		return -1
	}
	return m.cursor
}

// hintFilesMultiModel is a multi-select Bubble Tea model for hint file selection.
type hintFilesMultiModel struct {
	files    []string
	selected []bool
	cursor   int
	done     bool
	quit     bool
}

func newHintFilesMultiModel(files []string) hintFilesMultiModel {
	sel := make([]bool, len(files))
	for i := range sel {
		sel[i] = true
	}
	return hintFilesMultiModel{files: files, selected: sel}
}

func (m hintFilesMultiModel) Init() tea.Cmd { return nil }

func (m hintFilesMultiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "ctrl+c", "q", "esc":
		m.quit, m.done = true, true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
	case "enter":
		m.selected[m.cursor] = !m.selected[m.cursor]
	case " ":
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m hintFilesMultiModel) View() string {
	parts := []string{renderSectionTitle("Found guidance files - select which to include:"), ""}
	for i, f := range m.files {
		check := "[ ] "
		if m.selected[i] {
			check = "[✓] "
		}
		line := check + f
		if i == m.cursor {
			parts = append(parts, selected.Render("› "+line))
		} else {
			parts = append(parts, "  "+line)
		}
	}
	parts = append(parts, "", muted.Render("↑↓ navigate  •  enter toggle  •  space confirm"))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(parts, "\n"))
}

func (m hintFilesMultiModel) result() []string {
	var out []string
	for i, f := range m.files {
		if m.selected[i] {
			out = append(out, f)
		}
	}
	return out
}

func runHintFilesSelect(found []string) ([]string, error) {
	m := initChoiceModel{
		title: renderSectionTitle("Improve hints using repo instruction docs?"),
		body: []string{
			"  Helps RepoGuide understand your repo. No other files will be shared.",
		},
		options: []string{"Yes, use instruction doc content", "No, use telemetry only"},
		cursor:  0,
	}
	final1, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, err
	}
	c1 := final1.(initChoiceModel)
	if c1.quit {
		return nil, errInitAborted
	}
	if c1.choice() != 0 {
		return nil, nil
	}
	final2, err := tea.NewProgram(newHintFilesMultiModel(found), tea.WithAltScreen()).Run()
	if err != nil {
		return nil, err
	}
	c2 := final2.(hintFilesMultiModel)
	if c2.quit {
		return nil, errInitAborted
	}
	return c2.result(), nil
}

// promptHintFiles asks whether to include repo instruction docs in hints,
// then if yes, prompts the user to select from found files (none pre-selected).
// Returns the selected filenames (may be empty).
func promptHintFiles(in io.Reader, out io.Writer, found []string) ([]string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Improve hints using repo instruction docs?")
	fmt.Fprintln(out, "  Helps RepoGuide understand your repo. No other files will be shared.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  [1] No, use telemetry only")
	fmt.Fprintln(out, "  [3] Use instruction doc content")
	fmt.Fprintln(out)
	fmt.Fprint(out, "Choice [1]: ")

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if strings.TrimSpace(answer) != "3" {
		return nil, nil
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Found guidance files - select which to include:")
	for i, name := range found {
		fmt.Fprintf(out, "  [%d] %s\n", i+1, name)
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, "Numbers (e.g. 1 3), or enter to skip: ")

	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	var selected []string
	for _, tok := range strings.Fields(strings.TrimSpace(line)) {
		idx := 0
		if _, scanErr := fmt.Sscanf(tok, "%d", &idx); scanErr != nil || idx < 1 || idx > len(found) {
			continue
		}
		selected = append(selected, found[idx-1])
	}
	return selected, nil
}

func printHookSummary(result internal.InitResult) {
	writeHookSummary(os.Stdout, result)
}

func writeHookSummary(out io.Writer, result internal.InitResult) {
	if result.HooksPathCustom {
		writeSection(out, "Existing Git hooks path detected", renderPath(result.HooksPath))
		writeSection(out, "Git hooks",
			"Managed commit hooks are installed by `repoguide mcp install`",
			"Installation will be deferred while core.hooksPath is set",
		)
		return
	}
	if !result.CommitHooksEnabled {
		writeSection(out, "Git hooks", "Managed commit hooks not installed yet", "Run `repoguide mcp install` to enable them for MCP-managed AGENTS.md / CLAUDE.md files")
		return
	}
	lines := []string{
		"Managed commit hooks enabled",
		"Injected blocks (repoguide:mcp-instruction, repoguide:feedback-instruction) are stripped",
		"from CLAUDE.md / AGENTS.md before each commit and will not be committed.",
		"Real edits to those files are detected automatically and committed normally.",
		"Note: if you abort a commit, the files may briefly appear as modified —",
		"run `repoguide mcp fix` to re-hide them.",
	}
	for _, hook := range result.Hooks {
		state := "not installed"
		if hook.Installed {
			state = "installed"
		}
		lines = append(lines, fmt.Sprintf("%s: %s", hook.Name, state))
	}
	writeSection(out, "Git hooks", lines...)
}

func printAgentSummary(agents []internal.DetectedSource) {
	writeAgentSummary(os.Stdout, agents, false)
}

func writeAgentSummary(out io.Writer, agents []internal.DetectedSource, localMode bool) {
	foundAny := false
	for _, agent := range agents {
		if agent.Found {
			foundAny = true
			break
		}
	}

	if !foundAny {
		nextCommand := "repoguide sync"
		if localMode {
			nextCommand = "repoguide setup --offline"
		}
		writeSection(
			out,
			"No local AI coding sessions found yet.",
			"RepoGuide is initialized. Once you use Claude Code, Codex CLI, or Aider in this repo, run:",
			nextCommand,
		)
		return
	}

	fmt.Fprintln(out, renderSectionTitle("Detected local agents"))
	for _, agent := range agents {
		switch {
		case agent.Name == "Aider" && !agent.Found:
			fmt.Fprintf(out, "  %-12s not found\n", agent.DisplayName)
		case agent.Name == "Aider" && agent.Found:
			fmt.Fprintf(out, "  %-12s repo files found\n", agent.DisplayName)
		case agent.Unit == "workspaces found":
			fmt.Fprintf(out, "  %-12s %d %s\n", agent.DisplayName, agent.Count, agent.Unit)
		case agent.Found:
			fmt.Fprintf(out, "  %-12s %d %s\n", agent.DisplayName, agent.Count, agent.Unit)
		default:
			fmt.Fprintf(out, "  %-12s not found\n", agent.DisplayName)
		}
	}
	fmt.Fprintln(out)
}

func printWarmSummary(results []sessionimport.SessionIndexWarmResult) {
	writeWarmSummary(os.Stdout, results)
}

func writeWarmSummary(out io.Writer, results []sessionimport.SessionIndexWarmResult) {
	if len(results) == 0 {
		return
	}

	fmt.Fprintln(out, renderSectionTitle("Session index"))
	for _, result := range results {
		fmt.Fprintf(out, "  %-12s %d cached\n", displayAgentName(result.Agent), result.SessionCount)
	}
	fmt.Fprintln(out)
}

func printAnalysisWarmSummary(results []sessionimport.SessionArtifactWarmResult) {
	writeAnalysisWarmSummary(os.Stdout, results)
}

func writeAnalysisWarmSummary(out io.Writer, results []sessionimport.SessionArtifactWarmResult) {
	if len(results) == 0 {
		return
	}

	fmt.Fprintln(out, renderSectionTitle("Session analysis"))
	for _, result := range results {
		fmt.Fprintf(out, "  %-12s %d cached\n", displayAgentName(result.Agent), result.SessionCount)
	}
	fmt.Fprintln(out)
}

func writeSection(out io.Writer, title string, lines ...string) {
	fmt.Fprintln(out, renderSectionTitle(title))
	for _, line := range lines {
		fmt.Fprintf(out, "  %s\n", line)
	}
	fmt.Fprintln(out)
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if home != "" && path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if home != "" && strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix)
	}
	if idx := strings.Index(path, string(filepath.Separator)+".repoguide"); idx >= 0 {
		return "~" + path[idx:]
	}
	return path
}

// resolveLocalConfig determines which AI backend to use for local mode.
// Priority: saved API key → saved CLI choice → --approve (default to Claude CLI) → interactive choice.
func resolveLocalConfig(approve bool) runtime.Config {
	if key := config.AnthropicAPIKey(); key != "" {
		return runtime.Config{AnthropicAPIKey: key}
	}
	if config.UseClaudeCLI() {
		return runtime.Config{CLIBackend: "claude"}
	}
	if approve {
		_ = config.SetUseClaudeCLI(true)
		return runtime.Config{UseClaudeCLI: true}
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
		return runtime.Config{} // caller will print error
	}
	return promptLocalAIChoice()
}

// promptLocalAIChoice offers an interactive choice between API key and supported CLIs.
func promptLocalAIChoice() runtime.Config {
	m := initChoiceModel{
		title: renderSectionTitle("How should RepoGuide call AI?"),
		body: []string{
			"  Local mode uses an AI provider to analyze your sessions and build topic context.",
		},
		options: []string{
			"Use claude CLI (already logged in via Claude Code)",
			"Use codex CLI (already logged in via Codex)",
			"Use gemini CLI (already logged in via Gemini)",
			"Enter Anthropic API key (sk-ant-...)",
		},
		cursor: 0,
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil || final.(initChoiceModel).quit {
		return runtime.Config{}
	}
	switch final.(initChoiceModel).choice() {
	case 0:
		_ = config.SetUseClaudeCLI(true)
		return runtime.Config{CLIBackend: "claude"}
	case 1:
		return runtime.Config{CLIBackend: "codex"}
	case 2:
		return runtime.Config{CLIBackend: "gemini"}
	}
	// Prompt for API key.
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter Anthropic API key (sk-ant-...): ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Println("API key required. Press Ctrl-C to cancel.")
			continue
		}
		if err := config.SetAnthropicAPIKey(line); err == nil {
			_ = os.Setenv("ANTHROPIC_API_KEY", line)
		}
		return runtime.Config{AnthropicAPIKey: line}
	}
}
