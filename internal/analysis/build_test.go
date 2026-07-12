package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/repoguide/repoguide-core/model"
)

func TestBuildRepoAnalysisIncludesRelatedSessionNames(t *testing.T) {
	repoRoot := filepath.Clean("/repo")
	sourcePath := filepath.Join(repoRoot, "pkg", "foo.go")
	testPath := filepath.Join(repoRoot, "pkg", "foo_test.go")
	subsystemPath := filepath.Join(repoRoot, "backend", "repoanalysis", "build.go")

	stored := []model.RepoSessionEvents{
		{
			Agent:     "codex",
			ID:        "s1",
			Name:      "Subsystem sweep",
			UpdatedAt: time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC),
			Events: []model.SessionEvent{
				{Index: 1, Timestamp: "2026-06-20T10:00:00Z", Kind: "prompt", Text: "inspect"},
				{Index: 2, Kind: "tool_call", ReadPaths: []string{subsystemPath, sourcePath}},
			},
		},
		{
			Agent:     "codex",
			ID:        "s2",
			Name:      "Test friction run",
			UpdatedAt: time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC),
			Events: []model.SessionEvent{
				{Index: 1, Timestamp: "2026-06-21T11:00:00Z", Kind: "prompt", Text: "edit"},
				{Index: 2, Kind: "tool_call", ReadPaths: []string{sourcePath, testPath}, WritePaths: []string{sourcePath, testPath}},
			},
		},
		{
			Agent:     "codex",
			ID:        "s3",
			Name:      "Seen together again",
			UpdatedAt: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),
			Events: []model.SessionEvent{
				{Index: 1, Timestamp: "2026-06-21T12:00:00Z", Kind: "prompt", Text: "verify"},
				{Index: 2, Kind: "tool_call", ReadPaths: []string{sourcePath, testPath}},
			},
		},
	}

	bundle, err := BuildRepoAnalysis(repoRoot, stored, false)
	if err != nil {
		t.Fatalf("BuildRepoAnalysis: %v", err)
	}

	assertHasNamedSession := func(label string, sessions []RepoAnalysisSessionRef, wantID, wantName string) {
		t.Helper()
		for _, session := range sessions {
			if session.ID == wantID && session.Name == wantName {
				return
			}
		}
		t.Fatalf("%s missing session %q (%q): %#v", label, wantID, wantName, sessions)
	}

	var subsystem *RepoAnalysisSubsystem
	for i := range bundle.Subsystems {
		if bundle.Subsystems[i].Name == "backend/repoanalysis" {
			subsystem = &bundle.Subsystems[i]
			break
		}
	}
	if subsystem == nil {
		t.Fatalf("expected backend/repoanalysis subsystem in %#v", bundle.Subsystems)
	}
	assertHasNamedSession("subsystem related sessions", subsystem.RelatedSessions, "s1", "Subsystem sweep")

	if len(bundle.SeenWithGroups) == 0 {
		t.Fatalf("expected seen_with_groups in bundle")
	}
	assertHasNamedSession("seen_with related sessions", bundle.SeenWithGroups[0].RelatedSessions, "s3", "Seen together again")

	if len(bundle.TestSignals.SourceAndTestCoEdit) == 0 {
		t.Fatalf("expected source_and_test_co_edit signals in %#v", bundle.TestSignals)
	}
	assertHasNamedSession("test signal related sessions", bundle.TestSignals.SourceAndTestCoEdit[0].RelatedSessions, "s2", "Test friction run")

	if len(bundle.TestSignals.TestFriction) == 0 {
		t.Fatalf("expected test_friction signals in %#v", bundle.TestSignals)
	}
	assertHasNamedSession("test friction related sessions", bundle.TestSignals.TestFriction[0].RelatedSessions, "s2", "Test friction run")
}

func TestBuildRepoAnalysisIncludesSessionPromptSummaries(t *testing.T) {
	repoRoot := filepath.Clean("/repo")
	longPrompt := strings.Repeat("a", 310)

	stored := []model.RepoSessionEvents{
		{
			Agent:     "codex",
			ID:        "s1",
			Name:      "Older session",
			UpdatedAt: time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC),
			Events: []model.SessionEvent{
				{Index: 1, Timestamp: "2026-06-20T10:00:00Z", Kind: "prompt", Text: "first prompt"},
				{Index: 2, Kind: "tool_call"},
			},
		},
		{
			Agent:     "codex",
			ID:        "s2",
			Name:      "Newest session",
			UpdatedAt: time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC),
			Events: []model.SessionEvent{
				{Index: 1, Timestamp: "2026-06-21T10:00:00Z", Kind: "prompt", Text: longPrompt},
				{Index: 2, Kind: "tool_call"},
				{Index: 3, Kind: "prompt", Text: "second prompt"},
				{Index: 4, Kind: "tool_call"},
			},
		},
	}

	bundle, err := BuildRepoAnalysis(repoRoot, stored, false)
	if err != nil {
		t.Fatalf("BuildRepoAnalysis: %v", err)
	}

	if len(bundle.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %#v", bundle.Sessions)
	}
	if bundle.Sessions[0].ID != "s2" {
		t.Fatalf("expected newest session first, got %#v", bundle.Sessions)
	}
	if bundle.Sessions[0].Title != "Newest session" {
		t.Fatalf("expected session title to be preserved, got %#v", bundle.Sessions[0])
	}
	if len(bundle.Sessions[0].Prompts) != 2 {
		t.Fatalf("expected 2 prompt previews, got %#v", bundle.Sessions[0])
	}
	if got := bundle.Sessions[0].Prompts[0]; got != strings.Repeat("a", 300) {
		t.Fatalf("expected first prompt preview to be truncated to 300 chars, got %q (%d chars)", got, len([]rune(got)))
	}
	if got := bundle.Sessions[0].Prompts[1]; got != "second prompt" {
		t.Fatalf("expected second prompt preview to be preserved, got %q", got)
	}
}

func TestBuildRepoAnalysisIgnoresLocalCommandCaveatPrompts(t *testing.T) {
	repoRoot := filepath.Clean("/repo")

	stored := []model.RepoSessionEvents{
		{
			Agent:     "codex",
			ID:        "s1",
			Name:      "Session with caveat",
			UpdatedAt: time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC),
			Events: []model.SessionEvent{
				{Index: 1, Timestamp: "2026-06-21T10:00:00Z", Kind: "prompt", Text: "<local-command-caveat>Caveat: The messages below were generated by the user while running local comm"},
				{Index: 2, Kind: "prompt", Text: "real prompt"},
			},
		},
	}

	bundle, err := BuildRepoAnalysis(repoRoot, stored, false)
	if err != nil {
		t.Fatalf("BuildRepoAnalysis: %v", err)
	}

	if len(bundle.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %#v", bundle.Sessions)
	}
	if got := bundle.Sessions[0].Prompts; len(got) != 1 || got[0] != "real prompt" {
		t.Fatalf("expected caveat prompt to be ignored, got %#v", got)
	}
}

// TestRepoRelPathCanonicalizesNestedRepoPaths guards against a real bug: in a
// multi-repo workspace (a container repo with nested git repos checked out
// inside it, e.g. submodules), some sessions record a file relative to the
// nested repo's own root ("backend/server/repo_handlers.go") while others
// record it relative to the container root
// ("repoguide-cloud/backend/server/repo_handlers.go"). Both must resolve to
// the same canonical path or session/read counts silently split in two.
func TestRepoRelPathCanonicalizesNestedRepoPaths(t *testing.T) {
	repoRoot := t.TempDir()
	nested := filepath.Join(repoRoot, "repoguide-cloud")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(nested, "backend", "server")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "repo_handlers.go"), []byte("package server"), 0o644); err != nil {
		t.Fatal(err)
	}

	fromNestedRoot := repoRelPath(repoRoot, "backend/server/repo_handlers.go")
	fromContainerRoot := repoRelPath(repoRoot, "repoguide-cloud/backend/server/repo_handlers.go")
	if fromNestedRoot != fromContainerRoot {
		t.Fatalf("expected both recordings to canonicalize to the same path, got %q vs %q", fromNestedRoot, fromContainerRoot)
	}
	if want := "repoguide-cloud/backend/server/repo_handlers.go"; fromNestedRoot != want {
		t.Fatalf("expected canonical path %q, got %q", want, fromNestedRoot)
	}

	// A path that genuinely doesn't exist anywhere still falls back to its
	// cleaned, as-recorded form rather than erroring or dropping it.
	if got := repoRelPath(repoRoot, "unknown/made_up.go"); got != "unknown/made_up.go" {
		t.Fatalf("expected unresolved path to fall back to cleaned form, got %q", got)
	}
}
