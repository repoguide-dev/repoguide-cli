package cmd

import (
	"fmt"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show RepoGuide setup status for the current repository",
	Run:   runStatus,
}

func runStatus(_ *cobra.Command, _ []string) {
	status := repopkg.DetectLocalSetup()
	if !status.InGitRepo {
		printRunInsideRepo("repoguide status")
		return
	}

	printSection("Git project", renderRepoPath(status.RepoRoot))

	if !status.Initialized {
		printSection("RepoGuide", "Not initialized")
		printNext("repoguide repo init")
		return
	}

	printSection("RepoGuide",
		"Initialized",
		"Repo ID: "+status.RepoID,
		"Store: "+renderPath(status.StoreDir),
	)
	printCommitHookStatus(status)

	if t, ok := clientauth.Load(); ok {
		printSection("Account", fmt.Sprintf("Signed in as %s", t.Email))
	} else {
		printSection("Account", "Not signed in", "Run: repoguide login")
	}
}
