package mcp

import (
	"os"
	"os/exec"
	"testing"
)

// initTestRepo and runGit are duplicated from internal/repo/repo_store_test.go
// (trivial, self-contained git-init test helpers) per this package's shim
// conventions -- see internal_shim.go.

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

func runGit(t *testing.T, repoRoot string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
