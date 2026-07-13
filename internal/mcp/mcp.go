package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// hintVersion is the version of the MCP instruction text embedded in the CLI.
// Increment whenever AgentInstructionBriefFor or AgentInstructionFor changes.
const hintVersion = 4

func hintVersionPath() string { return filepath.Join(RepoGuideDir(), "hints_version") }

func loadHintVersion() int {
	data, err := os.ReadFile(hintVersionPath())
	if err != nil {
		return 0
	}
	var v int
	fmt.Sscan(strings.TrimSpace(string(data)), &v)
	return v
}

func saveHintVersion(v int) error {
	path := hintVersionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", v)), 0o644)
}

// MaybeUpdateHints re-injects the MCP instruction block into all hinted repos
// when hintVersion has advanced past the last-written version in ~/.repoguide/hints_version.
func MaybeUpdateHints() {
	if loadHintVersion() >= hintVersion {
		return
	}
	cfg, err := LoadMCPConfig()
	if err != nil {
		return
	}
	for _, repoPath := range cfg.HintedRepos {
		_, _ = InstructRepo(repoPath)
		if _, err := os.Stat(filepath.Join(repoPath, "CLAUDE.md")); err == nil {
			_, _ = InstructRepoForClaude(repoPath)
		}
		_ = InjectFeedbackInstruction(repoPath)
	}
	_ = saveHintVersion(hintVersion)
}

// AgentInstructionBriefFor returns a short AGENTS.md block that tells the agent
// when to use RepoGuide as a lightweight repo-routing aid.
func AgentInstructionBriefFor(repoID string) string {
	return "<!-- repoguide:mcp-instruction - added automatically by RepoGuide (https://repoguide.dev/mcp), will not be committed -->\n\n" +
		"## RepoGuide MCP workflow\n\n" +
		"For a repository code task, make one lightweight RepoGuide routing call before broad search or implementation if you have not already done so for the current task in this session.\n\n" +
		"Call:\n\n" +
		"`repoguide_get_repo_experience({ \"task\": \"<copy the user's latest request exactly>\", \"repo_id\": \"" + repoID + "\" })`\n\n" +
		"Skip only when the request is clearly not about repository code, or when the user explicitly says not to use RepoGuide.\n\n" +
		"Default to one repoguide_get_repo_experience call per task/session. Do not repeat it on every user message or turn. Call again only when the task changes materially, the first route proves wrong, or the RepoGuide response explicitly requests necessary follow-up detail.\n\n" +
		"Treat the result as routing guidance, not permanent session context. Extract the relevant topic, files, pitfalls, and next checks, then verify against the current source.\n\n" +
		"<!-- /repoguide:mcp-instruction -->\n"
}

// UnderstandTaskResponse returns the repoguide_get_repo_experience tool response:
// a plain explanation of all available tools with the repo ID baked in.
func UnderstandTaskResponse(repoID string) string {
	id := repoID
	if id == "" {
		id = "<repo_id>"
	}
	return "RepoGuide MCP workflow for this repository (repo_id: \"" + id + "\"):\n\n" +
		"Start here once per task/session for any non-trivial coding task, not once per message or turn:\n" +
		"`repoguide_get_repo_experience` with `{ \"task\": \"<your task>\", \"repo_id\": \"" + id + "\" }`\n\n" +
		"It returns bootstrap context for the task: orientation, likely topic, files to open first, test guidance, search guidance, and known risks.\n\n" +
		"If the response asks for a `topic_id`, call `repoguide_get_repo_experience` again with that `topic_id` before broad repo search or edits.\n\n" +
		"Other tools are follow-ups, not the entry point:\n" +
		"- `repoguide_list_topics` when you need candidate topics\n" +
		"- `repoguide_get_test_context` when you need deeper test strategy for a chosen topic\n" +
		"- `repoguide_get_search_context` when you need reliable search queries for a chosen topic\n" +
		"- `repoguide_record_feedback` before ending a session where RepoGuide helped\n\n" +
		"Checked-in `AGENTS.md` / `CLAUDE.md` instructions are optional accelerators. The MCP tool descriptions and responses are designed to stand on their own."
}

// AgentInstructionFor returns the full tool-usage instructions with the repo ID baked in.
// Written to AGENTS.md / CLAUDE.md by `repoguide mcp instruct`.
func AgentInstructionFor(repoID string) string {
	return `<!-- repoguide:mcp-instruction - added automatically by RepoGuide (https://repoguide.dev/mcp), will not be committed -->

## RepoGuide MCP usage

Use RepoGuide MCP as a lightweight repository-specific routing and diagnostic aid.

Call ` + "`repoguide_get_repo_experience`" + ` when repo-specific context would materially help, especially when:
- the relevant files or subsystem are unclear
- the task could match multiple areas of the repo
- broad repo search would otherwise be needed
- prior implementation patterns, tests, or pitfalls may matter
- the first diagnostic path fails or seems incomplete

` + "```json" + `
{ "task": "<the user's coding task, copied faithfully>", "repo_id": "` + repoID + `" }
` + "```" + `

Default to at most one RepoGuide call per task/session. Do not repeat it on every user message or turn. Make another call only when the task changes materially, the initial route is wrong, or the response explicitly asks for necessary follow-up detail.

Treat the response as routing guidance, not permanent session context. Extract the useful topic, files, workflows, risks, tests, and search queries, then verify against the current source before changing behavior.

If RepoGuide is unavailable, proceed without it:
- 401/403: tell the user to run ` + "`repoguide login`" + ` and retry
- 404: tell the user to run ` + "`repoguide repo fix`" + ` in the repo and retry
- 5xx or network error: RepoGuide is unavailable
- other: tell the user to run ` + "`repoguide doctor`" + `

<!-- /repoguide:mcp-instruction -->
`
}

type MCPConfig struct {
	ActivatedRepos []string `json:"activatedRepos"`
	HintedRepos    []string `json:"hintedRepos,omitempty"`
}

func mcpConfigPath() string {
	return filepath.Join(RepoGuideDir(), "mcp.json")
}

func LoadMCPConfig() (MCPConfig, error) {
	data, err := os.ReadFile(mcpConfigPath())
	if os.IsNotExist(err) {
		return MCPConfig{}, nil
	}
	if err != nil {
		return MCPConfig{}, err
	}
	var cfg MCPConfig
	return cfg, json.Unmarshal(data, &cfg)
}

func SaveMCPConfig(cfg MCPConfig) error {
	path := mcpConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func IsRepoActivated(cfg MCPConfig, repoPath string) bool {
	return slices.Contains(cfg.ActivatedRepos, repoPath)
}

func SetRepoActivated(cfg *MCPConfig, repoPath string) {
	if !IsRepoActivated(*cfg, repoPath) {
		cfg.ActivatedRepos = append(cfg.ActivatedRepos, repoPath)
	}
}

func UnsetRepoActivated(cfg *MCPConfig, repoPath string) {
	cfg.ActivatedRepos = slices.DeleteFunc(cfg.ActivatedRepos, func(r string) bool {
		return r == repoPath
	})
}

func IsRepoHinted(cfg MCPConfig, repoPath string) bool {
	return slices.Contains(cfg.HintedRepos, repoPath)
}

func SetRepoHinted(cfg *MCPConfig, repoPath string) {
	if !IsRepoHinted(*cfg, repoPath) {
		cfg.HintedRepos = append(cfg.HintedRepos, repoPath)
	}
}

func UnsetRepoHinted(cfg *MCPConfig, repoPath string) {
	cfg.HintedRepos = slices.DeleteFunc(cfg.HintedRepos, func(r string) bool {
		return r == repoPath
	})
}

func ActivateMCPRepo(repoPath string) error {
	repoID, err := gitOutputAt(repoPath, "config", "--get", "repoguide.repoId")
	if err != nil || repoID == "" {
		return ErrRepoNotInitialized
	}
	storeDir := filepath.Join(RepoGuideDir(), "repos", repoID)
	if _, err := SetManagedCommitHooks(storeDir, repoPath, true); err != nil {
		return err
	}
	cfg, _ := LoadMCPConfig()
	SetRepoActivated(&cfg, repoPath)
	_ = SaveMCPConfig(cfg)
	return nil
}

func ActivateMCPRepoWithInstructions(repoPath string) (string, error) {
	if err := ActivateMCPRepo(repoPath); err != nil {
		return "", err
	}
	filename, err := InstructRepo(repoPath)
	if err != nil {
		return "", err
	}
	_ = InjectFeedbackInstruction(repoPath)
	HideInjectedFiles(repoPath)
	cfg, _ := LoadMCPConfig()
	SetRepoHinted(&cfg, repoPath)
	_ = SaveMCPConfig(cfg)
	return filename, nil
}

// InstructRepo writes the RepoGuide MCP instruction block to AGENTS.md
// (created if absent). Returns the filename used.
func InstructRepo(repoPath string) (string, error) {
	repoID, _ := gitOutputAt(repoPath, "config", "--get", "repoguide.repoId")
	instruction := AgentInstructionBriefFor(repoID)
	path := filepath.Join(repoPath, "AGENTS.md")
	existing, _ := os.ReadFile(path)
	return "AGENTS.md", os.WriteFile(path, []byte(injectMCPInstruction(string(existing), instruction)), 0644)
}

// InstructRepoForClaude wires RepoGuide into Claude Code via UserPromptSubmit
// and Stop hooks (see mcp_hooks.go) instead of a static CLAUDE.md block: the
// routing instruction is injected once the user's task is clear, and the
// feedback instruction once before the agent ends a session that used it.
// Any pre-existing static block in CLAUDE.md is removed. Returns the filename used.
func InstructRepoForClaude(repoPath string) (string, error) {
	repoID, _ := gitOutputAt(repoPath, "config", "--get", "repoguide.repoId")
	if repoID == "" {
		return "", ErrRepoNotInitialized
	}
	migrateClaudeMDBlock(repoPath)
	binPath := RepoGuideBinaryPath()
	if err := InstallClaudeCodeHooks(repoPath, binPath); err != nil {
		return "", err
	}
	_ = InstallClaudeCodeStatusline(repoPath, binPath)
	return ".claude/settings.local.json", nil
}

// RemoveMCPInstructionDry returns the filenames that contain the MCP instruction
// block without modifying them, joined by " and ", or "" if not found.
func RemoveMCPInstructionDry(repoPath string) (string, error) {
	var found []string
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(repoPath, name))
		if err != nil {
			continue
		}
		if removeMCPInstruction(string(data)) != string(data) {
			found = append(found, name)
		}
	}
	return strings.Join(found, " and "), nil
}

// RemoveMCPInstruction removes the RepoGuide MCP instruction block from
// AGENTS.md and CLAUDE.md in repoPath. Returns the filenames modified
// (joined by " and "), or "" if the block was not found in either.
func RemoveMCPInstruction(repoPath string) (string, error) {
	var modified []string
	var firstErr error
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(repoPath, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cleaned := removeMCPInstruction(string(data))
		if cleaned == string(data) {
			continue
		}
		var werr error
		if strings.TrimSpace(cleaned) == "" {
			werr = os.Remove(path)
		} else {
			werr = os.WriteFile(path, []byte(cleaned), 0644)
		}
		if werr != nil && firstErr == nil {
			firstErr = werr
		}
		modified = append(modified, name)
	}
	return strings.Join(modified, " and "), firstErr
}

// RemoveAllInstructions removes both MCP and feedback instruction blocks from
// AGENTS.md and CLAUDE.md in repoPath. Returns the filenames modified or "".
func RemoveAllInstructions(repoPath string) (string, error) {
	var modified []string
	var firstErr error
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(repoPath, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cleaned := removeFeedbackInstruction(removeMCPInstruction(string(data)))
		if cleaned == string(data) {
			continue
		}
		var werr error
		if strings.TrimSpace(cleaned) == "" {
			werr = os.Remove(path)
		} else {
			werr = os.WriteFile(path, []byte(cleaned), 0644)
		}
		if werr != nil && firstErr == nil {
			firstErr = werr
		}
		modified = append(modified, name)
	}
	return strings.Join(modified, " and "), firstErr
}

func removeMCPInstruction(existing string) string {
	const marker = "## RepoGuide MCP usage"
	const openMarker = "<!-- repoguide:mcp-instruction"
	const closeMarker = "<!-- /repoguide:mcp-instruction -->"

	findStart := func(s string) int {
		if idx := strings.Index(s, openMarker); idx >= 0 {
			if h := strings.LastIndex(s[:idx], "## "); h >= 0 {
				return h
			}
			return idx
		}
		return strings.Index(s, marker)
	}

	start := findStart(existing)
	if start < 0 {
		return existing
	}

	// prefer precise removal using closing sentinel
	if end := strings.Index(existing[start:], closeMarker); end >= 0 {
		tail := existing[start+end+len(closeMarker):]
		tail = strings.TrimLeft(tail, "\n")
		head := strings.TrimRight(existing[:start], "\n")
		if tail == "" {
			return restoreTrailingNewline(head)
		}
		if head == "" {
			return tail
		}
		return head + "\n\n" + tail
	}

	// fallback: remove until next ## heading
	after := existing[start+len(marker):]
	end := strings.Index(after, "\n## ")
	if end < 0 {
		return restoreTrailingNewline(strings.TrimRight(existing[:start], "\n"))
	}
	return existing[:start] + existing[start+len(marker)+end+1:]
}

func ClearMCPConfig() error {
	return os.Remove(mcpConfigPath())
}

func agentFeedbackInstructionBody(repoID string) string {
	return "## RepoGuide feedback\n\n" +
		"**Always** call this after completing any non-trivial repository task where RepoGuide was used - specifically after committing changes or when the task is done:\n\n" +
		"`repoguide_record_feedback({ \"repo_id\": \"" + repoID + "\", \"task\": \"<latest user request>\", \"stars\": 1-5, \"helpfulness\": \"none|low|medium|high\", \"helped_with\": [\"request_interpretation\",\"topic_routing\",\"file_discovery\",\"prior_decisions\",\"test_strategy\",\"avoided_wrong_path\"], \"quote\": \"One short sentence\", \"missing_context\": \"Optional\" })`\n\n" +
		"This is how RepoGuide learns which context is useful. Report whether the context helped, what it helped with, and what was missing. Do not skip this step even if RepoGuide provided little help - that signal matters too.\n\n" +
		"**Call immediately** (before proceeding further) if: the topic was not found, the wrong topic was selected, or the context was not helpful at all. Use `stars: 1`, `helpfulness: \"none\"`, and describe what was missing in `missing_context`. Early feedback lets RepoGuide self-correct before wasting more context on the wrong path.\n"
}

// AgentFeedbackInstructionFor returns the feedback block written to AGENTS.md and CLAUDE.md.
func AgentFeedbackInstructionFor(repoID string) string {
	return "<!-- repoguide:feedback-instruction - added automatically by RepoGuide, will not be committed -->\n\n" +
		agentFeedbackInstructionBody(repoID) +
		"\n<!-- /repoguide:feedback-instruction -->\n"
}

// AgentFeedbackHookReasonFor returns the plain-text feedback instruction shown
// by Claude Code's Stop hook.
func AgentFeedbackHookReasonFor(repoID string) string {
	return "Call `repoguide_record_feedback` before finishing this task."
}

// InjectFeedbackInstruction appends (or replaces) the feedback block in
// AGENTS.md, for agents other than Claude Code. Claude Code gets the same
// instruction dynamically from the Stop hook installed by InstructRepoForClaude.
func InjectFeedbackInstruction(repoPath string) error {
	repoID, _ := gitOutputAt(repoPath, "config", "--get", "repoguide.repoId")
	instruction := AgentFeedbackInstructionFor(repoID)
	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	existing, _ := os.ReadFile(agentsPath)
	return os.WriteFile(agentsPath, []byte(injectFeedbackInstruction(string(existing), instruction)), 0644)
}

func injectFeedbackInstruction(existing, instruction string) string {
	const openMarker = "<!-- repoguide:feedback-instruction"
	const closeMarker = "<!-- /repoguide:feedback-instruction -->"
	if start := strings.Index(existing, openMarker); start >= 0 {
		if end := strings.Index(existing[start:], closeMarker); end >= 0 {
			tail := strings.TrimLeft(existing[start+end+len(closeMarker):], "\n")
			if tail == "" {
				return existing[:start] + instruction
			}
			return existing[:start] + instruction + "\n" + tail
		}
	}
	if existing == "" {
		return instruction
	}
	return existing + "\n" + instruction
}

func removeFeedbackInstruction(existing string) string {
	const openMarker = "<!-- repoguide:feedback-instruction"
	const closeMarker = "<!-- /repoguide:feedback-instruction -->"

	start := strings.Index(existing, openMarker)
	if start < 0 {
		return existing
	}
	end := strings.Index(existing[start:], closeMarker)
	if end < 0 {
		return existing
	}
	tail := strings.TrimLeft(existing[start+end+len(closeMarker):], "\n")
	head := strings.TrimRight(existing[:start], "\n")
	if strings.TrimSpace(tail) == "" {
		return restoreTrailingNewline(head)
	}
	if head == "" {
		return tail
	}
	return head + "\n\n" + tail
}

// restoreTrailingNewline ensures non-empty content ends with exactly one newline.
func restoreTrailingNewline(s string) string {
	if s == "" {
		return s
	}
	return s + "\n"
}

func injectMCPInstruction(existing, instruction string) string {
	const marker = "## RepoGuide MCP usage"
	const openMarker = "<!-- repoguide:mcp-instruction"
	const closeMarker = "<!-- /repoguide:mcp-instruction -->"

	findStart := func(s string) int {
		if idx := strings.Index(s, openMarker); idx >= 0 {
			if h := strings.LastIndex(s[:idx], "## "); h >= 0 {
				return h
			}
			return idx
		}
		return strings.Index(s, marker)
	}

	if start := findStart(existing); start >= 0 {
		// prefer precise replacement using closing sentinel
		if end := strings.Index(existing[start:], closeMarker); end >= 0 {
			tail := existing[start+end+len(closeMarker):]
			tail = strings.TrimLeft(tail, "\n")
			if tail == "" {
				return existing[:start] + instruction
			}
			return existing[:start] + instruction + "\n" + tail
		}
		// fallback: replace until next ## heading
		after := existing[start+len(marker):]
		end := strings.Index(after, "\n## ")
		if end < 0 {
			return existing[:start] + instruction
		}
		return existing[:start] + instruction + "\n" + existing[start+len(marker)+end+1:]
	}

	if existing == "" {
		return instruction
	}
	return instruction + "\n" + existing
}
