package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
)

func TestRunStatuslineHookChainsPreviousCommandAndAppendsBadge(t *testing.T) {
	repo := setupHookTestRepo(t)

	chain := base64.StdEncoding.EncodeToString([]byte("printf existing-badge"))
	payload := `{"cwd":"` + repo + `"}`

	var out bytes.Buffer
	if err := RunStatuslineHook(strings.NewReader(payload), &out, chain); err != nil {
		t.Fatalf("RunStatuslineHook: %v", err)
	}
	if !strings.Contains(out.String(), "existing-badge") {
		t.Fatalf("expected chained command output preserved, got %q", out.String())
	}
	if !strings.Contains(out.String(), "RepoGuide") {
		t.Fatalf("expected RepoGuide badge appended, got %q", out.String())
	}
}

func TestRunStatuslineHookNoBadgeOutsideActivatedRepo(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	_ = os.MkdirAll(homeDir, 0o755)
	t.Setenv("HOME", homeDir)

	payload := `{"cwd":"` + tempDir + `"}`
	var out bytes.Buffer
	if err := RunStatuslineHook(strings.NewReader(payload), &out, ""); err != nil {
		t.Fatalf("RunStatuslineHook: %v", err)
	}
	if strings.Contains(out.String(), "RepoGuide") {
		t.Fatalf("expected no badge outside a RepoGuide repo, got %q", out.String())
	}
}

func TestRunStatuslineHookHandlesEmptyChain(t *testing.T) {
	repo := setupHookTestRepo(t)

	payload := `{"cwd":"` + repo + `"}`
	var out bytes.Buffer
	if err := RunStatuslineHook(strings.NewReader(payload), &out, ""); err != nil {
		t.Fatalf("RunStatuslineHook: %v", err)
	}
	if !strings.Contains(out.String(), "RepoGuide") {
		t.Fatalf("expected RepoGuide badge with no prior statusLine to chain, got %q", out.String())
	}
}

func TestRunStatuslineHookShowsOfflineLabelForLocalRepos(t *testing.T) {
	repo := setupHookTestRepo(t)
	writeRepoMode(t, "repo_one", "local")

	payload := `{"cwd":"` + repo + `"}`
	var out bytes.Buffer
	if err := RunStatuslineHook(strings.NewReader(payload), &out, ""); err != nil {
		t.Fatalf("RunStatuslineHook: %v", err)
	}
	if !strings.Contains(out.String(), "RepoGuide Enabled (Offline) ✓") {
		t.Fatalf("expected offline badge, got %q", out.String())
	}
}

func TestRunStatuslineHookShowsProLabelFromCachedAuthPlan(t *testing.T) {
	repo := setupHookTestRepo(t)
	writeCachedAuthToken(t, clientauth.Token{Token: "tok", Email: "test@example.com", Plan: "PRO"})

	payload := `{"cwd":"` + repo + `"}`
	var out bytes.Buffer
	if err := RunStatuslineHook(strings.NewReader(payload), &out, ""); err != nil {
		t.Fatalf("RunStatuslineHook: %v", err)
	}
	if !strings.Contains(out.String(), "RepoGuide Enabled (Pro) ✓") {
		t.Fatalf("expected pro badge, got %q", out.String())
	}
}

func TestRunStatuslineHookShowsTeamLabelFromCachedAuthPlan(t *testing.T) {
	repo := setupHookTestRepo(t)
	writeCachedAuthToken(t, clientauth.Token{Token: "tok", Email: "test@example.com", Plan: "TEAM"})

	payload := `{"cwd":"` + repo + `"}`
	var out bytes.Buffer
	if err := RunStatuslineHook(strings.NewReader(payload), &out, ""); err != nil {
		t.Fatalf("RunStatuslineHook: %v", err)
	}
	if !strings.Contains(out.String(), "RepoGuide Enabled (Team) ✓") {
		t.Fatalf("expected team badge, got %q", out.String())
	}
}

// TestInstallClaudeCodeStatuslineWithNoExistingCommandIsValidShell guards
// against a real regression: with nothing to chain, an unquoted --chain
// value left a dangling flag with no argument, which cobra rejected at
// runtime. It runs the generated command through an actual shell (with a
// stub standing in for the repoguide binary) so a quoting mistake here
// fails the test instead of only showing up live in Claude Code.
func TestInstallClaudeCodeStatuslineWithNoExistingCommandIsValidShell(t *testing.T) {
	repo := setupHookTestRepo(t)

	stubDir := t.TempDir()
	binPath := filepath.Join(stubDir, "repoguide")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$#\"\n"
	if err := os.WriteFile(binPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("WriteFile(stub): %v", err)
	}

	if err := InstallClaudeCodeStatusline(repo, binPath); err != nil {
		t.Fatalf("InstallClaudeCodeStatusline: %v", err)
	}
	data, err := os.ReadFile(claudeSettingsLocalPath(repo))
	if err != nil {
		t.Fatalf("ReadFile(settings.local.json): %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	cmd := raw["statusLine"].(map[string]any)["command"].(string)

	c := exec.Command("sh", "-c", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("generated statusLine command failed to parse: %v, output: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "4" {
		t.Fatalf("expected the stub to receive 4 args (mcp statusline --chain <value>), got %q", out)
	}
}

func TestInstallClaudeCodeStatuslineChainsExistingUserCommand(t *testing.T) {
	repo := setupHookTestRepo(t)

	// simulate an existing user-level statusLine (e.g. ponytail's)
	userSettings := filepath.Join(homeDirFromEnv(), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(userSettings), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := writeJSON(userSettings, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "bash ponytail-statusline.sh"},
	}); err != nil {
		t.Fatalf("writeJSON(user settings): %v", err)
	}

	if err := InstallClaudeCodeStatusline(repo, "/usr/local/bin/repoguide"); err != nil {
		t.Fatalf("InstallClaudeCodeStatusline: %v", err)
	}

	data, err := os.ReadFile(claudeSettingsLocalPath(repo))
	if err != nil {
		t.Fatalf("ReadFile(settings.local.json): %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	sl := raw["statusLine"].(map[string]any)
	cmd := sl["command"].(string)
	if !strings.Contains(cmd, "mcp statusline --chain ") {
		t.Fatalf("expected wrapped statusLine command, got %q", cmd)
	}
	encoded := strings.TrimPrefix(cmd, "\"/usr/local/bin/repoguide\" mcp statusline --chain \"")
	encoded = strings.TrimSuffix(encoded, "\"")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != "bash ponytail-statusline.sh" {
		t.Fatalf("expected chained command to embed the prior statusLine command, got %q", decoded)
	}

	// installing again must be a no-op, not double-wrap
	if err := InstallClaudeCodeStatusline(repo, "/usr/local/bin/repoguide"); err != nil {
		t.Fatalf("InstallClaudeCodeStatusline (second call): %v", err)
	}
	data2, err := os.ReadFile(claudeSettingsLocalPath(repo))
	if err != nil {
		t.Fatalf("ReadFile after second install: %v", err)
	}
	if !bytes.Equal(data, data2) {
		t.Fatalf("expected idempotent install, settings.local.json changed:\nbefore: %s\nafter: %s", data, data2)
	}
}

func TestRemoveClaudeCodeStatuslineOnlyRemovesOwnEntry(t *testing.T) {
	repo := setupHookTestRepo(t)

	// a statusLine the user configured directly at the repo level, not via us
	path := claudeSettingsLocalPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := writeJSON(path, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "echo my-own-statusline"},
	}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if err := RemoveClaudeCodeStatusline(repo); err != nil {
		t.Fatalf("RemoveClaudeCodeStatusline: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "my-own-statusline") {
		t.Fatalf("expected user's own statusLine preserved, got %s", data)
	}

	// now install ours (overwriting the user's own, same as before), then
	// remove: settings.local.json had nothing else in it, so it's deleted.
	if err := InstallClaudeCodeStatusline(repo, "/usr/local/bin/repoguide"); err != nil {
		t.Fatalf("InstallClaudeCodeStatusline: %v", err)
	}
	if err := RemoveClaudeCodeStatusline(repo); err != nil {
		t.Fatalf("RemoveClaudeCodeStatusline: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected settings.local.json removed once RepoGuide's entry was its only content, stat err = %v", err)
	}
}

func homeDirFromEnv() string {
	h, _ := os.UserHomeDir()
	return h
}

func writeCachedAuthToken(t *testing.T, token clientauth.Token) {
	t.Helper()
	if err := clientauth.Save(token); err != nil {
		t.Fatalf("Save(auth token): %v", err)
	}
}

func writeRepoMode(t *testing.T, repoID, mode string) {
	t.Helper()
	storeDir := filepath.Join(homeDirFromEnv(), ".repoguide", "repos", repoID)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(storeDir): %v", err)
	}
	if err := writeJSON(filepath.Join(storeDir, "repo.json"), map[string]any{
		"version":       1,
		"repoId":        repoID,
		"repoRoot":      filepath.Join(homeDirFromEnv(), "repo"),
		"initializedAt": "2026-07-02T00:00:00Z",
		"mode":          mode,
	}); err != nil {
		t.Fatalf("writeJSON(repo.json): %v", err)
	}
}
