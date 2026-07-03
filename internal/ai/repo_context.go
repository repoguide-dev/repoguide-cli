package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
)

const repoContextModel = "claude-sonnet-4-6"

var repoContextSystemPrompt = `You are analyzing historical conversations between a developer and coding agents.

Your task is to produce a compact context file shown to future agents before they begin work. The file has one job: help the agent correctly interpret what this developer means when they write a prompt.

Your PRIMARY source is the session interactions. Use repository documentation only to understand domain terminology.

Do NOT summarize conversations. Do NOT invent patterns. Only include what is evidenced across multiple sessions.

Ignore: one-off requests, specific tasks, temporary state, personal information.

` + prompts.RepoContextStructure + `

Requirements:

* Maximum 500 words total.
* Concise bullet points.
* Evidence-based only - no speculation.
* Do not quote or reference specific conversations.
* Write for an experienced agent reading this once before starting work.
* Omit any section with no signal.`

// GenerateRepoContext produces a compact context file from session interactions and docs.
func GenerateRepoContext(ctx context.Context, bundle contracts.RepoAnalysisBundle, docs map[string]string) (string, Usage, error) {
	prompt := buildRepoContextPrompt(bundle, docs)
	raw, usage, err := callClaude(ctx, repoContextModel, prompt)
	if err != nil {
		return "", usage, err
	}
	return strings.TrimSpace(raw), usage, nil
}

func buildRepoContextPrompt(bundle contracts.RepoAnalysisBundle, docs map[string]string) string {
	var sb strings.Builder
	sb.WriteString(repoContextSystemPrompt)
	sb.WriteString("\n\n---\n\n")

	if len(docs) > 0 {
		sb.WriteString("## Repository Documentation\n\n")
		for name, content := range docs {
			fmt.Fprintf(&sb, "### %s\n\n%s\n\n", name, content)
		}
	}

	sb.WriteString("## Session Interactions\n\n")
	total := sb.Len()
	const maxTotal = 50000
	const maxMsgLen = 200

	for i, session := range bundle.Sessions {
		if len(session.Interactions) == 0 {
			continue
		}
		header := fmt.Sprintf("session #%d:\n", i+1)
		if total+len(header) > maxTotal {
			break
		}
		sb.WriteString(header)
		total += len(header)

		done := false
		for _, interaction := range session.Interactions {
			text := interaction.Text
			if len(text) > maxMsgLen {
				text = text[:maxMsgLen]
			}
			line := interaction.Role + ": " + text + "\n"
			if total+len(line) > maxTotal {
				done = true
				break
			}
			sb.WriteString(line)
			total += len(line)
		}
		sb.WriteString("\n")
		if done {
			break
		}
	}

	return sb.String()
}
