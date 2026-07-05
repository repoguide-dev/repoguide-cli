package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	statsCmd.Flags().String("repo", "", "Filter to a specific repo (default: current git repo)")
	statsCmd.Flags().BoolP("global", "g", false, "Show all repos grouped by repo (overrides --repo)")
	statsCmd.Flags().Bool("all", false, "Alias for --global")
	statsCmd.Flags().String("since", "30d", "Limit to sessions newer than duration (e.g. 7d, 30d, 90d, 24h)")
	statsCmd.Flags().String("by", "", "Group by 'model', 'repo', 'agent', or 'lines' (lines-edited buckets; default: model; repo implies --global)")
	statsCmd.Flags().Bool("graph", false, "Interactive time-series graph")
	_ = statsCmd.Flags().MarkHidden("all")
	root.AddCommand(statsCmd)
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Summarize AI coding session statistics",
	RunE:  runStats,
}

type analyzedSession struct {
	summary internal.SessionSummary
	metrics internal.SessionMetrics
}

func runStats(cmd *cobra.Command, _ []string) error {
	repoFlag, _ := cmd.Flags().GetString("repo")
	globalFlag, _ := cmd.Flags().GetBool("global")
	allFlag, _ := cmd.Flags().GetBool("all")
	sinceFlag, _ := cmd.Flags().GetString("since")
	byFlag, _ := cmd.Flags().GetString("by")
	graphFlag, _ := cmd.Flags().GetBool("graph")

	byFlag = strings.ToLower(strings.TrimSpace(byFlag))
	if byFlag != "" && byFlag != "model" && byFlag != "repo" && byFlag != "agent" && byFlag != "lines" {
		return fmt.Errorf("--by must be 'model', 'repo', 'agent', or 'lines'")
	}

	isGlobal := globalFlag || allFlag || byFlag == "repo"

	repoFilter := strings.TrimSpace(repoFlag)
	if isGlobal {
		repoFilter = ""
	} else if repoFilter == "" {
		repoFilter = detectCwdGitRoot()
		if repoFilter == "" {
			isGlobal = true
		}
	}

	if byFlag == "" {
		if isGlobal {
			byFlag = "repo"
		} else {
			byFlag = "model"
		}
	}

	var since time.Time
	if sinceFlag != "" {
		d, err := parseDuration(sinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since %q: use e.g. 30d, 7d, 24h", sinceFlag)
		}
		since = time.Now().Add(-d)
	}

	page, err := sessionimport.LoadAllAgentsSessionPage(0, -1, sessionimport.SessionLoadOptions{Repo: repoFilter})
	if err != nil {
		return err
	}
	sessions := page.Sessions
	if !since.IsZero() {
		filtered := sessions[:0]
		for _, s := range sessions {
			if s.Timestamp.After(since) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	// Build missing analyses
	toAnalyze := make([]internal.SessionSummary, 0, len(sessions))
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

	// Collect analyzed sessions
	analyzed := make([]analyzedSession, 0, len(sessions))
	for _, s := range sessions {
		cached, ok, _ := sessionimport.LoadCachedSessionAnalysis(s)
		var m internal.SessionMetrics
		if ok {
			m = cached.Analysis.Metrics
		}
		analyzed = append(analyzed, analyzedSession{summary: s, metrics: m})
	}

	if graphFlag {
		return runStatsGraph(analyzed)
	}

	scope := "all repos"
	if repoFilter != "" {
		scope = repoDisplayBase(repoFilter)
	}

	outliers := collectOutliers(analyzed)
	outlierPaths := make(map[string]bool, len(outliers))
	for _, o := range outliers {
		outlierPaths[o.session.summary.Path] = true
	}

	var total sessionStat
	for _, a := range analyzed {
		if !outlierPaths[a.summary.Path] {
			total.add(a.metrics, a.summary.UsedRepoGuide)
		}
	}

	groups := map[string]*sessionStat{}
	order := []string{}
	lineGroups := map[string]*sessionStat{}
	lineOrder := []string{}
	for _, a := range analyzed {
		if outlierPaths[a.summary.Path] {
			continue
		}
		key := statsGroupKey(a.summary, a.metrics, byFlag)
		if _, exists := groups[key]; !exists {
			groups[key] = &sessionStat{}
			order = append(order, key)
		}
		groups[key].add(a.metrics, a.summary.UsedRepoGuide)

		lineKey := lineBucketLabel(a.metrics)
		if _, exists := lineGroups[lineKey]; !exists {
			lineGroups[lineKey] = &sessionStat{}
			lineOrder = append(lineOrder, lineKey)
		}
		lineGroups[lineKey].add(a.metrics, a.summary.UsedRepoGuide)
	}
	sort.Slice(order, func(i, j int) bool {
		return groups[order[i]].sessions > groups[order[j]].sessions
	})
	sort.Slice(lineOrder, func(i, j int) bool { return lineBucketRank(lineOrder[i]) < lineBucketRank(lineOrder[j]) })

	byLabel := strings.ToUpper(byFlag[:1]) + byFlag[1:]
	titleText := fmt.Sprintf("RepoGuide Stats - %s - last %s", scope, sinceFlag)

	fmt.Println(titleStyle.Render(titleText))
	fmt.Println()
	printOverview(&total)
	fmt.Println()
	printGroupTable(byLabel, order, groups)
	if total.repoguide != nil && total.repoguide.sessions > 0 {
		fmt.Println()
		fmt.Printf("%s - RepoGuide vs. not, per bucket\n", headStyle.Render("By lines edited"))
		fmt.Println()
		printLinesBucketTable(lineOrder, lineGroups)
	}
	printContextBlock(&total)
	printExplorationBlock(&total)
	printOutliersBlock(outliers)
	return nil
}

// lineBucketRank orders line-edit buckets from smallest to largest instead of by session count.
func lineBucketRank(bucket string) int {
	for i, b := range lineBuckets {
		if b.label == bucket {
			return i
		}
	}
	return len(lineBuckets)
}

type lineBucket struct {
	label string
	max   int // 0 means unbounded
}

var lineBuckets = []lineBucket{
	{"1-20", 20},
	{"21-100", 100},
	{"101-500", 500},
	{"500+", 0},
}

func lineBucketLabel(m internal.SessionMetrics) string {
	total := m.LinesAdded + m.LinesRemoved
	for _, b := range lineBuckets {
		if b.max != 0 && total <= b.max {
			return b.label
		}
	}
	return lineBuckets[len(lineBuckets)-1].label
}

// ── sessionStat ───────────────────────────────────────────────────────────────

type sessionStat struct {
	sessions            int
	costUSD             float64
	prompts             int
	toolCalls           int
	edits               int
	reads               int
	inputTokens         int64
	outputTokens        int64
	preEditToolCalls    int
	preEditReads        int
	preEditSearches     int
	preEditCostUSD      float64
	preEditInputTokens  int64
	preEditOutputTokens int64
	// total tokens only for sessions that have pre-edit token data (used for %)
	preEditBaseInputTokens  int64
	preEditBaseOutputTokens int64
	peakContextSum          int64
	peakContextCount        int
	pressureCounts          map[string]int
	repoguide               *sessionStat // sessions where repoguide was used (subset of the total)
	nonRepoguide            *sessionStat // sessions where repoguide was not used (disjoint from repoguide)
}

func (s *sessionStat) add(m internal.SessionMetrics, usedRepoGuide bool) {
	s.addBase(m)
	if usedRepoGuide {
		if s.repoguide == nil {
			s.repoguide = &sessionStat{}
		}
		s.repoguide.addBase(m)
	} else {
		if s.nonRepoguide == nil {
			s.nonRepoguide = &sessionStat{}
		}
		s.nonRepoguide.addBase(m)
	}
}

// addBase accumulates m into s without touching the repoguide/nonRepoguide
// sub-cohorts — add() calls this once for the top-level total and once for
// whichever sub-cohort the session belongs to.
func (s *sessionStat) addBase(m internal.SessionMetrics) {
	s.sessions++
	s.costUSD += m.EstimatedCostUSD
	s.prompts += m.UserPromptCount
	s.toolCalls += m.ToolCallCount
	s.edits += m.EditedFileCount
	s.reads += m.ReadFileCount
	if m.TokenUsage != nil {
		s.inputTokens += m.TokenUsage.InputTokens
		s.outputTokens += m.TokenUsage.OutputTokens
	}
	if es := m.ExplorationStats; es != nil {
		s.preEditToolCalls += es.ToolCallsBeforeFirstEdit
		s.preEditReads += es.FilesReadBeforeFirstEdit
		s.preEditSearches += es.SearchesBeforeFirstEdit
		s.preEditCostUSD += es.CostBeforeFirstEditUSD
		if es.TokensBeforeFirstEdit != nil {
			s.preEditInputTokens += es.TokensBeforeFirstEdit.InputTokens
			s.preEditOutputTokens += es.TokensBeforeFirstEdit.OutputTokens
			if m.TokenUsage != nil {
				s.preEditBaseInputTokens += m.TokenUsage.InputTokens
				s.preEditBaseOutputTokens += m.TokenUsage.OutputTokens
			}
		}
	}
	if cs := m.ContextStats; cs != nil && cs.MaxEffectiveInputTokens > 0 {
		s.peakContextSum += cs.MaxEffectiveInputTokens
		s.peakContextCount++
		if s.pressureCounts == nil {
			s.pressureCounts = map[string]int{}
		}
		if cs.ContextPressure != "" {
			s.pressureCounts[cs.ContextPressure]++
		}
	}
}

// exclusiveBaseline returns the non-RepoGuide cohort when both cohorts are present, so
// "Overall" vs. "With RepoGuide" compares two disjoint sets instead of a subset vs. its
// superset (which the subset is always inside of, biasing the comparison).
func exclusiveBaseline(s *sessionStat) *sessionStat {
	if s.repoguide != nil && s.repoguide.sessions > 0 && s.nonRepoguide != nil && s.nonRepoguide.sessions > 0 {
		return s.nonRepoguide
	}
	return s
}

// ── small helpers ─────────────────────────────────────────────────────────────

func statsGroupKey(s internal.SessionSummary, m internal.SessionMetrics, by string) string {
	switch by {
	case "model":
		return valueOrFallback(s.Model, "(unknown)")
	case "repo":
		return valueOrFallback(s.RepoName, "(no repo)")
	case "agent":
		return valueOrFallback(displayAgentName(s.Agent), "(unknown)")
	case "lines":
		return lineBucketLabel(m)
	}
	return ""
}

func formatCost(v float64) string {
	if v <= 0 {
		return "-"
	}
	if v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func fmtAvg(total, count int) string {
	if count == 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", float64(total)/float64(count))
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func safeDivideInt(a int64, b int) int64 {
	if b == 0 {
		return 0
	}
	return a / int64(b)
}

// preEditTokenPct returns the pre-edit token share as "45%" or "-" when unavailable.
func preEditTokenPct(preEditIn, preEditOut, totalIn, totalOut int64) string {
	total := totalIn + totalOut
	preEdit := preEditIn + preEditOut
	if total == 0 || preEdit == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", float64(preEdit)/float64(total)*100)
}

// preEditCostPct returns the pre-edit cost share as "45%" or "-" when unavailable.
func preEditCostPct(preEditCost, totalCost float64) string {
	if totalCost == 0 || preEditCost == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", preEditCost/totalCost*100)
}

// fmtTokensShort renders token counts compactly for table cells: 1.2M, 34k, 891.
func fmtTokensShort(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

// parseDuration extends time.ParseDuration with d (days) and w (weeks).
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
