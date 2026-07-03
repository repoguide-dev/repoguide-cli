package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/model"
)

const feedbackClassifierModel = "claude-haiku-4-5-20251001"

// ClassifyFeedback calls Haiku to classify a feedback record and returns the classification and token usage.
func ClassifyFeedback(ctx context.Context, fb *model.MCPFeedback) (*model.FeedbackClassification, Usage, error) {
	input := map[string]any{
		"feedback_id":     fb.FeedbackID,
		"repo_id":         fb.RepoID,
		"task":            fb.Task,
		"stars":           fb.Stars,
		"helpfulness":     fb.Helpfulness,
		"helped_with":     fb.HelpedWith,
		"quote":           fb.Quote,
		"missing_context": fb.MissingContext,
	}
	if fb.SessionID != "" {
		input["session_id"] = fb.SessionID
	}
	if fb.TopicID != "" {
		input["selected_topic_id"] = fb.TopicID
	}
	if fb.WhatWentWrong != "" {
		input["what_went_wrong"] = fb.WhatWentWrong
	}
	if fb.WhatCouldBeImproved != "" {
		input["what_could_be_improved"] = fb.WhatCouldBeImproved
	}
	userMsg, _ := json.Marshal(input)

	var totalUsage Usage
	for attempt := range 2 {
		raw, usage, err := callClaudeWithSystem(ctx, feedbackClassifierModel, prompts.FeedbackClassifierSystem, string(userMsg), 1024)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.Model = usage.Model
		if err != nil {
			return nil, totalUsage, err
		}
		var c model.FeedbackClassification
		if err := json.Unmarshal([]byte(stripFences(raw)), &c); err != nil {
			if attempt == 0 {
				continue
			}
			return nil, totalUsage, fmt.Errorf("feedback classifier: parse response: %w", err)
		}
		if err := c.Validate(); err != nil {
			if attempt == 0 {
				continue
			}
			return nil, totalUsage, fmt.Errorf("feedback classifier: %w", err)
		}
		return &c, totalUsage, nil
	}
	return nil, totalUsage, fmt.Errorf("feedback classifier: all attempts failed")
}
