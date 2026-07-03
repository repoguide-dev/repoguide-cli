package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
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
	root.AddCommand(reposCmd)
}

var reposCmd = &cobra.Command{
	Use:   "repos",
	Short: "Browse configured RepoGuide repositories",
	RunE:  runRepos,
}

func runRepos(_ *cobra.Command, _ []string) error {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		stats, err := sessionimport.LoadRepoSessionStats()
		if err != nil {
			return err
		}
		if len(stats) == 0 {
			fmt.Println("No repos found.")
			return nil
		}
		for _, stat := range stats {
			status := "unconfigured"
			if stat.Configured {
				status = "configured"
			}
			fmt.Printf("%s\t%s\t%s\t%d\t%d\t%d\n",
				repoDisplayName(stat.Repo),
				stat.Repo.RepoRoot,
				status,
				stat.Total,
				stat.AgentCounts["codex"],
				stat.AgentCounts["claude"],
			)
		}
		return nil
	}

	model := newReposModel()
	_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

type reposView int

const (
	reposListView reposView = iota
	reposDetailView
	reposSessionsView
)

type reposLoadedMsg struct {
	stats []sessionimport.RepoSessionStats
	err   error
}

type repoInitDoneMsg struct {
	repoRoot string
	err      error
}

type repoRemovedMsg struct {
	repoRoot string
	repoID   string
	err      error
}

type reposModel struct {
	view          reposView
	width         int
	height        int
	spinner       spinner.Model
	table         table.Model
	loading       bool
	err           error
	statusMessage string
	repos         []sessionimport.RepoSessionStats
	agentCursor   int
	sessionsChild sessionsModel
	confirmDelete bool
	focusRepoRoot string // if set, jump to this repo's detail view after load
}

func newReposModel() reposModel {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return reposModel{
		view:    reposListView,
		spinner: spin,
		table:   newSessionTable(),
		loading: true,
	}
}

func (m *reposModel) applyRepoTableData() {
	cols := repoTableColumns(m.width - 4)
	tcols := make([]table.Column, len(cols))
	for i, c := range cols {
		tcols[i] = table.Column{Title: c.title, Width: c.width}
	}
	rows := make([]table.Row, len(m.repos))
	for i, r := range m.repos {
		rows[i] = table.Row{
			repoDisplayName(r.Repo),
			repoStatusLabel(r),
			renderRepoPath(r.Repo.RepoRoot),
			fmt.Sprintf("%d", r.Total),
			fmt.Sprintf("%d", r.AgentCounts["codex"]),
			fmt.Sprintf("%d", r.AgentCounts["claude"]),
		}
	}
	m.table.SetColumns(tcols)
	m.table.SetRows(rows)
}

func (m *reposModel) applyRepoTableSize() {
	if m.height > 0 {
		m.table.SetHeight(max(5, m.height-6))
	}
	if m.width > 0 {
		m.table.SetWidth(m.width - 4)
	}
}

func (m reposModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadRepoStatsCmd())
}

func (m reposModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = sizeMsg.Width
		m.height = sizeMsg.Height
		m.applyRepoTableSize()
		if len(m.repos) > 0 {
			m.applyRepoTableData()
		}
	}

	if m.view == reposSessionsView {
		child, cmd := m.sessionsChild.Update(msg)
		m.sessionsChild = child.(sessionsModel)
		if m.sessionsChild.closed {
			m.sessionsChild.closed = false
			m.view = reposDetailView
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case reposLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.repos = msg.stats
		m.applyRepoTableSize()
		m.applyRepoTableData()
		if m.focusRepoRoot != "" {
			for i, r := range m.repos {
				if r.Repo.RepoRoot == m.focusRepoRoot {
					m.table.SetCursor(i)
					m.agentCursor = 0
					m.view = reposDetailView
					break
				}
			}
			m.focusRepoRoot = ""
		}
		return m, nil
	case repoInitDoneMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.statusMessage = fmt.Sprintf("Initialized %s", renderRepoPath(msg.repoRoot))
			return m, tea.Batch(m.spinner.Tick, loadRepoStatsCmd())
		}
		return m, nil
	case repoRemovedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.statusMessage = fmt.Sprintf("Removed %s", renderRepoPath(msg.repoRoot))
			return m, tea.Batch(m.spinner.Tick, loadRepoStatsCmd())
		}
		return m, nil
	case tea.KeyMsg:
		switch m.view {
		case reposListView:
			return m.updateListView(msg)
		case reposDetailView:
			return m.updateDetailView(msg)
		}
	}

	return m, nil
}

func (m reposModel) updateListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmDelete {
		switch msg.String() {
		case "y":
			repo := m.selectedRepo()
			m.confirmDelete = false
			if repo == nil || !repo.Configured {
				return m, nil
			}
			m.statusMessage = ""
			m.loading = true
			m.err = nil
			return m, tea.Batch(m.spinner.Tick, removeRepoCmd(repo.Repo.RepoRoot, repo.Repo.RepoID))
		default:
			m.confirmDelete = false
		}
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		m.table.MoveUp(1)
	case "down", "j":
		m.table.MoveDown(1)
	case "enter":
		if len(m.repos) > 0 {
			m.agentCursor = 0
			m.view = reposDetailView
		}
	case "r":
		m.statusMessage = ""
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, loadRepoStatsCmd())
	case "i":
		repo := m.selectedRepo()
		if repo == nil || repo.Configured {
			return m, nil
		}
		m.statusMessage = ""
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, initRepoCmd(repo.Repo.RepoRoot))
	case "d":
		repo := m.selectedRepo()
		if repo == nil || !repo.Configured {
			return m, nil
		}
		m.confirmDelete = true
		m.statusMessage = ""
	}
	return m, nil
}

func (m reposModel) updateDetailView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace", "q":
		m.view = reposListView
	case "up", "k":
		if m.agentCursor > 0 {
			m.agentCursor--
		}
	case "down", "j":
		if m.agentCursor < len(sessionimport.SupportedSessionAgents())-1 {
			m.agentCursor++
		}
	case "enter":
		repo := m.selectedRepo()
		if repo == nil {
			return m, nil
		}
		agent := sessionimport.SupportedSessionAgents()[m.agentCursor]
		if repo.AgentCounts[agent] == 0 {
			return m, nil
		}
		m.sessionsChild = newSessionsModel(agent, sessionsModelOptions{
			repoFilter:   repo.Repo.RepoRoot,
			embedded:     true,
			parentFooter: footerHint("enter open", "esc back", "r reload", "q back"),
		})
		m.view = reposSessionsView
		return m, m.sessionsChild.Init()
	case "i":
		repo := m.selectedRepo()
		if repo == nil {
			return m, nil
		}
		m.statusMessage = ""
		m.loading = true
		m.err = nil
		m.view = reposListView
		return m, tea.Batch(m.spinner.Tick, initRepoCmd(repo.Repo.RepoRoot))
	}
	return m, nil
}

func (m reposModel) selectedRepo() *sessionimport.RepoSessionStats {
	c := m.table.Cursor()
	if len(m.repos) == 0 || c < 0 || c >= len(m.repos) {
		return nil
	}
	return &m.repos[c]
}

func loadRepoStatsCmd() tea.Cmd {
	return func() tea.Msg {
		stats, err := sessionimport.LoadRepoSessionStats()
		if err != nil {
			return reposLoadedMsg{stats: stats, err: err}
		}
		token, ok := clientauth.Load()
		if ok {
			client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
			for i := range stats {
				if stats[i].Repo.Mode == "local" {
					continue
				}
				info, _ := client.GetRepo(stats[i].Repo.RepoID)
				if info != nil {
					stats[i].Online = true
					stats[i].LastSynced = info.LastSynced
				}
			}
		}
		return reposLoadedMsg{stats: stats, err: nil}
	}
}

func initRepoCmd(repoRoot string) tea.Cmd {
	return func() tea.Msg {
		result, err := internal.InitRepoAt(repoRoot, internal.InitOptions{})
		if err == nil {
			err = syncRepoRegistration(result)
		}
		return repoInitDoneMsg{repoRoot: repoRoot, err: err}
	}
}

func removeRepoCmd(repoRoot, repoID string) tea.Cmd {
	return func() tea.Msg {
		result, err := repopkg.RemoveTrackedRepoAt(repoRoot)
		if err != nil {
			return repoRemovedMsg{repoRoot: repoRoot, repoID: repoID, err: err}
		}
		if result.Mode != "local" {
			if token, ok := clientauth.Load(); ok && result.RepoID != "" {
				_ = (sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}).DeleteRepo(result.RepoID)
			}
		}
		return repoRemovedMsg{repoRoot: result.RepoRoot, repoID: result.RepoID, err: nil}
	}
}

func syncRepoRegistration(result internal.InitResult) error {
	token, ok := clientauth.Load()
	if !ok {
		return nil
	}
	client := sessionimport.CloudClient{
		BaseURL: getBackendURL(),
		Token:   token.Token,
	}
	if err := client.RegisterRepo(result.RepoID, result.RepoRoot); err != nil {
		return err
	}
	if err := client.UploadRepoEvents(result.RepoID, result.RepoRoot); err != nil {
		return err
	}
	return nil
}
