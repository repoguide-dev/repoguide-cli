package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

const topicCuratorModel = "claude-sonnet-4-6"

type TopicCurationSuggestion = contracts.TopicCurationSuggestion
type TopicCurationSkip = contracts.TopicCurationSkip
type TopicSuggestionDecision = contracts.TopicSuggestionDecision
type TopicCuration = contracts.TopicCuration

// CurateTopicContext calls the model to generate suggestions for topic based on feedbacks.
// pending contains existing suggestions the model may accept/reject/keep.
func CurateTopicContext(ctx context.Context, topic *model.TopicContext, feedbacks []*model.MCPFeedback, pending []model.TopicPatchSuggestion) (*TopicCuration, Usage, error) {
	type feedbackInput struct {
		FeedbackID          string                        `json:"feedback_id"`
		Task                string                        `json:"task"`
		Stars               int                           `json:"stars,omitempty"`
		Helpfulness         string                        `json:"helpfulness"`
		HelpedWith          []string                      `json:"helped_with,omitempty"`
		Quote               string                        `json:"quote,omitempty"`
		MissingContext      string                        `json:"missing_context,omitempty"`
		WhatWentWrong       string                        `json:"what_went_wrong,omitempty"`
		WhatCouldBeImproved string                        `json:"what_could_be_improved,omitempty"`
		Classification      *model.FeedbackClassification `json:"classification,omitempty"`
	}

	fbInputs := make([]feedbackInput, len(feedbacks))
	for i, fb := range feedbacks {
		fbInputs[i] = feedbackInput{
			FeedbackID:          fb.FeedbackID,
			Task:                fb.Task,
			Stars:               fb.Stars,
			Helpfulness:         fb.Helpfulness,
			HelpedWith:          fb.HelpedWith,
			Quote:               fb.Quote,
			MissingContext:      fb.MissingContext,
			WhatWentWrong:       fb.WhatWentWrong,
			WhatCouldBeImproved: fb.WhatCouldBeImproved,
			Classification:      fb.Classification,
		}
	}

	userMsg, _ := json.Marshal(map[string]any{
		"repo_id":             feedbacks[0].RepoID,
		"topic":               topic,
		"feedbacks":           fbInputs,
		"pending_suggestions": pending,
	})

	raw, usage, err := callClaudeWithSystem(ctx, topicCuratorModel, prompts.TopicCuratorSystem, string(userMsg), 4096)
	if err != nil {
		return nil, usage, err
	}

	var curation TopicCuration
	if err := json.Unmarshal([]byte(stripFences(raw)), &curation); err != nil {
		return nil, usage, fmt.Errorf("topic curator: parse response: %w", err)
	}
	return &curation, usage, nil
}

// ApplyTopicCuration applies a list of suggestions to a topic and returns the updated copy.
// It is idempotent: ops that would add duplicates are silently skipped.
// The caller is responsible for passing only suggestions that should be applied.
func ApplyTopicCuration(topic model.TopicContext, suggestions []TopicCurationSuggestion) model.TopicContext {
	for _, s := range suggestions {
		switch s.Kind {
		case "noop_duplicate":
			// intentional no-op

		case "add_when_to_use":
			var v string
			if json.Unmarshal(s.Value, &v) == nil && v != "" && !slices.Contains(topic.WhenToUse, v) {
				topic.WhenToUse = append(topic.WhenToUse, v)
			}

		case "add_prompt_keyword":
			var v string
			if json.Unmarshal(s.Value, &v) == nil && v != "" && !slices.Contains(topic.PromptKeywords, v) {
				topic.PromptKeywords = append(topic.PromptKeywords, v)
			}

		case "add_known_workflow":
			var v string
			if json.Unmarshal(s.Value, &v) == nil && v != "" && !slices.Contains(topic.KnownWorkflows, v) {
				topic.KnownWorkflows = append(topic.KnownWorkflows, v)
			}

		case "add_avoid_wasting_time":
			var v string
			if json.Unmarshal(s.Value, &v) == nil && v != "" && !slices.Contains(topic.AvoidWastingTime, v) {
				topic.AvoidWastingTime = append(topic.AvoidWastingTime, v)
			}

		case "add_risk_flag", "mark_stale":
			flag := s.Path
			if s.Kind == "mark_stale" {
				flag = "stale"
			}
			if flag != "" && !slices.Contains(topic.RiskFlags, flag) {
				topic.RiskFlags = append(topic.RiskFlags, flag)
			}

		case "add_start_here":
			var f model.TopicStartFile
			if json.Unmarshal(s.Value, &f) == nil && f.Path != "" {
				if !slices.ContainsFunc(topic.StartHere, func(x model.TopicStartFile) bool { return x.Path == f.Path }) {
					topic.StartHere = append(topic.StartHere, f)
				}
			}

		case "update_start_here_reason":
			var v struct {
				Why string `json:"why"`
			}
			if json.Unmarshal(s.Value, &v) == nil && s.Path != "" {
				for i := range topic.StartHere {
					if topic.StartHere[i].Path == s.Path {
						topic.StartHere[i].Why = v.Why
						break
					}
				}
			}

		case "add_important_file":
			target := fileListPtr(&topic.ImportantFiles, s.TargetField)
			if target != nil && s.Path != "" && !slices.Contains(*target, s.Path) {
				*target = append(*target, s.Path)
			}

		case "remove_item":
			switch s.TargetField {
			case "when_to_use":
				var v string
				if json.Unmarshal(s.Value, &v) == nil {
					topic.WhenToUse = slices.DeleteFunc(topic.WhenToUse, func(x string) bool { return x == v })
				}
			case "prompt_keywords":
				var v string
				if json.Unmarshal(s.Value, &v) == nil {
					topic.PromptKeywords = slices.DeleteFunc(topic.PromptKeywords, func(x string) bool { return x == v })
				}
			case "known_workflows":
				var v string
				if json.Unmarshal(s.Value, &v) == nil {
					topic.KnownWorkflows = slices.DeleteFunc(topic.KnownWorkflows, func(x string) bool { return x == v })
				}
			case "avoid_wasting_time":
				var v string
				if json.Unmarshal(s.Value, &v) == nil {
					topic.AvoidWastingTime = slices.DeleteFunc(topic.AvoidWastingTime, func(x string) bool { return x == v })
				}
			case "risk_flags":
				flag := s.Path
				if flag == "" {
					_ = json.Unmarshal(s.Value, &flag)
				}
				topic.RiskFlags = slices.DeleteFunc(topic.RiskFlags, func(x string) bool { return x == flag })
			case "start_here":
				topic.StartHere = slices.DeleteFunc(topic.StartHere, func(x model.TopicStartFile) bool { return x.Path == s.Path })
			default:
				target := fileListPtr(&topic.ImportantFiles, s.TargetField)
				if target != nil && s.Path != "" {
					*target = slices.DeleteFunc(*target, func(x string) bool { return x == s.Path })
				}
			}

		case "move_file_between_importance_groups":
			var v struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if json.Unmarshal(s.Value, &v) == nil && s.Path != "" {
				from := fileListPtr(&topic.ImportantFiles, "important_files."+v.From)
				to := fileListPtr(&topic.ImportantFiles, "important_files."+v.To)
				if from != nil && to != nil {
					*from = slices.DeleteFunc(*from, func(p string) bool { return p == s.Path })
					if !slices.Contains(*to, s.Path) {
						*to = append(*to, s.Path)
					}
				}
			}
		}
	}
	return topic
}

func fileListPtr(f *model.TopicImportantFiles, field string) *[]string {
	switch field {
	case "important_files.edit_targets":
		return &f.EditTargets
	case "important_files.reference_files":
		return &f.ReferenceFiles
	case "important_files.cross_cutting_files":
		return &f.CrossCuttingFiles
	}
	return nil
}
