package cmd

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/repoguide/repoguide-cli/internal"
)

func TestSingleAvailableAgent(t *testing.T) {
	t.Run("returns only populated agent", func(t *testing.T) {
		agent, ok := singleAvailableAgent(map[string]int{
			"codex":  3,
			"claude": 0,
		})
		if !ok {
			t.Fatal("expected single available agent")
		}
		if agent != "codex" {
			t.Fatalf("expected codex, got %q", agent)
		}
	})

	t.Run("rejects multiple populated agents", func(t *testing.T) {
		agent, ok := singleAvailableAgent(map[string]int{
			"codex":  3,
			"claude": 2,
		})
		if ok {
			t.Fatalf("expected no auto-selected agent, got %q", agent)
		}
	})

	t.Run("rejects empty counts", func(t *testing.T) {
		agent, ok := singleAvailableAgent(map[string]int{
			"codex":  0,
			"claude": 0,
		})
		if ok {
			t.Fatalf("expected no auto-selected agent, got %q", agent)
		}
	})
}

func TestSessionDetailLines(t *testing.T) {
	session := internal.SessionSummary{
		ID:              "session-123",
		Agent:           "codex",
		Name:            "Fix OAuth callback",
		Cwd:             "/tmp/repoguide/cli",
		RepoName:        "repoguide",
		RepoRelativeCwd: "cli",
		Model:           "gpt-5",
		UsedRepoGuide:   true,
		Timestamp:       time.Date(2026, 6, 18, 9, 30, 0, 0, time.FixedZone("CEST", 2*60*60)),
	}

	got := strings.Join(sessionDetailLines(session, false), "\n")

	for _, want := range []string{
		"Fix OAuth callback",
		"Agent: Codex",
		"RepoGuide: yes",
		"Repo: repoguide",
		"Cwd: /tmp/repoguide/cli",
		"Model: gpt-5",
		"ID: session-123",
		"Timestamp: 2026-06-18",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to contain %q", got, want)
		}
	}
}

func TestSessionLabelIncludesRepoGuideIndicator(t *testing.T) {
	label := sessionLabel(internal.SessionSummary{Name: "Fix OAuth callback", UsedRepoGuide: true})
	if label != "Fix OAuth callback [RepoGuide]" {
		t.Fatalf("expected RepoGuide indicator in label, got %q", label)
	}
}

func TestEmbeddedSessionsModelQClosesInsteadOfQuitting(t *testing.T) {
	model := newSessionsModel("codex", sessionsModelOptions{
		repoFilter: "/work/repo",
		embedded:   true,
	})
	model.view = viewList

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	next := updated.(sessionsModel)

	if !next.closed {
		t.Fatal("expected embedded sessions model to mark itself closed")
	}
	if cmd != nil {
		t.Fatal("expected no quit command for embedded sessions model")
	}
}

func TestSessionDetailQReturnsToList(t *testing.T) {
	model := newSessionsModel("codex", sessionsModelOptions{})
	model.view = viewDetail
	model.sessions = []internal.SessionSummary{{
		ID:        "session-123",
		Agent:     "codex",
		Name:      "Fix OAuth callback",
		Model:     "gpt-5",
		Timestamp: time.Now(),
	}}
	model.total = 1
	model.applyDetailViewport()
	model.refreshDetailContent()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	next := updated.(sessionsModel)

	if next.view != viewList {
		t.Fatalf("expected q from detail to return to list, got view %v", next.view)
	}
	if cmd != nil {
		t.Fatal("expected no quit command when leaving detail view")
	}
}

func TestRenderDetailPromptsForAnalysisWhenCacheMissing(t *testing.T) {
	model := newSessionDetailModel(internal.SessionSummary{
		ID:        "session-123",
		Agent:     "codex",
		Name:      "Fix OAuth callback",
		Model:     "gpt-5",
		Timestamp: time.Now(),
	})
	model.detailLoading = false

	view := model.renderDetail()
	if !strings.Contains(view, "No cached analysis. Press enter to analyze.") {
		t.Fatalf("expected missing-cache prompt in detail view, got %q", view)
	}
	if !strings.Contains(view, "enter analyze") {
		t.Fatalf("expected enter analyze hint in detail view, got %q", view)
	}
}

func TestRenderDetailShowsCachedAnalysis(t *testing.T) {
	model := newSessionDetailModel(internal.SessionSummary{
		ID:        "session-123",
		Agent:     "codex",
		Name:      "Fix OAuth callback",
		Model:     "gpt-5",
		Timestamp: time.Now(),
	})
	model.detailLoading = false
	model.analysisPath = "/tmp/analysis.json"
	model.analysis = &internal.SessionAnalysis{
		Metrics: internal.SessionMetrics{
			EventCount:            12,
			UserPromptCount:       2,
			AssistantMessageCount: 2,
			TurnCount:             2,
			ToolCallCount:         3,
			ReadFileCount:         1,
			EditedFileCount:       1,
			ReadFiles:             []string{"README.md"},
			EditedFiles:           []string{"cli/cmd/sessions.go"},
		},
	}
	model.refreshDetailContent()

	view := model.renderDetail()
	for _, want := range []string{
		"Analysis",
		"12 events",
		"Files read (1):",
		"README.md",
		"Files edited (1):",
		"sessions.go",
		"analysis cached",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in detail view, got %q", want, view)
		}
	}
}

func TestRenderDetailPreservesPromptDetailContent(t *testing.T) {
	model := newSessionDetailModel(internal.SessionSummary{
		ID:        "session-123",
		Agent:     "codex",
		Name:      "Fix OAuth callback",
		Model:     "gpt-5",
		Timestamp: time.Now(),
	})
	model.analysis = &internal.SessionAnalysis{
		Metrics: internal.SessionMetrics{
			PromptBlocks: []internal.PromptBlock{{
				Text:                 "inspect session parsing",
				FullText:             "inspect session parsing\nthen verify prompt detail rendering",
				ReadFiles:            []string{"cli/cmd/sessions.go"},
				ReadFileCountsBefore: map[string]int{"cli/cmd/sessions.go": 2},
			}},
		},
	}
	model.view = viewPromptDetail
	model.detailVP.SetContent(model.promptDetailContent())

	view := model.renderDetail()
	if !strings.Contains(view, "Prompt #1") {
		t.Fatalf("expected prompt detail heading in render, got %q", view)
	}
	if strings.Contains(view, "Agent: Codex") {
		t.Fatalf("expected prompt detail render to avoid resetting to session summary, got %q", view)
	}
}

func TestBuildPromptDetailContentShowsPromptCosts(t *testing.T) {
	session := internal.SessionSummary{
		RepoRoot: "/tmp/repoguide",
		Cwd:      "/tmp/repoguide/cli",
	}
	pricing := &internal.ModelPricing{
		InputPerMTokUSD:      1.25,
		OutputPerMTokUSD:     10.0,
		CacheReadPerMTokUSD:  0.125,
		CacheWritePerMTokUSD: 1.5,
	}
	block := internal.PromptBlock{
		Text:        "first do this then rework all tests",
		FullText:    "first do this then rework all tests",
		EditCount:   5,
		DurationSec: 312,
		ReadFiles:   []string{"README.md", "cli/cmd/sessions.go"},
		EditedFiles: []string{"cli/cmd/sessions.go"},
		ReadFileCountsBefore: map[string]int{
			"README.md": 8,
		},
		ReadFileCountsAfter: map[string]int{
			"cli/cmd/sessions.go": 1,
		},
		TokenUsage: &internal.TokenUsage{
			InputTokens:      1200,
			OutputTokens:     300,
			CacheReadTokens:  600,
			CacheWriteTokens: 150,
		},
	}

	view := buildPromptDetailContent(block, 2, pricing, session, true)

	for _, want := range []string{
		"Prompt #2",
		"Activity",
		"  Searches   0",
		"  Reads      9",
		"  Edits      5",
		"  Files      2",
		"Costs",
		"  Reads                  1k tokens",
		"  Cached reads           600 tokens",
		"  Output                 300 tokens",
		"  Write                  150 tokens",
		"  Estimated dollar cost  $0.0048",
		"  Duration               5m 12s",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in prompt detail, got %q", want, view)
		}
	}
}

func TestBuildPromptDetailContentOmitsCostsWithoutTokenUsage(t *testing.T) {
	view := buildPromptDetailContent(internal.PromptBlock{
		Text:                 "first do this then rework all tests",
		FullText:             "first do this then rework all tests",
		EditCount:            1,
		ReadFiles:            []string{"README.md"},
		EditedFiles:          []string{"README.md"},
		ReadFileCountsBefore: map[string]int{"README.md": 1},
	}, 1, nil, internal.SessionSummary{}, true)

	if strings.Contains(view, "Costs") {
		t.Fatalf("expected cost section to be omitted, got %q", view)
	}
}

func TestFormatSearchQueryMakesRegexAlternationReadable(t *testing.T) {
	for _, input := range []string{`table\.\|lipgloss\.`, `table\\.\\|lipgloss\\.`} {
		got := formatSearchQuery(input)
		if got != "table.  OR  lipgloss." {
			t.Fatalf("unexpected formatted query %q for %q", got, input)
		}
	}
}

func TestFileChainIncludesSearchBeforeReadAndEdit(t *testing.T) {
	got := fileChain("cli/cmd/sessions.go", []internal.PromptBlock{{
		Searches: []internal.SearchTrace{{
			Query:      "sessions",
			EditTarget: "cli/cmd/sessions.go",
		}},
		ReadFiles:   []string{"cli/cmd/sessions.go"},
		EditedFiles: []string{"cli/cmd/sessions.go"},
	}})
	if got != "search → read → edit" {
		t.Fatalf("unexpected file chain %q", got)
	}
}

func TestFormatFileListLineAlignsStatsColumn(t *testing.T) {
	first := formatFileListLine("playerapp/src/components/Dashboard.tsx", 24, "2 reads")
	second := formatFileListLine("playerapp/src/components/dashboard/usePostSubmissionFlow.ts", 24, "10 reads")

	firstIndex := strings.Index(first, "2 reads")
	secondIndex := strings.Index(second, "10 reads")
	if firstIndex == -1 || secondIndex == -1 {
		t.Fatalf("expected stats labels in both lines, got %q and %q", first, second)
	}
	if firstIndex != secondIndex {
		t.Fatalf("expected aligned stats columns, got %d and %d for %q / %q", firstIndex, secondIndex, first, second)
	}
}

func TestTruncateMiddlePreservesPathTail(t *testing.T) {
	got := truncateMiddle("playerapp/src/components/dashboard/usePostSubmissionFlow.ts", 24)
	if !strings.Contains(got, "...") {
		t.Fatalf("expected middle truncation marker, got %q", got)
	}
	if !strings.HasSuffix(got, "missionFlow.ts") {
		t.Fatalf("expected truncated path to preserve filename tail, got %q", got)
	}
}
