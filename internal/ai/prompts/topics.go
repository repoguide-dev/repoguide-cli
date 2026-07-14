package prompts

import "fmt"

// SharedTopicOutputObject is the JSON schema for a single topic context object.
// Used by BuildFeedbackTopicPrompt; the multi-topic prompt embeds an equivalent inline.
const SharedTopicOutputObject = `{
    "name": "concise topic name, 3-6 words",
    "summary": "one sentence explaining what work this topic covers",
    "confidence": 0.88,
    "when_to_use": [
      "short condition for selecting this topic"
    ],
    "prompt_keywords": ["keyword", "phrase"],
    "start_here": [
      {
        "path": "path/to/file.go",
        "role": "short role label (e.g. main handler, type definitions, router)",
        "why": "one sentence explaining why to open this first"
      }
    ],
    "important_files": {
      "edit_targets": ["files usually changed for this topic"],
      "reference_files": ["files usually read to understand changes"],
      "test_files": ["tests associated with this topic"],
      "cross_cutting_files": ["files outside the main directory often co-touched"]
    },
    "tests": {
      "start_with": ["most relevant test files"],
      "signal": "one of the four test_touch_signal enum values",
      "notes": ["evidence-grounded textual test advice"],
      "commands": []
    },
    "known_workflows": [
      {"text": "short workflow summary", "steps": ["ordered observed step"], "files": ["observed/path.go"]}
    ],
    "scope_boundaries": [
      {"text": "evidence-backed architecture or ownership boundary", "files": ["optional/anchor.go"]}
    ],
    "avoid_wasting_time": [
      {"text": "specific evidence-backed warning", "severity": "info|warning|critical", "files": ["optional/anchor.go"]}
    ],
    "risk_flags": ["applicable flags from the enum"],
    "evidence": {
      "reason": "brief explanation based on prompts and touched files",
      "representative_prompts": ["short prompt fragment"]
    }
  }`

// SharedFieldRules describes output fields shared by both topic prompts.
const SharedFieldRules = `- name: 3-6 words, Title Case, e.g. "MCP context routing".
- summary: <= 160 characters.
- confidence: 0.80-1.00 clear recurring topic | 0.55-0.79 usable but mixed | 0.30-0.54 weak/noisy | <0.30 skip the topic.
- start_here: 1-5 files. Priority: frequently edited > knowledge_hub files (read before editing others) > schema/type files > tests_as_spec. Include role and why for each.
- important_files.edit_targets: from top_edited_files.
- important_files.reference_files: from top_read_files that are not in edit_targets.
- important_files.test_files: from test_files input.
- important_files.cross_cutting_files: relevant seen_with entries only.
- tests.signal: use the pre-computed test_touch_signal value unless you have strong evidence to override it.
- tests.notes: concise textual advice grounded in test files, observed behavior, or corrections. Omit generic testing advice.
- tests.commands: only commands copied verbatim from the commands input that run tests or validate changes, at most 3. Empty array if no such command was observed. Never invent commands.
- known_workflows: structured reusable workflows. text summarizes the pattern; steps preserve a useful observed sequence; files contain only paths present in the topic evidence. Omit steps when evidence supports only a textual observation.
- scope_boundaries: durable, evidence-backed ownership or architecture limits. Include file anchors when present.
- avoid_wasting_time: concrete warnings grounded in failed commands, corrections, context-tax files, stale paths, or repeated wrong starts. severity must be info, warning, or critical.
- risk_flags: compact evidence-backed labels such as schema_sensitive, low_test_signal, tests_as_spec, search_friction, high_context_cost, cross_subsystem, config_coupled, doc_driven, or generated_or_snapshot_adjacent.
- when_to_use: 2-4 items, each <= 80 characters.
- prompt_keywords: 3-8 lowercase keywords or short phrases from the prompts. Drop noisy command words and malformed fragments.
- evidence.reason: <= 180 characters.
- evidence.representative_prompts: 1-3 short fragments.`

// SharedNamingExamples is the topic naming guidance shared by both prompts.
const SharedNamingExamples = `Topic naming examples:
Good: "MCP context routing", "Session telemetry parsing", "CLI auth flow", "Repository analysis reports", "Payment retry handling", "Webhook event reconciliation"
Bad:  "CLI", "Backend", "Frontend", "UI features", "Miscellaneous changes", "Improve code", "Update files"`

const topicPromptRepoCtxTemplate = `Repository context (file structure and docs):
%s

`

const topicPromptTemplate = `You are generating routing topics from coding agent session telemetry for one software repository.

Each group contains sessions that mostly edited files in the same directory or nearby file cluster.
Your job is to infer the developer-facing work topics and produce compact, practical context objects that help future agents start work without exploring the repository from scratch.

Input fields per group:
- group_id: stable identifier that you must reference in output
- directory_hint: primary directory edited in these sessions
- session_count: how many sessions touched this group
- last_active: date of the most recent session in this group - older groups are more likely to contain stale file paths
- prompts: actual user messages, most recent sessions first - primary signal for naming, routing, and choosing observed files. Later corrections can narrow the intended domain.
- commands: shell commands agents actually ran in these sessions, with run and failure counts. Use exact observed commands only; failures may support specific warnings.
- top_edited_files: files most frequently written
- top_read_files: files most frequently read (often reference/schema files)
- test_files: test files associated with this directory
- seen_with: files from other directories that co-occurred, with session counts
- subsystem_labels: classification tags for the directory (active_development, friction, high_context, low_test_context)
- file_labels: per-file classification tags (hotspot, context_tax, friction, knowledge_hub, easy_surface, churn, test_as_spec, etc.)
- test_touch_signal: pre-computed signal - one of: "tests usually edited with source" | "tests used as spec" | "source often changed without tests" | "no clear test signal"
- read_before_edit_hints: observed patterns where one file is read before another is edited

Important rules:
- Return as many topics as the evidence supports - do not target a topic count. Emit every distinct recurring work area you can see, and no topic you cannot ground in the input.
- Skip groups entirely if you are not confident they represent a distinct recurring workflow.
- When session_count is 0, infer topics from file names, types, and the repository context above instead of prompts.
- You may merge multiple related groups into one topic when prompts and files clearly point to the same recurring work.
- You may also split one input group into multiple topics when the prompts, files, or workflows show distinct recurring areas within the same directory. Reuse the same group_id across those topics only when each topic has a clearly different focus.
- Every topic object must include group_ids with one or more input group_id values.
- Keep output order aligned to the first referenced group_id for each topic.
- Do not invent files not present in the input data.
- Use the whole group/bundle context when selecting files for a topic. Relevant files may come from top_edited_files, top_read_files, test_files, seen_with, file_labels, and read_before_edit_hints across the group, not only from the most obvious prompt wording.
- When repository context with a file structure is provided, prefer file paths that appear in it. If a frequently-touched path from session data no longer exists in the file structure (e.g. the repo was reorganized), the sessions are stale: use the current path if the mapping is obvious, otherwise omit the file rather than emitting a dead path.
- Prefer evidence from recent sessions (see last_active and prompt order) over older sessions when they conflict.
- Prefer developer intent from prompts over directory names.
- seen_with entries are candidates for cross_cutting_files: include them if relevant, skip if incidental.
- Do not use a layer bucket (for example "Cloud Web App Pages", "Cloud Backend API and Auth", or "CLI Commands and Init Flow") as the final topic name when the group contains two or more recurring named domains. Split it into those domains instead. A broad layer bucket is permitted only when the evidence truly has no narrower recurring intent.
- If you are unsure, omit the topic instead of inventing a generic one.
- Ignore repeated command-like strings, corrupted prompt fragments, obvious tool noise, and low-signal identifiers unless reinforced by file evidence.
- Treat this as retrieval, not coaching. The goal is to help an agent orient quickly with repository-specific experience, not to tell it how to implement the task.
- Prefer repo-specific nouns over advice: file paths, package names, mirrored modules, APIs, commands that actually failed, and distinctive terms from prompts are higher value than general instructions.
- Only include guidance that is clearly supported by repeated evidence in prompts, commands, or file patterns. If a statement would still sound plausible in many unrelated repositories, it is probably too generic - omit it.
- Do not output generic process advice such as "start with", "before editing", "run tests from", "update X first", "manually mirror changes", or "use git diff" unless that exact constraint is evidenced by the input as a recurring repo-specific pitfall.
- You may author evidence-grounded textual advice, structured workflows, scope boundaries, and warnings. Every file, step, command, and claim must be traceable to the supplied topic evidence; omit anything merely plausible.
- Create topics per domain area: each topic should map to one recurring business/domain concept, feature area, workflow, integration, or service boundary that an agent would intentionally choose.
- Topic names must describe a recurring kind of work, not a folder name.
- Prefer area-focused topics over layer buckets: name the concrete feature, workflow, page, service, or integration, not "frontend", "backend", "UI", or the whole app area.
- If one directory contains multiple recurring features, split them by developer intent and touched files instead of collapsing them into one broad layer topic.
- If two sets of sessions touch the same layer but different domains, create separate topics for those domains rather than one combined layer topic.
- For each domain topic, use the surrounding group/bundle evidence to pull in the larger relevant file context: include neighboring schema, shared state, validators, tests, and cross-cutting files when they materially support the domain.
- Avoid vague names like "Backend updates", "CLI changes", "Code improvements".
- A topic is a domain area agents return to repeatedly, not a single task. Judge recurrence across the group's whole prompt list, never from one prompt in isolation: prompts come from different sessions, so several separate one-off tasks that all touch the same domain (e.g. "add cart page", "persist cart to localStorage", "add cart quantity controls") are strong evidence for a topic on that domain - mint it. Only when a task stands alone with nothing else in the input touching its domain should you fold it into a broader topic if one fits, or omit it.
- Name and summary the topic by the domain it covers (e.g. "Session telemetry parsing"), not by the task that happened to generate the evidence (e.g. "Fix telemetry timestamp bug").
- Keep output compact - it will be injected directly into an agent's context window.

Return only valid JSON. No markdown, no comments.

Return a JSON array. Each object represents one topic and may reference one or more groups:

[
  {
    "group_ids": ["backend_server", "backend_model"],
    "name": "concise topic name, 3-6 words",
    "summary": "one sentence explaining what work this topic covers",
    "confidence": 0.88,
    "when_to_use": [
      "short condition for selecting this topic"
    ],
    "prompt_keywords": ["keyword", "phrase"],
    "start_here": [
      {
        "path": "path/to/file.go",
        "role": "short role label (e.g. main handler, type definitions, router)",
        "why": "one sentence explaining why to open this first"
      }
    ],
    "important_files": {
      "edit_targets": ["files usually changed for this topic"],
      "reference_files": ["files usually read to understand changes"],
      "test_files": ["tests associated with this topic"],
      "cross_cutting_files": ["files outside the main directory often co-touched"]
    },
    "tests": {
      "start_with": ["most relevant test files"],
      "signal": "one of the four test_touch_signal enum values",
      "notes": ["evidence-grounded textual test advice"],
      "commands": []
    },
    "known_workflows": [
      {"text": "short workflow summary", "steps": ["ordered observed step"], "files": ["observed/path.go"]}
    ],
    "scope_boundaries": [
      {"text": "evidence-backed architecture or ownership boundary", "files": ["optional/anchor.go"]}
    ],
    "avoid_wasting_time": [
      {"text": "specific evidence-backed warning", "severity": "info|warning|critical", "files": ["optional/anchor.go"]}
    ],
    "risk_flags": ["applicable flags from the enum"],
    "evidence": {
      "reason": "brief explanation based on prompts and touched files",
      "representative_prompts": ["short prompt fragment"]
    }
  }
]

Field rules:
 - group_ids: 1+ input group_id values. Reuse a group_id across multiple topics only when the same directory truly contains multiple distinct recurring areas; otherwise use each input group once.
` + SharedFieldRules + `

` + SharedNamingExamples + `

Groups JSON:
%s`

func BuildTopicPrompt(groupsJSON, repoCtx string) string {
	prefix := ""
	if repoCtx != "" {
		prefix = fmt.Sprintf(topicPromptRepoCtxTemplate, repoCtx)
	}
	return prefix + fmt.Sprintf(topicPromptTemplate, groupsJSON)
}
