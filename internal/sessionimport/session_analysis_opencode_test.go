package sessionimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildSessionArtifactsOpencode(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	storageDir := filepath.Join(homeDir, ".local", "share", "opencode", "storage")
	sessionPath := filepath.Join(storageDir, "session", "proj_1", "ses_1.json")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	sessionJSON := `{"id":"ses_1","title":"inspect repo","directory":"/repo","cost":0.02,"model":{"id":"claude-sonnet-4-5","providerID":"anthropic"},"time":{"created":1767312000000,"updated":1767312005000}}`
	if err := os.WriteFile(sessionPath, []byte(sessionJSON), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	userMsgDir := filepath.Join(storageDir, "message", "ses_1")
	if err := os.MkdirAll(userMsgDir, 0o755); err != nil {
		t.Fatalf("mkdir message dir: %v", err)
	}
	userMsgPath := filepath.Join(userMsgDir, "msg_1.json")
	if err := os.WriteFile(userMsgPath, []byte(`{"id":"msg_1","sessionID":"ses_1","role":"user","time":{"created":1767312000000}}`), 0o644); err != nil {
		t.Fatalf("write user message: %v", err)
	}
	assistantMsgPath := filepath.Join(userMsgDir, "msg_2.json")
	assistantJSON := `{"id":"msg_2","sessionID":"ses_1","role":"assistant","modelID":"claude-sonnet-4-5","providerID":"anthropic","time":{"created":1767312001000},"tokens":{"input":100,"output":20,"reasoning":0,"cache":{"read":10,"write":5}}}`
	if err := os.WriteFile(assistantMsgPath, []byte(assistantJSON), 0o644); err != nil {
		t.Fatalf("write assistant message: %v", err)
	}

	userPartDir := filepath.Join(storageDir, "part", "msg_1")
	if err := os.MkdirAll(userPartDir, 0o755); err != nil {
		t.Fatalf("mkdir user part dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userPartDir, "prt_1.json"), []byte(`{"type":"text","text":"inspect repo"}`), 0o644); err != nil {
		t.Fatalf("write user part: %v", err)
	}

	assistantPartDir := filepath.Join(storageDir, "part", "msg_2")
	if err := os.MkdirAll(assistantPartDir, 0o755); err != nil {
		t.Fatalf("mkdir assistant part dir: %v", err)
	}
	toolPart := `{"type":"tool","tool":"read","callID":"call_1","state":{"status":"completed","input":{"filePath":"README.md"},"output":"file contents"}}`
	if err := os.WriteFile(filepath.Join(assistantPartDir, "prt_1.json"), []byte(toolPart), 0o644); err != nil {
		t.Fatalf("write tool part: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assistantPartDir, "prt_2.json"), []byte(`{"type":"text","text":"done"}`), 0o644); err != nil {
		t.Fatalf("write assistant text part: %v", err)
	}

	session := SessionSummary{
		ID:        "ses_1",
		Agent:     "opencode",
		Model:     "claude-sonnet-4-5",
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
	if artifacts.Analysis.Metrics.ToolCallCount != 1 {
		t.Fatalf("expected 1 tool call, got %d", artifacts.Analysis.Metrics.ToolCallCount)
	}
	if artifacts.Analysis.Metrics.ReadFileCount != 1 || artifacts.Analysis.Metrics.ReadFiles[0] != "README.md" {
		t.Fatalf("unexpected read files: %#v", artifacts.Analysis.Metrics.ReadFiles)
	}
	if artifacts.Analysis.Metrics.TokenUsage == nil || artifacts.Analysis.Metrics.TokenUsage.InputTokens != 100 {
		t.Fatalf("unexpected token usage: %#v", artifacts.Analysis.Metrics.TokenUsage)
	}

	summary, err := readOpencodeSession(sessionPath)
	if err != nil {
		t.Fatalf("readOpencodeSession: %v", err)
	}
	if summary.Name != "inspect repo" || summary.Cwd != "/repo" || summary.Model != "claude-sonnet-4-5" {
		t.Fatalf("unexpected session summary: %#v", summary)
	}
}
