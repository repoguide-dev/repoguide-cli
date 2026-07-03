package sessionimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterSessionEntriesByRepoMatchesConfiguredRepo(t *testing.T) {
	repos := []RepoConfig{
		{RepoID: "repo_alpha", RepoRoot: "/work/alpha"},
		{RepoID: "repo_beta", RepoRoot: "/work/beta"},
	}
	entries := []sessionIndexEntry{
		{RepoID: "repo_alpha"},
		{RepoID: "repo_beta"},
		{},
	}

	t.Run("matches repo id", func(t *testing.T) {
		filtered := filterSessionEntriesByRepo(entries, repos, "repo_alpha")
		if len(filtered) != 1 || filtered[0].RepoID != "repo_alpha" {
			t.Fatalf("unexpected filtered entries: %+v", filtered)
		}
	})

	t.Run("matches repo name", func(t *testing.T) {
		filtered := filterSessionEntriesByRepo(entries, repos, "beta")
		if len(filtered) != 1 || filtered[0].RepoID != "repo_beta" {
			t.Fatalf("unexpected filtered entries: %+v", filtered)
		}
	})

	t.Run("matches repo root path", func(t *testing.T) {
		filtered := filterSessionEntriesByRepo(entries, repos, "/work/alpha")
		if len(filtered) != 1 || filtered[0].RepoID != "repo_alpha" {
			t.Fatalf("unexpected filtered entries: %+v", filtered)
		}
	})
}

func TestFilterSessionEntriesByRepoMatchesEquivalentPaths(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	repoAlias := filepath.Join(tempDir, "repo-link")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo): %v", err)
	}
	if err := os.Symlink(repoRoot, repoAlias); err != nil {
		t.Fatalf("Symlink(repo): %v", err)
	}

	repos := []RepoConfig{{RepoID: "repo_alpha", RepoRoot: repoRoot}}
	entries := []sessionIndexEntry{{RepoID: "repo_alpha"}}

	filtered := filterSessionEntriesByRepo(entries, repos, repoRoot)
	if len(filtered) != 1 || filtered[0].RepoID != "repo_alpha" {
		t.Fatalf("unexpected filtered entries: %+v", filtered)
	}
}

func TestSessionAnnotatorMarksEquivalentConfiguredRepoInitialized(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	repoAlias := filepath.Join(tempDir, "repo-link")
	workingDir := filepath.Join(repoAlias, "nested")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}
	if err := os.Symlink(repoRoot, repoAlias); err != nil {
		t.Fatalf("Symlink(repo): %v", err)
	}
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workingDir): %v", err)
	}

	annotator := newSessionAnnotator([]RepoConfig{{RepoID: "repo_alpha", RepoRoot: repoRoot}})
	session := SessionSummary{Cwd: workingDir}
	annotator.annotate(&session)

	if session.RepoID != "repo_alpha" {
		t.Fatalf("RepoID = %q, want repo_alpha", session.RepoID)
	}
	if session.RepoRoot != repoRoot {
		t.Fatalf("RepoRoot = %q, want %q", session.RepoRoot, repoRoot)
	}
	if !session.RepoInitialized {
		t.Fatal("expected RepoInitialized to be true")
	}
	if session.RepoRelativeCwd != "nested" {
		t.Fatalf("RepoRelativeCwd = %q, want nested", session.RepoRelativeCwd)
	}
}
