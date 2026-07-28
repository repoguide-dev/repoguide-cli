package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/repoguide/repoguide-cli/internal/analysis"
)

func TestReportPageRenders(t *testing.T) {
	bundle := analysis.RepoAnalysisBundle{
		Repo:    analysis.RepoAnalysisRepo{Name: "demo"},
		Summary: analysis.RepoAnalysisSummary{Sessions: 5, FileReads: 100, ContextTokens: 50000},
		Files: []analysis.RepoAnalysisFile{
			{Path: "web/src/landing.jsx", Sessions: 10, Reads: 20, EditSessions: 2, ContextTokens: 500},
			{Path: "repoguide-cloud", Sessions: 4, Reads: 4, EditSessions: 0, ContextTokens: 10},
		},
		TracePatterns: []analysis.RepoAnalysisTrace{
			// Real evidence: should survive filtering and appear in the table.
			{
				Type: "expensive_edit_target", Target: "repo_handlers.go", Sessions: 12, AvgReadsBeforeEdit: 9.8,
				TopPrecedingReads: []analysis.RepoAnalysisPathCount{{Path: "repos_test.go", Sessions: 8}},
			},
			// Zero reads-before-edit: real bug this test guards against (was
			// shown as a "biggest opportunity" despite 0 investigation reads).
			{Type: "expensive_edit_target", Target: "repoguide-cloud", Sessions: 27, AvgReadsBeforeEdit: 0},
			// Directory/submodule/git-ref targets: must never appear as a "path".
			{Type: "expensive_edit_target", Target: "repoguide-cli", Sessions: 24, AvgReadsBeforeEdit: 4},
			{Type: "expensive_edit_target", Target: "origin/main", Sessions: 16, AvgReadsBeforeEdit: 3},
			// Wrong trace type: no real read-before-edit semantics, must be excluded.
			{Type: "read_before_edit_pattern", Source: "a.go", Target: "b.go", Sessions: 9},
		},
		Discoverability: analysis.RepoAnalysisDiscoverability{DeadEndSearches: 715},
	}
	view := buildReportView(bundle)
	view.UseRepoGuideURL = "https://repoguide.dev/#install"
	view.RepoAverages = analysis.RepoStartCost{Sessions: 4, AvgReads: 8.25, AvgContextTokens: 12400, AvgToolCalls: 11.5}

	if len(view.RecurringPaths) != 1 {
		t.Fatalf("expected only the one trace with real evidence to survive filtering, got %d: %+v", len(view.RecurringPaths), view.RecurringPaths)
	}
	if got := view.RecurringPaths[0].Target; got != "repo_handlers.go" {
		t.Fatalf("expected repo_handlers.go to be the only recurring path, got %q", got)
	}
	var buf bytes.Buffer
	if err := reportPageTmpl.Execute(&buf, view); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()
	if len(out) < 500 {
		t.Fatalf("output too short: %d", len(out))
	}
	for _, bad := range []string{">repoguide-cloud<", ">repoguide-cli<", ">origin/main<", "$0.00"} {
		if bytes.Contains(buf.Bytes(), []byte(bad)) {
			t.Fatalf("rendered output must not contain %q", bad)
		}
	}
	for _, want := range []string{"The agent navigation tax", "rediscovering your repo", "8.2 files", "11.5 tool calls", "The code isn’t the bottleneck", "Use RepoGuide on your next task", ">Share</button>", "Share this report", "Create 14-day report link", "Copy post &amp; open LinkedIn", "Share post on X", "https://x.com/intent/post"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("expected rendered output to contain %q", want)
		}
	}
	for _, unwanted := range []string{"LinkedIn-ready takeaway", "The average navigation tax before the first edit", `class="repo-avg"`, "Share report · 7-day link", "Keep local only"} {
		if bytes.Contains(buf.Bytes(), []byte(unwanted)) {
			t.Fatalf("expected simplified report not to contain %q", unwanted)
		}
	}
	t.Logf("rendered %d bytes", len(out))
}

func TestReportPageRendersEmpty(t *testing.T) {
	view := buildReportView(analysis.RepoAnalysisBundle{Repo: analysis.RepoAnalysisRepo{Name: "empty"}})
	var buf bytes.Buffer
	if err := reportPageTmpl.Execute(&buf, view); err != nil {
		t.Fatalf("render failed on empty bundle: %v", err)
	}
}

func TestReportPageRendersSessionStrips(t *testing.T) {
	view := buildReportView(analysis.RepoAnalysisBundle{Repo: analysis.RepoAnalysisRepo{Name: "demo"}})
	view.SessionStrips = []analysis.SessionStrip{{
		Title: "Fix Gson unexpected JSON structure", Calls: 3, EditIndex: 3, CostUSD: 1.25,
		Markers: []string{"cold", "reopen", "edit"},
		Labels:  []string{"Grep · handler", "Read · api/main.go", "Edit · api/main.go"},
		Files:   []string{"", "api/main.go", "api/main.go"},
	}}
	var buf bytes.Buffer
	if err := reportPageTmpl.Execute(&buf, view); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	for _, want := range []string{"Every session as a trace — 1 session", "Fix Gson unexpected JSON structure", "3 calls · edit at 3 · $1.25",
		`data-l="Read · api/main.go" data-f="api/main.go"`, `class="m-edit"`, "re-read a file"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, buf.String())
		}
	}
}

func TestReportPageRendersWorstSessionCaseStudy(t *testing.T) {
	bundle := analysis.RepoAnalysisBundle{
		Repo:    analysis.RepoAnalysisRepo{Name: "demo"},
		Summary: analysis.RepoAnalysisSummary{Sessions: 20, FileReads: 200, ContextTokens: 100000},
		TracePatterns: []analysis.RepoAnalysisTrace{
			{
				Type: "expensive_edit_target", Target: "api/main.go", Sessions: 13, AvgReadsBeforeEdit: 7.3,
				TopPrecedingReads: []analysis.RepoAnalysisPathCount{{Path: "api/handler/session.go", Sessions: 10}},
			},
		},
	}
	view := buildReportView(bundle)
	if !view.HasBiggestPath {
		t.Fatalf("expected a biggest path to be selected")
	}
	view.HasWorstSession = true
	view.UseRepoGuideURL = "https://repoguide.dev/#install"
	view.RepoAverages = analysis.RepoStartCost{Sessions: 12, AvgReads: 6.5, AvgContextTokens: 9000, AvgToolCalls: 8.2}
	view.WorstSession = analysis.WorstSessionCase{
		SessionTitle:  "Repository deletion bug",
		Prompt:        "This fallback prompt should not render.",
		FilesRead:     24,
		Searches:      11,
		FilesReopened: 3,
		Detour:        []string{"server.go", "model.go", "db.go"},
		EditedFile:    "api/main.go",
		Score:         38,
	}

	var buf bytes.Buffer
	if err := reportPageTmpl.Execute(&buf, view); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	// The worst session still feeds the share payload and its manifest, but is
	// no longer rendered as a case-study card - the per-session strips replaced it.
	for _, unwanted := range []string{"This fallback prompt should not render.", `class="case-study"`, "searches run"} {
		if bytes.Contains(buf.Bytes(), []byte(unwanted)) {
			t.Fatalf("expected rendered output not to contain %q", unwanted)
		}
	}
	if !bytes.Contains(buf.Bytes(), []byte("The case-study session title shown above")) {
		t.Fatalf("expected the share manifest to still declare the session title it sends")
	}
	heroIndex := bytes.Index(buf.Bytes(), []byte("Before the first edit, an agent reads"))
	actionIndex := bytes.Index(buf.Bytes(), []byte(`class="savings"`))
	evidenceIndex := bytes.Index(buf.Bytes(), []byte(`class="evidence"`))
	if !(heroIndex < actionIndex && actionIndex < evidenceIndex) {
		t.Fatalf("report order must be averages, action/share, evidence; got indexes %d, %d, %d", heroIndex, actionIndex, evidenceIndex)
	}
}

func TestReportUploadResponseErrorPreservesHTTPFailure(t *testing.T) {
	err := reportUploadResponseError("404 Not Found", []byte("404"))
	if got := err.Error(); got != "upload report: server returned 404 Not Found: 404" {
		t.Fatalf("unexpected upload error: %q", got)
	}

	err = reportUploadResponseError("502 Bad Gateway", []byte(`{"error":"public reports unavailable"}`))
	if got := err.Error(); got != "upload report: server returned 502 Bad Gateway: public reports unavailable" {
		t.Fatalf("unexpected JSON upload error: %q", got)
	}
}

func TestSharePayloadUsesPublicReportFieldNames(t *testing.T) {
	worst := analysis.WorstSessionCase{
		SessionTitle: "Fix the handler",
		FilesRead:    8,
		Searches:     3,
		EditedFile:   "api/handler.go",
	}
	payload := sharePayload{
		RepoAnalysisBundle: analysis.RepoAnalysisBundle{Repo: analysis.RepoAnalysisRepo{Name: "demo"}},
		WorstSessionCase:   &worst,
		RepoStartCost:      analysis.RepoStartCost{Sessions: 4, AvgReads: 7.5, AvgContextTokens: 8200, AvgToolCalls: 10},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal share payload: %v", err)
	}
	for _, want := range []string{`"repo_start_cost"`, `"avg_reads":7.5`, `"worst_session_case"`, `"session_title":"Fix the handler"`, `"files_read":8`, `"edited_file":"api/handler.go"`} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Fatalf("expected payload to contain %s, got %s", want, encoded)
		}
	}
}
