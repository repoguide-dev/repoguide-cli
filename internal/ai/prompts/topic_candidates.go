package prompts

import "fmt"

const topicCandidatePrompt = `You group repository work sources into topic candidates before any topic is named or described.

Inputs contain sessions, commits, and eventually pull requests. For sessions, use only the first few prompts to infer intent. Commit subjects and PR titles are intent hints. Do not name topics and do not write topic descriptions in this step.

Structural rules:
- Consider every input source exactly once. Put it in one candidate or unassigned_source_ids.
- A candidate normally needs at least two independent sources.
- Prefer candidates with four or more semantically related sources.
- Group by developer-facing work area, not by broad layer names such as frontend, backend, CLI, or tests.
- Similar wording alone is insufficient. Prefer groups whose changed files are plausibly related.
- Keep areas separate when their changed-file sets have materially lower overlap.
- Do not create a candidate from one isolated prompt, commit, or PR.
- Existing topics are separation context: do not form a new candidate that merely duplicates one unless the sources clearly describe a distinct area.
- Do not invent source IDs.

Return only JSON:
{
  "candidates": [
    {"candidate_id":"candidate_1","source_ids":["source id"],"reason":"brief grouping rationale without a topic name"}
  ],
  "unassigned_source_ids": ["isolated source id"]
}

Existing topics JSON:
%s

Sources JSON:
%s`

func BuildTopicCandidatePrompt(sourcesJSON, existingTopicsJSON string) string {
	return fmt.Sprintf(topicCandidatePrompt, existingTopicsJSON, sourcesJSON)
}
