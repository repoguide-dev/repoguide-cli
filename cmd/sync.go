package cmd

import (
	"fmt"

	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(syncCmd)
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Upload local AI coding sessions for configured repositories",
	RunE:  runSync,
}

// runSync is the foreground counterpart to backgroundSync: the same upload,
// but the command does not return until it has finished or failed.
func runSync(_ *cobra.Command, _ []string) error {
	token, ok := clientauth.Load()
	if !ok {
		return fmt.Errorf("not logged in")
	}
	repos, err := repopkg.ListConfiguredRepos()
	if err != nil {
		return err
	}
	var cloudRepos []internal.RepoConfig
	for _, r := range repos {
		if r.Mode != "local" {
			cloudRepos = append(cloudRepos, r)
		}
	}
	if len(cloudRepos) == 0 {
		fmt.Println("No cloud-synced repos configured.")
		return nil
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
	for _, repo := range cloudRepos {
		if err := client.UploadRepoEvents(repo.RepoID, repo.RepoRoot); err != nil {
			return fmt.Errorf("sync %s: %w", repo.RepoRoot, err)
		}
		fmt.Printf("Synced %s\n", repo.RepoRoot)
	}
	return nil
}
