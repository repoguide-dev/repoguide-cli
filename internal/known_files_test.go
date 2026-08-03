package internal

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// ls-tree stops at a gitlink, so an umbrella repo used to report only its own
// top-level files and every path inside a submodule looked deleted - which made
// callers filter away the entire codebase.
func TestKnownFilesForBranchIncludesSubmoduleFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub-src")
	super := filepath.Join(dir, "super")

	for _, path := range []string{sub, super} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, path, "init", "-b", "main")
		runGit(t, path, "config", "user.email", "test@example.com")
		runGit(t, path, "config", "user.name", "Test User")
		runGit(t, path, "config", "protocol.file.allow", "always")
	}

	writeFile(t, filepath.Join(sub, "parser.go"), "package sub")
	runGit(t, sub, "add", ".")
	runGit(t, sub, "commit", "-m", "sub")

	writeFile(t, filepath.Join(super, "README.md"), "# umbrella")
	runGit(t, super, "add", ".")
	runGit(t, super, "commit", "-m", "super")
	runGit(t, super, "-c", "protocol.file.allow=always", "submodule", "add", sub, "child")
	runGit(t, super, "commit", "-m", "add submodule")

	files := KnownFilesForBranch(super, "main")
	if !slices.Contains(files, "README.md") {
		t.Fatalf("superproject files missing: %v", files)
	}
	if !slices.Contains(files, "child/parser.go") {
		t.Fatalf("submodule files must be listed under their superproject path: %v", files)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
