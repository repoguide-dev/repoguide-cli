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
		{title: "Avg cost", width: 9},
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
			formatCost(safeDivide(g.costUSD, float64(n))),
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
// Each metric is one column formatted as "without (with)".
func renderLinesBucketTableBlock(order []string, groups map[string]*sessionStat) string {
	cols := []tableColumn{
		{title: "Lines edited", width: 14},
		{title: "N", width: 12},
		{title: "Median $/10 lines", width: 22},
		{title: "Pre-edit calls", width: 16},
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
		withoutWith := func(without, with string) string {
			return fmt.Sprintf("%s (%s)", without, with)
		}
		costPer10Lines := func(s *sessionStat) string {
			m, ok := s.medianCostPer10Lines()
			if !ok {
				return "-"
			}
			return formatCost(m)
		}
		lines = append(lines, renderTableRow(cols,
			bucket,
			withoutWith(strconv.Itoa(no.sessions), strconv.Itoa(yes.sessions)),
			withoutWith(costPer10Lines(no), costPer10Lines(yes)),
			withoutWith(fmtAvg(no.preEditToolCalls, no.sessions), fmtAvg(yes.preEditToolCalls, yes.sessions)),
		))
	}
	return strings.Join(lines, "\n")
}

func printLinesBucketTable(order []string, groups map[string]*sessionStat) {
	fmt.Println(renderLinesBucketTableBlock(order, groups))
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
