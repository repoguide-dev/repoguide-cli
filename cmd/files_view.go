package cmd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/repoguide/repoguide-cli/internal"
)

func (m *filesModel) rebuildTabTable(i int) {
	tab := &m.tabs[i]
	const fixedCols = 6 // Sess Edited Search→Edit Avg pre-read Context Last
	const fixedW = 4 + 6 + 11 + 12 + 8 + 7
	fileW := m.width - 4 - fixedW - fixedCols
	if fileW < 20 {
		fileW = 20
	}
	if fileW > 55 {
		fileW = 55
	}
	cols := []table.Column{
		{Title: "File", Width: fileW},
		{Title: "Sess", Width: 4},
		{Title: "Edited", Width: 6},
		{Title: "Search→Edit", Width: 11},
		{Title: "Avg pre-read", Width: 12},
		{Title: "Context", Width: 8},
		{Title: "Last", Width: 7},
	}
	trows := make([]table.Row, len(tab.rows))
	for j, r := range tab.rows {
		trows[j] = table.Row{
			truncate(m.dispPath(r.path), fileW),
			fmt.Sprintf("%d", r.stat.sessions),
			fmt.Sprintf("%d", r.stat.editSessions),
			fmt.Sprintf("%d", r.stat.searchEditSessions),
			formatAvgSearchReads(r.stat),
			fmtTokens(r.stat.totalTokens),
			timeAgo(r.stat.lastSeen),
		}
	}
	tab.tbl.SetColumns(cols)
	tab.tbl.SetRows(trows)
}

func formatAvgSearchReads(stat fileStat) string {
	if stat.searchesBeforeEdit == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(stat.readsAfterSearch)/float64(stat.searchesBeforeEdit))
}

func (m filesModel) View() string {
	if m.sub != nil {
		return m.sub.View()
	}
	if len(m.tabs) == 0 {
		return "No files found.\n"
	}

	blinkStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Underline(true)
	steadyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	tabParts := make([]string, len(m.tabs))
	for i, t := range m.tabs {
		label := tabLabels[t.role]
		if i == m.activeTab {
			if m.blinkOn {
				tabParts[i] = blinkStyle.Render(label)
			} else {
				tabParts[i] = steadyStyle.Render(label)
			}
		} else {
			tabParts[i] = inactiveStyle.Render(label)
		}
	}
	tabBar := strings.Join(tabParts, muted.Render("  ·  "))
	desc := muted.Render(tabDescriptions[m.tabs[m.activeTab].role])

	insights := fileInsights(m.tabs, m.dispPath)
	insightLines := make([]string, len(insights))
	for i, s := range insights {
		insightLines[i] = muted.Render(s)
	}

	parts := []string{
		titleStyle.Render(m.header),
		"",
		tabBar,
		"",
		desc,
		"",
		m.tabs[m.activeTab].tbl.View(),
		"",
	}
	if len(insightLines) > 0 {
		parts = append(parts, insightLines...)
		parts = append(parts, "")
	}
	parts = append(parts, muted.Render("←→ tabs  •  ↑↓ scroll  •  enter trace  •  q quit"))
	return strings.Join(parts, "\n")
}

func fileInsights(tabs []filesTabModel, dispPath func(string) string) []string {
	var ctxOnly int
	var bestFile string
	var bestCPE int64
	for _, tab := range tabs {
		for _, r := range tab.rows {
			if r.stat.editSessions == 0 {
				ctxOnly++
			} else {
				cpe := r.stat.totalTokens / int64(r.stat.editSessions)
				if cpe > bestCPE {
					bestCPE = cpe
					bestFile = dispPath(r.path)
				}
			}
		}
	}
	var out []string
	if ctxOnly == 1 {
		out = append(out, "• 1 file read repeatedly but never edited.")
	} else if ctxOnly > 1 {
		out = append(out, fmt.Sprintf("• %d files read repeatedly but never edited.", ctxOnly))
	}
	if bestFile != "" {
		out = append(out, fmt.Sprintf("• Highest ctx/edit: %s - %s per edit.", bestFile, fmtTokens(bestCPE)))
	}
	if ctxOnly >= 3 {
		out = append(out, "• Consider summarizing conventions in AGENTS.md to reduce orientation cost.")
	}
	return out
}

func (m *fileTraceModel) refreshContent() {
	content, cursorLine := m.buildContent()
	m.vp.SetContent(content)
	offset := cursorLine - m.vp.Height/2
	if offset < 0 {
		offset = 0
	}
	m.vp.SetYOffset(offset)
}

func (m *fileTraceModel) buildContent() (string, int) {
	var lines []string
	cursorLine := 0

	lastLabel := ""
	for i, s := range m.sessions {
		a := m.analyses[i]

		label := dateGroupLabel(s.Timestamp)
		if label != lastLabel {
			if lastLabel != "" {
				lines = append(lines, "")
			}
			lines = append(lines, headStyle.Render(label))
			lastLabel = label
		}

		timeStr := s.Timestamp.Local().Format("15:04")
		name := valueOrFallback(s.Name, "(untitled)")

		reads, edits := 0, 0
		searches := 0
		var searchQueries []string
		if a != nil {
			reads = a.Metrics.ReadFileCounts[m.filePath]
			edits = a.Metrics.EditFileCounts[m.filePath]
			searches, searchQueries = fileSearchSummary(m.filePath, a.Metrics.PromptBlocks)
		}

		cursor := "  "
		rowStyle := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = "› "
			rowStyle = selected
			cursorLine = len(lines)
		}

		statParts := []string{}
		if a != nil {
			if searches > 0 {
				word := "searches"
				if searches == 1 {
					word = "search"
				}
				statParts = append(statParts, fmt.Sprintf("%d %s", searches, word))
			}
			if reads > 0 {
				word := "reads"
				if reads == 1 {
					word = "read"
				}
				statParts = append(statParts, fmt.Sprintf("%d %s", reads, word))
			}
			if edits > 0 {
				word := "edits"
				if edits == 1 {
					word = "edit"
				}
				statParts = append(statParts, fmt.Sprintf("%d %s", edits, word))
			}
		}

		firstLine := cursor + timeStr + "  " + name
		if len(statParts) > 0 {
			firstLine += "  " + muted.Render(strings.Join(statParts, ", "))
		}
		lines = append(lines, rowStyle.Render(firstLine))

		if a != nil {
			chain := fileChain(m.filePath, a.Metrics.PromptBlocks)
			if chain != "" {
				lines = append(lines, muted.Render("        chain: "+chain))
			}
			if len(searchQueries) > 0 {
				formatted := make([]string, len(searchQueries))
				for j, query := range searchQueries {
					formatted[j] = "“" + formatSearchQuery(query) + "”"
				}
				lines = append(lines, "        "+muted.Render("queries: ")+searchQueryStyle.Render(strings.Join(formatted, ", ")))
			}
			prompts := filePromptNums(m.filePath, a.Metrics.PromptBlocks)
			if len(prompts) > 0 {
				nums := make([]string, len(prompts))
				for j, p := range prompts {
					nums[j] = fmt.Sprintf("#%d", p)
				}
				lines = append(lines, muted.Render("        prompts: "+strings.Join(nums, ", ")))
			}
		} else {
			lines = append(lines, muted.Render("        loading..."))
		}
	}

	return strings.Join(lines, "\n"), cursorLine
}

func (m fileTraceModel) View() string {
	if m.sub != nil {
		return m.sub.View()
	}
	title := titleStyle.Render("Trace - " + m.dispPath)
	footer := muted.Render(footerHint("enter open", "↑↓ navigate", "esc back", "q quit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(
		strings.Join([]string{title, "", m.vp.View(), "", footer}, "\n"),
	)
}

func dateGroupLabel(t time.Time) string {
	local := t.Local()
	now := time.Now()
	y, mo, d := local.Date()
	yn, mon, dn := now.Date()
	if y == yn && mo == mon && d == dn {
		return "Today"
	}
	yy, my, dy := now.AddDate(0, 0, -1).Date()
	if y == yy && mo == my && d == dy {
		return "Yesterday"
	}
	return local.Format("Jan 2")
}

func fileChain(filePath string, blocks []internal.PromptBlock) string {
	var ops []string
	for _, b := range blocks {
		var didRead, didEdit bool
		for _, search := range b.Searches {
			if search.EditTarget == filePath {
				ops = append(ops, "search")
			}
		}
		for _, f := range b.ReadFiles {
			if f == filePath {
				didRead = true
				break
			}
		}
		for _, f := range b.EditedFiles {
			if f == filePath {
				didEdit = true
				break
			}
		}
		if didRead {
			ops = append(ops, "read")
		}
		if didEdit {
			ops = append(ops, "edit")
		}
	}
	// collapse consecutive identical ops: read read read → read ×3
	var parts []string
	for i := 0; i < len(ops); {
		j := i + 1
		for j < len(ops) && ops[j] == ops[i] {
			j++
		}
		if j-i > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", ops[i], j-i))
		} else {
			parts = append(parts, ops[i])
		}
		i = j
	}
	return strings.Join(parts, " → ")
}

func fileSearchSummary(filePath string, blocks []internal.PromptBlock) (int, []string) {
	count := 0
	seen := map[string]struct{}{}
	var queries []string
	for _, block := range blocks {
		for _, search := range block.Searches {
			if search.EditTarget != filePath {
				continue
			}
			count++
			query := strings.TrimSpace(search.Query)
			if query == "" {
				continue
			}
			if _, ok := seen[query]; ok {
				continue
			}
			seen[query] = struct{}{}
			queries = append(queries, query)
		}
	}
	if len(queries) > 3 {
		queries = queries[:3]
	}
	return count, queries
}

func filePromptNums(filePath string, blocks []internal.PromptBlock) []int {
	var nums []int
	for i, b := range blocks {
		touched := false
		for _, f := range b.ReadFiles {
			if f == filePath {
				touched = true
				break
			}
		}
		if !touched {
			for _, f := range b.EditedFiles {
				if f == filePath {
					touched = true
					break
				}
			}
		}
		if touched {
			nums = append(nums, i+1)
		}
	}
	return nums
}

func (m *fileSessionDetailModel) refreshContent() {
	m.vp.SetContent(m.buildContent())
	m.vp.GotoTop()
}

func (m *fileSessionDetailModel) refreshContentKeepScroll() {
	offset := m.vp.YOffset
	m.vp.SetContent(m.buildContent())
	m.vp.SetYOffset(offset)
}

func (m *fileSessionDetailModel) buildContent() string {
	if m.promptOpen && m.analysis != nil && m.promptCursor < len(m.promptIndices) {
		blockIdx := m.promptIndices[m.promptCursor]
		return buildPromptDetailContent(
			m.analysis.Metrics.PromptBlocks[blockIdx],
			blockIdx+1,
			m.analysis.ModelPricing,
			m.session,
			m.promptExpanded,
		)
	}

	var lines []string

	// header
	lines = append(lines, headStyle.Render(m.dispPath))
	name := valueOrFallback(m.session.Name, "(untitled)")
	meta := m.session.Timestamp.Local().Format("2006-01-02 15:04") + "  " + name
	if m.session.Model != "" {
		meta += "  " + m.session.Model
	}
	lines = append(lines, muted.Render(meta))
	lines = append(lines, "")

	if m.analysis == nil {
		lines = append(lines, muted.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	met := m.analysis.Metrics
	reads := met.ReadFileCounts[m.filePath]
	edits := met.EditFileCounts[m.filePath]

	totals := []string{}
	if reads > 0 {
		totals = append(totals, fmt.Sprintf("%d reads", reads))
	}
	if edits > 0 {
		totals = append(totals, fmt.Sprintf("%d edits", edits))
	}
	if len(totals) > 0 {
		lines = append(lines, strings.Join(totals, ", "))
	}
	searches, searchReads := 0, 0
	queryCounts := map[string]int{}
	foundViaSearch := false
	for _, block := range met.PromptBlocks {
		for _, search := range block.Searches {
			if search.EditTarget != m.filePath || !search.FoundViaSearch {
				continue
			}
			foundViaSearch = true
			searches++
			searchReads += search.ReadsBeforeEdit
			if query := strings.TrimSpace(search.Query); query != "" {
				queryCounts[query]++
			}
		}
	}
	if foundViaSearch {
		lines = append(lines, "", headStyle.Render("Discovery"))
		lines = append(lines,
			"  Found via search before edit: yes",
			fmt.Sprintf("  Searches before edit:        %d", searches),
			fmt.Sprintf("  Avg reads before edit:       %.1f", float64(searchReads)/float64(searches)),
		)
		if queries := topFileSearchQueries(queryCounts, 5); len(queries) > 0 {
			lines = append(lines, "  Common search terms:         "+strings.Join(queries, ", "))
		}
	}

	// per-prompt trace - only blocks that touched this file, with cursor
	promptLines := buildFilePromptTrace(m.filePath, met.PromptBlocks, m.promptIndices, m.promptCursor, m.vp.Width)
	if len(promptLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, headStyle.Render("Trace"))
		lines = append(lines, promptLines...)
	}

	return strings.Join(lines, "\n")
}

func topFileSearchQueries(counts map[string]int, limit int) []string {
	type entry struct {
		query string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for query, count := range counts {
		entries = append(entries, entry{query: query, count: count})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if a.count != b.count {
			return b.count - a.count
		}
		return strings.Compare(a.query, b.query)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = fmt.Sprintf("%s (%d)", entry.query, entry.count)
	}
	return out
}

func (m fileSessionDetailModel) View() string {
	title := titleStyle.Render("Trace - " + m.dispPath)
	var footer string
	if m.promptOpen {
		footer = muted.Render(footerHint("↑↓ scroll", "e expand", "esc back", "q quit") + scrollHint(m.vp))
	} else if len(m.promptIndices) > 0 {
		footer = muted.Render(footerHint("↑↓ scroll+select", "enter open", "esc back", "q quit") + scrollHint(m.vp))
	} else {
		footer = muted.Render(footerHint("↑↓ scroll", "esc back", "q quit") + scrollHint(m.vp))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(
		strings.Join([]string{title, "", m.vp.View(), "", footer}, "\n"),
	)
}

// buildFilePromptTrace returns per-prompt lines for all blocks.
// Blocks that touched filePath show the operation chain; others are shown dimmed with right-aligned label.
// indices is the pre-filtered list of block indices that touched the file; cursor is the selected position within indices.
func buildFilePromptTrace(filePath string, blocks []internal.PromptBlock, indices []int, cursor, width int) []string {
	touchedAt := make(map[int]int, len(indices)) // block idx → cursor position in indices
	for ci, blockIdx := range indices {
		touchedAt[blockIdx] = ci
	}

	const untouched = "(did not touch file)"
	const textW = 50
	var lines []string
	for blockIdx, b := range blocks {
		ci, touched := touchedAt[blockIdx]
		oneLiner := `"` + promptOneLiner(b.Text) + `"`
		if !touched {
			lines = append(lines, muted.Render(fmt.Sprintf("  P%-2d  %-*s  %s", blockIdx+1, textW, truncate(oneLiner, textW), untouched)))
			continue
		}

		lines = append(lines, "")
		if ci == cursor {
			lines = append(lines, selected.Render(fmt.Sprintf("› P%-2d %s", blockIdx+1, oneLiner)))
		} else {
			lines = append(lines, fmt.Sprintf("  P%-2d ", blockIdx+1)+promptTextStyle.Render(oneLiner))
		}

		pre := b.ReadFileCountsBefore[filePath]
		post := b.ReadFileCountsAfter[filePath]
		edited := slices.Contains(b.EditedFiles, filePath)
		var ops []string
		if pre > 0 {
			ops = append(ops, fmt.Sprintf("read ×%d", pre))
		}
		for _, search := range b.Searches {
			if search.EditTarget == filePath {
				ops = append([]string{"search “" + formatSearchQuery(search.Query) + "”"}, ops...)
				break
			}
		}
		if edited {
			ops = append(ops, "edit")
		}
		if post > 0 {
			ops = append(ops, fmt.Sprintf("read ×%d", post))
		}
		lines = append(lines, "    "+strings.Join(ops, " → "))
	}
	return lines
}
