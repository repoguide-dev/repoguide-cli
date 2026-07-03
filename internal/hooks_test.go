package internal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetManagedCommitHooksInstallAndUninstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)

	repoRoot := initTestRepo(t, filepath.Join(tempDir, "repo"), "repo_one")
	storeDir := filepath.Join(RepoGuideDir(), "repos", "repo_one")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(storeDir): %v", err)
	}
	if err := writeJSON(filepath.Join(storeDir, "repo.json"), RepoConfig{RepoID: "repo_one", RepoRoot: repoRoot}); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}

	hooks, err := SetManagedCommitHooks(storeDir, repoRoot, true)
	if err != nil {
		t.Fatalf("SetManagedCommitHooks(enable) returned error: %v", err)
	}
	if len(hooks) != 2 || !hooks[0].Installed || !hooks[1].Installed {
		t.Fatalf("hooks after install = %#v, want both installed", hooks)
	}

	for _, name := range []string{hookPreCommit, hookPostCommit} {
		path, err := gitHookFilePath(repoRoot, name)
		if err != nil {
			t.Fatalf("gitHookFilePath(%s): %v", name, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if !strings.Contains(string(data), managedHookStart) {
			t.Fatalf("%s missing managed hook block: %q", name, string(data))
		}
	}

	cfg, err := LoadRepoConfigFile(storeDir)
	if err != nil {
		t.Fatalf("LoadRepoConfigFile: %v", err)
	}
	if !commitHooksEnabled(cfg) {
		t.Fatalf("commit hooks should be enabled in repo config")
	}

	hooks, err = SetManagedCommitHooks(storeDir, repoRoot, false)
	if err != nil {
		t.Fatalf("SetManagedCommitHooks(disable) returned error: %v", err)
	}
	for _, hook := range hooks {
		if hook.Installed {
			t.Fatalf("hook %s still installed after disable", hook.Name)
		}
	}
	cfg, err = LoadRepoConfigFile(storeDir)
	if err != nil {
		t.Fatalf("LoadRepoConfigFile after disable: %v", err)
	}
	if commitHooksEnabled(cfg) {
		t.Fatalf("commit hooks should be disabled in repo config")
	}
}

func initTestRepo(t *testing.T, path, repoID string) string {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo): %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test User")
	runGit(t, path, "config", "repoguide.enabled", "true")
	runGit(t, path, "config", "repoguide.repoId", repoID)
	runGit(t, path, "config", "repoguide.version", "1")
	return path
}

func runGit(t *testing.T, repoRoot string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
