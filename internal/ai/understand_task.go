package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

const understandTaskModel = "claude-haiku-4-5-20251001"

type TopicSummary = contracts.TopicSummary

type SelectTopicResult = contracts.SelectTopicResult

// SelectTopic calls Claude Haiku to pick the single best topic id for the task,
// or return a needs_clarification JSON object when the task is too ambiguous.
// System prompt is static (cached); variable content goes in the user message.
func uniqueWords(task string) int {
	seen := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(task)) {
		w = strings.Trim(w, `.,;:!?"'`)
		if w != "" {
			seen[w] = true
		}
	}
	return len(seen)
}

func SelectTopic(ctx context.Context, repoContext string, topics []TopicSummary, task string, sessionPrompts []string) (SelectTopicResult, Usage, error) {
	if uniqueWords(task) < 3 {
		return SelectTopicResult{
			Status:   "needs_clarification",
			Reason:   "task is too vague to route",
			Question: "What specific area, file, command, or behavior does your task involve?",
		}, Usage{}, nil
	}
	topicsJSON, _ := json.MarshalIndent(topics, "", "  ")
	userMsg := fmt.Sprintf("Repository context:\n%s\n\nAvailable topics:\n%s\n\nTask: %s", repoContext, string(topicsJSON), task)
	if len(sessionPrompts) > 0 {
		userMsg += "\n\nRecent session context:\n" + strings.Join(sessionPrompts, "\n---\n")
	}
	raw, usage, err := callClaudeWithSystem(ctx, understandTaskModel, prompts.SelectTopicSystem, userMsg, 1024)
	if err != nil {
		return SelectTopicResult{}, usage, err
	}
	raw = strings.TrimSpace(raw)
	if after, ok := strings.CutPrefix(raw, "```json"); ok {
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(after), "```"))
	} else if after, ok := strings.CutPrefix(raw, "```"); ok {
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(after), "```"))
	}
	var result struct {
		Status            string   `json:"status"`
		TopicID           string   `json:"topic_id"`
		Reason            string   `json:"reason"`
		Question          string   `json:"question"`
		CandidateTopicIDs []string `json:"candidate_topic_ids"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return SelectTopicResult{}, usage, fmt.Errorf("topic chooser returned non-JSON: %w", err)
	}
	if result.Status == "needs_clarification" {
		return SelectTopicResult{
			Status:            "needs_clarification",
			Reason:            result.Reason,
			Question:          result.Question,
			CandidateTopicIDs: result.CandidateTopicIDs,
		}, usage, nil
	}
	return SelectTopicResult{TopicID: result.TopicID}, usage, nil
}

// PriorSession holds a summary of a previous session on this topic.
type PriorSession = contracts.PriorSession

// WriteOrientationHint calls Claude Haiku to produce a 1-paragraph hint.
// Returns found=false when the model had no specific hint; callers should render
// a compact fallback in that case. System prompt is static (cached).
func WriteOrientationHint(ctx context.Context, repoContext string, topic model.TopicContext, task string, sessionPrompts []string, priorSessions []PriorSession) (string, bool, Usage, error) {
	userMsg := fmt.Sprintf("User request:\n%s\n\nRepository context:\n%s\n\nSelected topic:\n%s\n\nCompact topic metadata:\n%s",
		task, repoContext, topic.Name+" - "+topic.Summary, renderHintContext(topic))
	if len(sessionPrompts) > 0 {
		userMsg += "\n\nRecent session context:\n" + strings.Join(sessionPrompts, "\n---\n")
	}
	if len(priorSessions) > 0 {
		userMsg += "\n\nPrior sessions on this topic (most recent first):\n"
		for i, s := range priorSessions {
			line := fmt.Sprintf("%d. Task: %s", i+1, s.Task)
			if len(s.Files) > 0 {
				line += fmt.Sprintf(" | Changed: %s", strings.Join(s.Files, ", "))
			}
			userMsg += line + "\n"
		}
	}
	hint, usage, err := callClaudeWithSystem(ctx, understandTaskModel, prompts.UnderstandTaskHintSystem, userMsg, 1024)
	if err != nil {
		return "", false, usage, err
	}
	hint = strings.TrimSpace(hint)
	return hint, hint != "", usage, nil
}

// RenderTopicContextText produces a compact text representation of a topic's
// context package - returned to the CLI as the context_text field.
func RenderTopicContextText(topic model.TopicContext) string {
	return renderTopicContextBlock(topic)
}

// CompactTopicHint renders the minimal hint for a topic when the AI has no
// specific orientation signal to add.
func CompactTopicHint(topic model.TopicContext) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Topic: %s\nSummary: %s\n", topic.Name, topic.Summary)
	if len(topic.StartHere) > 0 {
		sb.WriteString("\nStart here:\n")
		for _, f := range topic.StartHere {
			fmt.Fprintf(&sb, "- %s\n", f.Path)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderTopicContextBlock produces the full context block for context_text (shown to the agent).
func renderTopicContextBlock(topic model.TopicContext) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Topic: %s\nSummary: %s\n", topic.Name, topic.Summary)

	if len(topic.StartHere) > 0 {
		sb.WriteString("\nStart here:\n")
		for _, f := range topic.StartHere {
			fmt.Fprintf(&sb, "- %s\n", f.Path)
		}
	}
	if len(topic.KnownWorkflows) > 0 {
		sb.WriteString("\nKnown workflows:\n")
		for _, w := range topic.KnownWorkflows {
			fmt.Fprintf(&sb, "- %s\n", w)
		}
	}
	if len(topic.AvoidWastingTime) > 0 {
		sb.WriteString("\nAvoid:\n")
		for _, a := range topic.AvoidWastingTime {
			fmt.Fprintf(&sb, "- %s\n", a)
		}
	}

	imp := topic.ImportantFiles
	var importantFiles []string
	importantFiles = append(importantFiles, imp.EditTargets...)
	importantFiles = append(importantFiles, imp.ReferenceFiles...)
	importantFiles = append(importantFiles, imp.CrossCuttingFiles...)
	seen := make(map[string]struct{}, len(importantFiles))
	deduped := importantFiles[:0]
	for _, f := range importantFiles {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		deduped = append(deduped, f)
	}
	if len(deduped) > 0 {
		sb.WriteString("\nImportant files:\n")
		for _, f := range deduped {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// renderHintContext produces a compact block for the hint writer.
// Excludes KnownWorkflows - those go in context_package, not the preface hint.
func renderHintContext(topic model.TopicContext) string {
	var sb strings.Builder
	if len(topic.AvoidWastingTime) > 0 {
		sb.WriteString("Avoid:\n")
		for _, a := range topic.AvoidWastingTime {
			fmt.Fprintf(&sb, "- %s\n", a)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
