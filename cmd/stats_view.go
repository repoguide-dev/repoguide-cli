package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func renderOverviewBlock(s *sessionStat) string {
	lines := []string{
		headStyle.Render("Overview"),
		fmt.Sprintf("  Sessions:      %d", s.sessions),
	}
	if s.costUSD > 0 {
		lines = append(lines, fmt.Sprintf("  Total cost:    %s", lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("$%.2f", s.costUSD))))
	}
	if s.inputTokens+s.outputTokens > 0 {
		lines = append(lines, fmt.Sprintf("  Total tokens:  %s", formatTokensK(s.inputTokens+s.outputTokens)))
	}
	b := exclusiveBaseline(s)
	if b.costUSD > 0 {
		avgCost := fmt.Sprintf("  Avg cost:      $%.2f/session", safeDivide(b.costUSD, float64(b.sessions)))
		avgCost += hCmp(s, func(h *sessionStat) string {
			return fmt.Sprintf("$%.2f/session", safeDivide(h.costUSD, float64(h.sessions)))
		})
		lines = append(lines, avgCost)
	}
	if total := b.inputTokens + b.outputTokens; total > 0 {
		avgTok := fmt.Sprintf("  Avg tokens:    %s/session", fmtTokensShort(safeDivideInt(total, b.sessions)))
		avgTok += hCmp(s, func(h *sessionStat) string {
			return fmtTokensShort(safeDivideInt(h.inputTokens+h.outputTokens, h.sessions)) + "/session"
		})
		lines = append(lines, avgTok)
	}
	if b.edits > 0 && b.costUSD > 0 {
		avgCostPerEdit := fmt.Sprintf("  Avg cost/edit: $%.2f", safeDivide(b.costUSD, float64(b.edits)))
		avgCostPerEdit += hCmp(s, func(h *sessionStat) string {
			if h.edits == 0 {
				return ""
			}
			return fmt.Sprintf("$%.2f", safeDivide(h.costUSD, float64(h.edits)))
		})
		lines = append(lines, avgCostPerEdit)
	}
	return strings.Join(lines, "\n")
}

func printOverview(s *sessionStat) {
	fmt.Println(renderOverviewBlock(s))
}

func renderGroupTableBlock(byLabel string, order []string, groups map[string]*sessionStat) string {
	cols := []tableColumn{
		{title: byLabel, width: 22},
		{title: "Sessions", width: 8},
		{title: "Cost", width: 9},
		{title: "Avg cost", width: 9},
		{title: "Prompts", width: 7},
		{title: "Tools", width: 6},
		{title: "Reads", width: 6},
		{title: "Edits", width: 6},
		{title: "Tokens/session", width: 14},
		{title: "Pre-edit %", width: 10},
	}
	lines := []string{fmt.Sprintf("%s - avg per session", headStyle.Render("By "+byLabel)), renderTableHeader(cols)}
	sepWidth := (len(cols) - 1) * 2
	for _, c := range cols {
		sepWidth += c.width
	}
	lines = append(lines, muted.Render(strings.Repeat("─", sepWidth)))
	for _, key := range order {
		g := groups[key]
		label := key
		if label == "" {
			label = "-"
		}
		n := g.sessions
		lines = append(lines, renderTableRow(cols,
			label,
			strconv.Itoa(n),
			formatCost(g.costUSD),
			formatCost(safeDivide(g.costUSD, float64(n))),
			fmtAvg(g.prompts, n),
			fmtAvg(g.toolCalls, n),
			fmtAvg(g.reads, n),
			fmtAvg(g.edits, n),
			fmtTokensShort(safeDivideInt(g.inputTokens+g.outputTokens, n)),
			preEditTokenPct(g.preEditInputTokens, g.preEditOutputTokens, g.preEditBaseInputTokens, g.preEditBaseOutputTokens),
		))
	}
	return strings.Join(lines, "\n")
}

func printGroupTable(byLabel string, order []string, groups map[string]*sessionStat) {
	fmt.Println(renderGroupTableBlock(byLabel, order, groups))
}

// renderLinesBucketTableBlock compares RepoGuide-used vs. not-used sessions within each
// lines-edited bucket, so cost/exploration differences aren't confounded by task size.
// Each metric gets a no/yes column pair so the two cohorts read side by side.
func renderLinesBucketTableBlock(order []string, groups map[string]*sessionStat) string {
	cols := []tableColumn{
		{title: "Lines edited", width: 14},
		{title: "N (no)", width: 7},
		{title: "N (yes)", width: 7},
		{title: "Cost (no)", width: 10},
		{title: "Cost (yes)", width: 10},
		{title: "Pre-edit (no)", width: 13},
		{title: "Pre-edit (yes)", width: 14},
		{title: "Reads (no)", width: 11},
		{title: "Reads (yes)", width: 11},
	}
	lines := []string{renderTableHeader(cols)}
	sepWidth := (len(cols) - 1) * 2
	for _, c := range cols {
		sepWidth += c.width
	}
	lines = append(lines, muted.Render(strings.Repeat("─", sepWidth)))
	for _, bucket := range order {
		g := groups[bucket]
		no, yes := g.nonRepoguide, g.repoguide
		if (no == nil || no.sessions == 0) && (yes == nil || yes.sessions == 0) {
			continue
		}
		if no == nil {
			no = &sessionStat{}
		}
		if yes == nil {
			yes = &sessionStat{}
		}
		row := func(n int) string {
			if n == 0 {
				return "-"
			}
			return strconv.Itoa(n)
		}
		lines = append(lines, renderTableRow(cols,
			bucket,
			row(no.sessions), row(yes.sessions),
			formatCost(safeDivide(no.costUSD, float64(no.sessions))), formatCost(safeDivide(yes.costUSD, float64(yes.sessions))),
			fmtAvg(no.preEditToolCalls, no.sessions), fmtAvg(yes.preEditToolCalls, yes.sessions),
			fmtAvg(no.preEditReads, no.sessions), fmtAvg(yes.preEditReads, yes.sessions),
		))
	}
	return strings.Join(lines, "\n")
}

func printLinesBucketTable(order []string, groups map[string]*sessionStat) {
	fmt.Println(renderLinesBucketTableBlock(order, groups))
}

func renderContextBlock(s *sessionStat) string {
	if s.peakContextCount == 0 {
		return ""
	}
	b := exclusiveBaseline(s)
	avgPeak := b.peakContextSum / int64(b.peakContextCount)
	peakLine := fmt.Sprintf("  Avg peak input:  ~%s", formatTokensK(avgPeak))
	if h := s.repoguide; h != nil && h.peakContextCount > 0 {
		peakLine += fmt.Sprintf("  (~%s with RepoGuide)", formatTokensK(h.peakContextSum/int64(h.peakContextCount)))
	}
	lines := []string{headStyle.Render("Context"), peakLine}
	if len(b.pressureCounts) > 0 {
		levels := []string{"Low", "Medium", "High", "Critical"}
		parts := make([]string, 0, len(levels))
		for _, level := range levels {
			if n := b.pressureCounts[level]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", level, n))
			}
		}
		if len(parts) > 0 {
			pressureLine := fmt.Sprintf("  Pressure:        %s", strings.Join(parts, "  •  "))
			if h := s.repoguide; h != nil && len(h.pressureCounts) > 0 {
				hParts := make([]string, 0, len(levels))
				for _, level := range levels {
					if n := h.pressureCounts[level]; n > 0 {
						hParts = append(hParts, fmt.Sprintf("%s %d", level, n))
					}
				}
				if len(hParts) > 0 {
					pressureLine += fmt.Sprintf("  (%s with RepoGuide)", strings.Join(hParts, "  •  "))
				}
			}
			lines = append(lines, pressureLine)
		}
	}
	return strings.Join(lines, "\n")
}

func printContextBlock(s *sessionStat) {
	if block := renderContextBlock(s); block != "" {
		fmt.Println()
		fmt.Println(block)
	}
}

func hCmp(s *sessionStat, field func(*sessionStat) string) string {
	if s.repoguide == nil || s.repoguide.sessions == 0 {
		return ""
	}
	v := field(s.repoguide)
	if v == "" || v == "-" || v == "0" {
		return ""
	}
	return fmt.Sprintf("  (%s with RepoGuide)", v)
}

func renderExplorationBlock(s *sessionStat) string {
	if s.preEditToolCalls == 0 && s.preEditReads == 0 {
		return ""
	}
	b := exclusiveBaseline(s)
	n := b.sessions
	lines := []string{
		headStyle.Render("Before first edit"),
		fmt.Sprintf("  Avg tool calls:  %s%s", fmtAvg(b.preEditToolCalls, n),
			hCmp(s, func(h *sessionStat) string { return fmtAvg(h.preEditToolCalls, h.sessions) })),
		fmt.Sprintf("  Avg files read:  %s%s", fmtAvg(b.preEditReads, n),
			hCmp(s, func(h *sessionStat) string { return fmtAvg(h.preEditReads, h.sessions) })),
		fmt.Sprintf("  Avg searches:    %s%s", fmtAvg(b.preEditSearches, n),
			hCmp(s, func(h *sessionStat) string { return fmtAvg(h.preEditSearches, h.sessions) })),
	}
	if pct := preEditTokenPct(b.preEditInputTokens, b.preEditOutputTokens, b.preEditBaseInputTokens, b.preEditBaseOutputTokens); pct != "-" {
		line := fmt.Sprintf("  Read token %%:    %s of all tokens", pct)
		line += hCmp(s, func(h *sessionStat) string {
			return preEditTokenPct(h.preEditInputTokens, h.preEditOutputTokens, h.preEditBaseInputTokens, h.preEditBaseOutputTokens)
		})
		lines = append(lines, line)
	}
	if b.preEditCostUSD > 0 {
		costLine := fmt.Sprintf("  Avg cost:        $%.2f", safeDivide(b.preEditCostUSD, float64(n)))
		costLine += hCmp(s, func(h *sessionStat) string {
			if h.preEditCostUSD <= 0 {
				return ""
			}
			return fmt.Sprintf("$%.2f", safeDivide(h.preEditCostUSD, float64(h.sessions)))
		})
		lines = append(lines, costLine)
		if pct := preEditCostPct(b.preEditCostUSD, b.costUSD); pct != "-" {
			pctLine := fmt.Sprintf("  Avg cost %%:      %s of total", pct)
			pctLine += hCmp(s, func(h *sessionStat) string {
				return preEditCostPct(h.preEditCostUSD, h.costUSD)
			})
			lines = append(lines, pctLine)
		}
	}
	return strings.Join(lines, "\n")
}

func printExplorationBlock(s *sessionStat) {
	if block := renderExplorationBlock(s); block != "" {
		fmt.Println()
		fmt.Println(block)
	}
}

type statsOutlier struct {
	label   string
	value   string
	session analyzedSession
}

func collectOutliers(analyzed []analyzedSession) []statsOutlier {
	var (
		highCost      *statsOutlier
		highContext   *statsOutlier
		mostTools     *statsOutlier
		highPreEdit   *statsOutlier
		maxCost       float64
		maxContext    int64
		maxTools      int
		maxPreEditPct float64
	)
	for _, a := range analyzed {
		m := a.metrics
		if m.EstimatedCostUSD > maxCost {
			maxCost = m.EstimatedCostUSD
			highCost = &statsOutlier{label: "Highest cost", session: a, value: fmt.Sprintf("$%.2f", maxCost)}
		}
		if cs := m.ContextStats; cs != nil && cs.MaxEffectiveInputTokens > maxContext {
			maxContext = cs.MaxEffectiveInputTokens
			highContext = &statsOutlier{label: "Highest context", session: a, value: formatTokensK(maxContext)}
		}
		if m.ToolCallCount > maxTools {
			maxTools = m.ToolCallCount
			mostTools = &statsOutlier{label: "Most tools", session: a, value: fmt.Sprintf("%d calls", maxTools)}
		}
		if es := m.ExplorationStats; es != nil && es.TokensBeforeFirstEdit != nil && m.TokenUsage != nil && m.EditedFileCount > 0 {
			preEditTok := es.TokensBeforeFirstEdit.InputTokens + es.TokensBeforeFirstEdit.OutputTokens
			totalTok := m.TokenUsage.InputTokens + m.TokenUsage.OutputTokens
			if totalTok > 0 {
				pct := float64(preEditTok) / float64(totalTok) * 100
				if pct > maxPreEditPct {
					maxPreEditPct = pct
					highPreEdit = &statsOutlier{label: "Highest pre-edit %", session: a, value: fmt.Sprintf("%.0f%% (%d edits)", pct, m.EditedFileCount)}
				}
			}
		}
	}
	outliers := make([]statsOutlier, 0, 4)
	for _, outlier := range []*statsOutlier{highCost, highContext, mostTools, highPreEdit} {
		if outlier != nil {
			outliers = append(outliers, *outlier)
		}
	}
	return outliers
}

func renderOutliersBlock(outliers []statsOutlier) string {
	if len(outliers) == 0 {
		return ""
	}
	lines := []string{headStyle.Render("Outliers")}
	for _, outlier := range outliers {
		name := sessionLabel(outlier.session.summary)
		repo := valueOrFallback(outlier.session.summary.RepoName, "")
		display := name
		if repo != "" {
			display = repo + " / " + name
		}
		lines = append(lines, fmt.Sprintf("  %-20s %s - %s", outlier.label+":", display, outlier.value))
	}
	return strings.Join(lines, "\n")
}

func printOutliersBlock(outliers []statsOutlier) {
	if block := renderOutliersBlock(outliers); block != "" {
		fmt.Println()
		fmt.Println(block)
	}
}

// repoDisplayBase extracts just the repo name from a full path.
func repoDisplayBase(path string) string {
	path = strings.TrimRight(path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
