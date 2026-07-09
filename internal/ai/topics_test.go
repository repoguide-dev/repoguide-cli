package ai

import (
	"strings"
	"testing"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
)

func TestParseTopicResultsRecoversPartialArray(t *testing.T) {
	raw := `[{"name":"CLI Auth Flow","summary":"s","confidence":0.9,"when_to_use":["a"],"prompt_keywords":["auth"]},`

	got, err := parseTopicResults(raw)
	if err == nil {
		t.Fatalf("expected partial parse error")
	}
	if len(got) != 1 {
		t.Fatalf("recovered %d items, want 1", len(got))
	}
	if got[0].Name != "CLI Auth Flow" {
		t.Fatalf("first name = %q, want %q", got[0].Name, "CLI Auth Flow")
	}
}

func TestParseTopicResultsEmptyResponse(t *testing.T) {
	_, err := parseTopicResults("")
	if err == nil {
		t.Fatalf("expected error for empty response")
	}
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("error = %q, want unexpected end of JSON input", err)
	}
}

func TestFallbackTopicResultBuildsUsableContext(t *testing.T) {
	candidate := topicCandidate{
		subsystem: contracts.RepoAnalysisSubsystem{
			Name:           "backend/server",
			Sessions:       4,
			TopFiles:       []string{"backend/server/repo_handlers.go", "backend/server/server.go"},
			Classification: []string{"high_context", "low_test_context"},
		},
		prompts:      []string{"fix repo analyze topic parsing for backend server routes"},
		tests:        []string{"backend/server/repos_test.go"},
		topReadFiles: []string{"backend/model/repo.go", "backend/server/repo_handlers.go"},
		seenWith:     []seenWithEntry{{File: "backend/db/db.go", Sessions: 2}},
		fileClassifications: map[string][]string{
			"backend/model/repo.go": {"context_tax"},
		},
		readBeforeEditHints: []string{"Read backend/model/repo.go before editing backend/server/repo_handlers.go"},
		testTouchSignal:     "tests used as spec",
	}

	got := fallbackTopicResult(candidate)
	if got.Name == "" {
		t.Fatalf("expected fallback name")
	}
	if len(got.ImportantFiles.EditTargets) == 0 {
		t.Fatalf("expected edit targets")
	}
	if got.Tests.Signal != "tests used as spec" {
		t.Fatalf("test signal = %q", got.Tests.Signal)
	}
	if len(got.StartHere) == 0 {
		t.Fatalf("expected start_here entries")
	}
	if len(got.RiskFlags) == 0 {
		t.Fatalf("expected risk flags")
	}
}

func TestMaterializeTopicContextsAllowsSubsetOfGroups(t *testing.T) {
	groupByID := map[string]topicCandidate{
		"backend_server": {
			subsystem: contracts.RepoAnalysisSubsystem{
				Sessions:    4,
				SourceEdits: 3,
				TestEdits:   1,
			},
			readFiles: 7,
		},
		"cli_cmd": {
			subsystem: contracts.RepoAnalysisSubsystem{
				Sessions:    2,
				SourceEdits: 1,
				TestEdits:   0,
			},
			readFiles: 2,
		},
	}

	results := []llmTopicResult{{
		GroupIDs:       []string{"backend_server"},
		Name:           "Repo Analysis Handlers",
		Summary:        "Covers repo-analysis API handler work.",
		Confidence:     0.82,
		WhenToUse:      []string{"Editing repo analysis endpoints"},
		PromptKeywords: []string{"repo analysis", "handlers"},
	}}

	topics := materializeTopicContexts(results, groupByID, []string{"backend_server", "cli_cmd"})
	if len(topics) != 1 {
		t.Fatalf("topic count = %d, want 1", len(topics))
	}
	if topics[0].Evidence.Sessions != 4 {
		t.Fatalf("sessions = %d, want 4", topics[0].Evidence.Sessions)
	}
	if topics[0].Evidence.EditedFiles != 4 {
		t.Fatalf("edited files = %d, want 4", topics[0].Evidence.EditedFiles)
	}
	if topics[0].Evidence.ReadFiles != 7 {
		t.Fatalf("read files = %d, want 7", topics[0].Evidence.ReadFiles)
	}
}

func TestMaterializeTopicContextsSkipsLowConfidenceTopics(t *testing.T) {
	groupByID := map[string]topicCandidate{
		"backend_server": {
			subsystem: contracts.RepoAnalysisSubsystem{Sessions: 3},
			readFiles: 4,
		},
	}

	topics := materializeTopicContexts([]llmTopicResult{{
		GroupIDs:       []string{"backend_server"},
		Name:           "Noisy Topic Guess",
		Summary:        "Weak evidence topic.",
		Confidence:     0.29,
		WhenToUse:      []string{"Maybe relevant"},
		PromptKeywords: []string{"noise"},
	}}, groupByID, []string{"backend_server"})

	if len(topics) != 0 {
		t.Fatalf("topic count = %d, want 0", len(topics))
	}
}

func TestBuildTopicPromptLetsEvidenceSetTopicCount(t *testing.T) {
	prompt := prompts.BuildTopicPrompt("[]", "")
	checks := []string{
		"Return as many topics as the evidence supports - do not target a topic count.",
		"Judge recurrence across the group's whole prompt list, never from one prompt in isolation",
		"Every topic object must include group_ids with one or more input group_id values.",
		"If you are unsure, omit the topic instead of inventing a generic one.",
		"Create topics per domain area",
		"Use the whole group/bundle context when selecting files for a topic.",
		"You may also split one input group into multiple topics when the prompts, files, or workflows show distinct recurring areas within the same directory.",
		"Prefer area-focused topics over layer buckets",
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}
