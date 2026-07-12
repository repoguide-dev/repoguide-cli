package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/repoguide/repoguide-core/model"
)

// touchFiles creates empty files at repoRoot/relPath for each path, so
// realFileExists (which checks the real filesystem to reject bogus
// "paths" like a leaked search regex) treats them as real reads.
func touchFiles(t *testing.T, repoRoot string, relPaths ...string) {
	t.Helper()
	for _, rel := range relPaths {
		full := filepath.Join(repoRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindWorstSessionPicksHighestNavigationScoreGlobally(t *testing.T) {
	repoRoot := t.TempDir()
	touchFiles(t, repoRoot, "a.go", "b.go", "c.go", "unrelated.go", "repos_test.go", "other.go")

	stored := []model.RepoSessionEvents{
		{
			ID: "cheap", Agent: "codex",
			Events: []model.SessionEvent{
				{Index: 1, Kind: "prompt", Text: "quick fix"},
				{Index: 2, Kind: "tool_call", ReadPaths: []string{"a.go"}},
				{Index: 3, Kind: "tool_call", WritePaths: []string{"other.go"}},
			},
		},
		{
			ID: "worst", Agent: "codex", Name: "Fix repository deletion",
			Events: []model.SessionEvent{
				{Index: 1, Kind: "prompt", Text: "Fix repository deletion behavior and update its tests."},
				{Index: 2, Kind: "tool_call", ToolName: "Grep", ReadPaths: []string{"a.go"}},
				{Index: 3, Kind: "tool_call", ReadPaths: []string{"b.go", "c.go", "a.go"}}, // a.go reopened
				{Index: 4, Kind: "tool_call", WritePaths: []string{"repos_test.go"}},
				// After the edit: must not count toward the score or detour.
				{Index: 5, Kind: "tool_call", ReadPaths: []string{"unrelated.go"}},
			},
		},
	}

	got, ok := FindWorstSession(repoRoot, stored)
	if !ok {
		t.Fatalf("expected a worst-session match")
	}
	if got.EditedFile != "repos_test.go" {
		t.Fatalf("EditedFile = %q, want repos_test.go (the higher-scoring session)", got.EditedFile)
	}
	if got.SessionTitle != "Fix repository deletion" {
		t.Fatalf("unexpected session title: %q", got.SessionTitle)
	}
	if got.Prompt != "" {
		t.Fatalf("prompt should be omitted when a session title is available, got %q", got.Prompt)
	}
	if got.FilesRead != 3 {
		t.Fatalf("FilesRead = %d, want 3 (a, b, c)", got.FilesRead)
	}
	if got.FilesReopened != 1 {
		t.Fatalf("FilesReopened = %d, want 1 (a.go read twice)", got.FilesReopened)
	}
	if got.Searches != 1 {
		t.Fatalf("Searches = %d, want 1 (Grep tool call)", got.Searches)
	}
	for _, path := range got.Detour {
		if path == "unrelated.go" {
			t.Fatalf("detour must not include reads that happened after the edit, got %v", got.Detour)
		}
	}
	if got.Score != 5 {
		t.Fatalf("Score = %d, want 5 (3 files + 1 reopen + 1 search)", got.Score)
	}
}

func TestFindWorstSessionRejectsNonFileReadPaths(t *testing.T) {
	// Regression: a search-tool call can leave a query pattern (e.g.
	// "rate.?limit") in ReadPaths instead of a real file. It must not be
	// displayed as if the agent had read that file.
	repoRoot := t.TempDir()
	touchFiles(t, repoRoot, "real.go", "target.go")

	stored := []model.RepoSessionEvents{
		{
			ID: "s1", Agent: "codex",
			Events: []model.SessionEvent{
				{Index: 1, Kind: "prompt", Text: "add rate limiting"},
				{Index: 2, Kind: "tool_call", ReadPaths: []string{"real.go", "rate.?limit"}},
				{Index: 3, Kind: "tool_call", WritePaths: []string{"target.go"}},
			},
		},
	}

	got, ok := FindWorstSession(repoRoot, stored)
	if !ok {
		t.Fatalf("expected a match")
	}
	if got.FilesRead != 1 {
		t.Fatalf("FilesRead = %d, want 1 (only real.go exists on disk)", got.FilesRead)
	}
	for _, p := range got.Detour {
		if p == "rate.?limit" {
			t.Fatalf("detour must not include non-existent paths, got %v", got.Detour)
		}
	}
}

func TestFindWorstSessionNoMatchWithoutAnyEdit(t *testing.T) {
	repoRoot := t.TempDir()
	touchFiles(t, repoRoot, "other.go")
	stored := []model.RepoSessionEvents{
		{ID: "s1", Events: []model.SessionEvent{{Index: 1, Kind: "tool_call", ReadPaths: []string{"other.go"}}}},
	}
	if _, ok := FindWorstSession(repoRoot, stored); ok {
		t.Fatalf("expected no match when no session contains any edit")
	}
}

func TestCleanPromptTextStripsInjectedPreamble(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "environment context with redacted opener",
			in:   "[redacted] <cwd>/Users/x/demo</cwd> <shell>zsh</shell> <current_date>2026-03-30</current_date> <timezone>Europe/Berlin</timezone> </environment_context> Fix the login bug",
			want: "Fix the login bug",
		},
		{
			name: "AGENTS.md instructions block",
			in:   "# AGENTS.md instructions for /repo/app\n<INSTRUCTIONS>\n# AGENTS.md\nThis file is the fast path...\n</INSTRUCTIONS>\nAdd rate limiting to the API.",
			want: "Add rate limiting to the API.",
		},
		{
			name: "plain prompt untouched",
			in:   "Fix repository deletion behavior and update its tests.",
			want: "Fix repository deletion behavior and update its tests.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanPromptText(tc.in); got != tc.want {
				t.Fatalf("cleanPromptText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWorstSessionSkipsTerminalMetadataPrompt(t *testing.T) {
	repoRoot := t.TempDir()
	touchFiles(t, repoRoot, "a.go", "target.go")
	stored := []model.RepoSessionEvents{{
		ID: "s1", Name: "(untitled)",
		Events: []model.SessionEvent{
			{Index: 1, Kind: "prompt", Text: "/Users/alex/demo zsh 2026-05-29 Europe/Berlin"},
			{Index: 2, Kind: "prompt", Text: "Fix interview delivery handling."},
			{Index: 3, Kind: "tool_call", ReadPaths: []string{"a.go"}},
			{Index: 4, Kind: "tool_call", WritePaths: []string{"target.go"}},
		},
	}}

	got, ok := FindWorstSession(repoRoot, stored)
	if !ok {
		t.Fatal("expected a worst-session match")
	}
	if got.Prompt != "Fix interview delivery handling." {
		t.Fatalf("Prompt = %q, want usable prompt fallback", got.Prompt)
	}
}
