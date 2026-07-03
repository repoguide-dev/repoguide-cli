package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/repoguide/repoguide-cli/internal"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(repoCmd)
	repoCmd.AddCommand(repoFixCmd)
	repoHookCmd.Hidden = true
	repoCmd.AddCommand(repoHookCmd)
}

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Show current repo in the repos browser, or run a subcommand",
	RunE:  runRepo,
}

func runRepo(_ *cobra.Command, _ []string) error {
	m := newReposModel()
	status := repopkg.DetectLocalSetup()
	if status.Initialized {
		m.focusRepoRoot = status.RepoRoot
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

var repoFixCmd = &cobra.Command{
	Use:   "fix",
	Short: "Check repo status and reinitialize without removing",
	RunE:  runRepoFix,
}

func runRepoFix(cmd *cobra.Command, _ []string) error {
	status := repopkg.DetectLocalSetup()
	if !status.InGitRepo {
		printRunInsideRepo("repoguide repo fix")
		return nil
	}

	printSection("Git project", renderRepoPath(status.RepoRoot))
	if status.Initialized {
		printSection("RepoGuide", "Initialized", "Repo ID: "+status.RepoID)
	} else {
		printSection("RepoGuide", "Not initialized")
	}

	hintFiles := internal.ScanHintFiles(status.RepoRoot)
	if len(hintFiles) > 0 {
		printSection("Agent/hint files found", hintFiles...)
	} else {
		printSection("Agent/hint files", "None found")
	}

	if isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()) {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Reinitialize this repo? [y/N]: ")
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return nil
		}
	}

	_ = initCmd.Flags().Set("force", "true")
	return runInit(initCmd, nil)
}

var repoHookCmd = &cobra.Command{
	Use:   "hook-run [pre-commit|post-commit]",
	Short: "Run internal managed hook logic",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return internal.RunManagedCommitHook(args[0])
	},
}
