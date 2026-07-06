package ai

import (
	"strings"
	"testing"

	"github.com/repoguide/repoguide-core/model"
)

func TestBuildRepoContextSessionIncludesDiffSnippets(t *testing.T) {
	events := []model.SessionEvent{
		{Kind: "prompt", Text: "please patch repo context"},
		{
			Kind:        "tool_call",
			CommandText: "apply_patch <<'PATCH'\n*** Begin Patch\n*** Update File: internal/foo.go\n@@\n-old\n+new\n*** End Patch\nPATCH",
			WritePaths:  []string{"internal/foo.go"},
		},
		{
			Kind:        "tool_call",
			CommandText: "go test ./...",
			WritePaths:  []string{"internal/foo.go"},
		},
	}

	sess := BuildRepoContextSession("fb1", "s1", events)
	if got := sess.UserPrompts; len(got) != 1 || got[0] != "please patch repo context" {
		t.Fatalf("UserPrompts = %v", got)
	}
	if got := sess.EditedFiles; len(got) != 1 || got[0] != "internal/foo.go" {
		t.Fatalf("EditedFiles = %v", got)
	}
	if got := sess.DiffSnippets; len(got) != 1 || !strings.Contains(got[0], "*** Update File: internal/foo.go") {
		t.Fatalf("DiffSnippets = %v", got)
	}
}
