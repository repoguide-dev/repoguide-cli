package sessionimport

import (
	"os/exec"
	"testing"
)

// runGit is duplicated from internal/repo/repo_store_test.go (a trivial,
// self-contained git test helper) per this package's shim conventions.
func runGit(t *testing.T, repoRoot string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
