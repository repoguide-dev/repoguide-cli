package prompts

import "fmt"

const feedbackTopicPromptTemplate = `You are generating a single routing topic context for a software repository.

A coding agent has flagged a topic area and provided feedback from a coding session. Your job is to produce one compact, practical topic context object that helps future agents start work on this topic without exploring the repository from scratch.

Existing topics (name → summary):
%s

Suggested topic name: %s

Agent feedback:
%s

Session data (one coding session that covered this topic):
%s

Rules:
- Only create a new topic if the feedback describes work not covered by any existing topic.
- Create topics per domain area. Prefer the smallest recurring domain that will help routing: broader than a single task, but narrower than a whole layer or app when the evidence supports a specific feature, workflow, page, service, integration, or business capability.
- If you decide not to create a topic, return {"skip": "<reason>"} where reason is one of:
    "duplicate"    - this topic already exists in the existing topics list
    "too_specific" - this feedback is too narrow (single task, not a recurring workflow)
    "invalid"      - the feedback is noise, low signal, or not actionable
- Use the suggested name as a strong hint; refine it to 3-6 Title Case words only if it is vague or inaccurate.
- Avoid layer-only names like "Frontend" or "Backend", or other broad umbrella names, when the session evidence points to a tighter recurring area.
- Do not invent files not present in the session data or file_labels.
- Use the broader session/group evidence when choosing files: include adjacent reference files, tests, shared state, and co-touched files that support the same domain topic, not just the most obvious edited file.
- Prefer developer intent from the user prompts over file paths.
- Session prompts are in order: later prompts often correct or narrow earlier ones. A correction is strong evidence for known_workflows or avoid_wasting_time.
- Session commands are shell commands the agent actually ran; failed_commands are the ones that errored. Use commands (never invented ones) for tests.commands and known_workflows; use failed_commands as avoid_wasting_time evidence when the failure looks repeatable rather than task-specific.
- Keep output compact - it will be injected directly into an agent's context window.

Return only valid JSON. No markdown, no comments.

Return {"skip": "<reason>"} if the feedback should not create a new topic. Otherwise return a single JSON object:

` + SharedTopicOutputObject + `

Field rules:
` + SharedFieldRules + `

` + SharedNamingExamples

// FeedbackTopicSessionData is the session summary passed to BuildFeedbackTopicPrompt.
// It is serialized as JSON and embedded in the prompt.
type FeedbackTopicSessionData struct {
	Prompts        []string            `json:"prompts"`
	ToolCalls      []string            `json:"tool_calls,omitempty"`
	Commands       []string            `json:"commands,omitempty"`
	FailedCommands []string            `json:"failed_commands,omitempty"`
	EditedFiles    []string            `json:"edited_files,omitempty"`
	ReadFiles      []string            `json:"read_files,omitempty"`
	FileLabels     map[string][]string `json:"file_labels,omitempty"`
}

// ExistingTopicSummary is the compact representation of an existing topic passed to the prompt.
type ExistingTopicSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

func BuildFeedbackTopicPrompt(suggestedName, feedback, sessionJSON, existingTopicsJSON string) string {
	return fmt.Sprintf(feedbackTopicPromptTemplate, existingTopicsJSON, suggestedName, feedback, sessionJSON)
}
