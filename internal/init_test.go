package internal

import "testing"

func GitRepoIDOrFatal(t *testing.T, repoRoot string) string {
	t.Helper()
	repoID, err := GitRepoID(repoRoot)
	if err != nil {
		t.Fatalf("GitRepoID: %v", err)
	}
	return repoID
}
