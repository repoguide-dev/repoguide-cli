package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSessionArtifactsCodex(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "18", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := strings.Join([]string{
		`{"timestamp":"2026-06-18T12:00:00Z","type":"session_meta","payload":{"id":"codex-session","cwd":"/repo"}}`,
		`{"timestamp":"2026-06-18T12:00:01Z","type":"turn_context","payload":{"cwd":"/repo","model":"gpt-5"}}`,
		`{"timestamp":"2026-06-18T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect repo"}]}}`,
		`{"timestamp":"2026-06-18T12:00:03Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"sed -n '1,5p' README.md && tee cli/main.go\"]}","call_id":"call_1"}}`,
		`{"timestamp":"2026-06-18T12:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":300,"output_tokens":200},"last_token_usage":{"input_tokens":100,"cached_input_tokens":30,"output_tokens":20}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write raw session: %v", err)
	}

	session := SessionSummary{
		ID:        "codex-session",
		Agent:     "codex",
		Model:     "gpt-5",
		Path:      sessionPath,
		Timestamp: time.Now(),
	}

	artifacts, err := BuildSessionArtifacts(session)
	if err != nil {
		t.Fatalf("BuildSessionArtifacts: %v", err)
	}
	if artifacts.EventsCached || artifacts.AnalysisCached {
		t.Fatalf("expected fresh artifacts, got cached=%v/%v", artifacts.EventsCached, artifacts.AnalysisCached)
	}
	if artifacts.Analysis.Metrics.UserPromptCount != 1 {
		t.Fatalf("expected 1 prompt, got %d", artifacts.Analysis.Metrics.UserPromptCount)
	}
	if artifacts.Analysis.Metrics.ToolCallCount != 1 {
		t.Fatalf("expected 1 tool call, got %d", artifacts.Analysis.Metrics.ToolCallCount)
	}
	if artifacts.Analysis.Metrics.ReadFileCount != 1 || artifacts.Analysis.Metrics.ReadFiles[0] != "README.md" {
		t.Fatalf("unexpected read files: %#v", artifacts.Analysis.Metrics.ReadFiles)
	}
	if artifacts.Analysis.Metrics.EditedFileCount != 1 || artifacts.Analysis.Metrics.EditedFiles[0] != "cli/main.go" {
		t.Fatalf("unexpected edited files: %#v", artifacts.Analysis.Metrics.EditedFiles)
	}
	if artifacts.Analysis.Metrics.TokenUsage == nil || artifacts.Analysis.Metrics.TokenUsage.InputTokens != 100 {
		t.Fatalf("unexpected token usage: %#v", artifacts.Analysis.Metrics.TokenUsage)
	}
	if len(artifacts.Analysis.Metrics.PromptBlocks) != 1 {
		t.Fatalf("expected 1 prompt block, got %d", len(artifacts.Analysis.Metrics.PromptBlocks))
	}
	blockUsage := artifacts.Analysis.Metrics.PromptBlocks[0].TokenUsage
	if blockUsage == nil {
		t.Fatal("expected prompt block token usage")
	}
	if blockUsage.InputTokens != 100 || blockUsage.CacheReadTokens != 30 || blockUsage.OutputTokens != 20 {
		t.Fatalf("unexpected prompt block token usage: %#v", blockUsage)
	}
	cached, err := BuildSessionArtifacts(session)
	if err != nil {
		t.Fatalf("BuildSessionArtifacts (cached): %v", err)
	}
	if !cached.EventsCached || !cached.AnalysisCached {
		t.Fatalf("expected cached artifacts, got cached=%v/%v", cached.EventsCached, cached.AnalysisCached)
	}

	loaded, ok, err := LoadCachedSessionAnalysis(session)
	if err != nil {
		t.Fatalf("LoadCachedSessionAnalysis: %v", err)
	}
	if !ok {
		t.Fatal("expected cached analysis to be found")
	}
	if loaded.Analysis.Metrics.UserPromptCount != 1 {
		t.Fatalf("expected cached prompt count 1, got %d", loaded.Analysis.Metrics.UserPromptCount)
	}
}

func TestBuildSessionArtifactsClaude(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	sessionPath := filepath.Join(homeDir, ".claude", "projects", "repo", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := strings.Join([]string{
		`{"type":"user","timestamp":"2026-06-18T12:00:00Z","message":{"role":"user","content":"analyse this session"}}`,
		`{"type":"assistant","timestamp":"2026-06-18T12:00:01Z","message":{"model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"tool_1","name":"Read","input":{"file_path":"/repo/README.md"}},{"type":"tool_use","id":"tool_2","name":"Write","input":{"file_path":"/repo/out.json"}},{"type":"text","text":"done"}],"usage":{"input_tokens":150221,"output_tokens":31842,"cache_read_input_tokens":44100,"cache_creation_input_tokens":8200}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write raw session: %v", err)
	}

	session := SessionSummary{
		ID:        "claude-session",
		Agent:     "claude",
		Model:     "claude-sonnet-4-6",
		Path:      sessionPath,
		Timestamp: time.Now(),
	}

	artifacts, err := BuildSessionArtifacts(session)
	if err != nil {
		t.Fatalf("BuildSessionArtifacts: %v", err)
	}
	if artifacts.Analysis.Metrics.UserPromptCount != 1 {
		t.Fatalf("expected 1 prompt, got %d", artifacts.Analysis.Metrics.UserPromptCount)
	}
	if artifacts.Analysis.Metrics.ToolCallCount != 2 {
		t.Fatalf("expected 2 tool calls, got %d", artifacts.Analysis.Metrics.ToolCallCount)
	}
	if artifacts.Analysis.Metrics.ReadFileCount != 1 || artifacts.Analysis.Metrics.ReadFiles[0] != "/repo/README.md" {
		t.Fatalf("unexpected read files: %#v", artifacts.Analysis.Metrics.ReadFiles)
	}
	if artifacts.Analysis.Metrics.EditedFileCount != 1 || artifacts.Analysis.Metrics.EditedFiles[0] != "/repo/out.json" {
		t.Fatalf("unexpected edited files: %#v", artifacts.Analysis.Metrics.EditedFiles)
	}
	if artifacts.Analysis.Metrics.TokenUsage == nil {
		t.Fatal("expected token usage")
	}
	if artifacts.Analysis.Metrics.TokenUsage.InputTokens != 150221 ||
		artifacts.Analysis.Metrics.TokenUsage.OutputTokens != 31842 ||
		artifacts.Analysis.Metrics.TokenUsage.CacheReadTokens != 44100 ||
		artifacts.Analysis.Metrics.TokenUsage.CacheWriteTokens != 8200 {
		t.Fatalf("unexpected token usage: %#v", artifacts.Analysis.Metrics.TokenUsage)
	}
	if artifacts.Analysis.ModelPricing == nil {
		t.Fatal("expected model pricing")
	}
	if artifacts.Analysis.Metrics.EstimatedCostUSD <= 0 {
		t.Fatalf("expected estimated cost, got %.4f", artifacts.Analysis.Metrics.EstimatedCostUSD)
	}
}

func TestLoadCachedSessionAnalysisReturnsFalseWhenMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "18", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write raw session: %v", err)
	}

	session := SessionSummary{
		ID:        "missing-analysis",
		Agent:     "codex",
		Model:     "gpt-5",
		Path:      sessionPath,
		Timestamp: time.Now(),
	}

	_, ok, err := LoadCachedSessionAnalysis(session)
	if err != nil {
		t.Fatalf("LoadCachedSessionAnalysis: %v", err)
	}
	if ok {
		t.Fatal("expected cached analysis to be missing")
	}
}
