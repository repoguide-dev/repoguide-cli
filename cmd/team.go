package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	teamJoinCmd.Flags().String("invite-code", "", "Team invite code")
	teamJoinCmd.Flags().String("repo", "", "Team repo ID or repo name")
	teamJoinCmd.Flags().String("path", "", "Path to an existing local checkout")
	teamCmd.AddCommand(teamJoinCmd)
	root.AddCommand(teamCmd)
}

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Manage team workspaces",
}

var teamJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Connect a local checkout to a shared team repo",
	RunE:  runTeamJoin,
}

func runTeamJoin(cmd *cobra.Command, _ []string) error {
	if err := ensureActiveLogin(); err != nil {
		return err
	}
	inviteCode, _ := cmd.Flags().GetString("invite-code")
	repoRef, _ := cmd.Flags().GetString("repo")
	repoPath, _ := cmd.Flags().GetString("path")
	if strings.TrimSpace(inviteCode) == "" {
		return fmt.Errorf("--invite-code is required")
	}
	if strings.TrimSpace(repoRef) == "" {
		return fmt.Errorf("--repo is required")
	}

	token, ok := clientauth.Load()
	if !ok {
		return fmt.Errorf("not logged in")
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
	team, err := client.JoinTeam(inviteCode)
	if err != nil {
		return err
	}
	if team == nil || strings.TrimSpace(team.ID) == "" {
		return fmt.Errorf("team not found")
	}
	repos, err := client.ListTeamRepos(team.ID)
	if err != nil {
		return err
	}
	repo, err := resolveTeamRepo(repos, repoRef)
	if err != nil {
		return err
	}

	previousRepoID := ""
	if strings.TrimSpace(repoPath) != "" {
		previousRepoID, _ = internal.GitRepoID(repoPath)
	}
	if strings.TrimSpace(repoPath) == "" {
		repoPath, err = findLocalRepoPath(*repo)
		if err != nil {
			return err
		}
		previousRepoID, _ = internal.GitRepoID(repoPath)
	}
	if strings.TrimSpace(previousRepoID) != "" && previousRepoID != repo.RepoID {
		// Flush sessions that were collected under the personal repo ID before
		// moving its cloud data into the shared team repo.
		if err := client.UploadRepoEvents(previousRepoID, repoPath); err != nil {
			return fmt.Errorf("sync existing repo before joining team: %w", err)
		}
		if err := client.MergeTeamRepo(team.ID, repo.RepoID, previousRepoID); err != nil {
			return err
		}
	}
	result, err := internal.LinkRepoAt(repoPath, repo.RepoID, "online", team.ID)
	if err != nil {
		return err
	}
	// Mark as connected immediately, don't wait for a session to actually sync.
	if err := client.MarkRepoConnected(team.ID, repo.RepoID); err != nil {
		return fmt.Errorf("mark team repo connected: %w", err)
	}
	// Do not wait for the next MCP request to sync sessions collected while the
	// checkout was being connected. Uploading events also refreshes the team's
	// derived analysis bundle on the backend.
	if err := client.UploadRepoEvents(repo.RepoID, repoPath); err != nil {
		return fmt.Errorf("sync team repo after joining: %w", err)
	}
	fmt.Printf("Connected %s to %s (%s)\n", result.RepoRoot, repoLabel(*repo), team.Name)
	return nil
}

func resolveTeamRepo(repos []sessionimport.TeamRepo, repoRef string) (*sessionimport.TeamRepo, error) {
	repoRef = strings.TrimSpace(repoRef)
	for i := range repos {
		repo := &repos[i]
		if repo.RepoID == repoRef || repo.RepoName == repoRef || repo.RepoURL == repoRef {
			return repo, nil
		}
	}
	return nil, fmt.Errorf("team repo %q not found", repoRef)
}

func findLocalRepoPath(repo sessionimport.TeamRepo) (string, error) {
	repos, err := sessionimport.DiscoverGitRepoRoots()
	if err != nil {
		return "", err
	}
	targetURL := normalizeRepoURL(repo.RepoURL)
	for _, root := range repos {
		remote, _ := internal.GitOutputAt(root, "remote", "get-url", "origin")
		if targetURL != "" && normalizeRepoURL(remote) == targetURL {
			return root, nil
		}
	}
	for _, root := range repos {
		if filepath.Base(root) == repo.RepoName {
			return root, nil
		}
	}
	if strings.TrimSpace(repo.RepoURL) == "" {
		return "", fmt.Errorf("no matching local checkout found for %s", repoLabel(repo))
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
		return "", fmt.Errorf("no matching local checkout found for %s", repoLabel(repo))
	}
	cwd, _ := os.Getwd()
	dest := filepath.Join(cwd, repoCloneName(repo.RepoURL, repoLabel(repo)))
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("No local checkout found for %s. Clone %s to %s? [Y/n] ", repoLabel(repo), repo.RepoURL, dest)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "n" || answer == "no" {
		return "", fmt.Errorf("cancelled")
	}
	cloneCmd := exec.Command("git", "clone", repo.RepoURL, dest)
	cloneCmd.Stdout = os.Stdout
	cloneCmd.Stderr = os.Stderr
	if err := cloneCmd.Run(); err != nil {
		return "", err
	}
	return dest, nil
}

func normalizeRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.Replace(raw, "git@github.com:", "https://github.com/", 1)
	raw = strings.Replace(raw, "ssh://git@github.com/", "https://github.com/", 1)
	return raw
}

func repoCloneName(repoURL, fallback string) string {
	name := filepath.Base(strings.TrimSuffix(strings.TrimSpace(repoURL), ".git"))
	if name != "" && name != "." && name != string(filepath.Separator) {
		return name
	}
	return fallback
}

func repoLabel(repo sessionimport.TeamRepo) string {
	if strings.TrimSpace(repo.RepoName) != "" {
		return repo.RepoName
	}
	return repo.RepoID
}
