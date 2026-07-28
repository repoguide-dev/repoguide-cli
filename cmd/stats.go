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
			total.add(a.metrics, sessionCohort(a.summary))
		}
	}

	groups := map[string]*sessionStat{}
	order := []string{}
	// Keyed by agent then band: pooling agents lets a cheap-agent baseline stand
	// in for an expensive-agent one, which can flip the sign of the comparison.
	cmpGroups := map[string]map[string]*sessionStat{}
	agentOrder := []string{}
	for _, a := range analyzed {
		if outlierPaths[a.summary.Path] {
			continue
		}
		key := statsGroupKey(a.summary, a.metrics, byFlag)
		if _, exists := groups[key]; !exists {
			groups[key] = &sessionStat{}
			order = append(order, key)
		}
		groups[key].add(a.metrics, sessionCohort(a.summary))

		band := comparisonBandLabel(a.metrics)
		if band == "" {
			continue
		}
		agent := valueOrFallback(displayAgentName(a.summary.Agent), "(unknown)")
		if _, exists := cmpGroups[agent]; !exists {
			cmpGroups[agent] = map[string]*sessionStat{}
			agentOrder = append(agentOrder, agent)
		}
		if _, exists := cmpGroups[agent][band]; !exists {
			cmpGroups[agent][band] = &sessionStat{}
		}
		cmpGroups[agent][band].add(a.metrics, sessionCohort(a.summary))
	}
	sort.Slice(order, func(i, j int) bool {
		return groups[order[i]].sessions > groups[order[j]].sessions
	})
	sort.Strings(agentOrder)

	byLabel := strings.ToUpper(byFlag[:1]) + byFlag[1:]
	titleText := fmt.Sprintf("RepoGuide Stats - %s - last %s", scope, sinceFlag)

	fmt.Println(titleStyle.Render(titleText))
	fmt.Println()
	printOverview(&total)
	fmt.Println()
	printGroupTable(byLabel, order, groups)
	printComparisonSection(agentOrder, cmpGroups, &total)
	printOutliersBlock(outliers)
	return nil
}

type lineBucket struct {
	label string
	max   int // 0 means unbounded
}

// lineBuckets is intentionally fine-grained — empty buckets are skipped at render
// time, so this only adds resolution where session volume actually supports it.
var lineBuckets = []lineBucket{
	{"0 (no edits)", -1}, // matched by the total==0 special case, never by max
	{"1-5", 5},
	{"6-20", 20},
	{"21-50", 50},
	{"51-100", 100},
	{"101-250", 250},
	{"251-500", 500},
	{"501-1000", 1000},
	{"1000+", 0},
}

// comparisonBands are deliberately coarser than lineBuckets. The bucket table
// splits sessions three ways (band x agent x cohort), and at realistic session
// volumes nine buckets leaves single-digit cells that read as signal while
// being noise. Three bands is what the data usually supports.
var comparisonBands = []lineBucket{
	{"small (1-50)", 50},
	{"medium (51-250)", 250},
	{"large (250+)", 0},
}

// minComparisonN is the floor below which a cell renders as "-" instead of a
// number. A median over a handful of sessions invites a conclusion the sample
// can't carry, and a blank cell is the honest way to say "no answer here".
const minComparisonN = 8

// comparisonBandLabel returns "" for sessions with no measured edits: cost per
// line is undefined there, so they belong in no band.
func comparisonBandLabel(m internal.SessionMetrics) string {
	total := m.LinesAdded + m.LinesRemoved
	if total <= 0 {
		return ""
	}
	for _, b := range comparisonBands {
		if b.max != 0 && total <= b.max {
			return b.label
		}
	}
	return comparisonBands[len(comparisonBands)-1].label
}

func lineBucketLabel(m internal.SessionMetrics) string {
	total := m.LinesAdded + m.LinesRemoved
	if total == 0 {
		return lineBuckets[0].label
	}
	for _, b := range lineBuckets {
		if b.max != 0 && total <= b.max {
			return b.label
		}
	}
	return lineBuckets[len(lineBuckets)-1].label
}

// ── sessionStat ───────────────────────────────────────────────────────────────

type sessionStat struct {
	sessions     int
	costUSD      float64
	edits        int
	linesEdited  int64
	inputTokens  int64
	outputTokens int64
	// per-session $/10-lines samples, for a median that one expensive
	// tiny-diff session can't distort the way a ratio of sums can
	costPer10LinesSamples []float64
	preEditToolCalls      int
	preEditInputTokens    int64
	preEditOutputTokens   int64
	// total tokens only for sessions that have pre-edit token data (used for %)
	preEditBaseInputTokens  int64
	preEditBaseOutputTokens int64
	repoguide               *sessionStat // sessions that received a briefing
	nonRepoguide            *sessionStat // sessions that never called RepoGuide (self-selected)
	holdout                 *sessionStat // sessions randomly refused a briefing (true control)
}

// cohort is which arm of the comparison a session belongs to. holdout sessions
// asked for a briefing and were randomly refused one, so they are the only
// control group that isn't self-selected; cohortNone sessions never asked,
// which usually says more about the task than about RepoGuide.
type cohort int

const (
	cohortNone cohort = iota
	cohortRepoGuide
	cohortHoldout
)

func sessionCohort(s internal.SessionSummary) cohort {
	switch {
	case s.UsedRepoGuide:
		return cohortRepoGuide
	case s.RepoGuideHoldout:
		return cohortHoldout
	default:
		return cohortNone
	}
}

func (s *sessionStat) add(m internal.SessionMetrics, c cohort) {
	s.addBase(m)
	target := &s.nonRepoguide
	switch c {
	case cohortRepoGuide:
		target = &s.repoguide
	case cohortHoldout:
		target = &s.holdout
	}
	if *target == nil {
		*target = &sessionStat{}
	}
	(*target).addBase(m)
}

// addBase accumulates m into s without touching the repoguide/nonRepoguide
// sub-cohorts — add() calls this once for the top-level total and once for
// whichever sub-cohort the session belongs to.
func (s *sessionStat) addBase(m internal.SessionMetrics) {
	s.sessions++
	s.costUSD += m.EstimatedCostUSD
	s.edits += m.EditedFileCount
	s.linesEdited += int64(m.LinesAdded + m.LinesRemoved)
	if lines := m.LinesAdded + m.LinesRemoved; lines > 0 && m.EstimatedCostUSD > 0 {
		s.costPer10LinesSamples = append(s.costPer10LinesSamples, m.EstimatedCostUSD/float64(lines)*10)
	}
	if m.TokenUsage != nil {
		s.inputTokens += m.TokenUsage.InputTokens
		s.outputTokens += m.TokenUsage.OutputTokens
	}
	if es := m.ExplorationStats; es != nil {
		s.preEditToolCalls += es.ToolCallsBeforeFirstEdit
		if es.TokensBeforeFirstEdit != nil {
			s.preEditInputTokens += es.TokensBeforeFirstEdit.InputTokens
			s.preEditOutputTokens += es.TokensBeforeFirstEdit.OutputTokens
			if m.TokenUsage != nil {
				s.preEditBaseInputTokens += m.TokenUsage.InputTokens
				s.preEditBaseOutputTokens += m.TokenUsage.OutputTokens
			}
		}
	}
}

// medianCostPer10Lines is robust to a single expensive tiny-diff session, which
// a ratio of sums (total cost / total lines) is not.
func (s *sessionStat) medianCostPer10Lines() (float64, bool) {
	// ponytail: <3 samples is too few for a meaningful median; render as "-"
	if len(s.costPer10LinesSamples) < 3 {
		return 0, false
	}
	v := append([]float64(nil), s.costPer10LinesSamples...)
	sort.Float64s(v)
	mid := len(v) / 2
	if len(v)%2 == 0 {
		return (v[mid-1] + v[mid]) / 2, true
	}
	return v[mid], true
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
