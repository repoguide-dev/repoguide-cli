package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/repoguide/repoguide-core/model"
)

func TestBuildSessionStripsMarkers(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	call := func(ev model.SessionEvent) model.SessionEvent { ev.Kind = "tool_call"; return ev }

	strips := BuildSessionStrips(root, []model.RepoSessionEvents{{
		Name: "Fix thing",
		Events: []model.SessionEvent{
			call(model.SessionEvent{ToolName: "Grep", SearchQuery: "x"}), // ok: a read follows
			call(model.SessionEvent{ReadPaths: []string{"b.go"}}),        // unused: never edited
			call(model.SessionEvent{ReadPaths: []string{"a.go"}}),        // ok: edited later
			call(model.SessionEvent{ReadPaths: []string{"a.go"}}),        // reopen
			call(model.SessionEvent{WritePaths: []string{"a.go"}}),       // first edit
			call(model.SessionEvent{ToolName: "Grep", SearchQuery: "y"}), // cold: nothing read after
		},
	}})

	if len(strips) != 1 {
		t.Fatalf("got %d strips, want 1", len(strips))
	}
	got := strips[0]
	// The whole session is drawn; the divider marks where the first edit landed.
	want := []string{markerOK, markerUnused, markerOK, markerReopen, markerEdit, markerCold}
	if len(got.Markers) != len(want) {
		t.Fatalf("got %d drawn markers, want %d: %v", len(got.Markers), len(want), got.Markers)
	}
	for i, w := range want {
		if got.Markers[i] != w {
			t.Errorf("marker %d = %q, want %q", i, got.Markers[i], w)
		}
	}
	if got.EditIndex != 5 || got.Calls != 6 || got.Title != "Fix thing" {
		t.Errorf("unexpected strip header: %+v", got)
	}
	if got.PreEditIndex() != 4 {
		t.Errorf("divider index = %d, want 4", got.PreEditIndex())
	}
	if got.AfterCalls != 1 || got.AfterOther != 1 {
		t.Errorf("tail should count the one post-edit search: %+v", got)
	}
	if got.TopFile != "a.go" || got.TopReads != 2 {
		t.Errorf("most-opened file = %q x%d, want a.go x2", got.TopFile, got.TopReads)
	}
}
