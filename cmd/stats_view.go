package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderOverviewBlock describes the session pool. It deliberately carries no
// "with RepoGuide" comparison: RepoGuide is chosen for harder tasks and is not
// evenly spread across agents, so a headline split here compares two unlike
// populations and reliably reads as an effect that isn't there. The per-agent
// comparison table is the only place that comparison can be made honestly.
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
	if s.costUSD > 0 {
		lines = append(lines, fmt.Sprintf("  Avg cost:      $%.2f/session", safeDivide(s.costUSD, float64(s.sessions))))
	}
	if total := s.inputTokens + s.outputTokens; total > 0 {
		lines = append(lines, fmt.Sprintf("  Avg tokens:    %s/session", fmtTokensShort(safeDivideInt(total, s.sessions))))
	}
	if s.edits > 0 && s.costUSD > 0 {
		lines = append(lines, fmt.Sprintf("  Avg cost/edit: $%.2f", safeDivide(s.costUSD, float64(s.edits))))
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

// hasHoldout reports whether a randomized control arm exists anywhere in the
// data. When it does the comparison is an experiment; when it doesn't, it is a
// description of two populations the user chose between, and must say so.
func hasHoldout(groups map[string]map[string]*sessionStat) bool {
	for _, bands := range groups {
		for _, g := range bands {
			if g.holdout != nil && g.holdout.sessions > 0 {
				return true
			}
		}
	}
	return false
}

// renderComparisonBlock compares RepoGuide against its control arm within each
// agent and size band. Both stratifications are load-bearing: task size drives
// cost per line, and agents differ several-fold in cost per line, so pooling
// either one lets the mix between groups masquerade as an effect.
func renderComparisonBlock(agents []string, groups map[string]map[string]*sessionStat, randomized bool) string {
	control := func(g *sessionStat) *sessionStat {
		if randomized {
			return g.holdout
		}
		return g.nonRepoguide
	}
	cols := []tableColumn{
		{title: "Lines edited", width: 17},
		{title: "N", width: 12},
		{title: "Median $/10 lines", width: 22},
		{title: "Pre-edit calls", width: 16},
	}
	controlName := "never called RepoGuide"
	if randomized {
		controlName = "holdout"
	}

	var out []string
	if randomized {
		out = append(out, headStyle.Render("RepoGuide vs. holdout")+" - randomized, per agent")
	} else {
		out = append(out, headStyle.Render("RepoGuide vs. not")+" - observational, per agent")
	}
	out = append(out, muted.Render(fmt.Sprintf("  each cell: %s (with RepoGuide)", controlName)))
	if !randomized {
		out = append(out, muted.Render("  Not an experiment: RepoGuide was chosen per session, typically on harder"))
		out = append(out, muted.Render("  tasks, so these differences include that choice. Enable a holdout to"))
		out = append(out, muted.Render("  measure an effect: repoguide repo config --holdout 20"))
	}
	out = append(out, muted.Render(fmt.Sprintf("  cells with fewer than %d sessions per arm show as \"-\"", minComparisonN)))

	shown := 0
	for _, agent := range agents {
		bands := groups[agent]
		var rows []string
		for _, band := range comparisonBands {
			g := bands[band.label]
			if g == nil {
				continue
			}
			no, yes := control(g), g.repoguide
			if no == nil {
				no = &sessionStat{}
			}
			if yes == nil {
				yes = &sessionStat{}
			}
			if no.sessions == 0 && yes.sessions == 0 {
				continue
			}
			pair := func(a, b string) string { return fmt.Sprintf("%s (%s)", a, b) }
			// Guarded per arm, not per row: one arm having enough sessions
			// says nothing about the other, and a lone number next to a blank
			// is what tells the user which side is missing.
			guarded := func(s *sessionStat, f func(*sessionStat) string) string {
				if s.sessions < minComparisonN {
					return "-"
				}
				return f(s)
			}
			costPer10Lines := func(s *sessionStat) string {
				m, ok := s.medianCostPer10Lines()
				if !ok {
					return "-"
				}
				return formatCost(m)
			}
			rows = append(rows, renderTableRow(cols,
				band.label,
				pair(strconv.Itoa(no.sessions), strconv.Itoa(yes.sessions)),
				pair(guarded(no, costPer10Lines), guarded(yes, costPer10Lines)),
				pair(guarded(no, func(s *sessionStat) string { return fmtAvg(s.preEditToolCalls, s.sessions) }),
					guarded(yes, func(s *sessionStat) string { return fmtAvg(s.preEditToolCalls, s.sessions) })),
			))
		}
		if len(rows) == 0 {
			continue
		}
		shown++
		sepWidth := (len(cols) - 1) * 2
		for _, c := range cols {
			sepWidth += c.width
		}
		out = append(out, "", "  "+headStyle.Render(agent), renderTableHeader(cols), muted.Render(strings.Repeat("─", sepWidth)))
		out = append(out, rows...)
	}
	if shown == 0 {
		return ""
	}
	return strings.Join(out, "\n")
}

func printComparisonSection(agents []string, groups map[string]map[string]*sessionStat, total *sessionStat) {
	if total.repoguide == nil || total.repoguide.sessions == 0 {
		return
	}
	block := renderComparisonBlock(agents, groups, hasHoldout(groups))
	if block == "" {
		return
	}
	fmt.Println()
	fmt.Println(block)
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
