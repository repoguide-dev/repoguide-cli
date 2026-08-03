package cmd

import (
	"os"
	"testing"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
)

// RemoveAllTrackedData deletes the entire RepoGuide directory, auth.json
// included. runRemoveAll therefore has to capture the token *before* calling
// it: loading afterwards yields no token, the cloud deletion loop is skipped
// without a word, and purge leaves every backend repo record orphaned - which
// is what shipped until now. This pins the premise, so anyone moving the load
// back below the wipe finds out why it breaks.
func TestRemoveAllTrackedDataDeletesTheAuthToken(t *testing.T) {
	dir := t.TempDir()
	// Both internal.RepoGuideDir and auth.tokenPath derive from the home
	// directory, and auth resolves its own path - an internal-only override
	// would leave Save writing to the real ~/.repoguide.
	t.Setenv("HOME", dir)
	// Run outside any git repo so no real repo config can be touched.
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := clientauth.Save(clientauth.Token{Token: "test-token"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := clientauth.Load(); !ok {
		t.Fatal("token should load before the purge")
	}

	if _, err := repopkg.RemoveAllTrackedData(); err != nil {
		t.Fatalf("RemoveAllTrackedData: %v", err)
	}

	if _, ok := clientauth.Load(); ok {
		t.Fatal("auth token survived the purge; if this is now intentional, " +
			"revisit the load-before-wipe ordering in runRemoveAll")
	}
}
