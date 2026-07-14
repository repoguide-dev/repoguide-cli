package prompts

import "fmt"

const topicCandidatePrompt = `You route repository work sources into existing topics or structurally supported new topic candidates before any new topic is named or described.

Inputs contain sessions, commits, and eventually pull requests. For sessions, use only the first few prompts to infer intent. Commit subjects and PR titles are intent hints. Do not name topics and do not write topic descriptions in this step.

Structural rules:
- Consider every input source exactly once. Assign it to one existing topic, put it in one new/split group, or mark it unassigned.
- Assign to an existing topic when its intent and edited files fit that topic's stated scope.
- Existing topics JSON may also contain candidate=true entries created by earlier 50-source turns. Assign matching sources to their id exactly like a persisted topic; this is how recurring areas continue across turns without duplicates.
- Suggest a new group when at least two sources describe a recurring area reasonably separate from existing topics.
- You may split an existing topic when the batch contains multiple recurring work areas with materially different intent and edited-file scope. Return the source groups supporting the proposed split.
- A candidate normally needs at least two independent sources.
- Prefer candidates with four or more semantically related sources.
- Keep useful two- or three-source candidates as weak candidates instead of leaving them unassigned merely because they are small.
- Group by a specific developer-facing feature or work area, not by broad layer or product buckets such as frontend, backend, cloud, CLI, tests, marketing, or repository dashboard.
- Split distinct pages, flows, commands, and subsystems even when they belong to the same product. For example, landing pages, public report generation, pricing, authentication, repository trees, and topic details are separate work areas.
- A candidate with more than 30 sources is usually too broad; split it unless the prompts consistently describe the same concrete feature.
- Similar wording alone is insufficient. Prefer groups whose changed files are plausibly related.
- Keep areas separate when their changed-file sets have materially lower overlap.
- Do not create a candidate from one isolated prompt, commit, or PR.
- Existing topics are separation context: do not form a new candidate that merely duplicates one unless the sources clearly describe a distinct area.
- Do not invent source IDs.
- Use unassigned only for a genuinely isolated source with no semantic peer. Do not discard a recurring but low-confidence area.

Return only compact JSON. Do not include reasons, names, descriptions, prose, or repeated source metadata:
{"assign":[{"topic_id":"existing-topic-id","source_ids":["source-id-1"]}],"new":[["source-id-2","source-id-3"]],"split":[{"topic_id":"broad-existing-id","groups":[["source-id-4","source-id-5"],["source-id-6","source-id-7"]]}],"unassigned":["isolated-source-id"]}

Existing topics JSON:
%s

Sources JSON:
%s`

func BuildTopicCandidatePrompt(sourcesJSON, existingTopicsJSON string) string {
	return fmt.Sprintf(topicCandidatePrompt, existingTopicsJSON, sourcesJSON)
}

const topicCandidateConsolidationPrompt = `You consolidate topic candidates produced by independent source batches.

Merge candidates only when their representative prompts describe the same developer-facing work area and their edited files are compatible. Keep sibling pages, flows, commands, integrations, and subsystems separate. Never merge candidates assigned to different existing_topic_id values. Do not name or describe topics. Use every candidate_id exactly once.

Return only compact JSON:
{"groups":[["candidate-id-1","candidate-id-2"],["candidate-id-3"]]}

Candidates JSON:
%s`

func BuildTopicCandidateConsolidationPrompt(candidatesJSON string) string {
	return fmt.Sprintf(topicCandidateConsolidationPrompt, candidatesJSON)
}
