package prompts

import (
	"strings"
	"testing"
)

func TestSelectAdviceSystemOnlyRanksEvidenceIDs(t *testing.T) {
	checks := []string{
		`evidence selector, not an advice author`,
		`Return IDs exactly as supplied`,
		`Positive and negative examples are relevance examples only`,
		`Never invent files, APIs, facts, algorithms, implementation steps, or new advice`,
		`fields other than the six allowed category arrays`,
	}
	for _, want := range checks {
		if !strings.Contains(SelectAdviceSystem, want) {
			t.Fatalf("SelectAdviceSystem missing %q", want)
		}
	}
}

func TestSelectTopicSystemGuardsAgainstSessionScopeDrift(t *testing.T) {
	checks := []string{
		`Use it only as weak supporting signal when the task description alone is ambiguous, and only when it is directly consistent with the current task and repository topics.`,
		`Ignore quoted tool output, prior guesses, and speculative scope claims.`,
		`Do not route to an operational, deployment, or non-code topic interpretation unless that scope is explicit in the current task or clearly grounded in the provided repository topics.`,
	}
	for _, want := range checks {
		if !strings.Contains(SelectTopicSystem, want) {
			t.Fatalf("SelectTopicSystem missing %q", want)
		}
	}
}
