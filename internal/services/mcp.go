package services

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/experience"
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
	MatchConfidence   float64
	ContextText       string
	Explanation       string
	SelectedAdvice    []contracts.AdviceItem
	CandidateTopics   []contracts.TopicMatch
	CandidateTopicIDs []string
	Reason            string
	Question          string
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
// knownFiles, when set, restricts surfaced file paths to those the caller
// confirmed exist on its configured branch (see contracts.MCPUnderstandTaskRequest.KnownFiles).
func (s *MCPService) UnderstandTask(ctx context.Context, repoID, task, topicID string, sessionPrompts []string, knownFiles []string) (*UnderstandTaskOutput, error) {
	repoCtxEntry, _ := s.store.Topics().GetRepoContext(ctx, repoID)
	repoCtx := ""
	if repoCtxEntry != nil {
		repoCtx = repoCtxEntry.Content
	}

	topics, err := s.store.Topics().GetTopics(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("load topics: %w", err)
	}

	feedback, _ := s.store.Feedback().ListActionableFeedback(ctx, repoID)
	positiveRoutes, negativeRoutes := experience.BuildRoutingExamples(feedback)

	summaries := make([]ai.TopicSummary, 0, len(topics))
	for _, t := range topics {
		summaries = append(summaries, ai.TopicSummary{ID: t.ID, Name: t.Name, Summary: t.Summary, WhenToUse: t.WhenToUse, PromptKeywords: t.PromptKeywords})
	}

	if topicID != "" {
		chosen := findTopic(topics, topicID)
		if chosen == nil {
			return nil, fmt.Errorf("topic %q not found", topicID)
		}
		return s.buildTaskOutput(ctx, repoID, task, *chosen, 0, 1, feedback, "Task-to-topic match: "+chosen.Name+" (selected by caller)", knownFiles), nil
	}

	// First call: select topic.
	result, _, err := s.ai.SelectTopic(ctx, repoCtx, summaries, task, sessionPrompts, positiveRoutes, negativeRoutes)
	if err != nil {
		return nil, fmt.Errorf("AI topic select: %w", err)
	}
	if result.Status == "needs_clarification" || result.TopicID == "" {
		matches := nameTopicMatches(result.CandidateTopics, topics)
		return &UnderstandTaskOutput{
			Status:            "needs_clarification",
			CandidateTopics:   matches,
			CandidateTopicIDs: result.CandidateTopicIDs,
			Reason:            result.Reason,
			Question:          result.Question,
		}, nil
	}
	chosen := findTopic(topics, result.TopicID)
	if chosen == nil {
		return &UnderstandTaskOutput{Status: "needs_clarification"}, nil
	}
	matchCount := plausibleMatchCount(result.CandidateTopics)
	explanation := fmt.Sprintf("Task-to-topic match: %.0f%% %s", result.Confidence*100, chosen.Name)
	return s.buildTaskOutput(ctx, repoID, task, *chosen, result.Confidence, matchCount, feedback, explanation, knownFiles), nil
}

// ── Render helpers ────────────────────────────────────────────────────────────

func (s *MCPService) buildTaskOutput(ctx context.Context, repoID, task string, topic model.TopicContext, confidence float64, matchCount int, feedback []model.MCPFeedback, explanation string, knownFiles []string) *UnderstandTaskOutput {
	repoRoot := ""
	if repo, _ := s.store.Repos().Get(ctx, repoID); repo != nil {
		repoRoot = repo.RepoRoot
	}
	sessions, _ := s.store.Events().Get(ctx, repoID)
	// Filter the topic before building, not just the package after: the topic's
	// curated paths seed both the rendered start files and the file-overlap gate
	// in BuildTaskPackage, so a stale path (renamed, moved, or left behind by a
	// repo split) otherwise both misdirects the agent and blocks session matches.
	known := knownFilesSet(knownFiles)
	topic = experience.FilterTopicToKnownFiles(topic, known)
	pkg := experience.BuildTaskPackage(task, repoRoot, topic, sessions)
	pkg = experience.FilterTaskPackageFiles(pkg, known)
	pkg = experience.SetSelectionBudget(pkg, matchCount)
	topicFeedback := experience.FeedbackForTopic(topic.ID, feedback)
	pkg = experience.ApplyAdviceFeedback(pkg, topicFeedback)
	positive, negative := experience.BuildTopicRoutingExamples(topic.ID, feedback)
	if len(pkg.CandidateAdvice) > 0 {
		if selected, _, err := s.ai.SelectAdvice(ctx, task, topic, pkg.CandidateAdvice, pkg.Budget, positive, negative, topicFeedback); err == nil {
			pkg = experience.SelectAdvice(pkg, selected)
		}
	}
	return &UnderstandTaskOutput{
		Status: "ok", TopicID: topic.ID, MatchConfidence: confidence,
		ContextText: experience.RenderTaskPackage(topic, pkg), Explanation: explanation,
		SelectedAdvice: pkg.SelectedAdvice,
	}
}

func knownFilesSet(files []string) map[string]bool {
	if files == nil {
		return nil
	}
	set := make(map[string]bool, len(files))
	for _, f := range files {
		set[f] = true
	}
	return set
}

func findTopic(topics []model.TopicContext, topicID string) *model.TopicContext {
	for i := range topics {
		if topics[i].ID == topicID {
			return &topics[i]
		}
	}
	return nil
}

func nameTopicMatches(matches []contracts.TopicMatch, topics []model.TopicContext) []contracts.TopicMatch {
	byID := make(map[string]string, len(topics))
	for _, topic := range topics {
		byID[topic.ID] = topic.Name
	}
	for i := range matches {
		matches[i].Name = byID[matches[i].TopicID]
	}
	return matches
}

func plausibleMatchCount(matches []contracts.TopicMatch) int {
	count := 0
	for _, match := range matches {
		if match.Confidence >= 0.60 {
			count++
		}
	}
	return max(1, count)
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
			fmt.Fprintf(&sb, "- %s\n", n.Text)
		}
	}
	if len(t.Tests.Commands) > 0 {
		sb.WriteString("\nObserved validation commands:\n")
		for _, command := range t.Tests.Commands {
			fmt.Fprintf(&sb, "- %s\n", command)
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
