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
