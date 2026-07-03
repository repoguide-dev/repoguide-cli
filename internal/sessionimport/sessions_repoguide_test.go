package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCodexSessionDetectsRepoGuideUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"session_meta","timestamp":"2026-06-18T12:00:00Z","payload":{"id":"codex-1","cwd":"/repo"}}`,
		`{"type":"turn_context","timestamp":"2026-06-18T12:00:01Z","payload":{"cwd":"/repo","model":"gpt-5"}}`,
		`{"type":"ai-title","timestamp":"2026-06-18T12:00:02Z","aiTitle":"Fix CLI"}`,
		`{"type":"response_item","timestamp":"2026-06-18T12:00:03Z","payload":{"type":"function_call","name":"mcp__repoguide__repoguide_get_repo_experience","call_id":"call_1"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, err := readCodexSession(path, nil)
	if err != nil {
		t.Fatalf("readCodexSession: %v", err)
	}
	if !session.UsedRepoGuide {
		t.Fatal("expected codex session to be marked as using repoguide")
	}
}

func TestReadClaudeSessionDetectsRepoGuideUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"assistant","timestamp":"2026-06-18T12:00:00Z","cwd":"/repo","message":{"model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"tool_1","name":"mcp__repoguide__repoguide_get_repo_experience","input":{"task":"fix cli"}},{"type":"text","text":"done"}]}}`,
		`{"type":"ai-title","timestamp":"2026-06-18T12:00:01Z","aiTitle":"Fix CLI"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, err := readClaudeSession(path)
	if err != nil {
		t.Fatalf("readClaudeSession: %v", err)
	}
	if !session.UsedRepoGuide {
		t.Fatal("expected claude session to be marked as using repoguide")
	}
}
