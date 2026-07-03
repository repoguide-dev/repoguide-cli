package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunManagedCommitHookScrubsInjectedMessagesFromIndex(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)

	repoRoot := initTestRepo(t, filepath.Join(tempDir, "repo"), "repo_one")
	content := strings.Join([]string{
		"# Repo guide",
		"",
		"Keep this section.",
		"",
		AgentInstructionBriefFor("repo_one"),
		"",
		AgentFeedbackInstructionFor("repo_one"),
	}, "\n")
	if err := os.WriteFile(filepath.Join(repoRoot, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(AGENTS.md): %v", err)
	}
	runGit(t, repoRoot, "add", "AGENTS.md")

	t.Chdir(repoRoot)
	if err := RunManagedCommitHook(hookPreCommit); err != nil {
		t.Fatalf("RunManagedCommitHook(pre-commit) returned error: %v", err)
	}

	staged, err := gitShowIndexFile(repoRoot, "AGENTS.md")
	if err != nil {
		t.Fatalf("gitShowIndexFile: %v", err)
	}
	if strings.Contains(staged, "repoguide:mcp-instruction") || strings.Contains(staged, "repoguide:feedback-instruction") {
		t.Fatalf("staged AGENTS.md still contains injected blocks: %q", staged)
	}
	if !strings.Contains(staged, "Keep this section.") {
		t.Fatalf("staged AGENTS.md lost repo content: %q", staged)
	}

	worktree, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md): %v", err)
	}
	if !strings.Contains(string(worktree), "repoguide:mcp-instruction") {
		t.Fatalf("worktree AGENTS.md should retain injected block: %q", string(worktree))
	}
}

func TestActivateMCPRepoEnablesManagedCommitHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)

	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo): %v", err)
	}
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	t.Chdir(repoRoot)

	result, err := InitRepo(InitOptions{})
	if err != nil {
		t.Fatalf("InitRepo returned error: %v", err)
	}
	filename, err := ActivateMCPRepo(repoRoot)
	if err != nil {
		t.Fatalf("ActivateMCPRepo returned error: %v", err)
	}
	if filename != "AGENTS.md" {
		t.Fatalf("filename = %q, want AGENTS.md", filename)
	}
	cfg, err := LoadRepoConfigFile(result.StoreDir)
	if err != nil {
		t.Fatalf("LoadRepoConfigFile: %v", err)
	}
	if !commitHooksEnabled(cfg) {
		t.Fatalf("commit hooks should be enabled after MCP activation")
	}
	for _, name := range []string{hookPreCommit, hookPostCommit} {
		path, err := gitHookFilePath(repoRoot, name)
		if err != nil {
			t.Fatalf("gitHookFilePath(%s): %v", name, err)
		}
		if !hookFileContainsManagedBlock(path) {
			t.Fatalf("expected managed hook block in %s after MCP activation", name)
		}
	}
}
