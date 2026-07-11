package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func GitRepoIDOrFatal(t *testing.T, repoRoot string) string {
	t.Helper()
	repoID, err := GitRepoID(repoRoot)
	if err != nil {
		t.Fatalf("GitRepoID: %v", err)
	}
	return repoID
}

func TestLinkRepoAtReplacesExistingRepoID(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)
	repoRoot := initTestRepo(t, filepath.Join(t.TempDir(), "repo"), "personal_repo")

	result, err := LinkRepoAt(repoRoot, "team_repo", "online", "team_123")
	if err != nil {
		t.Fatalf("LinkRepoAt: %v", err)
	}
	if result.RepoID != "team_repo" {
		t.Fatalf("RepoID = %q, want team_repo", result.RepoID)
	}
	if got := GitRepoIDOrFatal(t, repoRoot); got != "team_repo" {
		t.Fatalf("git repo ID = %q, want team_repo", got)
	}
	cfg, err := LoadRepoConfigFile(filepath.Join(homeDir, ".repoguide", "repos", "team_repo"))
	if err != nil {
		t.Fatalf("LoadRepoConfigFile: %v", err)
	}
	if cfg.RepoID != "team_repo" || cfg.Mode != "online" || cfg.TeamID != "team_123" {
		t.Fatalf("repo config = %#v, want team_repo in online mode", cfg)
	}
}
