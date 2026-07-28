package cmd

import (
	"strings"
	"testing"

	"github.com/repoguide/repoguide-cli/internal"
)

func metricsFor(lines int, cost float64) internal.SessionMetrics {
	return internal.SessionMetrics{LinesAdded: lines, EstimatedCostUSD: cost}
}

func fillBand(g *sessionStat, c cohort, n, lines int, cost float64) {
	for i := 0; i < n; i++ {
		g.add(metricsFor(lines, cost), c)
	}
}

// The pooled table used to let one agent's cheap baseline stand in for another
// agent's missing one, which is what turned a null result into a 2-3x penalty.
func TestComparisonSplitsByAgent(t *testing.T) {
	groups := map[string]map[string]*sessionStat{
		"claude": {"small (1-50)": &sessionStat{}},
		"codex":  {"small (1-50)": &sessionStat{}},
	}
	fillBand(groups["claude"]["small (1-50)"], cohortNone, 10, 20, 1.0)
	fillBand(groups["claude"]["small (1-50)"], cohortRepoGuide, 10, 20, 1.0)
	fillBand(groups["codex"]["small (1-50)"], cohortRepoGuide, 10, 20, 4.0)

	out := renderComparisonBlock([]string{"claude", "codex"}, groups, false)
	if !strings.Contains(out, "claude") || !strings.Contains(out, "codex") {
		t.Fatalf("both agents must appear, got:\n%s", out)
	}
	if !strings.Contains(out, "observational") {
		t.Error("a non-randomized comparison must be labelled as such")
	}
	if !strings.Contains(out, "--holdout") {
		t.Error("must point the user at the holdout when there is no control arm")
	}
}

// Codex had 1 baseline session in the large band; printing a median over that
// is the failure this guard exists to prevent.
func TestThinArmRendersBlank(t *testing.T) {
	g := &sessionStat{}
	fillBand(g, cohortNone, 2, 20, 1.0) // below minComparisonN
	fillBand(g, cohortRepoGuide, 12, 20, 2.0)
	groups := map[string]map[string]*sessionStat{"codex": {"small (1-50)": g}}

	out := renderComparisonBlock([]string{"codex"}, groups, false)
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "small (1-50)") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("expected a row for the band, got:\n%s", out)
	}
	if !strings.Contains(row, "2 (12)") {
		t.Errorf("N must stay visible so the thin arm is obvious: %q", row)
	}
	if !strings.Contains(row, "- (") {
		t.Errorf("under-powered arm must render as \"-\", got: %q", row)
	}
}

func TestHoldoutSwitchesToRandomizedFraming(t *testing.T) {
	g := &sessionStat{}
	fillBand(g, cohortHoldout, 10, 20, 1.0)
	fillBand(g, cohortRepoGuide, 10, 20, 1.0)
	fillBand(g, cohortNone, 30, 20, 9.0) // self-selected: must be ignored
	groups := map[string]map[string]*sessionStat{"claude": {"small (1-50)": g}}

	out := renderComparisonBlock([]string{"claude"}, groups, hasHoldout(groups))
	if !strings.Contains(out, "randomized") {
		t.Errorf("holdout data must be framed as randomized, got:\n%s", out)
	}
	if !strings.Contains(out, "10 (10)") {
		t.Errorf("control arm must be the holdout, not the never-called sessions:\n%s", out)
	}
}

// Overview is the most-read block; a headline split there compares populations
// that differ in task difficulty and agent mix.
func TestOverviewHasNoRepoGuideComparison(t *testing.T) {
	s := &sessionStat{}
	fillBand(s, cohortRepoGuide, 5, 20, 4.0)
	fillBand(s, cohortNone, 5, 20, 1.0)
	if out := renderOverviewBlock(s); strings.Contains(out, "with RepoGuide") {
		t.Errorf("overview must not carry a RepoGuide comparison:\n%s", out)
	}
}

// The headline must be able to say RepoGuide cost more. A savings figure that
// can only come out positive isn't a measurement.
func TestSavingsCanBeNegative(t *testing.T) {
	g := &sessionStat{}
	fillBand(g, cohortNone, 10, 100, 1.0)      // $0.10/10 lines
	fillBand(g, cohortRepoGuide, 10, 100, 3.0) // $0.30/10 lines
	groups := map[string]map[string]*sessionStat{"claude": {"medium (51-250)": g}}

	est, ok := estimateSavings([]string{"claude"}, groups, false)
	if !ok {
		t.Fatal("expected an estimate")
	}
	if est.usd >= 0 {
		t.Errorf("RepoGuide costing more must yield a negative figure, got %.2f", est.usd)
	}
	if out := renderSavingsHeadline(est); !strings.Contains(out, "Extra cost with RepoGuide") {
		t.Errorf("negative savings must render as extra cost:\n%s", out)
	}
}

// Dropping thin strata means the figure can cover a fraction of the work;
// coverage has to be stated or it reads as a total.
func TestSavingsExcludesThinStrataAndReportsCoverage(t *testing.T) {
	rich, thin := &sessionStat{}, &sessionStat{}
	fillBand(rich, cohortNone, 10, 100, 2.0)
	fillBand(rich, cohortRepoGuide, 10, 100, 1.0)
	fillBand(thin, cohortNone, 2, 100, 2.0) // under minComparisonN
	fillBand(thin, cohortRepoGuide, 10, 100, 1.0)
	groups := map[string]map[string]*sessionStat{
		"claude": {"medium (51-250)": rich, "small (1-50)": thin},
	}

	est, ok := estimateSavings([]string{"claude"}, groups, false)
	if !ok {
		t.Fatal("expected an estimate")
	}
	if est.strata != 1 {
		t.Errorf("thin stratum must be excluded, got %d strata", est.strata)
	}
	if est.coveredLines >= est.totalLines {
		t.Errorf("excluded lines must still count toward the total: %d/%d", est.coveredLines, est.totalLines)
	}
	if out := renderSavingsHeadline(est); !strings.Contains(out, "covers 50%") {
		t.Errorf("partial coverage must be stated:\n%s", out)
	}
}

// Pooling agents is what produced a fake result before; the estimator must
// compare within an agent, never across.
func TestSavingsStratifiesByAgent(t *testing.T) {
	cheap, pricey := &sessionStat{}, &sessionStat{}
	fillBand(cheap, cohortRepoGuide, 10, 100, 1.0) // no control arm at all
	fillBand(pricey, cohortNone, 10, 100, 5.0)     // no treated arm at all
	groups := map[string]map[string]*sessionStat{
		"codex":  {"medium (51-250)": cheap},
		"claude": {"medium (51-250)": pricey},
	}

	est, ok := estimateSavings([]string{"claude", "codex"}, groups, false)
	if ok && est.strata > 0 {
		t.Errorf("neither agent has both arms; must not borrow the other's baseline (got %.2f over %d strata)", est.usd, est.strata)
	}
}

func TestSavingsUsesHoldoutWhenRandomized(t *testing.T) {
	g := &sessionStat{}
	fillBand(g, cohortHoldout, 10, 100, 2.0)
	fillBand(g, cohortRepoGuide, 10, 100, 1.0)
	fillBand(g, cohortNone, 40, 100, 99.0) // self-selected: must be ignored
	groups := map[string]map[string]*sessionStat{"claude": {"medium (51-250)": g}}

	est, ok := estimateSavings([]string{"claude"}, groups, true)
	if !ok {
		t.Fatal("expected an estimate")
	}
	// 10 sessions x 100 lines = 1000 lines; ($0.20 - $0.10) per 10 lines = $10.
	if est.usd < 9.5 || est.usd > 10.5 {
		t.Errorf("must price against the holdout arm, not the never-called one: got %.2f", est.usd)
	}
	if out := renderSavingsHeadline(est); !strings.Contains(out, "measured against a randomized holdout") {
		t.Errorf("randomized estimate must say so:\n%s", out)
	}
}
