package cmd

import (
	"testing"

	"github.com/repoguide/repoguide-cli/internal"
)

func TestRepoTypeLabel(t *testing.T) {
	if got := repoTypeLabel(internal.RepoConfig{}); got != "personal" {
		t.Fatalf("repoTypeLabel(personal) = %q", got)
	}
	if got := repoTypeLabel(internal.RepoConfig{TeamID: "team_123"}); got != "team" {
		t.Fatalf("repoTypeLabel(team) = %q", got)
	}
}
