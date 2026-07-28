package session

import "strings"

// HoldoutMarker is embedded in the MCP server's withheld response. It is the
// wire format between the server and this package: without it a randomized
// control is indistinguishable from a session that got a real briefing.
const HoldoutMarker = "[repoguide:holdout]"

// RepoGuideCohort classifies a session from its event log, which is the only
// place every agent's data converges. Deriving it here rather than in each
// agent's summary parser matters: five of the seven parsers set "used
// RepoGuide" from the tool name alone, so a held-out session — one that asked
// for a briefing and was deliberately refused — was being counted as treated,
// putting controls in the treatment arm.
type RepoGuideCohort struct {
	Used    bool
	Holdout bool
}

// classifyRepoGuideEvents pairs repoguide tool calls with their results.
// Results are matched by call ID where the agent records one, and otherwise
// against the most recent unmatched call, since some agents (Gemini) emit no
// IDs at all.
func classifyRepoGuideEvents(events []SessionEvent) RepoGuideCohort {
	var out RepoGuideCohort
	pending := map[string]bool{}
	pendingUnkeyed := 0

	for _, e := range events {
		switch e.Kind {
		case "tool_call":
			if !isRepoGuideExperienceTool(e.ToolName) {
				continue
			}
			if e.ToolCallID != "" {
				pending[e.ToolCallID] = true
			} else {
				pendingUnkeyed++
			}
		case "tool_result":
			matched := false
			switch {
			case e.ToolCallID != "" && pending[e.ToolCallID]:
				delete(pending, e.ToolCallID)
				matched = true
			case e.ToolCallID == "" && pendingUnkeyed > 0:
				pendingUnkeyed--
				matched = true
			}
			if !matched || e.IsError {
				continue
			}
			if strings.Contains(e.Text, HoldoutMarker) {
				out.Holdout = true
				continue
			}
			if repoGuideTextIsExperience(e.Text) {
				out.Used = true
			}
		}
	}
	// A session that received a briefing at any point is treated, even if
	// another call in it was refused — it saw the guidance.
	if out.Used {
		out.Holdout = false
	}
	return out
}

func isRepoGuideExperienceTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(name, "repoguide") && strings.Contains(name, "repo_experience")
}

// repoGuideTextIsExperience mirrors the summary-scan predicate: clarification
// prompts and errors are calls that returned no guidance.
func repoGuideTextIsExperience(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.Contains(text, "Task maps to multiple topics") {
		return false
	}
	return !strings.Contains(text, "understand-task failed")
}
