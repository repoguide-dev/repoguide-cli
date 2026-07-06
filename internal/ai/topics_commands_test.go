package ai

import (
	"strings"
	"testing"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

func TestBuildCandidatesAggregatesObservedCommands(t *testing.T) {
	bundle := contracts.RepoAnalysisBundle{
		Sessions: []contracts.RepoAnalysisSession{
			{
				ID:        "s1",
				Timestamp: "2026-06-21T10:00:00Z",
				Prompts:   []string{"add plan limit check"},
				Commands: []contracts.RepoAnalysisCommand{
					{Text: "go test ./backend/server/...", Runs: 2, Failures: 1},
				},
			},
			{
				ID:        "s2",
				Timestamp: "2026-06-20T10:00:00Z",
				Prompts:   []string{"fix handler auth"},
				Commands: []contracts.RepoAnalysisCommand{
					{Text: "go test ./backend/server/...", Runs: 1},
					{Text: "docker compose up --build", Runs: 1, Failures: 1},
				},
			},
		},
		Subsystems: []contracts.RepoAnalysisSubsystem{
			{
				Name:     "backend/server",
				Sessions: 2,
				RelatedSessions: []contracts.RepoAnalysisSessionRef{
					{ID: "s1", Timestamp: "2026-06-21T10:00:00Z"},
					{ID: "s2", Timestamp: "2026-06-20T10:00:00Z"},
				},
			},
		},
	}

	candidates := buildCandidates(bundle)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]

	if c.lastActive != "2026-06-21" {
		t.Fatalf("lastActive = %q, want most recent session date", c.lastActive)
	}
	if len(c.commands) != 2 {
		t.Fatalf("expected 2 aggregated commands, got %#v", c.commands)
	}
	// Runs are summed across sessions; most-run first.
	if c.commands[0].Text != "go test ./backend/server/..." || c.commands[0].Runs != 3 || c.commands[0].Failures != 1 {
		t.Fatalf("aggregated test command wrong: %#v", c.commands[0])
	}
}

func TestObservedTestCommandsOnlyFromEvidence(t *testing.T) {
	got := observedTestCommands([]commandEntry{
		{Text: "go test ./backend/...", Runs: 3},
		{Text: "docker compose up --build", Runs: 2},
		{Text: "npm test", Runs: 1},
	})
	if len(got) != 2 {
		t.Fatalf("expected only test-like commands, got %#v", got)
	}
	if got[0] != "go test ./backend/..." || got[1] != "npm test" {
		t.Fatalf("unexpected test commands: %#v", got)
	}
}

func TestBuildFeedbackSessionSummaryCapturesCommandsAndFailures(t *testing.T) {
	events := []model.SessionEvent{
		{Kind: "prompt", Text: "wire the new parser"},
		{Kind: "tool_call", ToolName: "Bash", ToolCallID: "t1", CommandText: "go test ./internal/sessionimport/..."},
		{Kind: "tool_result", ToolCallID: "t1", IsError: true},
		{Kind: "tool_call", ToolName: "Bash", ToolCallID: "t2", CommandText: "go test ./internal/sessionimport/..."},
		{Kind: "tool_result", ToolCallID: "t2"},
	}
	summary := buildFeedbackSessionSummary(events, contracts.RepoAnalysisBundle{})

	if len(summary.Commands) != 1 || summary.Commands[0] != "go test ./internal/sessionimport/..." {
		t.Fatalf("commands = %#v", summary.Commands)
	}
	if len(summary.FailedCommands) != 1 || summary.FailedCommands[0] != "go test ./internal/sessionimport/..." {
		t.Fatalf("failed commands = %#v", summary.FailedCommands)
	}
}

// TestTopicPromptDemandsEvidenceBasedOutput pins the prompt rules that make
// topic generation evidence-based rather than generic. It fails if the prompt
// regresses to inventing commands, ignoring recency, or ignoring corrections.
func TestTopicPromptDemandsEvidenceBasedOutput(t *testing.T) {
	prompt := prompts.BuildTopicPrompt("[]", "repo ctx")
	wants := []string{
		// commands must come from observation, never invention
		"only commands copied verbatim from the commands input",
		"Never invent commands.",
		// failures and corrections are first-class evidence
		"commands with failures (name the command and that it failed here)",
		"treat corrections as strong evidence",
		// recency and stale-path handling
		"last_active",
		"omit the file rather than emitting a dead path",
		"Prefer evidence from recent sessions",
	}
	for _, want := range wants {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "always empty array") {
		t.Fatalf("prompt still forbids observed test commands")
	}
}

func TestBuildTopicCurationSessionSummarizesEvidence(t *testing.T) {
	events := []model.SessionEvent{
		{Kind: "prompt", Text: "add retry to the webhook handler"},
		{Kind: "tool_call", ToolName: "Read", ReadPaths: []string{"backend/server/webhooks.go"}},
		{Kind: "tool_call", ToolName: "Bash", ToolCallID: "t1", CommandText: "go test ./backend/server/..."},
		{Kind: "tool_result", ToolCallID: "t1", IsError: true},
		{Kind: "prompt", Text: "no - use the existing retry helper in backend/util"},
		{Kind: "tool_call", ToolName: "Edit", CommandText: "git diff -- backend/server/webhooks.go\ndiff --git a/backend/server/webhooks.go b/backend/server/webhooks.go\n@@\n-old\n+new", WritePaths: []string{"backend/server/webhooks.go"}},
	}
	sess := BuildTopicCurationSession("fb1", "s1", events)

	if sess.FeedbackID != "fb1" || sess.SessionID != "s1" {
		t.Fatalf("ids not carried: %#v", sess)
	}
	if len(sess.Prompts) != 2 || !strings.HasPrefix(sess.Prompts[1], "no - use the existing retry helper") {
		t.Fatalf("correction prompt missing: %#v", sess.Prompts)
	}
	if len(sess.EditedFiles) != 1 || sess.EditedFiles[0] != "backend/server/webhooks.go" {
		t.Fatalf("edited files: %#v", sess.EditedFiles)
	}
	if len(sess.FailedCommands) != 1 || sess.FailedCommands[0] != "go test ./backend/server/..." {
		t.Fatalf("failed commands: %#v", sess.FailedCommands)
	}
	if len(sess.DiffSnippets) != 1 || !strings.Contains(sess.DiffSnippets[0], "diff --git a/backend/server/webhooks.go") {
		t.Fatalf("diff snippets: %#v", sess.DiffSnippets)
	}
}

// TestTopicCuratorPromptDemandsSessionEvidence pins the curator rules that keep
// suggestions grounded in observed session data.
func TestTopicCuratorPromptDemandsSessionEvidence(t *testing.T) {
	wants := []string{
		"failed_commands",
		"never invent paths",
		"corrects the agent",
		"Commands come only from session data",
		"Use diff snippets as supporting evidence",
	}
	for _, want := range wants {
		if !strings.Contains(prompts.TopicCuratorSystem, want) {
			t.Fatalf("curator prompt missing %q", want)
		}
	}
}
