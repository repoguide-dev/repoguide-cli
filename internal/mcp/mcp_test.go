package mcp

import (
	"strings"
	"testing"
)

func TestAgentInstructionUsesUnderstandTask(t *testing.T) {
	instr := AgentInstructionFor("test-repo-id")
	for _, want := range []string{
		"repoguide_get_repo_experience",
		"test-repo-id",
		"compact selection of evidence-backed repository experience",
		"repoguide_get_full_topic_context",
	} {
		if !strings.Contains(instr, want) {
			t.Fatalf("expected AgentInstruction to contain %q", want)
		}
	}
}

func TestAgentInstructionBriefIncludesUnderstandTaskWithRepoID(t *testing.T) {
	instr := AgentInstructionBriefFor("test-repo-id")
	if !strings.Contains(instr, "repoguide_get_repo_experience") {
		t.Fatal("expected brief instruction to mention repoguide_get_repo_experience")
	}
	if !strings.Contains(instr, "test-repo-id") {
		t.Fatal("expected brief instruction to embed the repo ID")
	}
	if !strings.Contains(instr, "Do not repeat it on every user message or turn") {
		t.Fatal("expected brief instruction to forbid repeating RepoGuide on every message")
	}
}

func TestUnderstandTaskResponseExplainsStandaloneWorkflow(t *testing.T) {
	resp := UnderstandTaskResponse("test-repo-id")
	for _, want := range []string{
		"RepoGuide MCP workflow for this repository",
		"repoguide_get_repo_experience",
		"repoguide_list_topics",
		"repoguide_get_test_context",
		"repoguide_get_search_context",
		"repoguide_record_feedback",
		"optional accelerators",
		"not once per message or turn",
	} {
		if !strings.Contains(resp, want) {
			t.Fatalf("expected UnderstandTaskResponse to contain %q", want)
		}
	}
}

func TestAgentFeedbackInstructionRequestsTopicFilesAndCandidateRule(t *testing.T) {
	instr := AgentFeedbackInstructionFor("test-repo-id")
	for _, want := range []string{
		"stable advice IDs",
		"incorrect, unnecessary, or missing",
		"useful or misleading files",
		"exactly one reusable repository rule",
		"file anchors",
		"stored as a candidate",
	} {
		if !strings.Contains(instr, want) {
			t.Fatalf("expected feedback instruction to contain %q", want)
		}
	}
}
