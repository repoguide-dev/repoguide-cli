package internal

import (
	"os"
	"path/filepath"
	"testing"
)

// A purge removes the local record of a repo's ID. Without adoption the next
// init mints a fresh random ID and the backend, which keys only on repo_id,
// ends up with two records for one directory.
func TestInitRepoAdoptsProvidedRepoID(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	t.Setenv("HOME", dir)
	SetRepoGuideDirOverride(store)
	t.Cleanup(func() { SetRepoGuideDirOverride("") })

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")

	cwd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	const adopted = "repo_existing_deadbeef"
	res, err := InitRepo(InitOptions{Mode: "online", RepoID: adopted})
	if err != nil {
		t.Fatal(err)
	}
	if res.RepoID != adopted {
		t.Fatalf("RepoID = %q, want the adopted %q", res.RepoID, adopted)
	}

	// A repo that already has an identity keeps it - adoption must not override.
	res2, err := InitRepo(InitOptions{Mode: "online", RepoID: "repo_other_11111111", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.RepoID != adopted {
		t.Fatalf("existing identity was overridden: got %q, want %q", res2.RepoID, adopted)
	}
}

// InitRepoAt is the entry point `repoguide mcp install` uses, and it is the
// more common way a repo gets registered - so it has to adopt an existing ID
// too. Wiring adoption into `repo init` alone still minted a fresh ID here,
// which is how this workspace ended up with a fourth cloud record after a
// purge.
func TestInitRepoAtAdoptsProvidedRepoID(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	t.Setenv("HOME", dir)
	SetRepoGuideDirOverride(store)
	t.Cleanup(func() { SetRepoGuideDirOverride("") })

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")

	const adopted = "repo_existing_cafebabe"
	res, err := InitRepoAt(repo, InitOptions{Mode: "online", RepoID: adopted})
	if err != nil {
		t.Fatal(err)
	}
	if res.RepoID != adopted {
		t.Fatalf("RepoID = %q, want the adopted %q", res.RepoID, adopted)
	}
}
