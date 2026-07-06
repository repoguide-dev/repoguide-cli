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

const repoContextPatcherModel = "claude-sonnet-4-6"

type RepoContextSession = contracts.RepoContextSession
type RepoContextPatchEdit = contracts.RepoContextPatchEdit
type RepoContextPatchSkip = contracts.RepoContextPatchSkip
type RepoContextPatch = contracts.RepoContextPatch

const (
	maxRepoContextPromptRunes = 500
	maxRepoContextDiffRunes   = 2000
	maxRepoContextDiffs       = 5
)

// BuildRepoContextSession extracts compact per-feedback evidence for repo-context patching.
func BuildRepoContextSession(feedbackID, sessionID string, events []model.SessionEvent) RepoContextSession {
	sess := RepoContextSession{FeedbackID: feedbackID, SessionID: sessionID}
	seenFiles := map[string]bool{}
	for _, ev := range events {
		if ev.Kind == "prompt" && strings.TrimSpace(ev.Text) != "" && !strings.HasPrefix(strings.TrimSpace(ev.Text), "<local-command-caveat>") {
			sess.UserPrompts = append(sess.UserPrompts, truncateRunes(strings.TrimSpace(ev.Text), maxRepoContextPromptRunes))
		}
		for _, p := range ev.WritePaths {
			if !seenFiles[p] {
				seenFiles[p] = true
				sess.EditedFiles = append(sess.EditedFiles, p)
			}
		}
		sess.DiffSnippets = appendRepoContextDiffSnippet(sess.DiffSnippets, ev.CommandText)
	}
	return sess
}

func repoContextDiffSnippets(events []model.SessionEvent) []string {
	var snippets []string
	for _, ev := range events {
		snippets = appendRepoContextDiffSnippet(snippets, ev.CommandText)
	}
	return snippets
}

func appendRepoContextDiffSnippet(snippets []string, commandText string) []string {
	if len(snippets) >= maxRepoContextDiffs {
		return snippets
	}
	if snippet := repoContextDiffSnippet(commandText); snippet != "" {
		return append(snippets, snippet)
	}
	return snippets
}

func repoContextDiffSnippet(commandText string) string {
	text := strings.TrimSpace(commandText)
	if text == "" {
		return ""
	}
	if !strings.Contains(text, "apply_patch") && !strings.Contains(text, "diff --git") && !strings.Contains(text, "*** Begin Patch") {
		return ""
	}
	return truncateRunes(text, maxRepoContextDiffRunes)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// PatchRepoContext calls Haiku to generate a minimal patch for the repo context based on low-star feedbacks.
func PatchRepoContext(ctx context.Context, currentContext string, feedbacks []*model.MCPFeedback, sessions []RepoContextSession) (*RepoContextPatch, Usage, error) {
	type feedbackInput struct {
		FeedbackID          string                        `json:"feedback_id"`
		Task                string                        `json:"task"`
		Stars               int                           `json:"stars,omitempty"`
		Helpfulness         string                        `json:"helpfulness"`
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
			Quote:               fb.Quote,
			MissingContext:      fb.MissingContext,
			WhatWentWrong:       fb.WhatWentWrong,
			WhatCouldBeImproved: fb.WhatCouldBeImproved,
			Classification:      fb.Classification,
		}
	}

	userMsg, _ := json.Marshal(map[string]any{
		"repo_id":         feedbacks[0].RepoID,
		"current_context": currentContext,
		"feedbacks":       fbInputs,
		"sessions":        sessions,
	})

	raw, usage, err := callClaudeWithSystem(ctx, repoContextPatcherModel, prompts.RepoContextPatcherSystem, string(userMsg), 4096)
	if err != nil {
		return nil, usage, err
	}

	cleaned := extractJSON(raw)
	var patch RepoContextPatch
	if err := json.Unmarshal([]byte(cleaned), &patch); err != nil {
		return nil, usage, fmt.Errorf("repo context patcher: parse response: %w", err)
	}
	return &patch, usage, nil
}

var sectionHeaders = map[string]string{
	"terminology":               "## Terminology",
	"how_developer_writes":      "## How This Developer Writes Requests",
	"common_misinterpretations": "## Common Misinterpretations",
	"hard_limits":               "## Hard Limits",
}

// ApplyRepoContextPatch applies add_row/remove_row edits to the repo context string.
func ApplyRepoContextPatch(context string, patch *RepoContextPatch) string {
	lines := strings.Split(context, "\n")
	for _, edit := range patch.Edits {
		switch edit.Op {
		case "add_row":
			lines = addContextRow(lines, edit.Section, edit.Value)
		case "remove_row":
			lines = removeContextRow(lines, edit.Section, edit.Value)
		case "noop_duplicate":
			// intentional no-op
		}
	}
	return strings.Join(lines, "\n")
}

func addContextRow(lines []string, section, value string) []string {
	header, ok := sectionHeaders[section]
	if !ok {
		return lines
	}
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			start = i
			break
		}
	}
	if start == -1 {
		return lines
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") || strings.HasPrefix(strings.TrimSpace(lines[i]), "# ") {
			end = i
			break
		}
	}
	// Check not already present (strip leading "* " before comparing)
	norm := strings.TrimSpace(value)
	for i := start; i < end; i++ {
		existing := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "*"))
		existing = strings.TrimSpace(existing)
		if strings.EqualFold(existing, norm) {
			return lines
		}
	}
	// Insert before trailing blank lines of the section
	ins := end
	for ins > start+1 && strings.TrimSpace(lines[ins-1]) == "" {
		ins--
	}
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:ins]...)
	result = append(result, "* "+value)
	result = append(result, lines[ins:]...)
	return result
}

func removeContextRow(lines []string, section, value string) []string {
	header, ok := sectionHeaders[section]
	if !ok {
		return lines
	}
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			start = i
			break
		}
	}
	if start == -1 {
		return lines
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") || strings.HasPrefix(strings.TrimSpace(lines[i]), "# ") {
			end = i
			break
		}
	}
	norm := strings.TrimSpace(value)
	result := make([]string, 0, len(lines))
	for i, l := range lines {
		if i >= start && i < end {
			existing := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*"))
			existing = strings.TrimSpace(existing)
			if strings.EqualFold(existing, norm) {
				continue
			}
		}
		result = append(result, l)
	}
	return result
}
