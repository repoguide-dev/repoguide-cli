package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/config"
	"github.com/repoguide/repoguide-cli/internal/debugserver"
	"github.com/repoguide/repoguide-cli/internal/localrunner"
	mcpinternal "github.com/repoguide/repoguide-cli/internal/mcp"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/runtime"
	"github.com/repoguide/repoguide-cli/internal/services"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/repoguide/repoguide-cli/internal/sqlitestore"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpInstructCmd)
	mcpCmd.AddCommand(mcpInstallCmd)
	mcpCmd.AddCommand(mcpTestCmd)
	mcpCmd.AddCommand(mcpActivityCmd)
	mcpCmd.AddCommand(mcpUninstallCmd)
	mcpCmd.AddCommand(mcpServeCmd)
	mcpCmd.AddCommand(mcpFixCmd)
	mcpCmd.AddCommand(mcpHookCmd)
	mcpCmd.AddCommand(mcpStatuslineCmd)
	mcpStatuslineCmd.Flags().String("chain", "", "base64-encoded previous statusLine command to chain")

	mcpActivityCmd.Flags().String("repo", "", "Filter activity to a specific repo path")
	mcpActivityCmd.Flags().Int("limit", 20, "Maximum number of activity entries to show")
	mcpInstallCmd.Flags().Bool("approve", false, "Approve all prompts non-interactively (for scripts)")
	mcpInstallCmd.Flags().Bool("no-hooks", false, "Skip installing Claude Code and Codex hooks (still installs the plugin/MCP registration; for benchmark/test environments)")
	mcpServeCmd.Flags().String("debug", "", "Start a local HTTP debug server at this address (e.g. :9090)")
	mcpServeCmd.Flags().Bool("local", false, "Force local SQLite mode even when a cloud token is present")
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP integration commands",
}

var mcpInstructCmd = &cobra.Command{
	Use:   "instruct",
	Short: "Add RepoGuide MCP instruction to this repo's agent config",
	RunE: func(_ *cobra.Command, _ []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return instructAndActivate(cwd)
	},
}

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Interactively enable RepoGuide MCP across your repos",
	RunE:  runMCPInstall,
}

var mcpUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove RepoGuide MCP from agent configs and MCP clients",
	RunE:  runMCPUninstall,
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the RepoGuide MCP server (stdio)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		localFlag, _ := cmd.Flags().GetBool("local")
		token, _ := clientauth.Load()

		// Always open SQLite so local-mode repos can be served per-repo even
		// when a cloud token is present (hybrid routing).
		dbPath, err := internal.RepoGuideDBPath()
		if err != nil {
			return err
		}
		st, err := sqlitestore.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open local db: %w", err)
		}
		defer st.Close()
		rt := localrunner.New(st, runtime.Config{AnthropicAPIKey: config.AnthropicAPIKey(), UseClaudeCLI: config.UseClaudeCLI()})

		// Start debug HTTP server if requested.
		if addr, _ := cmd.Flags().GetString("debug"); addr != "" {
			go func() {
				if err := debugserver.New(rt.Services).Start(addr); err != nil {
					fmt.Fprintf(os.Stderr, "debug server: %v\n", err)
				}
			}()
		}

		// Start maintenance in the background so MCP requests can be answered
		// immediately, but wait for it before the process exits.
		waitForMaintenance := startLocalMaintenance(rt)

		localSvcFactory := func(repoID string) *services.Services {
			return localrunner.New(st, internal.LocalRuntimeConfigForRepo(repoID)).Services
		}

		var runErr error
		if token.Token != "" && !localFlag {
			// Hybrid mode: cloud for cloud-mode repos, SQLite for local-mode repos.
			runErr = mcpinternal.RunHybridMCPServer(os.Stdin, os.Stdout, getBackendURL(), token.Token, rt.Services, localSvcFactory)
		} else {
			runErr = mcpinternal.RunLocalMCPServer(os.Stdin, os.Stdout, rt.Services, localSvcFactory)
		}
		waitForMaintenance()
		return runErr
	},
}

func startLocalMaintenance(rt *localrunner.Runtime) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.RunStartupMaintenance(context.Background())
	}()
	return func() {
		<-done
	}
}

var mcpFixCmd = &cobra.Command{
	Use:   "fix",
	Short: "Re-register the RepoGuide MCP server with Claude Code",
	RunE:  runMCPFix,
}

var mcpActivityCmd = &cobra.Command{
	Use:   "activity",
	Short: "List recorded RepoGuide MCP tool calls",
	RunE:  runMCPActivity,
}

// mcpHookCmd backs the Claude Code UserPromptSubmit/Stop hooks installed by
// InstructRepoForClaude (see cli/internal/mcp_hooks.go). Not meant to be run
// by hand; Claude Code invokes it with the hook payload on stdin.
var mcpHookCmd = &cobra.Command{
	Use:    "hook [prompt|gemini-prompt|stop|gemini-stop]",
	Short:  "Internal: run a RepoGuide client hook (invoked by an agent client, not users)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		switch args[0] {
		case "prompt":
			return mcpinternal.RunPromptHook(os.Stdin, os.Stdout, cwd)
		case "gemini-prompt":
			return mcpinternal.RunGeminiPromptHook(os.Stdin, os.Stdout, cwd)
		case "stop":
			return mcpinternal.RunStopHook(os.Stdin, os.Stdout, cwd)
		case "gemini-stop":
			return mcpinternal.RunGeminiStopHook(os.Stdin, os.Stdout, cwd)
		default:
			return fmt.Errorf("unknown hook %q", args[0])
		}
	},
}

// mcpStatuslineCmd backs the Claude Code statusLine badge installed by
// InstallClaudeCodeStatusline (see cli/internal/mcp_statusline.go). Not meant
// to be run by hand; Claude Code invokes it with the statusLine payload on stdin.
var mcpStatuslineCmd = &cobra.Command{
	Use:    "statusline",
	Short:  "Internal: render the RepoGuide statusLine badge (invoked by Claude Code, not users)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		chain, _ := cmd.Flags().GetString("chain")
		return mcpinternal.RunStatuslineHook(os.Stdin, os.Stdout, chain)
	},
}

func runMCPFix(_ *cobra.Command, _ []string) error {
	detected := mcpinternal.DetectConfiguredMCPClients()
	if len(detected) == 0 {
		return fmt.Errorf("no configured MCP clients found; run: repoguide mcp install")
	}

	// Re-activate the current repo and refresh native client hooks. RepoGuide
	// does not add AGENTS.md instructions during MCP installation.
	if repoRoot, err := internal.CurrentRepoRoot(); err == nil {
		if err := mcpinternal.ActivateMCPRepo(repoRoot); err != nil {
			fmt.Printf("  ✗ %s: %v\n", filepath.Base(repoRoot), err)
		} else {
			_, _ = mcpinternal.InstructRepoForClaude(repoRoot)
			fmt.Printf("  ✓ %s hooks refreshed\n", filepath.Base(repoRoot))
		}
	}

	results, _ := mcpinternal.InstallSelectedMCPClients(detected, true)
	claudeFixed := false
	for _, r := range results {
		if r.Configured {
			fmt.Printf("  ✓ %s re-installed\n", r.Name)
			if r.Name == "Claude Code" {
				claudeFixed = true
			}
		} else {
			fmt.Printf("  ✗ %s: %v\n", r.Name, r.Err)
		}
	}

	if claudeFixed {
		_ = mcpinternal.AddClaudeCodePermission()
		_ = mcpinternal.AddClaudeCodeFeedbackPermission()
		fmt.Println()
		fmt.Println("Next steps (Claude Code):")
		fmt.Println("  1. Close your current Claude Code session.")
		fmt.Println("  2. Open a new session in this repo.")
		fmt.Println("  3. Run /reload-plugins to load the plugin.")
	}
	return nil
}

func runMCPActivity(cmd *cobra.Command, _ []string) error {
	repoFilter, _ := cmd.Flags().GetString("repo")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		return fmt.Errorf("--limit must be >= 0")
	}

	records, err := mcpinternal.LoadMCPActivity()
	if err != nil {
		return err
	}
	repoFilter = strings.TrimSpace(repoFilter)

	filtered := make([]mcpinternal.MCPActivityRecord, 0, len(records))
	for _, record := range records {
		if repoFilter != "" && record.Repo != repoFilter {
			continue
		}
		filtered = append(filtered, record)
	}
	if len(filtered) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No MCP activity found.")
		return nil
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	if isatty.IsTerminal(os.Stdout.Fd()) {
		model := newMCPActivityModel(filtered)
		_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
		return err
	}

	return renderMCPActivityTable(cmd, filtered)
}

func renderMCPActivityTable(cmd *cobra.Command, records []mcpinternal.MCPActivityRecord) error {

	fmt.Fprintln(cmd.OutOrStdout(), titleStyle.Render("RepoGuide MCP Activity"))
	fmt.Fprintln(cmd.OutOrStdout())
	columns := []tableColumn{
		{title: "Timestamp", width: 19},
		{title: "Repo", width: 24},
		{title: "Command", width: 24},
		{title: "Inputs", width: 56},
	}
	fmt.Fprintln(cmd.OutOrStdout(), renderTableHeader(columns))
	for _, record := range records {
		fmt.Fprintln(cmd.OutOrStdout(), renderTableRow(
			columns,
			formatMCPActivityTimestamp(record.Timestamp),
			displayPath(record.Repo),
			record.Command,
			formatMCPActivityInputs(record.Inputs),
		))
	}
	return nil
}

type mcpActivityView int

const (
	mcpActivityListView mcpActivityView = iota
	mcpActivityDetailView
)

type mcpActivityModel struct {
	view    mcpActivityView
	width   int
	height  int
	table   table.Model
	records []mcpinternal.MCPActivityRecord
	vp      viewport.Model
}

func newMCPActivityModel(records []mcpinternal.MCPActivityRecord) mcpActivityModel {
	m := mcpActivityModel{
		view:    mcpActivityListView,
		table:   newSessionTable(),
		records: records,
	}
	m.applyTableSize()
	m.applyTableRows()
	return m
}

func (m mcpActivityModel) Init() tea.Cmd {
	return nil
}

func (m mcpActivityModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyTableSize()
		m.applyTableRows()
		m.vp = newDetailViewport(m.width, m.height)
		if m.view == mcpActivityDetailView {
			m.refreshDetail()
		}
		return m, nil
	case tea.KeyMsg:
		switch m.view {
		case mcpActivityListView:
			return m.updateList(msg)
		case mcpActivityDetailView:
			return m.updateDetail(msg)
		}
	}
	return m, nil
}

func (m mcpActivityModel) View() string {
	switch m.view {
	case mcpActivityDetailView:
		return m.renderDetail()
	default:
		return m.renderList()
	}
}

func (m mcpActivityModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		m.table.MoveUp(1)
	case "down", "j":
		m.table.MoveDown(1)
	case "pgup", "b":
		m.table.MoveUp(max(1, m.table.Height()-1))
	case "pgdown", "f", " ":
		m.table.MoveDown(max(1, m.table.Height()-1))
	case "home", "g":
		m.table.GotoTop()
	case "end", "G":
		m.table.GotoBottom()
	case "enter":
		if len(m.records) == 0 {
			return m, nil
		}
		m.view = mcpActivityDetailView
		m.refreshDetail()
	}
	return m, nil
}

func (m mcpActivityModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		m.view = mcpActivityListView
		return m, nil
	}
	if vp, handled := updateViewportKeys(m.vp, msg); handled {
		m.vp = vp
		return m, nil
	}
	vp, cmd := m.vp.Update(msg)
	m.vp = vp
	return m, cmd
}

func (m *mcpActivityModel) applyTableSize() {
	if m.height > 0 {
		m.table.SetHeight(max(5, m.height-6))
	}
	if m.width > 0 {
		m.table.SetWidth(max(40, m.width-4))
	}
}

func (m *mcpActivityModel) applyTableRows() {
	cols := []table.Column{
		{Title: "Timestamp", Width: 19},
		{Title: "Repo", Width: max(18, max(0, m.width-4)/4)},
		{Title: "Command", Width: 24},
		{Title: "Inputs", Width: max(20, max(0, m.width-4)-19-24-max(18, max(0, m.width-4)/4)-8)},
	}
	rows := make([]table.Row, len(m.records))
	for i, record := range m.records {
		rows[i] = table.Row{
			formatMCPActivityTimestamp(record.Timestamp),
			displayPath(record.Repo),
			record.Command,
			formatMCPActivityInputs(record.Inputs),
		}
	}
	m.table.SetColumns(cols)
	m.table.SetRows(rows)
}

func (m *mcpActivityModel) refreshDetail() {
	record := m.selectedRecord()
	if record == nil {
		m.vp.SetContent("")
		return
	}
	lines := []string{
		titleStyle.Render("MCP Activity Entry"),
		"",
		fmt.Sprintf("%s %s", headStyle.Render("Timestamp:"), formatMCPActivityTimestamp(record.Timestamp)),
		fmt.Sprintf("%s %s", headStyle.Render("Repo:"), renderRepoPath(record.Repo)),
		fmt.Sprintf("%s %s", headStyle.Render("Command:"), record.Command),
		"",
		headStyle.Render("Inputs"),
		formatMCPActivityInputsPretty(record.Inputs),
	}
	if len(record.Response) > 0 {
		w := m.vp.Width
		if w <= 0 {
			w = 80
		}
		lines = append(lines, "", headStyle.Render("Response"), wordWrap(formatMCPActivityResponsePretty(record.Response), w))
	}
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.vp.GotoTop()
}

func (m mcpActivityModel) renderList() string {
	title := titleStyle.Render("RepoGuide MCP Activity")
	footer := muted.Render(footerHint("enter open entry", "↑/↓ move", "pgup/pgdn scroll", "q quit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + m.table.View() + "\n\n" + footer)
}

func (m mcpActivityModel) renderDetail() string {
	title := muted.Render(footerHint("q back", "↑/↓ scroll", "pgup/pgdn page", "ctrl+c quit") + scrollHint(m.vp))
	return lipgloss.NewStyle().Padding(1, 2).Render(m.vp.View() + "\n\n" + title)
}

func (m mcpActivityModel) selectedRecord() *mcpinternal.MCPActivityRecord {
	cursor := m.table.Cursor()
	if cursor < 0 || cursor >= len(m.records) {
		return nil
	}
	return &m.records[cursor]
}

func formatMCPActivityTimestamp(value string) string {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Local().Format("2006-01-02 15:04:05")
	}
	return value
}

func formatMCPActivityInputs(inputs map[string]any) string {
	data, err := json.Marshal(inputs)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func formatMCPActivityInputsPretty(inputs map[string]any) string {
	data, err := json.MarshalIndent(inputs, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func formatMCPActivityResponsePretty(response map[string]any) string {
	var raw string
	if text, ok := response["text"].(string); ok && len(response) == 1 {
		raw = text
	} else {
		data, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return "{}"
		}
		raw = string(data)
	}
	return stripXMLTags(raw)
}

func stripXMLTags(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func runMCPUninstall(_ *cobra.Command, _ []string) error {
	cfg, _ := mcpinternal.LoadMCPConfig()

	// collect what will be touched
	repoCount := len(cfg.ActivatedRepos)
	clientResults := mcpinternal.UninstallMCPClients() // detect only at this point - we detect lazily
	clientCount := len(clientResults)

	if repoCount == 0 && clientCount == 0 {
		fmt.Println("Nothing to uninstall.")
		return nil
	}

	// show preview
	fmt.Println(renderDanger("This will remove RepoGuide MCP from:"))
	if repoCount > 0 {
		fmt.Printf("  Agent configs: %d repo(s)\n", repoCount)
		for _, r := range cfg.ActivatedRepos {
			fmt.Printf("    %s\n", renderRepoPath(r))
		}
	}
	if clientCount > 0 {
		fmt.Printf("  MCP clients:  %d detected\n", clientCount)
		for _, r := range clientResults {
			fmt.Printf("    %s\n", r.Name)
		}
	}
	fmt.Println()
	fmt.Print(renderDanger("Continue? [y/N]: "))

	var answer string
	fmt.Scanln(&answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("Uninstall cancelled.")
		return nil
	}

	// remove instruction blocks from repos
	for _, repoPath := range cfg.ActivatedRepos {
		filename, err := mcpinternal.RemoveMCPInstruction(repoPath)
		if hookErr := mcpinternal.RemoveClaudeCodeHooks(repoPath); hookErr != nil && err == nil {
			err = hookErr
		}
		if slErr := mcpinternal.RemoveClaudeCodeStatusline(repoPath); slErr != nil && err == nil {
			err = slErr
		}
		name := filepath.Base(repoPath)
		if repoID, idErr := internal.GitRepoID(repoPath); idErr == nil && repoID != "" {
			storeDir := filepath.Join(internal.RepoGuideDir(), "repos", repoID)
			if _, hookErr := internal.SetManagedCommitHooks(storeDir, repoPath, false); hookErr != nil && err == nil {
				err = hookErr
			}
		}
		if err != nil {
			fmt.Printf("  ✗ %s: %s\n", name, err)
		} else if filename != "" {
			fmt.Printf("  ✓ %s - removed from %s\n", name, filename)
		} else {
			fmt.Printf("  - %s - no instruction found\n", name)
		}
	}

	// remove from MCP clients (already detected above, results stored)
	for _, r := range clientResults {
		if r.Err != nil {
			fmt.Printf("  ✗ %s: %s\n", r.Name, r.Err)
		} else {
			fmt.Printf("  ✓ %s removed\n", r.Name)
		}
	}

	_ = mcpinternal.ClearMCPConfig()
	fmt.Println("\nRepoGuide MCP uninstalled.")
	return nil
}

func instructAndActivate(repoPath string) error {
	filename, err := mcpinternal.ActivateMCPRepoWithInstructions(repoPath)
	if err != nil {
		return err
	}
	if claudeFile, claudeErr := mcpinternal.InstructRepoForClaude(repoPath); claudeErr == nil {
		filename = filename + " and " + claudeFile
	}
	fmt.Printf("Added RepoGuide MCP instruction to %s.\n\nThe agent should call repoguide_get_repo_experience once per non-trivial task/session, not once per message.\n", filename)
	return nil
}

// --- mcp install TUI ---

type mcpInstallView int

const (
	mcpInstallLoading mcpInstallView = iota
	mcpInstallSelect
	mcpInstallConfirm
	mcpInstallApplying     // enabling MCP for selected repos
	mcpInstallClientSelect // choose which MCP clients to install
	mcpInstallClients      // installing selected MCP clients
	mcpInstallPermission   // ask to allow repoguide_get_repo_experience in Claude Code
	mcpInstallComplete     // final screen
)

type mcpInstallModel struct {
	view              mcpInstallView
	spinner           spinner.Model
	width             int
	height            int
	repos             []string
	cfg               mcpinternal.MCPConfig
	cursor            int
	checked           map[int]bool
	selected          []string // repos from select screen
	confirmIdx        int
	confirmYes        bool
	confirmed         []string // repos user said yes to
	currentRepo       string
	repoResults       []mcpInstallResult
	detectedClients   []string
	clientChecked     map[int]bool
	clientCursor      int
	clientResults     []mcpinternal.MCPClientResult
	clientFallback    string
	vp                viewport.Model
	completeCursor    int // 0 = done, 1 = run mcp test
	runTest           bool
	permissionYes     bool
	permissionGranted bool
	feedbackEnabled   bool
}

type mcpInstallResult struct {
	path     string
	filename string
	err      error
}

type mcpReposLoadedMsg struct {
	repos       []string
	cfg         mcpinternal.MCPConfig
	currentRepo string // non-empty if pwd is an initialized repoguide repo
}

type mcpApplyDoneMsg struct {
	results []mcpInstallResult
}

type mcpClientsDoneMsg struct {
	results  []mcpinternal.MCPClientResult
	fallback string
}

func newMCPInstallModel() mcpInstallModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return mcpInstallModel{
		view:          mcpInstallLoading,
		spinner:       sp,
		checked:       make(map[int]bool),
		clientChecked: make(map[int]bool),
		confirmYes:    true,
		permissionYes: true,
	}
}

func (m mcpInstallModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		all, _ := sessionimport.LoadAllSessionRepoRoots()
		var repos []string
		for _, r := range all {
			if repopkg.IsRepoInitialized(r) {
				repos = append(repos, r)
			}
		}
		cfg, _ := mcpinternal.LoadMCPConfig()
		currentRepo, _ := internal.CurrentRepoRoot()
		if !repopkg.IsRepoInitialized(currentRepo) {
			currentRepo = ""
		}
		return mcpReposLoadedMsg{repos: repos, cfg: cfg, currentRepo: currentRepo}
	})
}

func (m mcpInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = newDetailViewport(m.width, m.height)
		if m.view == mcpInstallComplete {
			m.refreshCompleteViewport()
		}
		return m, nil
	case spinner.TickMsg:
		if m.view == mcpInstallLoading || m.view == mcpInstallApplying || m.view == mcpInstallClients {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case mcpReposLoadedMsg:
		m.repos = msg.repos
		m.cfg = msg.cfg
		m.currentRepo = msg.currentRepo
		for i, r := range m.repos {
			if mcpinternal.IsRepoActivated(m.cfg, r) {
				m.checked[i] = true
			}
		}
		if msg.currentRepo != "" {
			m.selected = []string{msg.currentRepo}
			m.confirmIdx = 0
			m.confirmYes = true
			m.confirmed = nil
			m.view = mcpInstallConfirm
		} else {
			m.view = mcpInstallSelect
		}
	case mcpApplyDoneMsg:
		m.repoResults = msg.results
		detected := mcpinternal.DetectMCPClients()
		m.detectedClients = detected
		m.clientChecked = make(map[int]bool, len(detected))
		for i := range detected {
			m.clientChecked[i] = true // all selected by default
		}
		m.clientCursor = 0
		if len(detected) == 0 {
			m.view = mcpInstallClients
			return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
				results, fallback := mcpinternal.InstallSelectedMCPClients(nil, true)
				return mcpClientsDoneMsg{results: results, fallback: fallback}
			})
		}
		m.view = mcpInstallClientSelect
	case mcpClientsDoneMsg:
		m.clientResults = msg.results
		m.clientFallback = msg.fallback
		if m.claudeInstalled() {
			_ = mcpinternal.AddClaudeCodePermission()
			m.permissionGranted = true
		}
		m.enableFeedback()
		m.view = mcpInstallComplete
		m.refreshCompleteViewport()
	case tea.KeyMsg:
		switch m.view {
		case mcpInstallSelect:
			return m.updateSelect(msg)
		case mcpInstallConfirm:
			return m.updateConfirm(msg)
		case mcpInstallClientSelect:
			return m.updateClientSelect(msg)
		case mcpInstallPermission:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				m.permissionYes = true
			case "down", "j":
				m.permissionYes = false
			case "enter":
				if m.permissionYes {
					_ = mcpinternal.AddClaudeCodePermission()
					m.permissionGranted = true
				}
				m.enableFeedback()
				m.view = mcpInstallComplete
				m.refreshCompleteViewport()
			}
			return m, nil
		case mcpInstallComplete:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.completeCursor > 0 {
					m.completeCursor--
				}
			case "down", "j":
				if m.completeCursor < 1 {
					m.completeCursor++
				}
			case "enter":
				if m.completeCursor == 1 {
					m.runTest = true
				}
				return m, tea.Quit
			default:
				if vp, handled := updateViewportKeys(m.vp, msg); handled {
					m.vp = vp
					return m, nil
				}
				vp, cmd := m.vp.Update(msg)
				m.vp = vp
				return m, cmd
			}
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m mcpInstallModel) updateSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.repos)-1 {
			m.cursor++
		}
	case "enter":
		m.checked[m.cursor] = !m.checked[m.cursor]
	case " ":
		sel := make([]string, 0)
		for i, r := range m.repos {
			if m.checked[i] {
				sel = append(sel, r)
			}
		}
		if len(sel) == 0 {
			return m, tea.Quit
		}
		m.selected = sel
		m.confirmIdx = 0
		m.confirmYes = true
		m.confirmed = nil
		m.view = mcpInstallConfirm
	}
	return m, nil
}

func (m mcpInstallModel) updateClientSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		if m.clientCursor > 0 {
			m.clientCursor--
		}
	case "down", "j":
		if m.clientCursor < len(m.detectedClients)-1 {
			m.clientCursor++
		}
	case "enter":
		m.clientChecked[m.clientCursor] = !m.clientChecked[m.clientCursor]
	case " ":
		var sel []string
		for i, name := range m.detectedClients {
			if m.clientChecked[i] {
				sel = append(sel, name)
			}
		}
		currentRepo := m.currentRepo
		m.view = mcpInstallClients
		return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
			claudeSelected := false
			for _, name := range sel {
				if name == "Claude Code" {
					claudeSelected = true
					break
				}
			}
			if claudeSelected && currentRepo != "" {
				_, _ = mcpinternal.InstructRepoForClaude(currentRepo)
			}
			results, fallback := mcpinternal.InstallSelectedMCPClients(sel, true)
			return mcpClientsDoneMsg{results: results, fallback: fallback}
		})
	}
	return m, nil
}

func (m mcpInstallModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		m.view = mcpInstallSelect
	case "up", "k", "down", "j":
		m.confirmYes = !m.confirmYes
	case "enter":
		if m.confirmYes {
			m.confirmed = append(m.confirmed, m.selected[m.confirmIdx])
		}
		m.confirmIdx++
		m.confirmYes = true
		if m.confirmIdx >= len(m.selected) {
			if len(m.confirmed) == 0 {
				return m, tea.Quit
			}
			confirmed := m.confirmed
			m.view = mcpInstallApplying
			return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
				results := make([]mcpInstallResult, 0, len(confirmed))
				for _, repoPath := range confirmed {
					_, initErr := internal.InitRepoAt(repoPath, internal.InitOptions{})
					var filename string
					var err error
					if initErr != nil {
						err = initErr
					} else {
						err = mcpinternal.ActivateMCPRepo(repoPath)
						if err == nil {
							filename = "MCP enabled"
						}
					}
					results = append(results, mcpInstallResult{path: repoPath, filename: filename, err: err})
				}
				return mcpApplyDoneMsg{results: results}
			})
		}
	}
	return m, nil
}

func (m mcpInstallModel) View() string {
	switch m.view {
	case mcpInstallLoading:
		return lipgloss.NewStyle().Padding(1, 2).Render(
			titleStyle.Render("RepoGuide MCP install") + "\n\n" + m.spinner.View() + " Loading repos...",
		)
	case mcpInstallApplying:
		return lipgloss.NewStyle().Padding(1, 2).Render(
			titleStyle.Render("RepoGuide MCP install") + "\n\n" + m.spinner.View() + " Enabling RepoGuide MCP...",
		)
	case mcpInstallClients:
		return lipgloss.NewStyle().Padding(1, 2).Render(
			titleStyle.Render("RepoGuide MCP install") + "\n\n" + m.spinner.View() + " Detecting MCP clients...",
		)
	case mcpInstallSelect:
		return m.viewSelect()
	case mcpInstallConfirm:
		return m.viewConfirm()
	case mcpInstallClientSelect:
		return m.viewClientSelect()
	case mcpInstallPermission:
		return m.viewPermission()
	case mcpInstallComplete:
		return m.viewComplete()
	}
	return ""
}

func (m mcpInstallModel) claudeInstalled() bool {
	for _, r := range m.clientResults {
		if r.Name == "Claude Code" && r.Configured {
			return true
		}
	}
	return false
}

func (m mcpInstallModel) viewPermission() string {
	yesLine := "  Yes, allow (recommended)"
	noLine := "  No, skip"
	if m.permissionYes {
		yesLine = selected.Render("› Yes, allow (recommended)")
	} else {
		noLine = selected.Render("› No, skip")
	}
	lines := []string{
		titleStyle.Render("Allow RepoGuide in Claude Code"),
		"",
		"Allow " + headStyle.Render("repoguide_get_repo_experience") + " to run without prompting?",
		muted.Render("This tool runs at the start of every task to orient the agent."),
		muted.Render("Without this, Claude Code will ask you to approve it each time."),
		"",
		yesLine,
		noLine,
		"",
		muted.Render(footerHint("↑↓ select", "enter confirm", "q quit")),
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

func (m *mcpInstallModel) enableFeedback() {
	if m.claudeInstalled() {
		_ = mcpinternal.AddClaudeCodeFeedbackPermission()
	}
	m.feedbackEnabled = true
}

func (m mcpInstallModel) viewClientSelect() string {
	lines := []string{titleStyle.Render("Select MCP clients to configure") + "  " + muted.Render("[SPACE to continue]"), ""}
	claudeIdx := -1
	for i, name := range m.detectedClients {
		check := "[ ]"
		if m.clientChecked[i] {
			check = "[✓]"
		}
		if name == "Claude Code" {
			claudeIdx = i
		}
		line := check + " " + name
		if i == m.clientCursor {
			lines = append(lines, selected.Render("› "+line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	if claudeIdx >= 0 && m.clientChecked[claudeIdx] {
		lines = append(lines, "", muted.Render("Claude Code selected: will also install RepoGuide hooks (not a CLAUDE.md block) in confirmed repos."))
	}
	lines = append(lines, "", muted.Render("enter toggle  •  space continue  •  q quit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

func (m mcpInstallModel) viewSelect() string {
	if len(m.repos) == 0 {
		return lipgloss.NewStyle().Padding(1, 2).Render(
			titleStyle.Render("RepoGuide MCP install") + "\n\nNo repos found in sessions.\n\n" + muted.Render("q quit"),
		)
	}
	lines := []string{titleStyle.Render("Select repos to enable RepoGuide MCP"), ""}
	for i, repoPath := range m.repos {
		check := "[ ]"
		if m.checked[i] {
			check = "[✓]"
		}
		name := filepath.Base(repoPath)
		line := fmt.Sprintf("%s %s  %s", check, name, muted.Render(renderRepoPath(repoPath)))
		if i == m.cursor {
			lines = append(lines, selected.Render("› "+line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	lines = append(lines, "", muted.Render(footerHint("enter toggle", "space continue", "q quit")))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

func (m mcpInstallModel) viewConfirm() string {
	if m.confirmIdx >= len(m.selected) {
		return ""
	}
	repoPath := m.selected[m.confirmIdx]
	name := filepath.Base(repoPath)
	progress := fmt.Sprintf("%d / %d", m.confirmIdx+1, len(m.selected))

	yesLine := "  Yes"
	noLine := "  No"
	if m.confirmYes {
		yesLine = selected.Render("› Yes")
	} else {
		noLine = selected.Render("› No")
	}

	lines := []string{
		titleStyle.Render("RepoGuide MCP install") + "  " + muted.Render(progress),
		"",
		fmt.Sprintf("Enable RepoGuide MCP for %s?", headStyle.Render(name)),
		muted.Render(renderRepoPath(repoPath)),
		muted.Render("This installs RepoGuide MCP and hooks without editing in-repo agent docs."),
		"",
		yesLine,
		noLine,
		"",
		muted.Render(footerHint("↑↓ select", "enter confirm", "q back")),
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

func (m mcpInstallModel) viewComplete() string {
	nextOpts := []string{"Done", "Run: repoguide mcp test"}
	var optLines []string
	for i, opt := range nextOpts {
		if i == m.completeCursor {
			optLines = append(optLines, selected.Render("› "+opt))
		} else {
			optLines = append(optLines, "  "+opt)
		}
	}
	nextSection := headStyle.Render("Next:") + "\n" + strings.Join(optLines, "\n")

	content := m.completeContent()
	if m.vp.Width == 0 || m.vp.Height == 0 {
		return lipgloss.NewStyle().Padding(1, 2).Render(content + "\n\n" + nextSection + "\n\n" + muted.Render("↑/↓ select  •  enter confirm  •  q quit"))
	}
	hint := muted.Render(footerHint("↑/↓ select", "pgup/pgdn scroll", "enter confirm", "q quit") + scrollHint(m.vp))
	return lipgloss.NewStyle().Padding(1, 2).Render(m.vp.View() + "\n\n" + nextSection + "\n\n" + hint)
}

func runMCPInstall(cmd *cobra.Command, _ []string) error {
	if approve, _ := cmd.Flags().GetBool("approve"); approve {
		noHooks, _ := cmd.Flags().GetBool("no-hooks")
		return runMCPInstallApprove(noHooks)
	}
	final, err := tea.NewProgram(newMCPInstallModel(), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if m, ok := final.(mcpInstallModel); ok && m.runTest {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		c := exec.Command(executable, "mcp", "test")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		return c.Run()
	}
	return nil
}

func runMCPInstallApprove(noHooks bool) error {
	var repos []string
	clientOnly := os.Getenv("REPOGUIDE_MCP_CLIENTS_ONLY") == "1"
	if !clientOnly {
		all, _ := sessionimport.LoadAllSessionRepoRoots()
		for _, r := range all {
			if repopkg.IsRepoInitialized(r) {
				repos = append(repos, r)
			}
		}
		// prefer just the current repo if it's initialized
		if cur, err := internal.CurrentRepoRoot(); err == nil && repopkg.IsRepoInitialized(cur) {
			repos = []string{cur}
		}
	}
	if len(repos) == 0 {
		fmt.Println("Installing MCP client integration only.")
	}

	for _, repoPath := range repos {
		if _, err := internal.InitRepoAt(repoPath, internal.InitOptions{}); err != nil {
			fmt.Printf("  ✗ %s: %s\n", filepath.Base(repoPath), err)
			continue
		}
		err := mcpinternal.ActivateMCPRepo(repoPath)
		if err != nil {
			fmt.Printf("  ✗ %s: %s\n", filepath.Base(repoPath), err)
		} else {
			fmt.Printf("  ✓ %s - MCP enabled\n", filepath.Base(repoPath))
		}
	}

	detected := mcpinternal.DetectMCPClients()
	claudeDetected := false
	var filtered []string
	for _, name := range detected {
		if name == "Claude Code" {
			claudeDetected = true
		}
		filtered = append(filtered, name)
	}
	if claudeDetected && len(repos) > 0 && !noHooks {
		_, _ = mcpinternal.InstructRepoForClaude(repos[0])
	}
	results, fallback := mcpinternal.InstallSelectedMCPClients(filtered, !noHooks)
	for _, r := range results {
		if r.Configured {
			fmt.Printf("  ✓ %s configured\n", r.Name)
		} else {
			fmt.Printf("  ✗ %s: %s\n", r.Name, r.Err)
		}
	}
	if fallback != "" {
		fmt.Println(fallback)
	}

	if !noHooks {
		_ = mcpinternal.AddClaudeCodePermission()
		_ = mcpinternal.AddClaudeCodeFeedbackPermission()
	}
	fmt.Println("\nRepoGuide MCP installed.")
	return nil
}

func (m *mcpInstallModel) refreshCompleteViewport() {
	if m.vp.Width == 0 || m.vp.Height == 0 {
		return
	}
	m.vp.SetContent(m.completeContent())
	m.vp.GotoTop()
}

func (m mcpInstallModel) completeContent() string {
	lines := []string{titleStyle.Render("RepoGuide MCP install"), ""}

	if len(m.clientResults) > 0 {
		lines = append(lines, headStyle.Render("Detected MCP clients:"), "")
		for _, r := range m.clientResults {
			if r.Configured {
				lines = append(lines, "  "+selected.Render("✓")+" "+r.Name)
			} else {
				lines = append(lines, "  "+dangerStyle.Render("✗")+" "+r.Name+muted.Render(": "+r.Err.Error()))
			}
		}
		lines = append(lines, "")
	}

	if m.permissionGranted {
		lines = append(lines, "  "+selected.Render("✓")+" repoguide_get_repo_experience allowed in Claude Code", "")
	}
	if m.feedbackEnabled {
		lines = append(lines, "  "+selected.Render("✓")+" repoguide_record_feedback enabled", "")
	}

	anyClientOK := false
	for _, r := range m.clientResults {
		if r.Configured {
			anyClientOK = true
			detail := []string{
				"  " + selected.Render("✓") + " " + headStyle.Render(r.Name+" configured"),
				"    " + muted.Render("server:  ") + "repoguide",
			}
			if r.ConfigPath != "" {
				detail = append(detail, "    "+muted.Render("config:  ")+displayPath(r.ConfigPath))
			} else {
				detail = append(detail, "    "+muted.Render("command: ")+"repoguide mcp serve")
			}
			lines = append(lines, detail...)
			lines = append(lines, "")
		}
	}

	if m.clientFallback != "" {
		lines = append(lines,
			headStyle.Render("No MCP clients detected. Add manually:"),
			"",
		)
		for l := range strings.SplitSeq(m.clientFallback, "\n") {
			lines = append(lines, "  "+muted.Render(l))
		}
		lines = append(lines, "")
	}

	if anyClientOK || m.clientFallback == "" {
		lines = append(lines, selected.Render("RepoGuide MCP installed."), "")
	}

	lines = append(lines,
		headStyle.Render("Next:"),
		"  Restart your agent/editor.",
	)

	return strings.Join(lines, "\n")
}
