package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex applies edits via a native patch tool, not a shell apply_patch heredoc.
// Before patch_apply_end was parsed, every such session reported zero lines edited.
func TestCodexPatchApplyEndYieldsEditsAndLineCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	raw := strings.Join([]string{
		`{"timestamp":"2026-07-17T12:00:00Z","type":"session_meta","payload":{"id":"s1","cwd":"/repo"}}`,
		// two files in one patch: 3 added / 1 removed, headers not counted
		`{"timestamp":"2026-07-17T12:00:01Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"exec-1","success":true,"changes":{"/repo/b.go":{"type":"update","unified_diff":"@@ -1,2 +1,3 @@\n ctx\n-old\n+new\n+extra\n"},"/repo/a.go":{"type":"add","unified_diff":"@@ -0,0 +1 @@\n+package a\n"}}}}`,
		// failed patch changed nothing on disk, so it must not count
		`{"timestamp":"2026-07-17T12:00:02Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"exec-2","success":false,"changes":{"/repo/c.go":{"type":"update","unified_diff":"@@ -1 +1 @@\n+nope\n"}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	events, err := buildCodexSessionEvents(path)
	if err != nil {
		t.Fatalf("buildCodexSessionEvents: %v", err)
	}

	var patches []SessionEvent
	for _, e := range events {
		if e.Kind == "patch_apply" {
			patches = append(patches, e)
		}
	}
	if len(patches) != 1 {
		t.Fatalf("want 1 patch_apply event (failed patch dropped), got %d", len(patches))
	}
	got := patches[0]
	if want := []string{"/repo/a.go", "/repo/b.go"}; strings.Join(got.WritePaths, ",") != strings.Join(want, ",") {
		t.Errorf("WritePaths = %v, want %v", got.WritePaths, want)
	}
	if got.LinesAdded != 3 || got.LinesRemoved != 1 {
		t.Errorf("lines = +%d/-%d, want +3/-1", got.LinesAdded, got.LinesRemoved)
	}
}

// A held-out session called RepoGuide and was refused: it is the randomized
// control, and must not be filed alongside sessions that never called at all.
func TestClassifyRepoGuideResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want repoGuideResultKind
	}{
		{"briefing", "Task-to-topic match\n- 82% Session Import", repoGuideExperience},
		{"holdout", "No repository experience is being served.\n\n[repoguide:holdout]", repoGuideHoldout},
		{"clarification", "Task maps to multiple topics", repoGuideNoExperience},
		{"error", "understand-task failed: boom", repoGuideNoExperience},
		{"empty", "   ", repoGuideNoExperience},
	} {
		if got := classifyRepoGuideResult(tc.text); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Codex reports cached_input_tokens as a subset of input_tokens. Left as-is,
// estimateCostUSD bills those tokens at the full input rate and again at the
// cache rate — locally that overstated Codex spend by 5.4x.
func TestCodexTokenUsageExcludesCachedInput(t *testing.T) {
	raw := `{"last_token_usage":{"input_tokens":18934,"cached_input_tokens":10496,"output_tokens":137}}`
	got := parseCodexTokenUsage([]byte(raw))
	if got == nil {
		t.Fatal("expected usage")
	}
	if got.InputTokens != 8438 {
		t.Errorf("InputTokens = %d, want 8438 (18934 - 10496)", got.InputTokens)
	}
	if got.CacheReadTokens != 10496 {
		t.Errorf("CacheReadTokens = %d, want 10496", got.CacheReadTokens)
	}
}

func TestUncachedInputClampsAtZero(t *testing.T) {
	if got := uncachedInput(100, 500); got != 0 {
		t.Errorf("cached > input must clamp to 0, got %d", got)
	}
}
