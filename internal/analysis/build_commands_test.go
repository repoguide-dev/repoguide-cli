package analysis

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/repoguide/repoguide-core/model"
)

// realisticSession models an observed agent session: the user asks for a change,
// the agent explores, runs a validation command that fails, the user corrects
// the approach, the agent edits and re-runs the command successfully.
func realisticSession(repoRoot string) model.RepoSessionEvents {
	source := filepath.Join(repoRoot, "backend", "server", "handlers.go")
	reference := filepath.Join(repoRoot, "backend", "model", "user.go")
	test := filepath.Join(repoRoot, "backend", "server", "handlers_test.go")
	return model.RepoSessionEvents{
		Agent:     "claude",
		ID:        "s1",
		Name:      "Add plan limit to handlers",
		UpdatedAt: time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC),
		Events: []model.SessionEvent{
			{Index: 0, Timestamp: "2026-06-21T10:00:00Z", Kind: "prompt", Text: "add a monthly plan limit check to the repo handlers"},
			{Index: 1, Kind: "tool_call", ToolName: "Bash", ToolCallID: "t1", CommandText: "ls backend/server"},
			{Index: 2, Kind: "tool_result", ToolCallID: "t1"},
			{Index: 3, Kind: "tool_call", ToolName: "Read", ReadPaths: []string{reference, source}},
			{Index: 4, Kind: "tool_call", ToolName: "Bash", ToolCallID: "t2", CommandText: "go test ./backend/server/..."},
			{Index: 5, Kind: "tool_result", ToolCallID: "t2", IsError: true, Text: "FAIL: TestPlanLimit missing fixture"},
			{Index: 6, Kind: "prompt", Text: "no - reuse the existing PlanLimit helper on the user model instead of adding a new check"},
			{Index: 7, Kind: "tool_call", ToolName: "Edit", WritePaths: []string{source, test}},
			{Index: 8, Kind: "tool_call", ToolName: "Bash", ToolCallID: "t3", CommandText: "go test ./backend/server/..."},
			{Index: 9, Kind: "tool_result", ToolCallID: "t3"},
		},
	}
}

func TestBuildRepoAnalysisExtractsObservedCommands(t *testing.T) {
	repoRoot := filepath.Clean("/repo")
	bundle, err := BuildRepoAnalysis(repoRoot, []model.RepoSessionEvents{realisticSession(repoRoot)}, false)
	if err != nil {
		t.Fatalf("BuildRepoAnalysis: %v", err)
	}
	if len(bundle.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(bundle.Sessions))
	}
	session := bundle.Sessions[0]

	if session.Timestamp == "" {
		t.Fatalf("expected session timestamp for recency signal")
	}

	var testCmd *RepoAnalysisCommand
	for i := range session.Commands {
		if session.Commands[i].Text == "go test ./backend/server/..." {
			testCmd = &session.Commands[i]
		}
		if session.Commands[i].Text == "ls backend/server" {
			t.Fatalf("navigation command should be filtered out: %#v", session.Commands)
		}
	}
	if testCmd == nil {
		t.Fatalf("expected observed test command in session commands: %#v", session.Commands)
	}
	if testCmd.Runs != 2 {
		t.Fatalf("test command runs = %d, want 2", testCmd.Runs)
	}
	if testCmd.Failures != 1 {
		t.Fatalf("test command failures = %d, want 1 (linked via tool_call id)", testCmd.Failures)
	}
}

func TestBuildRepoAnalysisKeepsCorrectionPromptIntact(t *testing.T) {
	repoRoot := filepath.Clean("/repo")
	bundle, err := BuildRepoAnalysis(repoRoot, []model.RepoSessionEvents{realisticSession(repoRoot)}, false)
	if err != nil {
		t.Fatalf("BuildRepoAnalysis: %v", err)
	}
	prompts := bundle.Sessions[0].Prompts
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %#v", prompts)
	}
	// The follow-up correction is the highest-signal evidence in the session;
	// it must survive extraction verbatim, not as a truncated fragment.
	if prompts[1] != "no - reuse the existing PlanLimit helper on the user model instead of adding a new check" {
		t.Fatalf("correction prompt was mangled: %q", prompts[1])
	}
}

func TestAmbiguousSearchesExcludeUnextractableShellCommands(t *testing.T) {
	repoRoot := filepath.Clean("/repo")
	read := filepath.Join(repoRoot, "a.go")
	events := []model.SessionEvent{
		{Index: 0, Timestamp: "2026-06-21T10:00:00Z", Kind: "prompt", Text: "find things"},
	}
	// One find-pipeline "search" repeated with many distinct read targets would
	// previously surface the full command line as an ambiguous search query.
	for i := 0; i < 6; i++ {
		events = append(events,
			model.SessionEvent{Kind: "tool_call", ToolName: "Bash", CommandText: `find /repo -type f | head -60 && echo "---"`},
			model.SessionEvent{Kind: "tool_call", ToolName: "Read", ReadPaths: []string{read, filepath.Join(repoRoot, "b.go"), filepath.Join(repoRoot, "c.go"), filepath.Join(repoRoot, "d.go"), filepath.Join(repoRoot, "e.go")}},
		)
	}
	bundle, err := BuildRepoAnalysis(repoRoot, []model.RepoSessionEvents{{
		Agent: "claude", ID: "s1", Name: "noise", UpdatedAt: time.Now(), Events: events,
	}}, false)
	if err != nil {
		t.Fatalf("BuildRepoAnalysis: %v", err)
	}
	for _, a := range bundle.Discoverability.AmbiguousSearches {
		if a.Query == "(unknown)" || strings.ContainsAny(a.Query, "|&") {
			t.Fatalf("unextractable shell command surfaced as ambiguous search: %#v", a)
		}
	}
}
