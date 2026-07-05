package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestReadCodexSessionRepoGuideUsage(t *testing.T) {
	call := `{"type":"response_item","timestamp":"2026-06-18T12:00:03Z","payload":{"type":"function_call","name":"mcp__repoguide__repoguide_get_repo_experience","call_id":"call_1"}}`
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"experience result", `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Wall time: 1.6s\nOutput:\n{\"text\":\"Topic: Parser Pipeline\nwire dispatch in sessions.go\"}"}}`, true},
		{"topic list only", `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"{\"text\":\"Task maps to multiple topics. Call repoguide_get_repo_experience again with your chosen topic_id\"}"}}`, false},
		{"error result", `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"understand-task failed: invalid token (401 Unauthorized)"}}`, false},
		{"call without result", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{
				`{"type":"session_meta","timestamp":"2026-06-18T12:00:00Z","payload":{"id":"codex-1","cwd":"/repo"}}`,
				`{"type":"turn_context","timestamp":"2026-06-18T12:00:01Z","payload":{"cwd":"/repo","model":"gpt-5"}}`,
				`{"type":"ai-title","timestamp":"2026-06-18T12:00:02Z","aiTitle":"Fix CLI"}`,
				call,
			}
			if tc.output != "" {
				lines = append(lines, tc.output)
			}
			session, err := readCodexSession(writeSession(t, lines), nil)
			if err != nil {
				t.Fatalf("readCodexSession: %v", err)
			}
			if session.UsedRepoGuide != tc.want {
				t.Fatalf("UsedRepoGuide = %v, want %v", session.UsedRepoGuide, tc.want)
			}
		})
	}
}

func TestReadClaudeSessionRepoGuideUsage(t *testing.T) {
	call := `{"type":"assistant","timestamp":"2026-06-18T12:00:00Z","cwd":"/repo","message":{"model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"tool_1","name":"mcp__repoguide__repoguide_get_repo_experience","input":{"task":"fix cli"}}]}}`
	cases := []struct {
		name   string
		result string
		want   bool
	}{
		{"experience result", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":[{"type":"text","text":"Topic: Parser Pipeline\nwire dispatch in sessions.go"}]}]}}`, true},
		{"string content result", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":"Topic: Parser Pipeline"}]}}`, true},
		{"topic list only", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":[{"type":"text","text":"Task maps to multiple topics. Call repoguide_get_repo_experience again with your chosen topic_id"}]}]}}`, false},
		{"error result", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","is_error":true,"content":"understand-task failed: invalid token (401 Unauthorized)"}]}}`, false},
		{"call without result", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{
				call,
				`{"type":"ai-title","timestamp":"2026-06-18T12:00:01Z","aiTitle":"Fix CLI"}`,
			}
			if tc.result != "" {
				lines = append(lines, tc.result)
			}
			session, err := readClaudeSession(writeSession(t, lines))
			if err != nil {
				t.Fatalf("readClaudeSession: %v", err)
			}
			if session.UsedRepoGuide != tc.want {
				t.Fatalf("UsedRepoGuide = %v, want %v", session.UsedRepoGuide, tc.want)
			}
		})
	}
}
