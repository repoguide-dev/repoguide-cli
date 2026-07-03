package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	mcpinternal "github.com/repoguide/repoguide-cli/internal/mcp"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	mcpTestCmd.Flags().String("repo", "", "Repository path to send to RepoGuide MCP tools")
	mcpTestCmd.Flags().String("task", "TEST-TASK", "Task text sent to RepoGuide MCP tools")
	mcpTestCmd.Flags().String("topic", "", "Topic ID to pass to repoguide_get_repo_experience, skipping topic selection (runs a single context-pack call)")
	mcpTestCmd.Flags().String("only", "", "Only run this tool call: understand_task, test_context, search_context")
	mcpTestCmd.Flags().Bool("verbose", false, "Print extra test context")
	mcpTestCmd.Flags().Bool("json", false, "Print the test report as JSON")
}

var mcpTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Start the MCP server and run a direct MCP test",
	RunE:  runMCPTest,
}

type mcpTestOutput struct {
	Task         string                      `json:"task"`
	RepoPath     string                      `json:"repo_path"`
	UsedTempRepo bool                        `json:"used_temp_repo"`
	Success      bool                        `json:"success"`
	TopicID      string                      `json:"topic_id,omitempty"`
	Topics       []mcpinternal.MCPSmokeTopic `json:"topics,omitempty"`
	Checks       []mcpinternal.MCPSmokeCheck `json:"checks"`
	Error        string                      `json:"error,omitempty"`
}

func runMCPTest(cmd *cobra.Command, _ []string) error {
	repoFlag, _ := cmd.Flags().GetString("repo")
	task, _ := cmd.Flags().GetString("task")
	topicID, _ := cmd.Flags().GetString("topic")
	only, _ := cmd.Flags().GetString("only")
	verbose, _ := cmd.Flags().GetBool("verbose")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	validOnly := map[string]bool{"": true, "understand_task": true, "test_context": true, "search_context": true}
	if !validOnly[only] {
		return fmt.Errorf("--only %q is not valid; choose: understand_task, test_context, search_context", only)
	}

	repoPath, usedTempRepo, cleanup, err := resolveMCPTestRepo(repoFlag)
	if err != nil {
		return err
	}
	defer cleanup()

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	if topicID != "" {
		return runMCPTestContext(executable, task, repoPath, topicID)
	}

	serveArgs := []string{"mcp", "serve"}
	if repopkg.RepoIsLocalMode(repoPath) {
		serveArgs = append(serveArgs, "--local")
	}
	report, err := mcpinternal.RunMCPSmoke(mcpinternal.MCPSmokeOptions{
		Command:  executable,
		Args:     serveArgs,
		Task:     task,
		RepoPath: repoPath,
		Only:     only,
	})

	output := mcpTestOutput{
		Task:         task,
		RepoPath:     repoPath,
		UsedTempRepo: usedTempRepo,
		Success:      report.Success() && err == nil,
		TopicID:      report.TopicID,
		Topics:       report.Topics,
		Checks:       report.Checks,
	}
	if err != nil {
		output.Error = err.Error()
	}

	if jsonOutput {
		if err := printMCPTestJSON(output); err != nil {
			return err
		}
	} else {
		printMCPTestReport(output, verbose, executable)
	}
	if err != nil {
		return err
	}
	if !report.Success() {
		return fmt.Errorf("mcp test checks failed")
	}
	return nil
}

func runMCPTestContext(executable, task, repoPath, topicID string) error {
	result, err := mcpinternal.RunMCPContextPack(mcpinternal.MCPSmokeOptions{
		Command:  executable,
		Args:     []string{"mcp", "serve"},
		Task:     task,
		RepoPath: repoPath,
		TopicID:  topicID,
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func resolveMCPTestRepo(repoFlag string) (string, bool, func(), error) {
	if repoFlag != "" {
		repoPath, err := filepath.Abs(repoFlag)
		if err != nil {
			return "", false, nil, err
		}
		return repoPath, false, func() {}, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", false, nil, err
	}
	if repopkg.IsRepoInitialized(cwd) {
		return cwd, false, func() {}, nil
	}

	repoPath, cleanup, err := createMCPSmokeRepo()
	return repoPath, true, cleanup, err
}

func createMCPSmokeRepo() (string, func(), error) {
	repoPath := preferredMCPTestRepoPath()
	if err := os.RemoveAll(repoPath); err != nil && !os.IsNotExist(err) {
		return "", nil, err
	}
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(repoPath)
	}

	files := map[string]string{
		"README.md":       "# TEST-REPO\n",
		"cli/go.mod":      "module testrepo\n\ngo 1.24.0\n",
		"cli/cmd/root.go": "package cmd\n",
	}
	for relPath, contents := range files {
		fullPath := filepath.Join(repoPath, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	return repoPath, cleanup, nil
}

func preferredMCPTestRepoPath() string {
	const repoName = "TEST-REPO"
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return filepath.Join("/tmp", repoName)
	}
	return filepath.Join(os.TempDir(), repoName)
}

func printMCPTestJSON(output mcpTestOutput) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func printMCPTestReport(output mcpTestOutput, verbose bool, executable string) {
	if verbose {
		repoLabel := output.RepoPath
		if output.UsedTempRepo {
			repoLabel += " (temp)"
		}
		fmt.Printf("repo: %s\n", repoLabel)
		fmt.Printf("task: %s\n", output.Task)
		fmt.Printf("server: %s mcp serve\n", executable)
	}

	passed := 0
	for _, check := range output.Checks {
		status := "FAIL"
		if check.OK {
			status = "PASS"
			passed++
		}
		if check.Detail != "" {
			fmt.Printf("%s %-38s %s\n", status, check.Name, check.Detail)
		} else {
			fmt.Printf("%s %s\n", status, check.Name)
		}
		if check.Name == "call_repoguide_list_topics" && check.OK {
			for _, t := range output.Topics {
				fmt.Printf("     %-32s %s\n", t.ID, t.Title)
			}
		}
		if verbose && check.Response != nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("     ", "  ")
			_ = enc.Encode(check.Response)
		}
	}
	fmt.Printf("%d/%d checks passed\n", passed, len(output.Checks))
}
