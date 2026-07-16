package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRemoveAllWithRetryRemovesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := removeAllWithRetry(path); err != nil {
		t.Fatalf("removeAllWithRetry: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be removed, stat err = %v", path, err)
	}

	if !isDirectoryNotEmpty(&os.PathError{Err: syscall.ENOTEMPTY}) {
		t.Fatal("expected ENOTEMPTY path error to be recognized as retryable")
	}
}

func TestRemoveAllTrackedDataRemovesStoreAndRepoConfig(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)

	repoOne := initTestRepo(t, filepath.Join(tempDir, "repo-one"), "repo_one")
	repoTwo := initTestRepo(t, filepath.Join(tempDir, "repo-two"), "repo_two")

	storeRoot := RepoGuideDir()
	if err := os.MkdirAll(filepath.Join(storeRoot, "repos", "repo_one"), 0o755); err != nil {
		t.Fatalf("MkdirAll(repo_one): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(storeRoot, "repos", "repo_two"), 0o755); err != nil {
		t.Fatalf("MkdirAll(repo_two): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(storeRoot, "cache"), 0o755); err != nil {
		t.Fatalf("MkdirAll(cache): %v", err)
	}
	if err := writeJSON(filepath.Join(storeRoot, "config.json"), GlobalConfig{Version: repoguideVersion}); err != nil {
		t.Fatalf("writeJSON(config): %v", err)
	}
	if err := writeJSON(filepath.Join(storeRoot, "repos", "repo_one", "repo.json"), RepoConfig{RepoID: "repo_one", RepoRoot: repoOne}); err != nil {
		t.Fatalf("writeJSON(repo_one): %v", err)
	}
	if err := writeJSON(filepath.Join(storeRoot, "repos", "repo_two", "repo.json"), RepoConfig{RepoID: "repo_two", RepoRoot: repoTwo}); err != nil {
		t.Fatalf("writeJSON(repo_two): %v", err)
	}
	if err := os.WriteFile(filepath.Join(storeRoot, "cache", "sessions.json"), []byte("cached"), 0o644); err != nil {
		t.Fatalf("WriteFile(cache): %v", err)
	}
	result, err := RemoveAllTrackedData()
	if err != nil {
		t.Fatalf("RemoveAllTrackedData returned error: %v", err)
	}

	if result.RepoGuideDir != storeRoot {
		t.Fatalf("RepoGuideDir = %q, want %q", result.RepoGuideDir, storeRoot)
	}
	if len(result.RemovedRepos) != 2 {
		t.Fatalf("RemovedRepos = %d, want 2", len(result.RemovedRepos))
	}
	if len(result.SkippedRepos) != 0 {
		t.Fatalf("SkippedRepos = %d, want 0", len(result.SkippedRepos))
	}

	if _, err := os.Stat(storeRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be removed, stat err = %v", storeRoot, err)
	}

	assertGitConfigUnset(t, repoOne, "repoguide.repoId")
	assertGitConfigUnset(t, repoOne, "repoguide.enabled")
	assertGitConfigUnset(t, repoTwo, "repoguide.repoId")
	assertGitConfigUnset(t, repoTwo, "repoguide.enabled")
}

func TestRemoveTrackedRepoRemovesHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)

	repoRoot := initTestRepo(t, filepath.Join(tempDir, "repo"), "repo_one")
	storeDir := filepath.Join(RepoGuideDir(), "repos", "repo_one")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(storeDir): %v", err)
	}
	if err := writeJSON(filepath.Join(storeDir, "repo.json"), RepoConfig{RepoID: "repo_one", RepoRoot: repoRoot}); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}
	if _, err := SetManagedCommitHooks(storeDir, repoRoot, true); err != nil {
		t.Fatalf("SetManagedCommitHooks(enable): %v", err)
	}

	t.Chdir(repoRoot)
	if _, err := RemoveTrackedRepo(); err != nil {
		t.Fatalf("RemoveTrackedRepo returned error: %v", err)
	}

	for _, name := range []string{hookPreCommit, hookPostCommit} {
		path, err := gitHookFilePath(repoRoot, name)
		if err != nil {
			t.Fatalf("gitHookFilePath(%s): %v", name, err)
		}
		if hookFileContainsManagedBlock(path) {
			t.Fatalf("expected managed hook block to be removed from %s", name)
		}
	}
}

func initTestRepo(t *testing.T, path, repoID string) string {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo): %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test User")
	runGit(t, path, "config", "repoguide.enabled", "true")
	runGit(t, path, "config", "repoguide.repoId", repoID)
	runGit(t, path, "config", "repoguide.version", "1")
	return path
}

func assertGitConfigUnset(t *testing.T, repoRoot, key string) {
	t.Helper()

	cmd := exec.Command("git", "-C", repoRoot, "config", "--get", key)
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected %s to be unset in %s", key, repoRoot)
	}
}

func runGit(t *testing.T, repoRoot string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
