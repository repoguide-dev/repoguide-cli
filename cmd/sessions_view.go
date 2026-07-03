package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
)

const (
	sessionTimeWidth  = 16
	sessionCostWidth  = 11
	sessionFilesWidth = 12
)

func sessionTableColumns(width int) []tableColumn {
	if width <= 0 {
		width = 100
	}
	const numCols = 6
	fixed := sessionCostWidth + sessionFilesWidth + sessionTimeWidth
	dyn := max(0, width-numCols-fixed)
	return []tableColumn{
		{title: "Name", width: max(20, dyn/4)},
		{title: "Repo/Cwd", width: max(28, dyn/3)},
		{title: "Model", width: max(12, dyn/6)},
		{title: "Cost (est)", width: sessionCostWidth},
		{title: "Files (e/r)", width: sessionFilesWidth},
		{title: "Timestamp", width: sessionTimeWidth},
	}
}

func newSessionTable() table.Model {
	return table.New(
		table.WithFocused(true),
		table.WithStyles(table.Styles{
			Header:   headStyle.PaddingRight(1),
			Cell:     lipgloss.NewStyle().PaddingRight(1),
			Selected: selected.PaddingRight(1),
		}),
	)
}

func (m sessionsModel) View() string {
	switch m.view {
	case viewAgent:
		return m.renderAgentPicker()
	case viewDetail, viewPromptDetail:
		return m.renderDetail()
	default:
		return m.renderList()
	}
}

func (m sessionsModel) renderAgentPicker() string {
	rows := make([]selectableRow, 0, len(m.agents))
	for i, agent := range m.agents {
		rows = append(rows, selectableRow{
			label:    formatAgentOption(agent, m.agentCounts[agent]),
			selected: i == m.agentCursor,
		})
	}
	footer := "enter select  •  q quit"
	if m.loading && m.agent != "" {
		footer = fmt.Sprintf("%s  •  %s Loading %s sessions...", footer, m.spinner.View(), displayAgentName(m.agent))
	}
	if m.repoFilter != "" {
		footer = fmt.Sprintf("%s  •  repo %s", footer, m.repoFilter)
	}
	return renderSelectableList(titleStyle.Render("Select session source"), rows, footer)
}

func (m sessionsModel) renderList() string {
	titleText := m.titleOverride
	if titleText == "" {
		titleText = fmt.Sprintf("%s sessions", displayAgentName(m.agent))
		if m.repoFilter != "" {
			titleText = fmt.Sprintf("%s sessions for %s", displayAgentName(m.agent), m.repoFilter)
		}
	}
	title := titleStyle.Render(titleText)
	if m.loading {
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + m.spinner.View() + " Loading local agent sessions...")
	}
	if m.err != nil {
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + m.err.Error() + "\n\n" + muted.Render(m.listFooterHint()))
	}
	if len(m.sessions) == 0 {
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\nNo sessions found.\n\n" + muted.Render(m.listFooterHint()))
	}

	footer := fmt.Sprintf(
		"%s  •  %d-%d of %d",
		m.listFooterHint(),
		min(m.pageOffset+1, m.total),
		min(m.pageOffset+len(m.sessions), m.total),
		m.total,
	)
	return lipgloss.NewStyle().Padding(1, 2).Render(
		strings.Join([]string{title, "", m.table.View(), "", muted.Render(footer)}, "\n"),
	)
}

func (m sessionsModel) renderDetail() string {
	if len(m.sessions) == 0 {
		return ""
	}
	if m.detailVP.Width == 0 || m.detailVP.Height == 0 {
		m.applyDetailViewport()
		m.refreshCurrentDetailContent()
	}
	footer := muted.Render(m.detailFooterHint() + scrollHint(m.detailVP))
	return lipgloss.NewStyle().Padding(1, 2).Render(
		strings.Join([]string{m.detailVP.View(), "", footer}, "\n"),
	)
}

func (m *sessionsModel) refreshCurrentDetailContent() {
	if m.view == viewPromptDetail {
		m.detailVP.SetContent(m.promptDetailContent())
		return
	}
	m.detailVP.SetContent(m.detailContent())
}

func (m *sessionsModel) detailContent() string {
	if len(m.sessions) == 0 {
		return ""
	}
	session := m.sessions[m.table.Cursor()]
	lines := sessionDetailLines(session, true)
	divider := muted.Render(strings.Repeat("─", max(24, m.width-4)))
	switch {
	case m.detailLoading:
		lines = append(lines, "", divider, headStyle.Render("Analysis"), muted.Render(fmt.Sprintf("%s Analyzing...", m.spinner.View())))
	case m.detailErr != nil:
		lines = append(lines, "", divider, headStyle.Render("Analysis"), m.detailErr.Error())
	case m.analysis == nil:
		lines = append(lines, "", divider, muted.Render("No cached analysis. Press enter to analyze."))
	default:
		lines = append(lines, "", divider)
		lines = append(lines, cachedAnalysisLines(*m.analysis, true)...)
		if blocks := m.analysis.Metrics.PromptBlocks; len(blocks) > 0 {
			lines = append(lines, "", headStyle.Render(fmt.Sprintf("Prompts (%d):", len(blocks))))
			lines = append(lines, muted.Render(fmt.Sprintf("  %-3s  %-38s  %4s  %5s  %5s  %5s  %s", "#", "Prompt", "Srch", "Reads", "Edits", "Files", "Result")))
			for i, b := range blocks {
				reads := promptBlockTotalReads(b)
				edits := b.EditCount
				files := promptBlockUniqueFiles(b)
				result := "no edit"
				if len(b.EditedFiles) > 0 {
					result = "edited"
				}
				tail := fmt.Sprintf("  %4d  %5d  %5d  %5d  %s", len(b.Searches), reads, edits, files, result)
				oneLiner := `"` + truncate(promptOneLiner(b.Text), 36) + `"`
				if i == m.promptCursor {
					plain := fmt.Sprintf("  %-3d  %-38s%s", i+1, oneLiner, tail)
					lines = append(lines, selected.Render("›"+plain[1:]))
				} else {
					paddedText := fmt.Sprintf("%-38s", oneLiner)
					lines = append(lines, fmt.Sprintf("  %-3d  ", i+1)+promptTextStyle.Render(paddedText)+tail)
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (m *sessionsModel) applyDetailViewport() {
	m.detailVP = newDetailViewport(m.width, m.height)
}

func (m *sessionsModel) refreshDetailContent() {
	m.detailVP.SetContent(m.detailContent())
	m.detailVP.GotoTop()
}

func (m *sessionsModel) refreshDetailContentKeepScroll() {
	offset := m.detailVP.YOffset
	m.detailVP.SetContent(m.detailContent())
	m.detailVP.SetYOffset(offset)
}

func (m *sessionsModel) applyTableData() {
	cols := sessionTableColumns(m.width - 4)
	tcols := make([]table.Column, len(cols))
	for i, c := range cols {
		tcols[i] = table.Column{Title: c.title, Width: c.width}
	}
	rows := make([]table.Row, len(m.sessions))
	for i, s := range m.sessions {
		cost := "-"
		if s.CostUSD > 0 {
			cost = fmt.Sprintf("$%.2f", s.CostUSD)
		}
		files := "-"
		if s.EditFileCount > 0 || s.ReadFileCount > 0 {
			files = fmt.Sprintf("%d/%d", s.EditFileCount, s.ReadFileCount)
		}
		repoText := repoDisplayText(s)
		if repoText == "" {
			repoText = s.Cwd
		}
		rows[i] = table.Row{
			sessionLabel(s),
			truncate(repoText, cols[1].width),
			valueOrFallback(s.Model, "-"),
			cost,
			files,
			s.Timestamp.Local().Format("2006-01-02 15:04"),
		}
	}
	m.table.SetColumns(tcols)
	m.table.SetRows(rows)
	m.table.SetCursor(m.sessionCursor)
}

func (m *sessionsModel) applyTableSize() {
	// overhead: padding(2) + title(1) + blank(1) + blank(1) + footer(1) = 6
	if m.height > 0 {
		m.table.SetHeight(max(5, m.height-6))
	}
	if m.width > 0 {
		m.table.SetWidth(m.width - 4)
	}
}

func (m sessionsModel) promptDetailContent() string {
	if m.analysis == nil || m.promptCursor >= len(m.analysis.Metrics.PromptBlocks) {
		return ""
	}
	return buildPromptDetailContent(
		m.analysis.Metrics.PromptBlocks[m.promptCursor],
		m.promptCursor+1,
		m.analysis.ModelPricing,
		m.sessions[m.table.Cursor()],
		m.promptExpanded,
	)
}

func (m sessionsModel) listFooterHint() string {
	repoToggle := ""
	if m.defaultRepoFilter != "" {
		if m.repoFilter != "" {
			repoToggle = "  •  A all sessions"
		} else {
			repoToggle = "  •  A repo sessions"
		}
	}
	escAction := "esc agents"
	qAction := "q quit"
	if m.embedded {
		escAction = "esc back"
		qAction = "q back"
	} else if m.defaultRepoFilter != "" {
		escAction = "esc quit"
	}
	if m.embedded && m.parentFooter != "" {
		return m.parentFooter + repoToggle
	}
	return footerHint("enter open", escAction, "r reload", qAction) + repoToggle
}

func (m sessionsModel) detailFooterHint() string {
	if m.view == viewPromptDetail {
		qAction := "q quit"
		if m.embedded {
			qAction = "q back"
		}
		if m.currentPromptIsLong() {
			return footerHint("↑↓ scroll", "e expand", "esc back", qAction)
		}
		return footerHint("↑↓ scroll", "esc back", qAction)
	}
	action := "enter analyze"
	if m.detailLoading {
		action = "analyzing..."
	} else if m.analysis != nil {
		if len(m.analysis.Metrics.PromptBlocks) > 0 {
			action = "enter open"
		} else {
			action = "analysis cached"
		}
	}
	qAction := "q quit"
	if m.embedded {
		qAction = "q back"
	}
	return footerHint(action, "↑↓ scroll", "esc back", qAction)
}

func (m sessionsModel) currentPromptIsLong() bool {
	if m.analysis == nil || m.promptCursor >= len(m.analysis.Metrics.PromptBlocks) {
		return false
	}
	b := m.analysis.Metrics.PromptBlocks[m.promptCursor]
	text := b.FullText
	if text == "" {
		text = b.Text
	}
	return strings.Count(text, "\n") >= 5
}

func sessionDetailLines(session internal.SessionSummary, styled bool) []string {
	head := func(value string) string {
		if styled {
			return headStyle.Render(value)
		}
		return value
	}
	title := sessionLabel(session)
	if styled {
		title = titleStyle.Render(title)
	}

	return []string{
		title,
		"",
		fmt.Sprintf("%s %s", head("Agent:"), displayAgentName(session.Agent)),
		fmt.Sprintf("%s %s", head("RepoGuide:"), yesNo(session.UsedRepoGuide)),
		fmt.Sprintf("%s %s", head("Repo:"), valueOrFallback(renderRepoLabel(session), "-")),
		fmt.Sprintf("%s %s", head("Cwd:"), valueOrFallback(renderCwdLabel(session), "-")),
		fmt.Sprintf("%s %s", head("Model:"), valueOrFallback(session.Model, "-")),
		fmt.Sprintf("%s %s", head("Timestamp:"), session.Timestamp.Local().Format("2006-01-02 15:04:05 MST")),
		fmt.Sprintf("%s %s", head("ID:"), valueOrFallback(session.ID, "-")),
		fmt.Sprintf("%s %s", head("Path:"), valueOrFallback(renderPath(session.Path), "-")),
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func sessionAnalysisLines(artifacts sessionimport.SessionArtifacts) []string {
	lines := append(sessionDetailLines(artifacts.Analysis.Session, false), "")
	lines = append(lines,
		"Analysis:",
		fmt.Sprintf("  Events cache: %s", artifacts.EventsPath),
		fmt.Sprintf("  Analysis cache: %s", artifacts.AnalysisPath),
		fmt.Sprintf("  Event count: %d", artifacts.Analysis.Metrics.EventCount),
		fmt.Sprintf("  User prompts: %d", artifacts.Analysis.Metrics.UserPromptCount),
		fmt.Sprintf("  Turns: %d", artifacts.Analysis.Metrics.TurnCount),
		fmt.Sprintf("  Tool calls: %d", artifacts.Analysis.Metrics.ToolCallCount),
		fmt.Sprintf("  Read files: %d", artifacts.Analysis.Metrics.ReadFileCount),
		fmt.Sprintf("  Edited files: %d", artifacts.Analysis.Metrics.EditedFileCount),
	)
	m := artifacts.Analysis.Metrics
	if usage := m.TokenUsage; usage != nil {
		lines = append(lines,
			"",
			"Token usage:",
			fmt.Sprintf("  Input tokens: %d", usage.InputTokens),
			fmt.Sprintf("  Output tokens: %d", usage.OutputTokens),
			fmt.Sprintf("  Cache read tokens: %d", usage.CacheReadTokens),
			fmt.Sprintf("  Cache write tokens: %d", usage.CacheWriteTokens),
		)
		if artifacts.Analysis.ModelPricing != nil {
			lines = append(lines, fmt.Sprintf("  Estimated cost (USD): $%.2f", m.EstimatedCostUSD))
		}
	}
	if cs := m.ContextStats; cs != nil {
		lines = append(lines, "", "Context:")
		if cs.MaxEffectiveInputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("  Peak effective input: ~%s", formatTokensK(cs.MaxEffectiveInputTokens)),
				fmt.Sprintf("  Avg effective input:  ~%s", formatTokensK(int64(cs.AvgEffectiveInputTokens))),
			)
			if cs.PeakContextBeforeFirstEdit > 0 {
				lines = append(lines, fmt.Sprintf("  Peak before first edit: ~%s", formatTokensK(cs.PeakContextBeforeFirstEdit)))
			}
		}
		if cs.CacheReuse != "" {
			lines = append(lines, fmt.Sprintf("  Cache reuse: %s", cs.CacheReuse))
		}
		if cs.ContextPressure != "" && cs.MaxEffectiveInputTokens > 0 {
			if artifacts.Analysis.ModelPricing != nil && artifacts.Analysis.ModelPricing.ContextWindowTokens > 0 {
				pct := int(float64(cs.MaxEffectiveInputTokens) / float64(artifacts.Analysis.ModelPricing.ContextWindowTokens) * 100)
				lines = append(lines, fmt.Sprintf("  Context pressure: %s (%d%% of %dk)", cs.ContextPressure, pct, artifacts.Analysis.ModelPricing.ContextWindowTokens/1000))
			} else {
				lines = append(lines, fmt.Sprintf("  Context pressure: %s", cs.ContextPressure))
			}
		}
	}
	if es := m.ExplorationStats; es != nil && es.ToolCallsBeforeFirstEdit > 0 {
		lines = append(lines, "", "Before first edit:")
		lines = append(lines,
			fmt.Sprintf("  Tool calls: %d", es.ToolCallsBeforeFirstEdit),
			fmt.Sprintf("  Files read: %d", es.FilesReadBeforeFirstEdit),
			fmt.Sprintf("  Searches:   %d", es.SearchesBeforeFirstEdit),
		)
		if es.CostBeforeFirstEditUSD > 0 {
			costLine := fmt.Sprintf("  Cost:       $%.2f", es.CostBeforeFirstEditUSD)
			if m.EstimatedCostUSD > 0 {
				pct := es.CostBeforeFirstEditUSD / m.EstimatedCostUSD * 100
				label, _ := preEditCostLabel(pct)
				costLine = fmt.Sprintf("  Cost:       $%.2f / $%.2f  %d%%  %s", es.CostBeforeFirstEditUSD, m.EstimatedCostUSD, int(pct), label)
			}
			lines = append(lines, costLine)
		}
	}
	if fs := m.FailureStats; fs != nil && fs.FailedToolCalls > 0 {
		lines = append(lines, "", "Failure handling:")
		lines = append(lines,
			fmt.Sprintf("  %d failed tool calls", fs.FailedToolCalls),
			fmt.Sprintf("  %d recovered", fs.RecoveredFailures),
			fmt.Sprintf("  %d unresolved at session end", fs.UnresolvedFailures),
		)
	}

	cwd := artifacts.Analysis.Session.Cwd
	shortPath := func(p string) string {
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
		return p
	}

	if len(m.ReadFileCounts) > 0 || len(m.EditFileCounts) > 0 {
		type fileStat struct {
			path  string
			reads int
			edits int
		}
		seen := map[string]bool{}
		var stats []fileStat
		for path, reads := range m.ReadFileCounts {
			seen[path] = true
			stats = append(stats, fileStat{path, reads, m.EditFileCounts[path]})
		}
		for path, edits := range m.EditFileCounts {
			if !seen[path] {
				stats = append(stats, fileStat{path, 0, edits})
			}
		}
		sort.Slice(stats, func(i, j int) bool {
			si := stats[i].reads + stats[i].edits*3
			sj := stats[j].reads + stats[j].edits*3
			if si != sj {
				return si > sj
			}
			return stats[i].path < stats[j].path
		})
		lines = append(lines, "", "Files:")
		for i, s := range stats {
			if i >= 10 {
				break
			}
			p := shortPath(s.path)
			editWord := "edits"
			if s.edits == 1 {
				editWord = "edit"
			}
			if s.edits > 0 {
				lines = append(lines, fmt.Sprintf("  %-42s %3d reads   %d %s", p, s.reads, s.edits, editWord))
			} else {
				lines = append(lines, fmt.Sprintf("  %-42s %3d reads", p, s.reads))
			}
		}
	}

	if len(m.PromptBlocks) > 0 {
		appendPaths := func(label string, paths []string) {
			lines = append(lines, fmt.Sprintf("      %s", label))
			for _, p := range paths {
				lines = append(lines, fmt.Sprintf("      - %s", shortPath(p)))
			}
		}
		lines = append(lines, "", "Prompt blocks:")
		for i, b := range m.PromptBlocks {
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("  #%d  %q", i+1, b.Text))
			if len(b.ReadFiles) > 0 {
				appendPaths("read:", b.ReadFiles)
			} else if b.ReadsBeforeFirstEdit > 0 {
				lines = append(lines, fmt.Sprintf("      %d reads before first edit", b.ReadsBeforeFirstEdit))
			}
			if len(b.EditedFiles) > 0 {
				appendPaths("edited:", b.EditedFiles)
			}
		}
	}

	return lines
}

func cachedAnalysisLines(analysis internal.SessionAnalysis, styled bool) []string {
	head := func(value string) string {
		if styled {
			return headStyle.Render(value)
		}
		return value
	}
	file := func(path string) string {
		short := shortFilePath(path, analysis.Session.RepoRoot)
		if short == path {
			short = shortFilePath(path, analysis.Session.Cwd)
		}
		if styled {
			return "  " + renderPathText(short)
		}
		return "  " + short
	}

	m := analysis.Metrics
	summary := fmt.Sprintf("%d events  •  %d prompts  •  %d tools  •  %d reads  •  %d edits",
		m.EventCount, m.UserPromptCount, m.ToolCallCount, m.ReadFileCount, m.EditedFileCount)

	lines := []string{
		head("Analysis"),
		summary,
	}
	if usage := m.TokenUsage; usage != nil {
		lines = append(lines, fmt.Sprintf("%s %d input  •  %d output  •  %d cache read  •  %d cache write",
			head("Tokens:"), usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens))
		if analysis.ModelPricing != nil {
			lines = append(lines, fmt.Sprintf("%s $%.4f", head("Cost:"), m.EstimatedCostUSD))
		}
	}
	if cs := m.ContextStats; cs != nil {
		if cs.MaxEffectiveInputTokens > 0 {
			lines = append(lines, "", head("Context:"))
			lines = append(lines, fmt.Sprintf("  Peak effective input:        ~%s", formatTokensK(cs.MaxEffectiveInputTokens)))
			lines = append(lines, fmt.Sprintf("  Avg effective input:         ~%s", formatTokensK(int64(cs.AvgEffectiveInputTokens))))
			if cs.PeakContextBeforeFirstEdit > 0 {
				lines = append(lines, fmt.Sprintf("  Peak before first edit:      ~%s", formatTokensK(cs.PeakContextBeforeFirstEdit)))
			}
		}
		if cs.CacheReuse != "" && cs.CacheReuse != "None" {
			if cs.MaxEffectiveInputTokens == 0 {
				lines = append(lines, "", head("Context:"))
			}
			lines = append(lines, fmt.Sprintf("  Cache reuse:                 %s", cs.CacheReuse))
		}
		if cs.ContextPressure != "" && cs.MaxEffectiveInputTokens > 0 {
			if analysis.ModelPricing != nil && analysis.ModelPricing.ContextWindowTokens > 0 {
				pct := int(float64(cs.MaxEffectiveInputTokens) / float64(analysis.ModelPricing.ContextWindowTokens) * 100)
				lines = append(lines, fmt.Sprintf("  Context pressure:            %s (%d%% of %dk)", cs.ContextPressure, pct, analysis.ModelPricing.ContextWindowTokens/1000))
			} else {
				lines = append(lines, fmt.Sprintf("  Context pressure:            %s", cs.ContextPressure))
			}
		}
		if m.TokenUsage != nil && m.TokenUsage.OutputTokens > 0 && cs.MaxEffectiveInputTokens > 0 {
			ratio := float64(m.TokenUsage.OutputTokens) / float64(cs.MaxEffectiveInputTokens)
			lines = append(lines, fmt.Sprintf("  Output/peak effective input: %.2f", ratio))
		}
		if m.EditedFileCount > 0 && m.TokenUsage != nil && m.TokenUsage.CacheWriteTokens > 0 {
			lines = append(lines, fmt.Sprintf("  Cache write/edited file:     %s", formatTokensK(m.TokenUsage.CacheWriteTokens/int64(m.EditedFileCount))))
		}
		if m.ToolCallCount > 0 && m.TokenUsage != nil && m.TokenUsage.CacheReadTokens > 0 {
			lines = append(lines, fmt.Sprintf("  Cache read/tool call:        %s", formatTokensK(m.TokenUsage.CacheReadTokens/int64(m.ToolCallCount))))
		}
		if analysis.ModelPricing != nil && m.EstimatedCostUSD > 0 && m.EditedFileCount > 0 {
			lines = append(lines, fmt.Sprintf("  Cost/edited file:            $%.4f", m.EstimatedCostUSD/float64(m.EditedFileCount)))
		}
	}
	if es := m.ExplorationStats; es != nil && es.ToolCallsBeforeFirstEdit > 0 {
		lines = append(lines, "", head("Before first edit:"))
		lines = append(lines, fmt.Sprintf("  Tool calls:  %d", es.ToolCallsBeforeFirstEdit))
		lines = append(lines, fmt.Sprintf("  Files read:  %d", es.FilesReadBeforeFirstEdit))
		lines = append(lines, fmt.Sprintf("  Searches:    %d", es.SearchesBeforeFirstEdit))
		if es.CostBeforeFirstEditUSD > 0 {
			costLine := fmt.Sprintf("  Cost:        $%.2f", es.CostBeforeFirstEditUSD)
			if m.EstimatedCostUSD > 0 {
				pct := es.CostBeforeFirstEditUSD / m.EstimatedCostUSD * 100
				label, color := preEditCostLabel(pct)
				pctStr := fmt.Sprintf("$%.2f / $%.2f  %d%%  %s", es.CostBeforeFirstEditUSD, m.EstimatedCostUSD, int(pct), label)
				if styled && color != "" {
					pctStr = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(pctStr)
				}
				costLine = "  Cost:        " + pctStr
			}
			lines = append(lines, costLine)
		}
	}
	if fs := m.FailureStats; fs != nil && fs.FailedToolCalls > 0 {
		lines = append(lines, "", head("Failure handling:"))
		lines = append(lines, fmt.Sprintf("  %-28s %d", "Failed tool calls:", fs.FailedToolCalls))
		lines = append(lines, fmt.Sprintf("  %-28s %d", "Recovered failures:", fs.RecoveredFailures))
		lines = append(lines, fmt.Sprintf("  %-28s %d", "Unresolved failures:", fs.UnresolvedFailures))
	}
	if len(m.ReadFileCounts) > 0 || len(m.EditFileCounts) > 0 {
		type fileStat struct {
			path  string
			reads int
			edits int
		}
		seen := map[string]bool{}
		var stats []fileStat
		for path, reads := range m.ReadFileCounts {
			seen[path] = true
			stats = append(stats, fileStat{path, reads, m.EditFileCounts[path]})
		}
		for path, edits := range m.EditFileCounts {
			if !seen[path] {
				stats = append(stats, fileStat{path, 0, edits})
			}
		}
		sort.Slice(stats, func(i, j int) bool {
			si := stats[i].reads + stats[i].edits*3
			sj := stats[j].reads + stats[j].edits*3
			if si != sj {
				return si > sj
			}
			return stats[i].path < stats[j].path
		})
		lines = append(lines, "", head(fmt.Sprintf("Files (%d):", len(stats))))
		displayPaths := make([]string, 0, min(len(stats), 10))
		for i, s := range stats {
			if i >= 10 {
				break
			}
			short := shortFilePath(s.path, analysis.Session.RepoRoot)
			if short == s.path {
				short = shortFilePath(s.path, analysis.Session.Cwd)
			}
			displayPaths = append(displayPaths, short)
		}
		pathWidth := fileListPathWidth(displayPaths)
		for i, s := range stats {
			if i >= 10 {
				break
			}
			p := s.path
			short := shortFilePath(p, analysis.Session.RepoRoot)
			if short == p {
				short = shortFilePath(p, analysis.Session.Cwd)
			}
			editWord := "edits"
			if s.edits == 1 {
				editWord = "edit"
			}
			var statsText string
			if s.edits > 0 {
				statsText = fmt.Sprintf("%3d reads   %d %s", s.reads, s.edits, editWord)
			} else {
				statsText = fmt.Sprintf("%3d reads", s.reads)
			}
			lines = append(lines, formatFileListLine(short, pathWidth, statsText))
		}
	} else if len(m.ReadFiles) > 0 || len(m.EditedFiles) > 0 {
		// fallback for old cached analyses without counts
		if len(m.ReadFiles) > 0 {
			lines = append(lines, "", head(fmt.Sprintf("Files read (%d):", len(m.ReadFiles))))
			for _, p := range m.ReadFiles {
				lines = append(lines, file(p))
			}
		}
		if len(m.EditedFiles) > 0 {
			lines = append(lines, "", head(fmt.Sprintf("Files edited (%d):", len(m.EditedFiles))))
			for _, p := range m.EditedFiles {
				lines = append(lines, file(p))
			}
		}
	}
	return lines
}

func renderRepoLabel(session internal.SessionSummary) string {
	if session.RepoName == "" {
		return ""
	}
	return renderRepoName(session, session.RepoName)
}

func renderCwdLabel(session internal.SessionSummary) string {
	cwd := strings.TrimSpace(session.Cwd)
	if cwd == "" {
		return ""
	}
	return renderPathText(cwd)
}

func renderRepoName(session internal.SessionSummary, value string) string {
	if session.RepoInitialized {
		return repoStyle.Render(value)
	}
	return untrackedRepo.Render(value)
}

func renderRepoSuffix(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return renderPathText(value)
}

func preEditCostLabel(pct float64) (label, color string) {
	switch {
	case pct < 10:
		return "DIRECT", "2"
	case pct < 30:
		return "NORMAL", "3"
	case pct < 50:
		return "HEAVY", "208"
	default:
		return "HIGH", "1"
	}
}

func formatAgentOption(agent string, count int) string {
	return fmt.Sprintf("%s (%d)", displayAgentName(agent), count)
}
