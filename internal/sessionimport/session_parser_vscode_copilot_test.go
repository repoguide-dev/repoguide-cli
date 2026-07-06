package sessionimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplayVSCodeChatLogAppliesPushAndSetPatches(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"kind":0,"v":{"sessionId":"abc","creationDate":1000,"requests":[]}}`,
		`{"kind":2,"k":["requests"],"v":{"requestId":"r1","message":{"text":"hello"},"timestamp":1001,"response":[]}}`,
		`{"kind":2,"k":["requests",0,"response"],"v":{"kind":"markdownContent","value":"hi there"}}`,
		`{"kind":1,"k":["requests",0,"modelId"],"v":"gpt-5"}`,
	}
	if err := os.WriteFile(logPath, []byte(joinLines(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := replayVSCodeChatLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	requests := vscodeRequests(state)
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	req := requests[0]
	if vscodeStr(req, "modelId") != "gpt-5" {
		t.Fatalf("expected modelId gpt-5, got %q", vscodeStr(req, "modelId"))
	}
	message, _ := req["message"].(map[string]any)
	if vscodeStr(message, "text") != "hello" {
		t.Fatalf("expected prompt text 'hello', got %q", vscodeStr(message, "text"))
	}
	response, _ := req["response"].([]any)
	if len(response) != 1 {
		t.Fatalf("expected 1 response part, got %d", len(response))
	}
	part := response[0].(map[string]any)
	if vscodeResponsePartText(part) != "hi there" {
		t.Fatalf("expected assistant text 'hi there', got %q", vscodeResponsePartText(part))
	}

	events, err := buildVSCodeCopilotSessionEvents(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "prompt" || events[1].Kind != "assistant_message" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
