package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupHookTestRepo(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	t.Setenv("HOME", homeDir)
	return initTestRepo(t, filepath.Join(tempDir, "repo"), "repo_one")
}

func TestRunPromptHookInjectsOncePerSession(t *testing.T) {
	repo := setupHookTestRepo(t)

	payload := `{"session_id":"sess-1","prompt":"fix the login bug","cwd":"` + repo + `"}`

	var out bytes.Buffer
	if err := RunPromptHook(strings.NewReader(payload), &out, repo); err != nil {
		t.Fatalf("RunPromptHook: %v", err)
	}
	if !strings.Contains(out.String(), "repoguide_get_repo_experience") {
		t.Fatalf("expected first prompt to inject routing instruction, got %q", out.String())
	}
	if !strings.Contains(out.String(), "repo_one") {
		t.Fatalf("expected instruction to embed repo id, got %q", out.String())
	}

	out.Reset()
	if err := RunPromptHook(strings.NewReader(payload), &out, repo); err != nil {
		t.Fatalf("RunPromptHook (second call): %v", err)
	}
	if out.String() != "" {
		t.Fatalf("expected no output on second prompt of same session, got %q", out.String())
	}
}

func TestRunPromptHookNoopOutsideActivatedRepo(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	_ = os.MkdirAll(homeDir, 0o755)
	t.Setenv("HOME", homeDir)

	payload := `{"session_id":"sess-1","prompt":"hello","cwd":"` + tempDir + `"}`
	var out bytes.Buffer
	if err := RunPromptHook(strings.NewReader(payload), &out, tempDir); err != nil {
		t.Fatalf("RunPromptHook: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("expected no output outside a RepoGuide repo, got %q", out.String())
	}
}

func TestRunGeminiPromptHookReturnsStructuredContext(t *testing.T) {
	repo := setupHookTestRepo(t)
	payload := `{"session_id":"sess-1","prompt":"fix the login bug","cwd":"` + repo + `"}`
	var out bytes.Buffer
	if err := RunGeminiPromptHook(strings.NewReader(payload), &out, repo); err != nil {
		t.Fatalf("RunGeminiPromptHook: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("expected structured Gemini hook output, got %q: %v", out.String(), err)
	}
	specific, _ := result["hookSpecificOutput"].(map[string]any)
	context, _ := specific["additionalContext"].(string)
	if !strings.Contains(context, "repoguide_get_repo_experience") {
		t.Fatalf("missing routing context: %#v", result)
	}
}

func TestRunStopHookNeverBlocksForFeedback(t *testing.T) {
	repo := setupHookTestRepo(t)

	stopPayload := `{"session_id":"sess-1","cwd":"` + repo + `"}`
	var out bytes.Buffer
	if err := RunStopHook(strings.NewReader(stopPayload), &out, repo); err != nil {
		t.Fatalf("RunStopHook: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("expected no output from an opt-in feedback stop hook, got %q", out.String())
	}
}

func TestInstallAndRemoveClaudeCodeHooksRoundTrip(t *testing.T) {
	repo := setupHookTestRepo(t)

	if err := InstallClaudeCodeHooks(repo, "/usr/local/bin/repoguide"); err != nil {
		t.Fatalf("InstallClaudeCodeHooks: %v", err)
	}
	settingsPath := filepath.Join(repo, ".claude", "settings.local.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings.local.json): %v", err)
	}
	if !strings.Contains(string(data), "mcp hook prompt") || strings.Contains(string(data), "mcp hook stop") {
		t.Fatalf("expected only the routing hook wired, got %s", data)
	}
	if !strings.Contains(string(data), "RepoGuide task routing") || strings.Contains(string(data), "RepoGuide feedback reminder") {
		t.Fatalf("expected no feedback reminder hook, got %s", data)
	}

	// simulate a user's own unrelated hook already present, on the same events
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	hooks := raw["hooks"].(map[string]any)
	promptGroup := hooks["UserPromptSubmit"].([]any)
	promptGroup = append(promptGroup, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "echo user-hook"}},
	})
	hooks["UserPromptSubmit"] = promptGroup
	if err := writeJSON(settingsPath, raw); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	if err := RemoveClaudeCodeHooks(repo); err != nil {
		t.Fatalf("RemoveClaudeCodeHooks: %v", err)
	}
	data, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile after remove: %v", err)
	}
	if strings.Contains(string(data), "mcp hook") {
		t.Fatalf("expected RepoGuide hooks removed, got %s", data)
	}
	if !strings.Contains(string(data), "echo user-hook") {
		t.Fatalf("expected user's own hook preserved, got %s", data)
	}
}

func TestRemoveClaudeCodeHooksDeletesFileWhenOnlyRepoGuideHooks(t *testing.T) {
	repo := setupHookTestRepo(t)

	if err := InstallClaudeCodeHooks(repo, "/usr/local/bin/repoguide"); err != nil {
		t.Fatalf("InstallClaudeCodeHooks: %v", err)
	}
	if err := RemoveClaudeCodeHooks(repo); err != nil {
		t.Fatalf("RemoveClaudeCodeHooks: %v", err)
	}
	settingsPath := filepath.Join(repo, ".claude", "settings.local.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("expected settings.local.json to be removed, stat err = %v", err)
	}
}

func TestInstructRepoForClaudeMigratesStaticBlock(t *testing.T) {
	repo := setupHookTestRepo(t)

	claudeMD := filepath.Join(repo, "CLAUDE.md")
	existing := "# My project\n\n" + AgentInstructionBriefFor("repo_one") + "\nsome other content\n" + AgentFeedbackInstructionFor("repo_one")
	if err := os.WriteFile(claudeMD, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile(CLAUDE.md): %v", err)
	}

	filename, err := InstructRepoForClaude(repo)
	if err != nil {
		t.Fatalf("InstructRepoForClaude: %v", err)
	}
	if filename != ".claude/settings.local.json" {
		t.Fatalf("filename = %q, want .claude/settings.local.json", filename)
	}

	cleaned, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md): %v", err)
	}
	if strings.Contains(string(cleaned), "repoguide:mcp-instruction") || strings.Contains(string(cleaned), "repoguide:feedback-instruction") {
		t.Fatalf("expected static RepoGuide blocks removed from CLAUDE.md, got %s", cleaned)
	}
	if !strings.Contains(string(cleaned), "some other content") {
		t.Fatalf("expected unrelated CLAUDE.md content preserved, got %s", cleaned)
	}

	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("expected settings.local.json to be created: %v", err)
	}
}
