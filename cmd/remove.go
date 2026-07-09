package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	mcpinternal "github.com/repoguide/repoguide-cli/internal/mcp"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	removeCmd.Flags().BoolP("yes", "y", false, "Remove tracking without confirmation")
	repoCmd.AddCommand(removeCmd)

	purgeCmd.Flags().BoolP("yes", "y", false, "Remove all data without confirmation")
	root.AddCommand(purgeCmd)
}

var removeCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove RepoGuide tracking for the current Git repository",
	RunE:  runRemove,
}

var purgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Remove all RepoGuide local data and repo configs",
	RunE:  runRemoveAll,
}

func runRemove(cmd *cobra.Command, _ []string) error {
	status := repopkg.DetectLocalSetup()
	if !status.InGitRepo {
		printRunInsideRepo("repoguide remove")
		return nil
	}

	if !status.Initialized {
		fmt.Println("RepoGuide is not tracking this repo.")
		fmt.Println()
		printSection("Git project", renderRepoPath(status.RepoRoot))
		printNext("repoguide repo init")
		return nil
	}

	assumeYes, _ := cmd.Flags().GetBool("yes")
	if !assumeYes {
		confirmed, err := confirmRemove(os.Stdin, os.Stdout, status.RepoRoot, status.StoreDir)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Remove cancelled.")
			return nil
		}
	}

	result, err := repopkg.RemoveTrackedRepo()
	if err != nil {
		if errors.Is(err, internal.ErrNotGitRepo) {
			fmt.Println("No Git repository found.")
			return nil
		}
		if errors.Is(err, internal.ErrRepoNotInitialized) {
			fmt.Println("RepoGuide is not tracking this repo.")
			return nil
		}
		return err
	}
	if result.Mode != "local" {
		if token, ok := clientauth.Load(); ok {
			if err := (sessionimport.CloudClient{
				BaseURL: getBackendURL(),
				Token:   token.Token,
			}).DeleteRepo(result.RepoID); err != nil {
				return fmt.Errorf("repo tracking removed locally, but backend repo deletion failed: %w", err)
			}
		}
	}

	mcpCfg, _ := mcpinternal.LoadMCPConfig()
	mcpinternal.UnsetRepoActivated(&mcpCfg, result.RepoRoot)
	mcpinternal.UnsetRepoHinted(&mcpCfg, result.RepoRoot)
	_ = mcpinternal.SaveMCPConfig(mcpCfg)

	if name, _ := mcpinternal.RemoveAllInstructions(result.RepoRoot); name != "" {
		printSection("✓ Removed RepoGuide instructions", name)
	}
	_ = internal.RemoveManagedCommitHooks(result.RepoRoot)
	if len(mcpCfg.ActivatedRepos) == 0 {
		for _, r := range mcpinternal.UninstallMCPClients() {
			if r.Err != nil {
				fmt.Printf("  ✗ %s: %s\n", r.Name, r.Err)
			} else {
				fmt.Printf("  ✓ %s MCP server removed\n", r.Name)
			}
		}
	}

	printSection("✓ Repo tracking removed", renderRepoPath(result.RepoRoot))
	printSection("Removed Repo ID", result.RepoID)
	printSection("Deleted local store", renderPath(result.StoreDir))
	printNext("repoguide repo init")
	return nil
}

func runRemoveAll(cmd *cobra.Command, _ []string) error {
	repos, err := repopkg.ListConfiguredRepos()
	if err != nil {
		return err
	}

	status := repopkg.DetectLocalSetup()
	repoCount := len(repos)
	if status.InGitRepo && status.Initialized {
		found := false
		for _, repo := range repos {
			if repo.RepoRoot == status.RepoRoot {
				found = true
				break
			}
		}
		if !found {
			repoCount++
		}
	}

	assumeYes, _ := cmd.Flags().GetBool("yes")
	if !assumeYes {
		confirmed, err := confirmRemoveAll(os.Stdin, os.Stdout, internal.RepoGuideDir(), repoCount)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Remove cancelled.")
			return nil
		}
	}

	result, err := repopkg.RemoveAllTrackedData()
	if err != nil {
		return err
	}
	if token, ok := clientauth.Load(); ok {
		client := sessionimport.CloudClient{
			BaseURL: getBackendURL(),
			Token:   token.Token,
		}
		for _, repo := range result.RemovedRepos {
			if repo.Mode == "local" {
				continue
			}
			if err := client.DeleteRepo(repo.RepoID); err != nil {
				return fmt.Errorf("repoguide data removed locally, but backend repo deletion failed for %s: %w", repo.RepoID, err)
			}
		}
	}

	for _, r := range mcpinternal.UninstallMCPClients() {
		if r.Err != nil {
			fmt.Printf("  ✗ %s: %s\n", r.Name, r.Err)
		} else {
			fmt.Printf("  ✓ %s plugin removed\n", r.Name)
		}
	}

	printSection("✓ RepoGuide data removed", renderPath(result.RepoGuideDir))
	printSection("Repo configs removed", fmt.Sprintf("%d", len(result.RemovedRepos)))
	if len(result.SkippedRepos) > 0 {
		lines := make([]string, 0, len(result.SkippedRepos))
		for _, repo := range result.SkippedRepos {
			lines = append(lines, renderRepoPath(repo.RepoRoot))
		}
		printSection("Skipped repos", lines...)
	}
	printNext("repoguide repo init")
	return nil
}

func confirmRemove(in io.Reader, out io.Writer, repoRoot, storeDir string) (bool, error) {
	fmt.Fprintln(out, renderDanger("This will remove RepoGuide tracking for:"))
	fmt.Fprintf(out, "  Repo:  %s\n", renderRepoPath(repoRoot))
	fmt.Fprintf(out, "  Store: %s\n", renderPath(storeDir))
	fmt.Fprintln(out)
	fmt.Fprint(out, renderDanger("Continue? [y/N]: "))

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func confirmRemoveAll(in io.Reader, out io.Writer, repoguideDir string, repoCount int) (bool, error) {
	fmt.Fprintln(out, renderDanger("This will remove ALL RepoGuide data:"))
	fmt.Fprintf(out, "  Store: %s\n", renderPath(repoguideDir))
	fmt.Fprintf(out, "  Repo config cleanup: %d repos\n", repoCount)
	fmt.Fprintln(out)
	fmt.Fprint(out, renderDanger("Continue? [y/N]: "))

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
