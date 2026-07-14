package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
)

const understandTaskModel = "claude-haiku-4-5-20251001"

const (
	strongTopicMatch       = 0.55
	plausibleSecondMatch   = 0.60
	ambiguousMatchDistance = 0.10
)

type TopicSummary = contracts.TopicSummary
type TopicMatch = contracts.TopicMatch
type TopicRoutingExample = contracts.TopicRoutingExample
type SelectTopicResult = contracts.SelectTopicResult

func uniqueWords(task string) int {
	seen := map[string]bool{}
	for _, word := range strings.Fields(strings.ToLower(task)) {
		word = strings.Trim(word, `.,;:!?"'`)
		if word != "" {
			seen[word] = true
		}
	}
	return len(seen)
}

// SelectTopic asks the LLM only for calibrated task-to-topic matching. Prior
// examples are admitted by feedback filters before this call and are relevance
// examples, not repository facts.
func SelectTopic(ctx context.Context, repoContext string, topics []TopicSummary, task string, sessionPrompts []string, positive, negative []TopicRoutingExample) (SelectTopicResult, Usage, error) {
	if uniqueWords(task) < 3 {
		return SelectTopicResult{Status: "needs_clarification", Reason: "task is too vague to route", Question: "What specific area, file, command, or behavior does your task involve?"}, Usage{}, nil
	}
	topicsJSON, _ := json.MarshalIndent(topics, "", "  ")
	userMsg := fmt.Sprintf("Repository context:\n%s\n\nAvailable topics:\n%s\n\nTask: %s", repoContext, string(topicsJSON), task)
	if len(positive) > 0 {
		data, _ := json.Marshal(positive)
		userMsg += "\n\nFeedback-qualified positive routing examples:\n" + string(data)
	}
	if len(negative) > 0 {
		data, _ := json.Marshal(negative)
		userMsg += "\n\nRejected routing examples:\n" + string(data)
	}
	if len(sessionPrompts) > 0 {
		userMsg += "\n\nRecent session context:\n" + strings.Join(sessionPrompts, "\n---\n")
	}
	raw, usage, err := callClaudeWithSystem(ctx, understandTaskModel, prompts.SelectTopicSystem, userMsg, 1024)
	if err != nil {
		return SelectTopicResult{}, usage, err
	}
	return parseSelectTopicResponse(raw, usage)
}

func parseSelectTopicResponse(raw string, usage Usage) (SelectTopicResult, Usage, error) {
	var result struct {
		Status            string       `json:"status"`
		TopicID           string       `json:"topic_id"`
		Confidence        float64      `json:"confidence"`
		Reason            string       `json:"reason"`
		Question          string       `json:"question"`
		CandidateTopics   []TopicMatch `json:"candidate_topics"`
		CandidateTopicIDs []string     `json:"candidate_topic_ids"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &result); err != nil {
		return SelectTopicResult{}, usage, fmt.Errorf("topic chooser returned non-JSON: %w", err)
	}
	matches := normalizeTopicMatches(result.TopicID, result.Confidence, result.CandidateTopics, result.CandidateTopicIDs)
	out := SelectTopicResult{
		TopicID: result.TopicID, Confidence: result.Confidence, Status: result.Status,
		Reason: result.Reason, Question: result.Question, CandidateTopics: matches,
	}
	for _, match := range matches {
		out.CandidateTopicIDs = append(out.CandidateTopicIDs, match.TopicID)
		if match.TopicID == out.TopicID {
			out.Confidence = match.Confidence
		}
	}
	if out.TopicID == "" || out.Confidence < strongTopicMatch {
		out.Status = "needs_clarification"
		out.TopicID = ""
		if out.Reason == "" {
			out.Reason = "no topic matches the task strongly enough"
		}
		return out, usage, nil
	}
	if len(matches) > 1 && matches[1].Confidence >= plausibleSecondMatch && matches[0].Confidence-matches[1].Confidence <= ambiguousMatchDistance {
		out.Reason = "multiple topics are relevant; using the strongest as the primary route"
	}
	out.Status = "ok"
	return out, usage, nil
}

func normalizeTopicMatches(topicID string, confidence float64, matches []TopicMatch, legacyIDs []string) []TopicMatch {
	byID := map[string]TopicMatch{}
	for _, match := range matches {
		match.TopicID = strings.TrimSpace(match.TopicID)
		if match.TopicID == "" {
			continue
		}
		match.Confidence = min(1, max(0, match.Confidence))
		if current, ok := byID[match.TopicID]; !ok || match.Confidence > current.Confidence {
			byID[match.TopicID] = match
		}
	}
	if topicID != "" {
		if current, ok := byID[topicID]; !ok || confidence > current.Confidence {
			byID[topicID] = TopicMatch{TopicID: topicID, Confidence: min(1, max(0, confidence))}
		}
	}
	for _, id := range legacyIDs {
		if _, ok := byID[id]; !ok && id != "" {
			byID[id] = TopicMatch{TopicID: id}
		}
	}
	out := make([]TopicMatch, 0, len(byID))
	for _, match := range byID {
		out = append(out, match)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return out[i].TopicID < out[j].TopicID
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}
