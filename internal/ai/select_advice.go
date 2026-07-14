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

type AdviceItem = contracts.AdviceItem
type SelectionBudget = contracts.SelectionBudget
type AdviceSelectionResponse = contracts.AdviceSelectionResponse

func SelectAdvice(ctx context.Context, task string, topic model.TopicContext, candidates []AdviceItem, budget SelectionBudget, positive, negative []TopicRoutingExample, feedback []model.MCPFeedback) (AdviceSelectionResponse, Usage, error) {
	input := map[string]any{"current_task": task, "candidate_topic": map[string]any{"id": topic.ID, "name": topic.Name, "summary": topic.Summary}, "selection_budget": budget, "candidate_advice": candidates}
	if len(positive) > 0 {
		input["positive_examples"] = positive
	}
	if len(negative) > 0 {
		input["negative_examples"] = negative
	}
	if examples := textualAdviceFeedback(topic.ID, feedback); len(examples) > 0 {
		input["textual_advice_feedback"] = examples
	}
	payload, _ := json.Marshal(input)
	raw, usage, err := callClaudeWithSystem(ctx, understandTaskModel, prompts.SelectAdviceSystem, string(payload), 2048)
	if err != nil {
		return AdviceSelectionResponse{}, usage, err
	}
	var selected AdviceSelectionResponse
	decoder := json.NewDecoder(strings.NewReader(extractJSON(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selected); err != nil {
		return AdviceSelectionResponse{}, usage, fmt.Errorf("advice selector returned non-JSON: %w", err)
	}
	return selected, usage, nil
}

func textualAdviceFeedback(topicID string, feedback []model.MCPFeedback) []map[string]any {
	examples := make([]map[string]any, 0, 12)
	for _, item := range feedback {
		if item.TopicID != topicID || item.AdviceEvaluation == nil {
			continue
		}
		evaluation := item.AdviceEvaluation
		if evaluation.UsefulAdvice == "" && evaluation.IncorrectAdvice == "" && evaluation.UnnecessaryAdvice == "" && evaluation.MissingAdvice == "" && len(evaluation.UsefulFiles) == 0 && len(evaluation.UnhelpfulFiles) == 0 {
			continue
		}
		examples = append(examples, map[string]any{"task": item.Task, "useful_advice": evaluation.UsefulAdvice, "incorrect_advice": evaluation.IncorrectAdvice, "unnecessary_advice": evaluation.UnnecessaryAdvice, "missing_advice": evaluation.MissingAdvice, "useful_files": evaluation.UsefulFiles, "unhelpful_files": evaluation.UnhelpfulFiles})
		if len(examples) == 12 {
			break
		}
	}
	return examples
}
