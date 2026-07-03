package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	filesCmd.Flags().String("since", "30d", "Limit to sessions newer than duration (e.g. 7d, 30d, 90d)")
	filesCmd.Flags().Int("top", 30, "Number of files to show")
	root.AddCommand(filesCmd)
}

var filesCmd = &cobra.Command{
	Use:   "files",
	Short: "Show most-accessed files across sessions for the current repo",
	RunE:  runFiles,
}

type fileStat struct {
	sessions           int
	editSessions       int
	totalReads         int // cumulative read events across all sessions
	totalTokens        int64
	lastSeen           time.Time
	searchEditSessions int
	searchesBeforeEdit int
	readsAfterSearch   int
	searchQueries      map[string]int
}

type fileRow struct {
	path string
	stat fileStat
}

func runFiles(cmd *cobra.Command, _ []string) error {
	repoRoot := detectCwdGitRoot()
	if repoRoot == "" {
		return fmt.Errorf("not inside a git repo - files command requires a repo context")
	}

	sinceFlag, _ := cmd.Flags().GetString("since")
	topN, _ := cmd.Flags().GetInt("top")

	page, err := sessionimport.LoadAllAgentsSessionPage(0, -1, sessionimport.SessionLoadOptions{Repo: repoRoot})
	if err != nil {
		return err
	}
	sessions := page.Sessions

	if sinceFlag != "" {
		d, err := parseDuration(sinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since %q: use e.g. 30d, 7d, 24h", sinceFlag)
		}
		cutoff := time.Now().Add(-d)
		filtered := sessions[:0]
		for _, s := range sessions {
			if s.Timestamp.After(cutoff) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	toAnalyze := make([]internal.SessionSummary, 0)
	for _, s := range sessions {
		if _, ok, _ := sessionimport.LoadCachedSessionAnalysis(s); !ok {
			toAnalyze = append(toAnalyze, s)
		}
	}
	if len(toAnalyze) > 0 {
		RunWithSpinner(fmt.Sprintf("Analyzing %d sessions...", len(toAnalyze)), func(progress func(int, int, string)) struct{} {
			for i, s := range toAnalyze {
				if progress != nil {
					progress(i+1, len(toAnalyze), s.Name)
				}
				sessionimport.BuildSessionArtifacts(s) //nolint:errcheck
			}
			return struct{}{}
		})
	}

	stats := map[string]*fileStat{}
	fileSessions := map[string][]internal.SessionSummary{}
	for _, s := range sessions {
		cached, ok, _ := sessionimport.LoadCachedSessionAnalysis(s)
		if !ok {
			continue
		}
		m := cached.Analysis.Metrics

		// collect all files accessed in this session
		allFiles := map[string]bool{}
		for _, f := range m.ReadFiles {
			allFiles[f] = true
		}
		for _, f := range m.EditedFiles {
			allFiles[f] = true
		}

		editSet := map[string]bool{}
		for _, f := range m.EditedFiles {
			editSet[f] = true
		}

		// ponytail: tokens attributed equally across files - proxy for "which files cost the most"
		var tokensPerFile int64
		if m.TokenUsage != nil && len(allFiles) > 0 {
			total := m.TokenUsage.InputTokens + m.TokenUsage.OutputTokens
			tokensPerFile = total / int64(len(allFiles))
		}

		for f := range allFiles {
			if stats[f] == nil {
				stats[f] = &fileStat{searchQueries: map[string]int{}}
			}
			stats[f].sessions++
			if editSet[f] {
				stats[f].editSessions++
			}
			if m.ReadFileCounts != nil {
				stats[f].totalReads += m.ReadFileCounts[f]
			}
			stats[f].totalTokens += tokensPerFile
			if s.Timestamp.After(stats[f].lastSeen) {
				stats[f].lastSeen = s.Timestamp
			}
			fileSessions[f] = append(fileSessions[f], s)
		}
		searchSeen := map[string]bool{}
		for _, block := range m.PromptBlocks {
			for _, search := range block.Searches {
				if !search.FoundViaSearch || search.EditTarget == "" {
					continue
				}
				if stats[search.EditTarget] == nil {
					continue
				}
				searchSeen[search.EditTarget] = true
				stats[search.EditTarget].searchesBeforeEdit++
				stats[search.EditTarget].readsAfterSearch += search.ReadsBeforeEdit
				query := strings.Join(strings.Fields(strings.ToLower(search.Query)), " ")
				if query != "" {
					stats[search.EditTarget].searchQueries[query]++
				}
			}
		}
		for path := range searchSeen {
			stats[path].searchEditSessions++
		}
	}

	rows := make([]fileRow, 0, len(stats))
	for path, stat := range stats {
		rows = append(rows, fileRow{path, *stat})
	}
	// sort by tokens desc within each future group; grouping happens in newFilesModel
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].stat.totalTokens > rows[j].stat.totalTokens
	})

	displayPath := func(p string) string {
		if !filepath.IsAbs(p) {
			return p // already relative - store it as-is (avoids basename collision)
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
		return filepath.Base(p)
	}

	header := fmt.Sprintf("RepoGuide Files - %s - last %s", repoDisplayBase(repoRoot), sinceFlag)

	if isatty.IsTerminal(os.Stdout.Fd()) {
		// topN applied per tab so every group is represented
		m := newFilesModel(rows, displayPath, header, len(sessions), topN, fileSessions)
		_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		return err
	}

	// non-TTY: apply topN globally after role-sort
	roleOrder := map[string]int{"Hotspot": 0, "Mixed": 1, "Context": 2}
	sort.SliceStable(rows, func(i, j int) bool {
		ri := roleOrder[fileRole(rows[i].stat.sessions, rows[i].stat.editSessions)]
		rj := roleOrder[fileRole(rows[j].stat.sessions, rows[j].stat.editSessions)]
		return ri < rj
	})
	if topN > 0 && len(rows) > topN {
		rows = rows[:topN]
	}

	// non-TTY: static output
	maxPathLen := 4 // "File"
	for _, r := range rows {
		if n := len(displayPath(r.path)); n > maxPathLen {
			maxPathLen = n
		}
	}
	if maxPathLen > 55 {
		maxPathLen = 55
	}
	cols := []tableColumn{
		{title: "File", width: maxPathLen},
		{title: "Sess", width: 5},
		{title: "Reads/s", width: 8},
		{title: "Edits", width: 6},
		{title: "Search→Edit", width: 11},
		{title: "Pre-reads", width: 9},
		{title: "Edit%", width: 6},
		{title: "Context", width: 8},
		{title: "Ctx/Edit", width: 9},
		{title: "Last", width: 8},
		{title: "Role", width: 11},
	}
	fmt.Println(titleStyle.Render(header))
	fmt.Println()
	fmt.Println(renderTableHeader(cols))
	currentRole := ""
	for _, r := range rows {
		role := fileRole(r.stat.sessions, r.stat.editSessions)
		if role != currentRole {
			if currentRole != "" {
				fmt.Println()
			}
			fmt.Printf("  - %s -\n", tabLabels[role])
			currentRole = role
		}
		editPct := 0
		if r.stat.sessions > 0 {
			editPct = 100 * r.stat.editSessions / r.stat.sessions
		}
		readsPerSess := "-"
		if r.stat.totalReads > 0 && r.stat.sessions > 0 {
			readsPerSess = fmt.Sprintf("%.1f", float64(r.stat.totalReads)/float64(r.stat.sessions))
		}
		ctxPerEdit := "ctx-only"
		if r.stat.editSessions > 0 {
			ctxPerEdit = fmtTokens(r.stat.totalTokens / int64(r.stat.editSessions))
		}
		fmt.Println(renderTableRow(cols,
			displayPath(r.path),
			fmt.Sprintf("%d", r.stat.sessions),
			readsPerSess,
			fmt.Sprintf("%d", r.stat.editSessions),
			fmt.Sprintf("%d", r.stat.searchEditSessions),
			formatAvgSearchReads(r.stat),
			fmt.Sprintf("%d%%", editPct),
			fmtTokens(r.stat.totalTokens),
			ctxPerEdit,
			timeAgo(r.stat.lastSeen),
			tabLabels[role],
		))
	}
	fmt.Printf("\n%d files across %d sessions\n", len(stats), len(sessions))
	return nil
}

func fileRole(sessions, editSessions int) string {
	if sessions == 0 {
		return "Context"
	}
	pct := 100 * editSessions / sessions
	switch {
	case pct < 25:
		return "Context"
	case pct < 75:
		return "Mixed"
	default:
		return "Hotspot"
	}
}

func fmtTokens(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < 24*time.Hour:
		return "today"
	case d < 48*time.Hour:
		return "1d ago"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 8*7*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	default:
		return t.Format("Jan 2")
	}
}

// ── interactive TUI ──────────────────────────────────────────────────────────

var tabRoleOrder = []string{"Hotspot", "Mixed", "Context"}

var tabLabels = map[string]string{
	"Hotspot": "HOTSPOTS",
	"Mixed":   "MIXED",
	"Context": "CONTEXT TAX",
}

var tabDescriptions = map[string]string{
	"Hotspot": "Agents returned here repeatedly and made changes.",
	"Mixed":   "High read cost relative to edit output - possible refactor targets.",
	"Context": "Read repeatedly, rarely changed - pure orientation cost.",
}

type filesTabModel struct {
	role string
	rows []fileRow
	tbl  table.Model
}

type filesModel struct {
	tabs         []filesTabModel
	activeTab    int
	width        int
	height       int
	header       string
	dispPath     func(string) string
	totalSess    int
	blinkOn      bool
	fileSessions map[string][]internal.SessionSummary
	sub          *fileTraceModel
}

type filesBlink struct{}

func newFilesModel(rows []fileRow, dispPath func(string) string, header string, totalSess int, topN int, fileSessions map[string][]internal.SessionSummary) filesModel {
	grouped := map[string][]fileRow{}
	for _, r := range rows {
		role := fileRole(r.stat.sessions, r.stat.editSessions)
		grouped[role] = append(grouped[role], r)
	}
	tabs := make([]filesTabModel, 0, 3)
	for _, role := range tabRoleOrder {
		rs := grouped[role]
		if topN > 0 && len(rs) > topN {
			rs = rs[:topN]
		}
		if len(rs) > 0 {
			tabs = append(tabs, filesTabModel{
				role: role,
				rows: rs,
				tbl:  newFilesTable(),
			})
		}
	}
	return filesModel{tabs: tabs, header: header, dispPath: dispPath, totalSess: totalSess, fileSessions: fileSessions}
}

func newFilesTable() table.Model {
	return table.New(
		table.WithFocused(true),
		table.WithStyles(table.Styles{
			Header:   headStyle.PaddingRight(1),
			Cell:     lipgloss.NewStyle().PaddingRight(1),
			Selected: selected.PaddingRight(1),
		}),
	)
}

func blinkTickCmd() tea.Cmd {
	return tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg { return filesBlink{} })
}

func (m filesModel) Init() tea.Cmd { return blinkTickCmd() }

func (m filesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.sub != nil {
		if m.sub.closed {
			m.sub = nil
			return m, nil
		}
		updated, cmd := m.sub.Update(msg)
		ft := updated.(fileTraceModel)
		if ft.closed {
			m.sub = nil
			return m, nil
		}
		m.sub = &ft
		return m, cmd
	}

	switch msg := msg.(type) {
	case filesBlink:
		m.blinkOn = !m.blinkOn
		return m, blinkTickCmd()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		for i := range m.tabs {
			// overhead: title(1)+blank(1)+tabbar(1)+desc(1)+blank(1)+blank(1)+footer(1)+insights(~4) = 11
			m.tabs[i].tbl.SetHeight(max(3, m.height-11))
			m.rebuildTabTable(i)
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "left", "h":
			if m.activeTab > 0 {
				m.activeTab--
				m.rebuildTabTable(m.activeTab)
			}
		case "right", "l":
			if m.activeTab < len(m.tabs)-1 {
				m.activeTab++
				m.rebuildTabTable(m.activeTab)
			}
		case "up", "k":
			m.tabs[m.activeTab].tbl.MoveUp(1)
		case "down", "j":
			m.tabs[m.activeTab].tbl.MoveDown(1)
		case "enter":
			if len(m.tabs) == 0 {
				return m, nil
			}
			tab := &m.tabs[m.activeTab]
			if len(tab.rows) == 0 {
				return m, nil
			}
			row := tab.rows[tab.tbl.Cursor()]
			fileSess := m.fileSessions[row.path]
			if len(fileSess) == 0 {
				return m, nil
			}
			sub := newFileTraceModel(row.path, m.dispPath(row.path), fileSess)
			sub.width = m.width
			sub.height = m.height
			sub.vp = newDetailViewport(m.width, m.height-2)
			sub.refreshContent()
			m.sub = &sub
			return m, sub.Init()
		}
	}
	return m, nil
}

// ── file trace TUI ───────────────────────────────────────────────────────────

type fileTraceModel struct {
	filePath string
	dispPath string
	sessions []internal.SessionSummary
	analyses []*internal.SessionAnalysis // parallel to sessions
	cursor   int
	vp       viewport.Model
	width    int
	height   int
	closed   bool
	sub      *fileSessionDetailModel
}

func newFileTraceModel(filePath, dispPath string, sessions []internal.SessionSummary) fileTraceModel {
	return fileTraceModel{
		filePath: filePath,
		dispPath: dispPath,
		sessions: sessions,
		analyses: make([]*internal.SessionAnalysis, len(sessions)),
	}
}

func (m fileTraceModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, len(m.sessions))
	for i, s := range m.sessions {
		cmds[i] = buildSessionAnalysisCmd(s)
	}
	return tea.Batch(cmds...)
}

func (m fileTraceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.sub != nil {
		if m.sub.closed {
			m.sub = nil
			m.refreshContent()
			return m, nil
		}
		updated, cmd := m.sub.Update(msg)
		sd := updated.(fileSessionDetailModel)
		if sd.closed {
			m.sub = nil
			m.refreshContent()
			return m, nil
		}
		m.sub = &sd
		return m, cmd
	}

	switch msg := msg.(type) {
	case sessionAnalysisBuiltMsg:
		if msg.err == nil {
			for i, s := range m.sessions {
				if s.ID == msg.sessionID {
					a := msg.artifacts.Analysis
					m.analyses[i] = &a
					break
				}
			}
			m.refreshContent()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = newDetailViewport(m.width, m.height-2)
		m.refreshContent()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.closed = true
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.refreshContent()
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
				m.refreshContent()
			}
		case "enter":
			if len(m.sessions) == 0 {
				return m, nil
			}
			sub := newFileSessionDetailModel(m.filePath, m.dispPath, m.sessions[m.cursor], m.analyses[m.cursor])
			sub.vp = newDetailViewport(m.width, m.height)
			sub.width = m.width
			sub.height = m.height
			sub.refreshContent()
			m.sub = &sub
			return m, sub.Init()
		default:
			vp, cmd := m.vp.Update(msg)
			m.vp = vp
			return m, cmd
		}
	}
	return m, nil
}

// ── per-session file detail TUI ──────────────────────────────────────────────

type fileSessionDetailModel struct {
	filePath       string
	dispPath       string
	session        internal.SessionSummary
	analysis       *internal.SessionAnalysis
	vp             viewport.Model
	width          int
	height         int
	closed         bool
	promptIndices  []int // indices of PromptBlocks that touched filePath
	promptCursor   int
	promptOpen     bool
	promptExpanded bool
}

func newFileSessionDetailModel(filePath, dispPath string, session internal.SessionSummary, analysis *internal.SessionAnalysis) fileSessionDetailModel {
	m := fileSessionDetailModel{
		filePath: filePath,
		dispPath: dispPath,
		session:  session,
		analysis: analysis,
	}
	m.buildPromptIndices()
	return m
}

func (m fileSessionDetailModel) Init() tea.Cmd {
	if m.analysis != nil {
		return nil
	}
	return buildSessionAnalysisCmd(m.session)
}

func (m fileSessionDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionAnalysisBuiltMsg:
		if msg.sessionID == m.session.ID && msg.err == nil {
			a := msg.artifacts.Analysis
			m.analysis = &a
			m.buildPromptIndices()
			m.refreshContent()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = newDetailViewport(m.width, m.height)
		m.refreshContent()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.closed = true
			return m, nil
		case "esc":
			if m.promptOpen {
				m.promptOpen = false
				m.promptExpanded = false
				m.refreshContent()
				m.vp.GotoBottom()
				return m, nil
			}
			m.closed = true
			return m, nil
		case "e":
			if m.promptOpen {
				m.promptExpanded = !m.promptExpanded
				m.refreshContentKeepScroll()
			}
			return m, nil
		case "enter":
			if !m.promptOpen && len(m.promptIndices) > 0 {
				m.promptOpen = true
				m.promptExpanded = false
				m.refreshContent()
				m.vp.GotoTop()
				return m, nil
			}
		case "up", "k":
			if m.promptOpen {
				break // fall through to viewport scroll
			}
			if m.promptCursor > 0 {
				m.promptCursor--
				m.refreshContentKeepScroll()
				return m, nil
			}
			vp, _ := updateViewportKeys(m.vp, msg)
			m.vp = vp
			return m, nil
		case "down", "j":
			if m.promptOpen {
				break // fall through to viewport scroll
			}
			if m.vp.AtBottom() && m.promptCursor < len(m.promptIndices)-1 {
				m.promptCursor++
				m.refreshContentKeepScroll()
				return m, nil
			}
			vp, _ := updateViewportKeys(m.vp, msg)
			m.vp = vp
			return m, nil
		}
		vp, cmd := m.vp.Update(msg)
		m.vp = vp
		return m, cmd
	}
	return m, nil
}

func (m *fileSessionDetailModel) buildPromptIndices() {
	m.promptIndices = nil
	if m.analysis == nil {
		return
	}
	for i, b := range m.analysis.Metrics.PromptBlocks {
		if b.ReadFileCountsBefore[m.filePath] > 0 || b.ReadFileCountsAfter[m.filePath] > 0 {
			m.promptIndices = append(m.promptIndices, i)
			continue
		}
		for _, f := range b.EditedFiles {
			if f == m.filePath {
				m.promptIndices = append(m.promptIndices, i)
				break
			}
		}
	}
}
