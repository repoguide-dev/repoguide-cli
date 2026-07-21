package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	contracts "github.com/repoguide/repoguide-core/contracts/v1"
)

func TestHandleMCPRequestListsTools(t *testing.T) {
	resp, ok := handleMCPRequest(mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	}, &CloudClient{})
	if !ok {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	tools, ok := result["tools"].([]mcpTool)
	if !ok {
		t.Fatalf("tools type = %T", result["tools"])
	}
	wantNames := []string{
		"repoguide_list_topics",
		"repoguide_get_full_topic_context",
		"repoguide_get_test_context",
		"repoguide_get_search_context",
		"repoguide_get_repo_experience",
		"repoguide_record_feedback",
	}
	if len(tools) != len(wantNames) {
		t.Fatalf("tool count = %d, want %d; got: %+v", len(tools), len(wantNames), tools)
	}
	for i, want := range wantNames {
		if tools[i].Name != want {
			t.Fatalf("tools[%d].Name = %q, want %q", i, tools[i].Name, want)
		}
	}
	if !strings.Contains(tools[0].Description, "repoguide_get_repo_experience") {
		t.Fatalf("expected repoguide_list_topics description to point back to repoguide_get_repo_experience, got %q", tools[0].Description)
	}
	if !strings.Contains(tools[4].Description, "Primary entry point") {
		t.Fatalf("expected repoguide_get_repo_experience description to advertise the compact bootstrap flow, got %q", tools[4].Description)
	}
	if !strings.Contains(tools[4].Description, "Full topic/test/search context is opt-in") {
		t.Fatalf("expected repoguide_get_repo_experience description to keep full context opt-in, got %q", tools[4].Description)
	}
	if !strings.Contains(tools[5].Description, "user explicitly requests") || !strings.Contains(tools[5].Description, "Never retry") {
		t.Fatalf("expected feedback tool to describe explicit consent and denial handling, got %q", tools[5].Description)
	}
	for _, want := range []string{
		"stable advice IDs",
		"right, wrong, unneeded, or absent",
		"exactly one reusable repository rule",
		"file anchors",
		"stored as a candidate",
	} {
		if !strings.Contains(tools[5].Description, want) {
			t.Fatalf("expected feedback tool description to contain %q, got %q", want, tools[5].Description)
		}
	}
	if required, ok := tools[0].InputSchema["required"]; ok && len(required.([]string)) > 0 {
		t.Fatalf("expected repoguide_list_topics to allow empty arguments, got required=%v", required)
	}
	feedbackSchema := tools[5].InputSchema
	required, _ := feedbackSchema["required"].([]string)
	for _, want := range []string{"advice_evaluation", "candidate_rule"} {
		if !slices.Contains(required, want) {
			t.Fatalf("feedback schema required=%v, want %q", required, want)
		}
	}
	properties := feedbackSchema["properties"].(map[string]any)
	if _, ok := properties["candidate_rule"]; !ok {
		t.Fatal("feedback schema must expose candidate_rule")
	}
	candidateRule := properties["candidate_rule"].(map[string]any)
	candidateProperties := candidateRule["properties"].(map[string]any)
	confidence := candidateProperties["confidence"].(map[string]any)
	if got := confidence["type"]; got != "number" {
		t.Fatalf("candidate rule confidence type = %v, want number", got)
	}
}

func TestDecodeRecordFeedbackAcceptsFractionalCandidateConfidence(t *testing.T) {
	var input repoguideRecordFeedbackInput
	err := decodeToolArguments(map[string]any{
		"repo_id": "repo",
		"stars":   5,
		"candidate_rule": map[string]any{
			"rule":             "Run the focused test first.",
			"applies_when":     "Changing the parser.",
			"evidence":         "The focused test caught the regression.",
			"exceptions":       "None.",
			"confidence":       0.98,
			"expected_benefit": "Faster feedback.",
			"anchor_files":     []string{"parser_test.go"},
			"scope":            map[string]any{},
		},
	}, &input)
	if err != nil {
		t.Fatalf("decode fractional candidate confidence: %v", err)
	}
	if input.CandidateRule == nil || input.CandidateRule.Confidence != 0.98 {
		t.Fatalf("candidate rule confidence = %#v, want 0.98", input.CandidateRule)
	}
}

func TestCallMCPToolListTopicsReturnsEmptyWithoutBackend(t *testing.T) {
	topicsResult, _, err := callMCPTool("repoguide_list_topics", map[string]any{
		"task":    "mock task",
		"repo_id": "test-repo",
	}, &CloudClient{})
	if err != nil {
		t.Fatalf("list topics error: %v", err)
	}
	topics, ok := topicsResult["topics"].([]repoguideTopic)
	if !ok {
		t.Fatalf("topics type = %T", topicsResult["topics"])
	}
	if len(topics) != 0 {
		t.Fatalf("expected 0 topics without backend, got %d", len(topics))
	}
}

func TestCallMCPToolListTopicsAllowsEmptyArguments(t *testing.T) {
	repoRoot := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repo root: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	_, _, err = callMCPTool("repoguide_list_topics", nil, &CloudClient{})
	if err != nil {
		t.Fatalf("list topics with no args error: %v", err)
	}
}

func TestRunMCPServerInitialize(t *testing.T) {
	input := buildMCPMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	var out bytes.Buffer
	if err := RunMCPServer(bytes.NewReader(input), &out, "", ""); err != nil {
		t.Fatalf("RunMCPServer returned error: %v", err)
	}

	body := parseMCPResponseBody(t, out.Bytes())
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Fatalf("jsonrpc = %v, want 2.0", resp["jsonrpc"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp["result"])
	}
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %s", result["protocolVersion"], mcpProtocolVersion)
	}
}

func TestCallMCPToolUnderstandTaskFallsBackWithoutBackend(t *testing.T) {
	result, _, err := callMCPTool("repoguide_get_repo_experience", map[string]any{
		"task":    "add a new CLI command",
		"repo_id": "test-repo",
	}, &CloudClient{})
	if err != nil {
		t.Fatalf("understand task error: %v", err)
	}
	text, ok := result["text"].(string)
	if !ok || text == "" {
		t.Fatalf("expected non-empty text, got %T: %v", result["text"], result["text"])
	}
	if !strings.Contains(text, "repoguide_get_repo_experience") {
		t.Fatalf("expected repoguide_get_repo_experience in fallback response, got: %s", text)
	}
	if !strings.Contains(text, "stand on their own") {
		t.Fatalf("expected fallback response to explain MCP-only discoverability, got: %s", text)
	}
}

func TestCallMCPToolUnderstandTaskPreservesClarification(t *testing.T) {
	var understandCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/repos/test-repo/mcp/understand-task":
			understandCalls++
			var req contracts.MCPUnderstandTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode understand-task request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			if req.TopicID != "" {
				t.Fatalf("understand-task topic_id = %q, want empty", req.TopicID)
			}
			_ = json.NewEncoder(w).Encode(contracts.MCPUnderstandTaskResult{
				Status: "needs_clarification",
				CandidateTopics: []contracts.TopicMatch{
					{TopicID: "t1", Name: "One", Confidence: 0.72},
					{TopicID: "t2", Name: "Two", Confidence: 0.68},
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	result, _, err := callMCPTool("repoguide_get_repo_experience", map[string]any{
		"task":    "add a new CLI command",
		"repo_id": "test-repo",
	}, &CloudClient{BaseURL: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("understand task error: %v", err)
	}
	text, ok := result["text"].(string)
	if !ok || text == "" {
		t.Fatalf("expected non-empty text, got %T: %v", result["text"], result["text"])
	}
	if !strings.Contains(text, "72% One") || !strings.Contains(text, "68% Two") {
		t.Fatalf("expected calibrated candidate percentages, got: %s", text)
	}
	if !strings.Contains(text, "Which topic should RepoGuide use?") {
		t.Fatalf("expected clarification question, got: %s", text)
	}
	if understandCalls != 1 {
		t.Fatalf("understand-task call count = %d, want 1", understandCalls)
	}
}

func TestSelectCandidateTopicsUsesRankedIDsAndLimit(t *testing.T) {
	topics := []contracts.MCPTopicSummary{
		{ID: "t1", Name: "One", Summary: "first"},
		{ID: "t2", Name: "Two", Summary: "second"},
		{ID: "t3", Name: "Three", Summary: "third"},
		{ID: "t4", Name: "Four", Summary: "fourth"},
		{ID: "t5", Name: "Five", Summary: "fifth"},
		{ID: "t6", Name: "Six", Summary: "sixth"},
	}

	got := selectCandidateTopics(topics, []string{"t4", "missing", "t2", "t4", "t6", "t1"}, 5)
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	wantIDs := []string{"t4", "t2", "t6", "t1"}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Fatalf("got[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

func TestSelectCandidateTopicsFallsBackToFirstFive(t *testing.T) {
	topics := []contracts.MCPTopicSummary{
		{ID: "t1", Name: "One", Summary: "first"},
		{ID: "t2", Name: "Two", Summary: "second"},
		{ID: "t3", Name: "Three", Summary: "third"},
		{ID: "t4", Name: "Four", Summary: "fourth"},
		{ID: "t5", Name: "Five", Summary: "fifth"},
		{ID: "t6", Name: "Six", Summary: "sixth"},
	}

	got := selectCandidateTopics(topics, nil, 5)
	if len(got) != 5 {
		t.Fatalf("len(got) = %d, want 5", len(got))
	}
	if got[4].ID != "t5" {
		t.Fatalf("got[4].ID = %q, want t5", got[4].ID)
	}
}

func parseMCPResponseBody(t *testing.T, payload []byte) []byte {
	t.Helper()
	parts := bytes.SplitN(payload, []byte("\r\n\r\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("invalid framed response: %q", string(payload))
	}
	return parts[1]
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
