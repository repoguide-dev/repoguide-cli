package sessionimport

import (
	"fmt"
	"testing"
)

func TestCodexEdits(t *testing.T) {
	sessions, err := LoadSessionPage("codex", 0, 10, SessionLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions.Sessions {
		artifacts, err := BuildSessionArtifacts(s)
		if err != nil {
			t.Fatal(err)
		}
		m := artifacts.Analysis.Metrics
		fmt.Printf("session %s: edits=%d reads=%d tools=%d eventsCached=%v\n", s.ID, m.EditedFileCount, m.ReadFileCount, m.ToolCallCount, artifacts.EventsCached)
	}
}
