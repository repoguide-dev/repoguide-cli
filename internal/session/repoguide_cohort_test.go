package session

import "testing"

func call(id string) SessionEvent {
	return SessionEvent{Kind: "tool_call", ToolName: "repoguide_get_repo_experience", ToolCallID: id}
}
func result(id, text string) SessionEvent {
	return SessionEvent{Kind: "tool_result", ToolCallID: id, Text: text}
}

// Five of the seven summary parsers set "used RepoGuide" from the tool name
// alone, so a held-out session was landing in the treatment arm — the one
// error that quietly destroys the experiment rather than just weakening it.
func TestHoldoutResultIsNotTreated(t *testing.T) {
	got := classifyRepoGuideEvents([]SessionEvent{
		call("a"), result("a", "No repository experience...\n\n"+HoldoutMarker),
	})
	if got.Used {
		t.Error("a withheld briefing must not count as used")
	}
	if !got.Holdout {
		t.Error("expected the session to be marked as holdout")
	}
}

func TestBriefingCountsAsUsed(t *testing.T) {
	got := classifyRepoGuideEvents([]SessionEvent{
		call("a"), result("a", "Task-to-topic match\n- 82% Session Import"),
	})
	if !got.Used || got.Holdout {
		t.Errorf("got %+v, want Used", got)
	}
}

// Gemini records no call IDs, so results must still pair with calls positionally.
func TestUnkeyedResultsStillPair(t *testing.T) {
	got := classifyRepoGuideEvents([]SessionEvent{
		{Kind: "tool_call", ToolName: "repoguide_get_repo_experience"},
		{Kind: "tool_result", Text: "briefing text"},
	})
	if !got.Used {
		t.Error("a result with no call ID must still pair with the pending call")
	}
}

// A tool_result that belongs to some other tool must never be read as a
// RepoGuide outcome.
func TestUnrelatedResultsIgnored(t *testing.T) {
	got := classifyRepoGuideEvents([]SessionEvent{
		{Kind: "tool_call", ToolName: "Bash", ToolCallID: "b"},
		result("b", "briefing text"),
	})
	if got.Used || got.Holdout {
		t.Errorf("unrelated tool result must not classify: %+v", got)
	}
}

func TestErrorsAndClarificationsAreNeither(t *testing.T) {
	for name, text := range map[string]string{
		"clarification": "Task maps to multiple topics",
		"error":         "understand-task failed: boom",
	} {
		got := classifyRepoGuideEvents([]SessionEvent{call("a"), result("a", text)})
		if got.Used || got.Holdout {
			t.Errorf("%s: got %+v, want neither", name, got)
		}
	}
}

// One refusal alongside one real briefing is a treated session: it saw the
// guidance, so counting it as a control would poison the control arm.
func TestBriefingWinsOverLaterHoldout(t *testing.T) {
	got := classifyRepoGuideEvents([]SessionEvent{
		call("a"), result("a", "real briefing"),
		call("b"), result("b", HoldoutMarker),
	})
	if !got.Used || got.Holdout {
		t.Errorf("got %+v, want Used only", got)
	}
}

func TestErroredResultIgnored(t *testing.T) {
	got := classifyRepoGuideEvents([]SessionEvent{
		call("a"), SessionEvent{Kind: "tool_result", ToolCallID: "a", Text: "briefing", IsError: true},
	})
	if got.Used || got.Holdout {
		t.Errorf("errored result must not classify: %+v", got)
	}
}
