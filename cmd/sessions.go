package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	sessionsCmd.Flags().String("agent", "", "Skip agent selection and open sessions for a specific agent")
	sessionsCmd.Flags().String("repo", "", "Filter sessions to a specific repo name, repo ID, or path")
	sessionsCmd.Flags().Bool("analyze", false, "Build and cache normalized events and session analysis")
	root.AddCommand(sessionsCmd)
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions [session-id]",
	Short: "Browse recorded AI coding sessions",
	RunE:  runSessions,
}

func runSessions(cmd *cobra.Command, args []string) error {
	agent, _ := cmd.Flags().GetString("agent")
	agent = strings.ToLower(strings.TrimSpace(agent))
	repoFilter, _ := cmd.Flags().GetString("repo")
	repoFilter = strings.TrimSpace(repoFilter)
	if repoFilter == "" {
		repoFilter = detectCwdGitRoot()
	}

	if agent != "" && !isSupportedAgent(agent) {
		return fmt.Errorf("unsupported agent %q", agent)
	}
	if len(args) > 1 {
		return fmt.Errorf("accepts at most 1 arg(s), received %d", len(args))
	}
	analyze, _ := cmd.Flags().GetBool("analyze")
	if len(args) == 1 {
		session, err := findSession(args[0], agent)
		if err != nil {
			return err
		}
		if analyze {
			artifacts, err := sessionimport.BuildSessionArtifacts(session)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(sessionAnalysisLines(artifacts), "\n"))
			return nil
		}
		if !isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(sessionDetailLines(session, false), "\n"))
			return nil
		}
		model := newSessionDetailModel(session)
		_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
		return err
	}

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		if agent == "" {
			agent = "all"
		}
		var page sessionimport.SessionPage
		var err error
		if agent == "all" {
			page, err = sessionimport.LoadAllAgentsSessionPage(0, -1, sessionimport.SessionLoadOptions{Repo: repoFilter})
		} else {
			page, err = sessionimport.LoadSessionPage(agent, 0, -1, sessionimport.SessionLoadOptions{Repo: repoFilter})
		}
		if err != nil {
			return err
		}
		for _, session := range page.Sessions {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n",
				session.Timestamp.Format("2006-01-02 15:04"),
				sessionLabel(session),
				valueOrFallback(repoDisplayText(session), valueOrFallback(session.Cwd, "-")),
				valueOrFallback(session.Model, "-"),
				session.Path,
			)
		}
		return nil
	}

	model := newSessionsModel(agent, sessionsModelOptions{repoFilter: repoFilter})
	_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

func newSessionDetailModel(session internal.SessionSummary) sessionsModel {
	m := sessionsModel{
		view:     viewDetail,
		agent:    session.Agent,
		sessions: []internal.SessionSummary{session},
		total:    1,
		width:    80,
		height:   24,
	}
	cached, ok, _ := sessionimport.LoadCachedSessionAnalysis(session)
	if ok {
		analysis := cached.Analysis
		m.analysis = &analysis
		m.analysisPath = cached.AnalysisPath
	}
	m.applyDetailViewport()
	m.refreshDetailContent()
	return m
}

func findSession(sessionID, agent string) (internal.SessionSummary, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return internal.SessionSummary{}, fmt.Errorf("session ID is required")
	}
	agents := []string{agent}
	if agent == "" {
		agents = sessionimport.SupportedSessionAgents()
	}

	for _, candidate := range agents {
		page, err := sessionimport.LoadSessionPage(candidate, 0, -1, sessionimport.SessionLoadOptions{})
		if err != nil {
			continue
		}
		for _, session := range page.Sessions {
			if session.ID == sessionID {
				return session, nil
			}
		}
	}

	if agent != "" {
		return internal.SessionSummary{}, fmt.Errorf("session %q not found for agent %q", sessionID, agent)
	}
	return internal.SessionSummary{}, fmt.Errorf("session %q not found", sessionID)
}

type sessionsView int

const (
	viewAgent sessionsView = iota
	viewList
	viewDetail
	viewPromptDetail
)

type sessionsLoadedMsg struct {
	agent  string
	page   sessionimport.SessionPage
	offset int
	err    error
}

type sessionAnalysisBuiltMsg struct {
	sessionID string
	artifacts sessionimport.SessionArtifacts
	err       error
}

type sessionsModel struct {
	embedded           bool
	closed             bool
	returnOnDetailBack bool
	titleOverride      string
	repoFilter         string
	defaultRepoFilter  string
	parentFooter       string
	view               sessionsView
	width              int
	height             int
	spinner            spinner.Model
	table              table.Model
	detailVP           viewport.Model
	agents             []string
	agentCounts        map[string]int
	agentCursor        int
	agent              string
	sessions           []internal.SessionSummary
	total              int
	pageOffset         int
	pageSize           int
	sessionCursor      int // desired cursor after async load; use m.table.Cursor() for current
	promptCursor       int
	promptExpanded     bool
	loading            bool
	err                error
	detailLoading      bool
	detailErr          error
	analysisPath       string
	analysis           *internal.SessionAnalysis
}

type sessionsModelOptions struct {
	repoFilter         string
	embedded           bool
	returnOnDetailBack bool
	parentFooter       string
	preSeededSessions  []internal.SessionSummary
	titleOverride      string
}

func newSessionsModel(agent string, opts sessionsModelOptions) sessionsModel {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	agentCounts := loadAgentCounts()
	m := sessionsModel{
		spinner:            spin,
		table:              newSessionTable(),
		view:               viewAgent,
		agents:             sessionimport.SupportedSessionAgents(),
		agentCounts:        agentCounts,
		pageSize:           20,
		repoFilter:         opts.repoFilter,
		defaultRepoFilter:  opts.repoFilter,
		embedded:           opts.embedded,
		returnOnDetailBack: opts.returnOnDetailBack,
		parentFooter:       opts.parentFooter,
		titleOverride:      opts.titleOverride,
	}
	if len(opts.preSeededSessions) > 0 {
		m.agent = "all"
		m.sessions = opts.preSeededSessions
		m.total = len(opts.preSeededSessions)
		m.view = viewList
		return m
	}
	if agent != "" {
		m.agent = agent
		m.view = viewList
		m.loading = true
		return m
	}
	if opts.repoFilter != "" {
		// ponytail: skip agent picker when in a repo context - show all agents merged
		m.agent = "all"
		m.view = viewList
		m.loading = true
		return m
	}
	if autoAgent, ok := singleAvailableAgent(agentCounts); ok {
		m.agent = autoAgent
		m.view = viewList
		m.loading = true
	}
	return m
}

func (m sessionsModel) Init() tea.Cmd {
	if m.loading {
		return tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, 0, m.pageSize))
	}
	if len(m.sessions) > 0 {
		cmds := make([]tea.Cmd, 0, len(m.sessions)+1)
		cmds = append(cmds, m.spinner.Tick)
		for _, s := range m.sessions {
			cmds = append(cmds, buildSessionAnalysisCmd(s))
		}
		return tea.Batch(cmds...)
	}
	return nil
}

func (m sessionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyTableSize()
		m.applyDetailViewport()
		if len(m.sessions) > 0 {
			m.applyTableData()
		}
		size := m.currentPageSize()
		if size != m.pageSize {
			m.pageSize = size
			if m.agent != "" && !m.loading {
				return m, tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, m.pageOffset, m.pageSize))
			}
		}
		return m, nil
	case spinner.TickMsg:
		if !m.loading && !m.detailLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case sessionsLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.sessions = msg.page.Sessions
		m.total = msg.page.Total
		m.pageOffset = msg.page.Offset
		if m.sessionCursor >= len(m.sessions) {
			m.sessionCursor = max(0, len(m.sessions)-1)
		}
		if msg.err == nil {
			m.view = viewList
			m.applyTableSize()
			m.applyTableData()
			cmds := make([]tea.Cmd, 0, len(m.sessions))
			for _, s := range m.sessions {
				cmds = append(cmds, buildSessionAnalysisCmd(s))
			}
			return m, tea.Batch(cmds...)
		}
		return m, nil
	case sessionAnalysisBuiltMsg:
		// always update list stats when analysis completes
		for i, s := range m.sessions {
			if s.ID == msg.sessionID {
				if msg.err == nil {
					m.sessions[i].CostUSD = msg.artifacts.Analysis.Metrics.EstimatedCostUSD
					m.sessions[i].ReadFileCount = msg.artifacts.Analysis.Metrics.ReadFileCount
					m.sessions[i].EditFileCount = msg.artifacts.Analysis.Metrics.EditedFileCount
				}
				m.applyTableData()
				break
			}
		}
		// also update detail view if this is the active session
		if m.currentSessionID() == msg.sessionID {
			m.detailLoading = false
			m.detailErr = msg.err
			if msg.err == nil {
				analysis := msg.artifacts.Analysis
				m.analysis = &analysis
				m.analysisPath = msg.artifacts.AnalysisPath
				m.refreshDetailContent()
			}
		}
		return m, nil
	case tea.KeyMsg:
		switch m.view {
		case viewAgent:
			return m.updateAgentView(msg)
		case viewList:
			return m.updateListView(msg)
		case viewDetail:
			return m.updateDetailView(msg)
		case viewPromptDetail:
			return m.updatePromptDetailView(msg)
		}
	}
	return m, nil
}

func (m sessionsModel) updateAgentView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.embedded {
			m.closed = true
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		if m.agentCursor > 0 {
			m.agentCursor--
		}
	case "down", "j":
		if m.agentCursor < len(m.agents)-1 {
			m.agentCursor++
		}
	case "enter":
		m.agent = m.agents[m.agentCursor]
		m.loading = true
		m.err = nil
		m.pageOffset = 0
		m.sessionCursor = 0
		return m, tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, 0, m.currentPageSize()))
	}
	return m, nil
}

func (m sessionsModel) updateListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.embedded {
			m.closed = true
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		if m.embedded {
			m.closed = true
			return m, nil
		}
		if m.agent != "" && !m.loading && m.defaultRepoFilter == "" {
			m.view = viewAgent
			m.err = nil
		} else if m.defaultRepoFilter != "" {
			return m, tea.Quit
		}
	case "up", "k":
		if m.table.Cursor() > 0 {
			m.table.MoveUp(1)
			m.sessionCursor = m.table.Cursor()
		} else if m.pageOffset > 0 {
			nextOffset := max(0, m.pageOffset-m.currentPageSize())
			m.loading = true
			m.err = nil
			m.sessionCursor = m.currentPageSize() - 1
			return m, tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, nextOffset, m.currentPageSize()))
		}
	case "down", "j":
		if m.table.Cursor() < len(m.sessions)-1 {
			m.table.MoveDown(1)
			m.sessionCursor = m.table.Cursor()
		} else if m.pageOffset+len(m.sessions) < m.total {
			m.loading = true
			m.err = nil
			m.sessionCursor = 0
			return m, tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, m.pageOffset+len(m.sessions), m.currentPageSize()))
		}
	case "enter":
		if len(m.sessions) > 0 {
			m.view = viewDetail
			m.detailErr = nil
			m.detailLoading = false
			session := m.sessions[m.table.Cursor()]
			cached, ok, _ := sessionimport.LoadCachedSessionAnalysis(session)
			if ok {
				analysis := cached.Analysis
				m.analysis = &analysis
				m.analysisPath = cached.AnalysisPath
			} else {
				m.analysis = nil
				m.analysisPath = ""
			}
			m.applyDetailViewport()
			m.refreshDetailContent()
			return m, nil
		}
	case "r":
		m.loading = true
		m.err = nil
		m.pageOffset = 0
		m.sessionCursor = 0
		return m, tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, 0, m.currentPageSize()))
	case "A":
		if m.defaultRepoFilter == "" {
			return m, nil
		}
		if m.repoFilter != "" {
			m.repoFilter = ""
		} else {
			m.repoFilter = m.defaultRepoFilter
		}
		m.loading = true
		m.err = nil
		m.pageOffset = 0
		m.sessionCursor = 0
		return m, tea.Batch(m.spinner.Tick, loadSessionsCmd(m.agent, m.repoFilter, 0, m.currentPageSize()))
	}
	return m, nil
}

func (m sessionsModel) updateDetailView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.embedded {
			m.closed = true
			return m, nil
		}
		return m, tea.Quit
	case "q", "esc", "backspace":
		if m.returnOnDetailBack {
			m.closed = true
			m.detailLoading = false
			m.detailErr = nil
			return m, nil
		}
		if msg.String() == "q" && m.embedded {
			m.closed = true
			return m, nil
		}
		m.view = viewList
		m.detailLoading = false
		m.detailErr = nil
		return m, nil
	case "up", "k":
		if m.analysis != nil && m.promptCursor > 0 {
			m.promptCursor--
			m.refreshDetailContentKeepScroll()
			return m, nil
		}
		if vp, handled := updateViewportKeys(m.detailVP, msg); handled {
			m.detailVP = vp
		}
		return m, nil
	case "down", "j":
		if m.analysis != nil && len(m.analysis.Metrics.PromptBlocks) > 0 {
			// once cursor > 0, we're in cursor mode; first step requires AtBottom
			if (m.promptCursor > 0 || m.detailVP.AtBottom()) && m.promptCursor < len(m.analysis.Metrics.PromptBlocks)-1 {
				m.promptCursor++
				m.refreshDetailContentKeepScroll()
				return m, nil
			}
		}
		if vp, handled := updateViewportKeys(m.detailVP, msg); handled {
			m.detailVP = vp
		}
		return m, nil
	case "enter":
		if m.analysis != nil && len(m.analysis.Metrics.PromptBlocks) > 0 {
			m.view = viewPromptDetail
			m.detailVP.SetContent(m.promptDetailContent())
			m.detailVP.GotoTop()
			return m, nil
		}
		if m.detailLoading || m.analysis != nil || len(m.sessions) == 0 {
			return m, nil
		}
		m.detailLoading = true
		m.detailErr = nil
		return m, tea.Batch(m.spinner.Tick, buildSessionAnalysisCmd(m.sessions[m.sessionCursor]))
	}
	if vp, handled := updateViewportKeys(m.detailVP, msg); handled {
		m.detailVP = vp
	}
	return m, nil
}

func loadSessionsCmd(agent, repoFilter string, offset, limit int) tea.Cmd {
	return func() tea.Msg {
		opts := sessionimport.SessionLoadOptions{Repo: repoFilter}
		var page sessionimport.SessionPage
		var err error
		if agent == "all" {
			page, err = sessionimport.LoadAllAgentsSessionPage(offset, limit, opts)
		} else {
			page, err = sessionimport.LoadSessionPage(agent, offset, limit, opts)
		}
		return sessionsLoadedMsg{
			agent:  agent,
			page:   page,
			offset: offset,
			err:    err,
		}
	}
}

func buildSessionAnalysisCmd(session internal.SessionSummary) tea.Cmd {
	return func() tea.Msg {
		artifacts, err := sessionimport.BuildSessionArtifacts(session)
		return sessionAnalysisBuiltMsg{
			sessionID: session.ID,
			artifacts: artifacts,
			err:       err,
		}
	}
}

func (m sessionsModel) updatePromptDetailView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.embedded {
			m.closed = true
			return m, nil
		}
		return m, tea.Quit
	case "q", "esc", "backspace":
		m.view = viewDetail
		m.refreshDetailContent()
		m.detailVP.GotoBottom()
		return m, nil
	case "e":
		m.promptExpanded = !m.promptExpanded
		m.detailVP.SetContent(m.promptDetailContent())
		m.detailVP.GotoTop()
		return m, nil
	default:
		if vp, handled := updateViewportKeys(m.detailVP, msg); handled {
			m.detailVP = vp
		}
	}
	return m, nil
}

func (m sessionsModel) currentSessionID() string {
	c := m.table.Cursor()
	if len(m.sessions) == 0 || c < 0 || c >= len(m.sessions) {
		return ""
	}
	return m.sessions[c].ID
}

func (m sessionsModel) currentPageSize() int {
	if m.height <= 0 {
		return m.pageSize
	}
	return max(10, m.height-8)
}

func repoDisplayText(session internal.SessionSummary) string {
	if session.RepoName == "" {
		return ""
	}
	if session.RepoRelativeCwd == "" {
		return session.RepoName
	}
	return session.RepoName + "/" + session.RepoRelativeCwd
}

func displayAgentName(agent string) string {
	switch strings.ToLower(agent) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "cursor":
		return "Cursor"
	case "opencode":
		return "OpenCode"
	case "copilot":
		return "GitHub Copilot"
	case "gemini":
		return "Gemini CLI"
	case "all":
		return "All"
	default:
		return agent
	}
}

// shortFilePath returns a display-friendly path relative to base, falling back to basename.
func shortFilePath(path, base string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if base != "" {
		if rel, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(path)
}

func isSupportedAgent(agent string) bool {
	for _, candidate := range sessionimport.SupportedSessionAgents() {
		if candidate == agent {
			return true
		}
	}
	return false
}

func loadAgentCounts() map[string]int {
	counts := make(map[string]int)
	for _, agent := range sessionimport.SupportedSessionAgents() {
		counts[agent] = sessionimport.QuickCountSessions(agent)
	}
	return counts
}

func detectCwdGitRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func singleAvailableAgent(counts map[string]int) (string, bool) {
	var selected string
	for _, agent := range sessionimport.SupportedSessionAgents() {
		if counts[agent] <= 0 {
			continue
		}
		if selected != "" {
			return "", false
		}
		selected = agent
	}
	return selected, selected != ""
}

func sessionLabel(session internal.SessionSummary) string {
	label := valueOrFallback(session.Name, "(untitled)")
	if session.UsedRepoGuide {
		return label + " [RepoGuide]"
	}
	return label
}

func valueOrFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
