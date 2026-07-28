package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/analysis"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

const reportLocalAddr = "localhost:7331"

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Build a local repo efficiency report; sharing it to RepoGuide Cloud is optional",
	RunE:  runReport,
}

func init() { root.AddCommand(reportCmd) }

func runReport(_ *cobra.Command, _ []string) error {
	status := repopkg.DetectLocalSetup()
	if !status.InGitRepo {
		return fmt.Errorf("run repoguide report inside a git repository")
	}

	repoID := status.RepoID
	var ephemeralDir string
	if !status.Initialized {
		// No `repoguide init` yet: register the repo in a throwaway store
		// instead of the real ~/.repoguide, so `report` works standalone.
		// Cleaned up on exit unless the report gets shared.
		dir, err := os.MkdirTemp("", "repoguide-report-")
		if err != nil {
			return fmt.Errorf("create temp store: %w", err)
		}
		ephemeralDir = dir
		internal.SetRepoGuideDirOverride(dir)
		id, err := internal.InitEphemeralRepo(status.RepoRoot)
		if err != nil {
			os.RemoveAll(dir)
			return fmt.Errorf("prepare local analysis: %w", err)
		}
		repoID = id
	}
	var shared atomic.Bool
	cleanup := func() {
		if ephemeralDir != "" && !shared.Load() {
			os.RemoveAll(ephemeralDir)
		}
	}

	events, _, err := sessionimport.ParseRepoEventsForLocal(repoID, status.RepoRoot)
	if err != nil {
		cleanup()
		return fmt.Errorf("analyze sessions: %w", err)
	}
	if len(events) == 0 {
		cleanup()
		return fmt.Errorf("no coding-agent sessions found for this repository")
	}
	bundle, err := analysis.BuildRepoAnalysis(status.RepoRoot, events, false)
	if err != nil {
		cleanup()
		return fmt.Errorf("build report: %w", err)
	}
	sanitizeReportBundle(&bundle)

	view := buildReportView(bundle)
	view.UseRepoGuideURL = strings.TrimRight(getBackendURL(), "/") + "/#install"
	// The worst-session case study needs the original prompt and raw
	// per-event read/write order, which sanitizeReportBundle strips from
	// the bundle. Pull it from the raw events instead. It's picked from the
	// whole session set, not just sessions matching a detected pattern - a
	// session doesn't need to have been fixable by prior evidence to be
	// worth showing as a concrete example. It's included in the share
	// payload too - the "Review data used" step shows it before anything
	// sends, so there's nothing hidden about a single reviewed example
	// prompt.
	var worstSessionForPayload *analysis.WorstSessionCase
	if worst, ok := analysis.FindWorstSession(status.RepoRoot, events); ok {
		view.WorstSession = worst
		view.HasWorstSession = true
		worstSessionForPayload = &worst
	}
	view.RepoAverages = analysis.AverageStartCost(events)
	// Per-session strips need the raw event order too, for the same reason as
	// the case study above: the sanitized bundle has no per-session events.
	view.SessionStrips = analysis.BuildSessionStrips(status.RepoRoot, events)
	view.StripBlockPx = stripBlockPx(view.SessionStrips)

	payload, err := json.MarshalIndent(sharePayload{
		RepoAnalysisBundle: bundle,
		WorstSessionCase:   worstSessionForPayload,
		RepoStartCost:      view.RepoAverages,
		RecurringPaths:     view.RecurringPaths,
		TopFiles:           view.TopFiles,
		SessionsMatched:    view.SessionsMatched,
	}, "", "  ")
	if err != nil {
		cleanup()
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		reportPageTmpl.Execute(w, view)
	})
	mux.HandleFunc("/payload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		url, expiresAt, err := uploadReport(payload)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		shared.Store(true)
		json.NewEncoder(w).Encode(map[string]string{"url": url, "expires_at": expiresAt.Local().Format(time.RFC1123)})
	})

	// Ctrl+C (or any termination) cleans up the ephemeral temp store unless
	// the report was shared, in which case there's nothing left to protect.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cleanup()
		os.Exit(0)
	}()

	fmt.Printf("Report generated locally (no data uploaded).\n\nOpen http://%s\n\nCtrl+C to stop.\n", reportLocalAddr)
	openBrowser("http://" + reportLocalAddr)
	err = http.ListenAndServe(reportLocalAddr, mux)
	cleanup()
	return err
}

// sharePayload is what gets shown in "Review data used" and POSTed to
// /upload: the aggregate bundle plus the one worst-session case study the
// report displays, including its title or fallback prompt. Everything is
// reviewable on-page before it is sent.
type sharePayload struct {
	analysis.RepoAnalysisBundle
	WorstSessionCase *analysis.WorstSessionCase   `json:"worst_session_case,omitempty"`
	RepoStartCost    analysis.RepoStartCost       `json:"repo_start_cost"`
	RecurringPaths   []analysis.RepoAnalysisTrace `json:"recurring_paths,omitempty"`
	TopFiles         []analysis.RepoAnalysisFile  `json:"top_read_without_edit_files,omitempty"`
	SessionsMatched  int                          `json:"sessions_matched"`
}

// sanitizeReportBundle strips everything that isn't aggregate navigation
// evidence: bulk session transcripts, session identifiers, and the absolute
// local path. The one exception is the worst-session case study's title or
// fallback prompt, added back separately in sharePayload - it's the single
// example displayed and reviewed before sharing, not a transcript dump.
func sanitizeReportBundle(bundle *analysis.RepoAnalysisBundle) {
	bundle.Sessions = nil
	bundle.Repo.Root = ""
	for i := range bundle.Subsystems {
		bundle.Subsystems[i].RelatedSessions = nil
	}
	for i := range bundle.SeenWithGroups {
		bundle.SeenWithGroups[i].RelatedSessions = nil
	}
	for i := range bundle.TestSignals.TestsAsSpec {
		bundle.TestSignals.TestsAsSpec[i].RelatedSessions = nil
	}
	for i := range bundle.TestSignals.SourceAndTestCoEdit {
		bundle.TestSignals.SourceAndTestCoEdit[i].RelatedSessions = nil
	}
	for i := range bundle.TestSignals.TestFriction {
		bundle.TestSignals.TestFriction[i].RelatedSessions = nil
	}
	for i := range bundle.TestSignals.TestChurn {
		bundle.TestSignals.TestChurn[i].RelatedSessions = nil
	}
}

// reportView is what the template renders. It leads with recurring discovery
// paths (trace patterns already seen in more than one session) instead of
// raw read/token counts, because a high read or token count alone doesn't
// tell you whether anything was wasted.
type reportView struct {
	analysis.RepoAnalysisBundle
	UseRepoGuideURL string
	RecurringPaths  []analysis.RepoAnalysisTrace
	BiggestPath     analysis.RepoAnalysisTrace
	HasBiggestPath  bool
	SessionsMatched int // sum of (sessions-1) across recurring paths: sessions that matched a path already seen elsewhere
	TopFiles        []analysis.RepoAnalysisFile

	LowSampleSize bool // fewer than minSessionsForReliableReport sessions: patterns below are unreliable
	SampleSize    int

	// Case study: one concrete session, not an aggregate, plus a repo-wide
	// baseline it can be read against. Built separately from raw session
	// events - see runReport - since sanitizeReportBundle strips prompts
	// from the aggregate bundle. The case study is included in the share
	// payload (sharePayload) as the one reviewed exception to that.
	HasWorstSession bool
	WorstSession    analysis.WorstSessionCase
	RepoAverages    analysis.RepoStartCost

	// One strip per session, shown locally only: the strips carry session
	// titles/prompts, so they stay out of sharePayload rather than widening
	// what a shared link exposes beyond the single reviewed case study.
	SessionStrips []analysis.SessionStrip
	// StripBlockPx is the width of one call block, shared by every strip so a
	// row's total length is its call count. Sized so the longest session fills
	// the column exactly and no row can overflow it.
	StripBlockPx float64

	// EstimatedSavingsUSD projects what RepoGuide would have saved across every
	// analyzed session: inputTokenSavingsRate off the input+cache-token slice
	// of cost (output tokens aren't reduced by loading less context).
	EstimatedSavingsUSD float64
	HasSavingsEstimate  bool
}

// inputTokenSavingsRate is the measured average input-token reduction
// RepoGuide gives an agent that already has the right context up front.
const inputTokenSavingsRate = 0.32

// Below this many sessions, "recurring" patterns are as likely to be
// coincidence as signal, so the report says so instead of presenting them
// with the same confidence as a larger sample.
const minSessionsForReliableReport = 25

// evidenceRowCap bounds the two evidence tables. They're collapsed behind
// <details> by default, so this can be larger than the top-3 highlighted
// elsewhere on the page without crowding it.
const evidenceRowCap = 10

func buildReportView(bundle analysis.RepoAnalysisBundle) reportView {
	v := reportView{RepoAnalysisBundle: bundle}
	v.SampleSize = bundle.Summary.Sessions
	v.LowSampleSize = bundle.Summary.Sessions < minSessionsForReliableReport
	if bundle.Summary.InputCostUSD > 0 {
		v.EstimatedSavingsUSD = bundle.Summary.InputCostUSD * inputTokenSavingsRate
		v.HasSavingsEstimate = true
	}

	// Only "expensive_edit_target" traces carry real read-before-edit
	// evidence (a populated AvgReadsBeforeEdit and the files most often read
	// first). Other trace types either leave AvgReadsBeforeEdit at zero or
	// aren't a file-to-file path at all, so they don't belong in a table
	// claiming "agents read these files before reaching this edit".
	for _, t := range bundle.TracePatterns {
		if t.Type != "expensive_edit_target" || t.Sessions < 2 || t.AvgReadsBeforeEdit <= 0 {
			continue
		}
		if analysis.IsDirectoryPath(t.Target) {
			continue
		}
		var reads []analysis.RepoAnalysisPathCount
		for _, r := range t.TopPrecedingReads {
			if !analysis.IsDirectoryPath(r.Path) {
				reads = append(reads, r)
			}
		}
		if len(reads) == 0 {
			continue
		}
		t.TopPrecedingReads = reads
		v.RecurringPaths = append(v.RecurringPaths, t)
	}
	// Rank by avoidable-read impact (sessions beyond the first × reads before
	// edit), not just session count, so the highlighted path is the one with
	// the most evidence behind it, not just the most popular file.
	sort.Slice(v.RecurringPaths, func(i, j int) bool {
		return float64(v.RecurringPaths[i].Sessions-1)*v.RecurringPaths[i].AvgReadsBeforeEdit >
			float64(v.RecurringPaths[j].Sessions-1)*v.RecurringPaths[j].AvgReadsBeforeEdit
	})
	if len(v.RecurringPaths) > 0 {
		v.BiggestPath = v.RecurringPaths[0]
		v.HasBiggestPath = true
	}
	// Count matched sessions against the full list, not the capped evidence
	// table below - the hero stat shouldn't shrink just because the
	// (now-collapsed) evidence section only displays the top slice.
	for _, t := range v.RecurringPaths {
		v.SessionsMatched += t.Sessions - 1
	}
	// The evidence table is collapsed behind <details> by default, so it can
	// afford to show more than a top-3 highlight without crowding the page.
	if len(v.RecurringPaths) > evidenceRowCap {
		v.RecurringPaths = v.RecurringPaths[:evidenceRowCap]
	}

	// Only surface files where reading didn't correlate with editing - a read
	// count alone isn't an issue, a read count with few matching edits is.
	for _, f := range bundle.Files {
		if analysis.IsDirectoryPath(f.Path) {
			continue
		}
		if f.Sessions < 2 || float64(f.EditSessions) >= float64(f.Sessions)*0.5 {
			continue
		}
		v.TopFiles = append(v.TopFiles, f)
	}
	sort.Slice(v.TopFiles, func(i, j int) bool { return v.TopFiles[i].ContextTokens > v.TopFiles[j].ContextTokens })
	if len(v.TopFiles) > evidenceRowCap {
		v.TopFiles = v.TopFiles[:evidenceRowCap]
	}

	return v
}

// stripColumnPx is the widest a strip may draw - the content column, minus
// room for the first-edit divider. A row is calls × block width, so this is
// what stops the longest session from running past the page.
const stripColumnPx = 900

// stripBlockPx sizes one call block so the longest session exactly fills the
// column. Capped at 5px so a repo with only short sessions doesn't render a
// handful of fat slabs.
func stripBlockPx(strips []analysis.SessionStrip) float64 {
	longest := 0
	for _, s := range strips {
		if s.Calls > longest {
			longest = s.Calls
		}
	}
	if longest == 0 {
		return 5
	}
	if px := stripColumnPx / float64(longest); px < 5 {
		return px
	}
	return 5
}

func traceLabel(t analysis.RepoAnalysisTrace) string {
	if t.Target != "" {
		return t.Target
	}
	if t.Chain != "" {
		return t.Chain
	}
	if len(t.Pattern) > 0 {
		return strings.Join(t.Pattern, " → ")
	}
	return t.File
}

// compactNumber renders large token-style counts as "3.9M"/"12.4k" instead
// of a long raw digit string, which reads better inside a stat card.
func compactNumber(n float64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", n/1_000)
	default:
		return fmt.Sprintf("%.0f", n)
	}
}

func precedingReadsLabel(t analysis.RepoAnalysisTrace) string {
	names := make([]string, 0, len(t.TopPrecedingReads))
	for i, r := range t.TopPrecedingReads {
		if i >= 3 {
			break
		}
		names = append(names, r.Path)
	}
	return strings.Join(names, ", ")
}

func uploadReport(payload []byte) (string, time.Time, error) {
	baseURL := strings.TrimRight(getBackendURL(), "/")
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/public-reports", bytes.NewReader(payload))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("upload report: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read upload response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, reportUploadResponseError(resp.Status, body)
	}
	var result struct {
		Token     string    `json:"token"`
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("decode upload response: %w", err)
	}
	// The report is served by the web app, not this API. Older backends don't
	// say where that is, so fall back to assuming they share an origin.
	if result.URL != "" {
		return result.URL, result.ExpiresAt, nil
	}
	return baseURL + "/reports/" + result.Token, result.ExpiresAt, nil
}

func reportUploadResponseError(status string, body []byte) error {
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil && strings.TrimSpace(response.Error) != "" {
		return fmt.Errorf("upload report: server returned %s: %s", status, response.Error)
	}
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("upload report: server returned %s", status)
	}
	return fmt.Errorf("upload report: server returned %s: %s", status, detail)
}

var reportPageTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"traceLabel":     traceLabel,
	"precedingReads": precedingReadsLabel,
	"minSessions":    func() int { return minSessionsForReliableReport },
	"compactNumber":  compactNumber,
}).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>RepoGuide report: {{.Repo.Name}}</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{background:radial-gradient(circle at 15% 0%,#181510 0%,#0B0B0A 45%);color:#C9C4B8;font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;margin:0}
.wrap{max-width:980px;margin:0 auto;padding:32px 22px 80px}
.banner{display:flex;align-items:center;gap:10px;background:linear-gradient(90deg,#173226,#122a1f);color:#a9eec3;border:1px solid #2f6b4f;border-radius:12px;padding:12px 18px;font-weight:600;font-size:13px;letter-spacing:.01em}
.banner::before{content:"●";color:#3fdc8a;font-size:10px}
.banner.caution{background:linear-gradient(90deg,#332413,#2a1f12);color:#f0c98a;border-color:#7a5522;margin-top:10px}
.banner.caution::before{color:#f0c98a}
.hero{margin-top:34px}
.tag{text-transform:uppercase;letter-spacing:.16em;font-size:11px;color:#F54E00;font-weight:700}
h1{color:#F7F5EF;font-size:clamp(34px,5vw,54px);line-height:1.04;margin:12px 0 0;font-weight:750;letter-spacing:-.04em;max-width:860px}
.hero h1 em{color:#F54E00;font-style:normal}
.diagnosis{font-size:19px;line-height:1.45;color:#F0EEE6;margin:20px 0 4px;max-width:760px;font-weight:500}
.diagnosis strong{color:#F54E00;font-weight:700}
.hero-actions{margin-top:22px;display:flex;align-items:center;gap:10px;flex-wrap:wrap}
.subdiagnosis{color:#A6A195;max-width:760px}
.opportunity{margin-top:22px;border:1px solid #4a2c14;border-radius:14px;padding:18px 20px;background:linear-gradient(135deg,#1c150d,#161310)}
.opportunity h3{margin:0 0 8px;color:#F7F5EF;font-size:14px;text-transform:uppercase;letter-spacing:.08em}
.opportunity h3::before{content:"★ ";color:#F54E00}
.case-study{margin-top:22px;border:1px solid #3a3630;border-radius:14px;padding:20px 22px;background:linear-gradient(135deg,#1b1917,#151310)}
.case-study .tag{margin:0;color:#B7B2A6;font-size:11px;text-transform:uppercase;letter-spacing:.1em}
.case-study .case-title{margin:7px 0 0;color:#F0EEE6;font-size:20px;font-weight:650;line-height:1.35}
.case-metrics{display:grid;grid-template-columns:repeat(3,1fr);gap:0;margin-top:18px;border:1px solid #3a3630;border-radius:10px;background:#0f0e0c}
.case-metric{padding:14px 16px;border-right:1px solid #3a3630}
.case-metric:last-child{border-right:0}
.case-metric b{display:block;color:#F54E00;font-size:27px;line-height:1;font-weight:800}
.case-metric span{display:block;margin-top:5px;color:#A6A195;font-size:12px}
.first-edit{margin:14px 0 0;color:#A6A195;font-size:13px}
.first-edit code{color:#a9eec3;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;overflow-wrap:anywhere}
.case-study .stat-line{margin-top:12px;color:#A6A195;font-size:13.5px}
.case-study .stat-line b{color:#F0EEE6}
details.evidence{margin-top:32px;border:1px solid #262420;border-radius:14px;padding:4px 20px}
details.evidence[open]{padding-bottom:18px}
details.evidence summary{cursor:pointer;padding:14px 0;color:#F0EEE6;font-weight:600;font-size:14px;list-style:none}
details.evidence summary::-webkit-details-marker{display:none}
details.evidence summary::before{content:"▸ ";color:#F54E00;display:inline-block;transition:transform .15s}
details.evidence[open] summary::before{transform:rotate(90deg)}
details.evidence section{margin-top:24px}
details.evidence section:first-of-type{margin-top:8px}
.footer{margin-top:28px;text-align:center;color:#6e6a61;font-size:12px}
section{margin-top:40px}
section h2{color:#F7F5EF;font-size:19px;margin-bottom:4px;letter-spacing:-.01em}
section .why{background:#141310;border:1px solid #262420;border-radius:10px;padding:10px 14px;font-size:13px;color:#B7B2A6;margin:10px 0 16px}
section .why b{color:#F0EEE6}
.card{border:1px solid #262420;border-radius:14px;overflow:hidden;background:#100f0d}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{text-align:left;padding:10px 14px;border-top:1px solid #211f1c;vertical-align:top}
thead th{border-top:none;background:#151310}
th{color:#8a857a;font-weight:500;font-size:11.5px;text-transform:uppercase;letter-spacing:.05em}
td.path{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#F0EEE6;font-size:12.5px}
.legend{display:flex;flex-wrap:wrap;gap:16px;align-items:center;margin:12px 0 18px;font-size:12.5px;color:#A6A195}
.legend span{display:flex;align-items:center;gap:6px}
.legend i{width:13px;height:13px;border-radius:3px;display:inline-block}
.legend i.m-edit{background:#2fbd7e}
.legend i.m-ok{background:#4a4741}
.legend i.m-cold{background:#F54E00}
.legend i.m-unused{background:#E5A50A}
.legend i.m-reopen{background:#8B7BFF}
.legend i.legend-mark{width:3px;height:15px;border-radius:0;background:#F7F5EF}
.legend .legend-hint{margin-left:auto;color:#6e6a61;font-size:11.5px}
.guide-badge{display:inline-block;margin-right:7px;padding:1px 6px;border-radius:5px;background:#24180f;color:#F54E00;font-size:10px;font-weight:700;letter-spacing:.04em;vertical-align:1px}
.strip-row{margin-bottom:12px}
.strip-head{font-size:12.5px}
.strip-head .strip-title{display:block;color:#F0EEE6;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.strip-line{display:grid;grid-template-columns:1fr;gap:2px;margin-top:4px}
.strip{display:flex;gap:0;overflow:hidden}
.strip i{flex:0 0 var(--u);height:14px;cursor:default}
.strip i.first-edit-mark{flex:0 0 2px;min-width:2px;border-radius:0;background:#F7F5EF;height:20px;margin:0 2px}
.strip-tail{color:#6e6a61;font-size:11.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.strip-tail b{color:#A6A195;font-weight:600}
.strip i:hover{outline:1px solid #C9C4B8}
.strip i.m-ok{background:#4a4741}
.strip i.m-cold{background:#F54E00}
.strip i.m-unused{background:#E5A50A}
.strip i.m-reopen{background:#8B7BFF}
.strip i.m-edit{background:#2fbd7e}
.strip i.hl{outline:1px solid #F7F5EF}
#strip-tip{position:fixed;z-index:60;display:none;max-width:min(520px,90vw);padding:6px 9px;border:1px solid #3a3630;border-radius:7px;background:#0c0b0a;color:#F0EEE6;font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;overflow-wrap:anywhere;pointer-events:none;box-shadow:0 6px 24px rgba(0,0,0,.5)}
.savings{margin-top:16px;border:1px solid #2f6b4f;border-radius:14px;padding:20px;background:linear-gradient(135deg,#132119,#0f1a14)}
.savings h2{color:#F7F5EF;margin:0 0 4px}
.savings .why{background:#0f1a14;border-color:#1f3d2e}
.actions{margin-top:14px;display:flex;align-items:center;gap:10px;flex-wrap:wrap}
button,.primary-cta{background:#F54E00;color:#fff;border:0;border-radius:9px;padding:11px 18px;font-size:14px;font-weight:600;cursor:pointer}
button:hover,.primary-cta:hover{background:#ff5c0f}
.primary-cta{display:inline-flex;align-items:center;text-decoration:none;padding:13px 20px}
button.secondary{background:transparent;border:1px solid #444;color:#C9C4B8}
button.secondary:hover{border-color:#666}
button.compact{padding:8px 13px;font-size:12px;color:#A6A195}
button.tertiary{background:transparent;border:0;color:#8a857a;padding:11px 4px;text-decoration:underline}
button.tertiary:hover{color:#C9C4B8}
.modal-backdrop{position:fixed;inset:0;z-index:50;display:none;align-items:center;justify-content:center;padding:20px;background:rgba(0,0,0,.72);backdrop-filter:blur(6px)}
.modal-backdrop.open{display:flex}
.share-modal{width:min(560px,100%);max-height:min(760px,calc(100vh - 40px));overflow:auto;border:1px solid #34312b;border-radius:16px;background:#11100e;padding:22px;box-shadow:0 24px 80px rgba(0,0,0,.55)}
.modal-head{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}
.modal-head h2{margin:0;color:#F7F5EF;font-size:22px}
.modal-head p{margin:4px 0 0;color:#8f8a7e;font-size:13px}
.modal-close{background:transparent;color:#8f8a7e;padding:2px 7px;font-size:22px;line-height:1}
.modal-close:hover{background:transparent;color:#F7F5EF}
.share-tagline{margin:18px 0 0;border-left:2px solid #F54E00;padding-left:12px;color:#C9C4B8;font-size:13px;line-height:1.5}
.share-steps{display:grid;gap:10px;margin-top:18px}
.share-step{display:grid;grid-template-columns:28px 1fr;gap:10px;align-items:center;border:1px solid #28251f;border-radius:10px;padding:12px;background:#151310}
.step-number{display:grid;width:24px;height:24px;place-items:center;border-radius:50%;background:#24180f;color:#F54E00;font-size:12px;font-weight:800}
.share-step button,.share-step .button-link{justify-self:start}
.button-link{display:inline-flex;align-items:center;text-decoration:none;background:transparent;border:1px solid #446653;color:#a9eec3;border-radius:8px;padding:8px 13px;font-size:13px;font-weight:600}
.button-link:hover{border-color:#6a9b7d}
.button-link.disabled{pointer-events:none;border-color:#302e29;color:#605d55}
.share-status{grid-column:2;margin:0;color:#8f8a7e;font-size:12px}
.share-status a{color:#a9eec3}
.modal-privacy{margin-top:16px;border-top:1px solid #28251f;padding-top:12px}
body.modal-open{overflow:hidden}
.manifest{display:none;margin-top:18px;border-top:1px solid #2a2420;padding-top:16px}
.manifest.open{display:block}
.manifest .cols{display:grid;grid-template-columns:1fr 1fr;gap:20px}
.manifest ul{margin:6px 0 0;padding-left:18px;font-size:13px}
.manifest .yes b{color:#a9eec3}
.manifest .no b{color:#e8a3a3}
pre{background:#0c0b0a;border:1px solid #262420;border-radius:10px;padding:14px;overflow:auto;max-height:320px;font-size:12px;margin-top:14px}
.status{margin-top:14px;font-size:14px}
@media(max-width:600px){.manifest .cols{grid-template-columns:1fr}.case-metrics{grid-template-columns:1fr}.case-metric{border-right:0;border-bottom:1px solid #28251f}.case-metric:last-child{border-bottom:0}}
</style></head>
<body><div class="wrap">
{{if .LowSampleSize}}<div class="banner caution">Insufficient data: only {{.SampleSize}} session{{if ne .SampleSize 1}}s{{end}} found (recommend {{minSessions}}+). Recurring patterns and estimates below are based on a small sample and may not be reliable.</div>{{end}}
<div class="hero">
<p class="tag">The agent navigation tax · {{.Repo.Name}}</p>
{{if .RepoAverages.Sessions}}
<h1>Your agents don’t start coding. They start <em>rediscovering your repo.</em></h1>
<p class="diagnosis">Before the first edit, an agent reads <strong>{{printf "%.1f" .RepoAverages.AvgReads}} files</strong>, loads <strong>{{compactNumber .RepoAverages.AvgContextTokens}} context tokens</strong>, and makes <strong>{{printf "%.1f" .RepoAverages.AvgToolCalls}} tool calls</strong> on average.</p>
<p class="subdiagnosis">Measured across {{.RepoAverages.Sessions}} session{{if ne .RepoAverages.Sessions 1}}s{{end}} that reached an edit. {{if .SessionsMatched}}Across them, agents reached an edit target {{.SessionsMatched}} times after reading the same files an earlier session had already read to reach it.{{else}}Without shared repo experience, every new session starts from zero.{{end}}</p>
<div class="hero-actions"><a class="primary-cta" href="{{.UseRepoGuideURL}}" target="_blank" rel="noopener">Use RepoGuide on your next task →</a><button class="secondary" onclick="openShareModal()">Share this report</button></div>
{{else}}
<h1>Your agents are <em>rediscovering {{.Repo.Name}}</em> one session at a time.</h1>
<p class="diagnosis">{{.SessionsMatched}} sessions matched an investigation pattern that another agent had already found.</p>
<div class="hero-actions"><a class="primary-cta" href="{{.UseRepoGuideURL}}" target="_blank" rel="noopener">Use RepoGuide on your next task →</a><button class="secondary" onclick="openShareModal()">Share this report</button></div>
{{end}}
</div>

{{if .HasBiggestPath}}
<div class="opportunity">
<h3>Biggest opportunity</h3>
<p class="subdiagnosis">Work around <strong class="path">{{traceLabel .BiggestPath}}</strong> matched across {{.BiggestPath.Sessions}} sessions, with {{printf "%.1f" .BiggestPath.AvgReadsBeforeEdit}} reads before the first edit{{if .BiggestPath.TopPrecedingReads}} — most often reading {{precedingReads .BiggestPath}} first{{end}}.</p>
</div>
{{end}}

{{if .SessionStrips}}
<section id="strips" style="--u:{{printf "%.3f" .StripBlockPx}}px">
<h2>Every session as a trace — {{len .SessionStrips}} session{{if ne (len .SessionStrips) 1}}s{{end}}</h2>
<p class="why"><b>What this is:</b> one strip per session, one block per tool call, left to right, every block the same width — so a row's length is its call count — with a white divider where the first edit landed: everything left of it is the hunt, everything right of it is the work. Everything after the first edit is counted on the right, not drawn. Only mechanical markers: a search with no file read in the next 3 calls, a read of a file the session never edited, a re-read of a file already opened, and the edits. <b>Why it matters:</b> none of these is proof of a wrong turn. The long rows are sessions that spent dozens of calls finding the file; the counts on the right are where the rest of the session went, which in the biggest sessions is mostly opening the same files again.</p>
<div class="legend">
<span><i class="m-ok"></i>nothing flagged</span>
<span><i class="m-cold"></i>search, no read after</span>
<span><i class="m-unused"></i>read, never edited</span>
<span><i class="m-reopen"></i>re-read a file</span>
<span><i class="m-edit"></i>edit</span>
<span><i class="legend-mark"></i>first edit</span>
<span class="legend-hint">hover a block for its tool and file</span>
</div>
{{range $s := .SessionStrips}}<div class="strip-row">
<div class="strip-head"><span class="strip-title">{{if $s.UsedGuide}}<b class="guide-badge">RepoGuide</b>{{end}}{{$s.Title}}</span></div>
<div class="strip-line">
<div class="strip">{{range $i, $m := $s.Markers}}{{if eq $i $s.PreEditIndex}}<i class="first-edit-mark" data-l="first edit"></i>{{end}}<i class="m-{{$m}}" data-l="{{index $s.Labels $i}}" data-f="{{index $s.Files $i}}"></i>{{end}}</div>
<div class="strip-tail"><b>{{$s.Calls}} calls · edit at {{$s.EditIndex}}{{if $s.CostUSD}} · {{printf "$%.2f" $s.CostUSD}}{{end}}</b>{{if $s.AfterCalls}} · after: {{$s.AfterEdits}} edits, {{$s.AfterReads}} re-reads, {{$s.AfterOther}} other{{end}}{{if $s.TopReads}} · {{$s.TopFile}} opened {{$s.TopReads}}×{{end}}</div>
</div>
</div>{{end}}
</section>
<script>
// Hovering one call lights up every other call against the same file within
// that same session - repeats inside one strip are what the marker is about.
// The native title tooltip is too slow and too easy to miss on a 14px block,
// so the tool/file label is drawn next to the cursor instead.
(function(){
  var strips=document.getElementById('strips'),marked=[];
  if(!strips)return;
  var tip=document.createElement('div');
  tip.id='strip-tip';document.body.appendChild(tip);
  function clear(){
    marked.forEach(function(el){el.classList.remove('hl');});marked=[];
    tip.style.display='none';
  }
  strips.addEventListener('mousemove',function(e){
    var block=e.target.closest('.strip i');
    clear();
    if(!block)return;
    tip.textContent=block.dataset.l||'';
    tip.style.display='block';
    // Flip to the left of the cursor when the label would run off-screen.
    var w=tip.offsetWidth;
    tip.style.left=Math.max(6,Math.min(e.clientX+14,window.innerWidth-w-6))+'px';
    tip.style.top=Math.max(6,e.clientY-tip.offsetHeight-10)+'px';
    var file=block.dataset.f;
    if(!file)return;
    Array.prototype.forEach.call(block.parentNode.children,function(el){
      if(el.dataset.f===file){el.classList.add('hl');marked.push(el);}
    });
  });
  strips.addEventListener('mouseleave',clear);
})();
</script>
{{end}}

<div class="savings">
{{if .HasSavingsEstimate}}
<h2>Estimated savings: {{printf "$%.2f" .EstimatedSavingsUSD}}</h2>
<p class="why">Based on the {{.SampleSize}} session{{if ne .SampleSize 1}}s{{end}} analyzed above, RepoGuide's context reduction cuts input-token cost by roughly <b>32%</b> — the rest of each session's cost (output tokens) is unaffected.</p>
{{else}}
<h2>Stop making every agent rediscover your repo</h2>
<p class="why">RepoGuide turns earlier sessions into <b>task-specific starting files, repository constraints, and validation steps</b> — before the next agent starts searching.</p>
{{end}}
<div class="actions">
<a class="primary-cta" href="{{.UseRepoGuideURL}}" target="_blank" rel="noopener">Use RepoGuide on your next task →</a>
<button class="secondary compact" onclick="openShareModal()">Share</button>
</div>
</div>

<details class="evidence">
<summary>View evidence</summary>

<section>
<h2>Files read without matching edits — top {{len .TopFiles}}</h2>
<p class="why"><b>What this is:</b> files read across multiple sessions but edited in fewer than half of them. <b>Why it matters:</b> these files frequently served as reference material without being edited. Tests, models, configuration, and interfaces are often read correctly without being changed, so this is not inherently a problem — high values may indicate recurring orientation cost, but don't by themselves imply wasted work.</p>
<div class="card">
<table><thead><tr><th>File</th><th>Sessions</th><th>Reads</th><th>Context tokens</th><th>Edited in</th></tr></thead><tbody>
{{range $f := .TopFiles}}<tr><td class="path">{{$f.Path}}</td><td>{{$f.Sessions}}</td><td>{{$f.Reads}}</td><td>{{$f.ContextTokens}}</td><td>{{$f.EditSessions}} of {{$f.Sessions}} sessions</td></tr>{{end}}
{{if not .TopFiles}}<tr><td colspan="5">No file shows a read-without-edit pattern worth flagging.</td></tr>{{end}}
</tbody></table>
</div>
</section>

<section>
<h2>Recurring pre-edit working sets — top {{len .RecurringPaths}}</h2>
<p class="why"><b>What this is:</b> an edited file grouped with the files most commonly read before that edit, where the same read-then-edit working set recurred in more than one session. Directories, repo/submodule names, and git refs are excluded — every row below is a real file. This groups an edited file with its commonly preceding reads; it doesn't prove sessions followed the exact same ordered sequence. <b>Why it matters:</b> a match doesn't prove the sessions shared a goal, but the more sessions match, the more likely surfacing that working set up front would have shortened the search.</p>
<div class="card">
<table><thead><tr><th>Edited file</th><th>Sessions</th><th>Avg. reads before edit</th><th>Most-read first</th></tr></thead><tbody>
{{range $t := .RecurringPaths}}<tr><td class="path">{{traceLabel $t}}</td><td>{{$t.Sessions}}</td><td>{{printf "%.1f" $t.AvgReadsBeforeEdit}}</td><td class="path">{{precedingReads $t}}</td></tr>{{end}}
{{if not .RecurringPaths}}<tr><td colspan="4">No edit target had both repeated sessions and evidence of preceding reads yet — not enough data to claim a recurring path.</td></tr>{{end}}
</tbody></table>
</div>
</section>
</details>

<p class="footer">Local report · Review data before sharing · Keep local available</p>
</div>

<div class="modal-backdrop" id="share-modal" aria-hidden="true" onclick="closeShareModal(event)">
<div class="share-modal" role="dialog" aria-modal="true" aria-labelledby="share-modal-title" onclick="event.stopPropagation()">
<div class="modal-head"><div><h2 id="share-modal-title">Share this report</h2><p>Create one private link, then choose where to post it.</p></div><button class="modal-close" onclick="closeShareModal()" aria-label="Close">×</button></div>
{{if .RepoAverages.Sessions}}<p class="share-tagline" id="share-copy">Before their first edit in my repository,sss my AI agents read {{printf "%.1f" .RepoAverages.AvgReads}} files, load {{compactNumber .RepoAverages.AvgContextTokens}} context tokens, and make {{printf "%.1f" .RepoAverages.AvgToolCalls}} tool calls. The code isn’t the bottleneck. Relearning it is.</p>{{else}}<p class="share-tagline" id="share-copy">My AI agents keep rediscovering {{.Repo.Name}}. The code isn’t the bottleneck. Relearning it is.</p>{{end}}
<div class="share-steps">
<div class="share-step"><span class="step-number">1</span><button id="create-share-button" onclick="createShareLink()">Create 14-day report link</button><p class="share-status" id="status">Nothing is uploaded until you create the link.</p></div>
<div class="share-step"><span class="step-number">2</span><button class="secondary compact" id="linkedin-link" disabled onclick="shareOnLinkedIn()">Copy post &amp; open LinkedIn</button><p class="share-status" id="linkedin-status">LinkedIn can’t be pre-filled from a link — the post text is copied, paste it in.</p></div>
<div class="share-step"><span class="step-number">3</span><a class="button-link disabled" id="x-link" aria-disabled="true" target="_blank" rel="noopener">Share post on X</a></div>
</div>
<div class="modal-privacy"><button class="tertiary" onclick="showManifest()">Review data used</button><div class="manifest" id="manifest"><div class="cols"><div class="yes"><p><strong>Included</strong></p><ul><li>Aggregated token and tool-use counts</li><li>File paths</li><li>Session-level navigation patterns</li><li>Derived findings and recommendations</li><li>Repo identifier and analysis timestamp</li>{{if .WorstSession.SessionTitle}}<li>The case-study session title shown above</li>{{else if .WorstSession.Prompt}}<li>The one fallback task prompt shown above</li>{{end}}</ul></div><div class="no"><p><strong>Not included</strong></p><ul><li>Source-code contents</li>{{if .WorstSession.Prompt}}<li>Prompts or responses beyond the reviewed task above</li>{{else}}<li>Prompts and responses</li>{{end}}<li>Environment variables, secrets, git credentials</li><li>Unredacted terminal output or raw agent sessions</li></ul></div></div><p>Exact payload:</p><pre id="payload">loading…</pre></div></div>
</div>
</div>
<script>
var sharedReportURL='';
function openShareModal(){
  var modal=document.getElementById('share-modal');
  modal.classList.add('open');modal.setAttribute('aria-hidden','false');document.body.classList.add('modal-open');
  modal.querySelector('.modal-close').focus();
}
function closeShareModal(event){
  if(event&&event.target!==event.currentTarget)return;
  var modal=document.getElementById('share-modal');
  modal.classList.remove('open');modal.setAttribute('aria-hidden','true');document.body.classList.remove('modal-open');
}
function showManifest(){
  var m=document.getElementById('manifest');
  m.classList.toggle('open');
  if(m.classList.contains('open')){
    fetch('/payload').then(r=>r.text()).then(t=>document.getElementById('payload').textContent=t);
  }
}
function postText(){return document.getElementById('share-copy').textContent.trim();}
// LinkedIn dropped pre-filled share text years ago - its share URL carries a
// link and nothing else, and a link alone posts as an empty card. So the post
// (numbers + link) goes to the clipboard and the composer opens for a paste.
function shareOnLinkedIn(){
  if(!sharedReportURL)return;
  var text=postText()+'\n\n'+sharedReportURL;
  var status=document.getElementById('linkedin-status');
  navigator.clipboard.writeText(text).then(function(){
    status.textContent='Post copied — paste it into the LinkedIn composer.';
  }).catch(function(){
    status.textContent='Copy failed. Post text: '+text;
  }).then(function(){
    window.open('https://www.linkedin.com/feed/?shareActive=true&shareUrl='+encodeURIComponent(sharedReportURL),'_blank','noopener');
  });
}
function createShareLink(){
  if(sharedReportURL)return;
  var button=document.getElementById('create-share-button');
  button.disabled=true;button.textContent='Creating link…';
  document.getElementById('status').textContent='Uploading the reviewed report data…';
  fetch('/upload',{method:'POST'}).then(r=>r.json()).then(d=>{
    if(d.error){document.getElementById('status').textContent='Failed: '+d.error;button.disabled=false;button.textContent='Try again';return;}
    sharedReportURL=d.url;
    button.textContent='14-day link created';
    document.getElementById('status').textContent='Ready · '+d.url+' · expires '+d.expires_at;
    document.getElementById('linkedin-link').disabled=false;
    var xText=postText()+'\n\n#AIEngineering #CodingAgents';
    var x=document.getElementById('x-link');
    x.href='https://x.com/intent/post?text='+encodeURIComponent(xText)+'&url='+encodeURIComponent(d.url);x.classList.remove('disabled');x.setAttribute('aria-disabled','false');
  }).catch(function(){
    // A network-level failure here means this page outlived its server:
    // "repoguide report" was stopped while the tab stayed open.
    document.getElementById('status').textContent='Failed: this report’s local server is no longer running. Re-run "repoguide report" and try again.';
    button.disabled=false;button.textContent='Try again';
  });
}
document.addEventListener('keydown',function(event){if(event.key==='Escape')closeShareModal();});
</script>
</body></html>`))
