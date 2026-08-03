package cmd

import (
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
)

// adoptedRepoIDFor returns the ID the backend already holds for repoPath, or ""
// when there is none, when logged out, or when the lookup fails.
//
// Every path that can register a repo needs this, not just `repoguide repo
// init`. A repo's identity normally lives in local state, and `repoguide purge`
// deletes exactly that while the backend record survives - so the next
// registration mints a fresh random ID and splits the repository's history
// across two records. `repoguide mcp install` is the more common of those
// paths, since it registers whatever repos the user selects.
func adoptedRepoIDFor(repoPath string) string {
	token, ok := clientauth.Load()
	if !ok {
		return ""
	}
	return sessionimport.CloudClient{
		BaseURL: getBackendURL(),
		Token:   token.Token,
	}.FindRepoIDForRoot(repoPath)
}
