package sessionimport

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverGitRepoRootsFindsHomeReposAndSkipsHeavyDirs(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoOne := filepath.Join(homeDir, "work", "one")
	repoTwo := filepath.Join(homeDir, "Documents", "two")
	skipped := filepath.Join(homeDir, "node_modules", "dep")
	if err := os.MkdirAll(repoOne, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoTwo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skipped, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
	runGit(t, repoOne, "init")
	runGit(t, repoTwo, "init")
	runGit(t, skipped, "init")

	got, err := DiscoverGitRepoRoots()
	if err != nil {
		t.Fatalf("DiscoverGitRepoRoots: %v", err)
	}
	want := []string{evalSymlinkPath(t, repoTwo), evalSymlinkPath(t, repoOne)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverGitRepoRoots = %#v, want %#v", got, want)
	}
}

func evalSymlinkPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestDiscoverGitRepoRootsReturnsEmptyWhenHomeMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HOME", missing)
	if err := os.Unsetenv("CDPATH"); err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	got, err := DiscoverGitRepoRoots()
	if err != nil {
		t.Fatalf("DiscoverGitRepoRoots: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("DiscoverGitRepoRoots = %#v, want empty", got)
	}
}

func TestDiscoverGitRepoRootsSkipsDeepHomeTraversal(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	shallowRepo := filepath.Join(homeDir, "work", "alpha")
	deepRepo := filepath.Join(homeDir, "one", "two", "three", "four", "five")
	if err := os.MkdirAll(shallowRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deepRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
	runGit(t, shallowRepo, "init")
	runGit(t, deepRepo, "init")

	got, err := DiscoverGitRepoRoots()
	if err != nil {
		t.Fatalf("DiscoverGitRepoRoots: %v", err)
	}
	want := []string{evalSymlinkPath(t, shallowRepo)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverGitRepoRoots = %#v, want %#v", got, want)
	}
}
