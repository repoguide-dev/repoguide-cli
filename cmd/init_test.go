package cmd

import (
	"strings"
	"testing"

	"github.com/repoguide/repoguide-cli/internal"
)

func TestRenderInitSummaryOfflineSuggestsOfflineSetup(t *testing.T) {
	out := renderInitSummary(initSummaryData{
		result: internal.InitResult{
			RepoRoot: "/tmp/repo",
			RepoID:   "repo_tmp",
			StoreDir: "~/.repoguide/repos/repo_tmp",
		},
		localMode: true,
	})

	if !strings.Contains(out, "repoguide setup --offline") {
		t.Fatalf("expected offline setup suggestion, got:\n%s", out)
	}
	if strings.Contains(out, "repoguide sync") {
		t.Fatalf("offline summary should not suggest sync, got:\n%s", out)
	}
}

func TestRenderInitSummaryOnlineSuggestsSync(t *testing.T) {
	out := renderInitSummary(initSummaryData{
		result: internal.InitResult{
			RepoRoot: "/tmp/repo",
			RepoID:   "repo_tmp",
			StoreDir: "~/.repoguide/repos/repo_tmp",
		},
	})

	if !strings.Contains(out, "repoguide sync") {
		t.Fatalf("expected sync suggestion, got:\n%s", out)
	}
}
