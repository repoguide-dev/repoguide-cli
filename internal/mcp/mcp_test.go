package mcp

import (
	"os"
	"path/filepath"
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

func TestAgentFeedbackInstructionAsksUnlessAutoFeedback(t *testing.T) {
	instr := AgentFeedbackInstructionFor("test-repo-id", false)
	for _, want := range []string{
		"ask the user whether they'd like to send RepoGuide feedback",
		"repoguide_record_feedback",
	} {
		if !strings.Contains(instr, want) {
			t.Fatalf("expected feedback instruction to contain %q", want)
		}
	}

	auto := AgentFeedbackInstructionFor("test-repo-id", true)
	for _, want := range []string{
		"Do not ask the user first",
		"repoguide_record_feedback",
	} {
		if !strings.Contains(auto, want) {
			t.Fatalf("expected auto feedback instruction to contain %q", want)
		}
	}
	// The detailed advice_evaluation/candidate_rule guidance now lives on the
	// repoguide_record_feedback tool description (see mcp_server_test.go),
	// not in this hook-facing instruction text.
}

func TestInstructRepoRemovesLegacyMandatoryFeedbackInstruction(t *testing.T) {
	repo := initTestRepo(t, t.TempDir(), "repo_one")
	path := filepath.Join(repo, "AGENTS.md")
	legacy := "# Project\n\n" + AgentFeedbackInstructionFor("repo_one", false)
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := InstructRepo(repo); err != nil {
		t.Fatalf("InstructRepo: %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(updated), "repoguide:feedback-instruction") {
		t.Fatalf("legacy feedback instruction was not removed: %s", updated)
	}
}
