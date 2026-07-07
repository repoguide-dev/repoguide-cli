package services

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

// MCPService implements the business logic for every MCP tool. HTTP handlers and
// stdio MCP servers are thin wrappers that call these methods.
type MCPService struct {
	store storecontract.Store
	ai    ai.LLM
}

// ── Tool outputs ──────────────────────────────────────────────────────────────

type UnderstandTaskOutput struct {
	// "ok" | "needs_clarification"
	Status            string
	TopicID           string
	ContextText       string // formatted topic context ready to show the agent
	Explanation       string // preamble before context
	CandidateTopicIDs []string
}

// ── Tool implementations ──────────────────────────────────────────────────────

// ListTopics returns candidate topics for a repo.
func (s *MCPService) ListTopics(ctx context.Context, repoID string) ([]model.TopicContext, error) {
	return s.store.Topics().GetTopics(ctx, repoID)
}

// GetTopicContext returns full topic context (bootstrap + tests + files).
func (s *MCPService) GetTopicContext(ctx context.Context, repoID, topicID string) (*model.TopicContext, error) {
	return s.store.Topics().GetTopic(ctx, repoID, topicID)
}

// GetFileContext returns file-level classification and related files.
func (s *MCPService) GetFileContext(ctx context.Context, repoID, path string) (*model.FileSummary, error) {
	return s.store.Topics().GetFile(ctx, repoID, path)
}

// GetSearchContext returns search guidance for a repo/topic.
func (s *MCPService) GetSearchContext(ctx context.Context, repoID, topicID string) (*model.BundleSearchData, error) {
	return s.store.Topics().GetSearchData(ctx, repoID)
}

// UnderstandTask routes a task description to the best topic using AI.
func (s *MCPService) UnderstandTask(ctx context.Context, repoID, task, topicID string, sessionPrompts []string) (*UnderstandTaskOutput, error) {
	repoCtxEntry, _ := s.store.Topics().GetRepoContext(ctx, repoID)
	repoCtx := ""
	if repoCtxEntry != nil {
		repoCtx = repoCtxEntry.Content
	}

	topics, err := s.store.Topics().GetTopics(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("load topics: %w", err)
	}

	summaries := make([]ai.TopicSummary, 0, len(topics))
	for _, t := range topics {
		summaries = append(summaries, ai.TopicSummary{ID: t.ID, Name: t.Name, Summary: t.Summary})
	}

	// Second call: topicID already chosen - write the orientation hint.
	if topicID != "" {
		var chosen *model.TopicContext
		for i := range topics {
			if topics[i].ID == topicID {
				chosen = &topics[i]
				break
			}
		}
		if chosen == nil {
			return nil, fmt.Errorf("topic %q not found", topicID)
		}
		hint, found, _, err := s.ai.WriteOrientationHint(ctx, repoCtx, *chosen, task, sessionPrompts, nil)
		if err != nil {
			return nil, fmt.Errorf("AI hint: %w", err)
		}
		if !found {
			hint = compactTopicHint(*chosen)
		}
		return &UnderstandTaskOutput{
			Status:      "ok",
			TopicID:     topicID,
			ContextText: renderTopicContextText(*chosen),
			Explanation: hint,
		}, nil
	}

	// First call: select topic.
	result, _, err := s.ai.SelectTopic(ctx, repoCtx, summaries, task, sessionPrompts)
	if err != nil {
		return nil, fmt.Errorf("AI topic select: %w", err)
	}
	if result.Status == "needs_clarification" || result.TopicID == "" {
		return &UnderstandTaskOutput{
			Status:            "needs_clarification",
			CandidateTopicIDs: result.CandidateTopicIDs,
		}, nil
	}
	var chosen *model.TopicContext
	for i := range topics {
		if topics[i].ID == result.TopicID {
			chosen = &topics[i]
			break
		}
	}
	if chosen == nil {
		return &UnderstandTaskOutput{Status: "needs_clarification"}, nil
	}
	hint, found, _, _ := s.ai.WriteOrientationHint(ctx, repoCtx, *chosen, task, sessionPrompts, nil)
	if !found {
		hint = compactTopicHint(*chosen)
	}
	return &UnderstandTaskOutput{
		Status:      "ok",
		TopicID:     result.TopicID,
		ContextText: renderTopicContextText(*chosen),
		Explanation: hint,
	}, nil
}

// ── Render helpers ────────────────────────────────────────────────────────────

func renderTopicContextText(t model.TopicContext) string {
	return ai.RenderTopicContextText(t)
}

func compactTopicHint(t model.TopicContext) string {
	return ai.CompactTopicHint(t)
}

// RenderTestContext produces test guidance text for the MCP test_context tool.
func RenderTestContext(t *model.TopicContext, files []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Test context for topic: %s.\n", t.Name)
	if t.Tests.Signal != "" {
		fmt.Fprintf(&sb, "Signal: %s.\n", t.Tests.Signal)
	}
	if len(t.Tests.StartWith) > 0 {
		sb.WriteString("\nStart with:\n")
		for _, f := range t.Tests.StartWith {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}
	if len(t.Tests.Notes) > 0 {
		sb.WriteString("\nNotes:\n")
		for _, n := range t.Tests.Notes {
			fmt.Fprintf(&sb, "- %s\n", n)
		}
	}
	if len(files) > 0 && len(t.ImportantFiles.TestFiles) > 0 {
		perFile := map[string][]string{}
		for _, f := range files {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
			for _, tf := range t.ImportantFiles.TestFiles {
				if strings.Contains(strings.ToLower(tf), base) {
					perFile[f] = append(perFile[f], tf)
				}
			}
		}
		if len(perFile) > 0 {
			sb.WriteString("\nRelated tests by file:\n")
			for f, tests := range perFile {
				fmt.Fprintf(&sb, "- %s → %s\n", f, strings.Join(tests, ", "))
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// RenderFileContext produces file context text for the MCP file_context tool.
func RenderFileContext(fc *model.FileSummary) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "File context: %s.\n", fc.Path)
	if len(fc.Classification) > 0 {
		fmt.Fprintf(&sb, "Classification: %s.\n", strings.Join(fc.Classification, ", "))
	}
	if len(fc.ReadBeforeEditOf) > 0 {
		sb.WriteString("\nRead before editing:\n")
		for _, f := range fc.ReadBeforeEditOf {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}
	if len(fc.RelatedTests) > 0 {
		sb.WriteString("\nRelated tests:\n")
		for _, t := range fc.RelatedTests {
			fmt.Fprintf(&sb, "- %s\n", t)
		}
	}
	if len(fc.CoEditedWith) > 0 {
		sb.WriteString("\nCommonly edited with:\n")
		for _, f := range fc.CoEditedWith {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

var reQueryMeta = regexp.MustCompile(`[\\^$\[\]{}]`)

// RenderSearchContext produces search guidance text for the MCP search_context tool.
func RenderSearchContext(topicID string, sc *model.BundleSearchData) string {
	var sb strings.Builder
	if topicID != "" {
		fmt.Fprintf(&sb, "Search context for topic: %s\n", topicID)
	} else {
		sb.WriteString("Search context.\n")
	}
	if len(sc.SearchHeavyTargets) > 0 {
		sb.WriteString("\nHigh-value search targets:\n")
		for _, t := range sc.SearchHeavyTargets {
			fmt.Fprintf(&sb, "- %s\n", t.Path)
			if len(t.TopQueries) > 0 {
				sanitized := make([]string, 0, len(t.TopQueries))
				for _, q := range t.TopQueries {
					sanitized = append(sanitized, reQueryMeta.ReplaceAllString(q, ""))
				}
				fmt.Fprintf(&sb, "  Queries: %s\n", strings.Join(sanitized, ", "))
			}
		}
	}
	if len(sc.AmbiguousSearches) > 0 {
		sb.WriteString("\nAvoid broad queries:\n")
		limit := min(5, len(sc.AmbiguousSearches))
		for _, a := range sc.AmbiguousSearches[:limit] {
			fmt.Fprintf(&sb, "- %s\n", a.Query)
		}
	}
	if len(sc.SearchHeavyTargets) == 0 && len(sc.AmbiguousSearches) == 0 {
		sb.WriteString("No search friction patterns recorded.\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
