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
      "notes": ["optional: concrete notes for test strategy"],
      "commands": []
    },
    "known_workflows": [
      "actionable step derived from read_before_edit_hints or prompt patterns"
    ],
    "avoid_wasting_time": [
      "concrete warning about what not to do first or what to skip"
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
- tests.notes: concrete, e.g. "Check response-shape tests before changing JSON fields." Leave empty if nothing specific.
- tests.commands: only commands copied verbatim from the commands input that run tests or validate changes, at most 3. Empty array if no such command was observed. Never invent commands.
- known_workflows: actionable steps, one per distinct recurring pattern - do not restate the same workflow twice. Derive from read_before_edit_hints, observed commands, prompt patterns (especially follow-up corrections), file co-edit signals. Format: verb phrase.
- avoid_wasting_time: concrete warnings, one per distinct pitfall - do not restate the same warning twice. Derive from commands with failures (name the command and that it failed here), follow-up corrections in prompts, context_tax file labels (do not start by reading these), search friction patterns, irrelevant seen_with files. Be specific - name files, commands, or file types when possible.
- risk_flags: apply any that fit from this enum:
  schema_sensitive (response/API/type shape often changes)
  low_test_signal (source changes often happen without tests)
  tests_as_spec (tests read for behavior but rarely changed)
  search_friction (agents often search before finding files)
  high_context_cost (this topic tends to burn many tokens)
  cross_subsystem (changes often span directories)
  config_coupled (config files often co-occur)
  doc_driven (docs/instructions often read before edits)
  generated_or_snapshot_adjacent (nearby generated/snapshot files may distract)
- when_to_use: 2-4 items, each <= 80 characters.
- prompt_keywords: 3-8 lowercase keywords or short phrases from the prompts. Drop noisy command words and malformed fragments.
- evidence.reason: <= 180 characters.
- evidence.representative_prompts: 1-3 short fragments.`

// SharedNamingExamples is the topic naming guidance shared by both prompts.
const SharedNamingExamples = `Topic naming examples:
Good: "MCP context routing", "Session telemetry parsing", "CLI auth flow", "Repository analysis reports"
Bad:  "CLI", "Backend", "Miscellaneous changes", "Improve code", "Update files"`

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
- prompts: actual user messages, most recent sessions first - primary signal for naming and known_workflows. Later prompts within a session are often corrections of what the agent got wrong; treat corrections as strong evidence for known_workflows and avoid_wasting_time.
- commands: shell commands agents actually ran in these sessions, with run and failure counts. Repeated successful commands are validation-workflow evidence; commands with failures are avoid_wasting_time evidence.
- top_edited_files: files most frequently written
- top_read_files: files most frequently read (often reference/schema files)
- test_files: test files associated with this directory
- seen_with: files from other directories that co-occurred, with session counts
- subsystem_labels: classification tags for the directory (active_development, friction, high_context, low_test_context)
- file_labels: per-file classification tags (hotspot, context_tax, friction, knowledge_hub, easy_surface, churn, test_as_spec, etc.)
- test_touch_signal: pre-computed signal - one of: "tests usually edited with source" | "tests used as spec" | "source often changed without tests" | "no clear test signal"
- read_before_edit_hints: observed patterns where one file is read before another is edited

Important rules:
- Return fewer topics than input groups when the evidence is weak, noisy, duplicated, or too ambiguous.
- It is better to return 4-8 strong topics than to force 12 weak ones.
- Skip groups entirely if you are not confident they represent a distinct recurring workflow.
- When session_count is 0, infer topics from file names, types, and the repository context above instead of prompts.
- You may merge multiple related groups into one topic when prompts and files clearly point to the same recurring work.
- Every topic object must include group_ids with one or more input group_id values.
- Keep output order aligned to the first referenced group_id for each topic.
- Do not invent files not present in the input data.
- When repository context with a file structure is provided, prefer file paths that appear in it. If a frequently-touched path from session data no longer exists in the file structure (e.g. the repo was reorganized), the sessions are stale: use the current path if the mapping is obvious, otherwise omit the file rather than emitting a dead path.
- Prefer evidence from recent sessions (see last_active and prompt order) over older sessions when they conflict.
- Prefer developer intent from prompts over directory names.
- seen_with entries are candidates for cross_cutting_files: include them if relevant, skip if incidental.
- If you are unsure, omit the topic instead of inventing a generic one.
- Ignore repeated command-like strings, corrupted prompt fragments, obvious tool noise, and low-signal identifiers unless reinforced by file evidence.
- Topic names must describe a recurring kind of work, not a folder name.
- Avoid vague names like "Backend updates", "CLI changes", "Code improvements".
- A topic is a domain area agents return to repeatedly, not a single task. If a group's prompts describe one specific fix, feature, or one-off request with no sign of repeat activity elsewhere in the input, do not mint a topic from it alone - fold it into a broader domain topic if one fits, otherwise omit it.
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
      "notes": ["optional: concrete notes for test strategy"],
      "commands": []
    },
    "known_workflows": [
      "actionable step derived from read_before_edit_hints or prompt patterns"
    ],
    "avoid_wasting_time": [
      "concrete warning about what not to do first or what to skip"
    ],
    "risk_flags": ["applicable flags from the enum"],
    "evidence": {
      "reason": "brief explanation based on prompts and touched files",
      "representative_prompts": ["short prompt fragment"]
    }
  }
]

Field rules:
- group_ids: 1+ input group_id values. Use each input group at most once across the whole output.
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
