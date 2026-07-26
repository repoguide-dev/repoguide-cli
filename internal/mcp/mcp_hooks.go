package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/config"
)

// Claude Code hooks that replace the static CLAUDE.md instruction blocks with
// dynamic prompt injection: repoguide_get_repo_experience is suggested as soon
// as the user states a task (UserPromptSubmit), and repoguide_record_feedback
// is nudged once, non-blockingly, at the end of a session that actually used
// RepoGuide (Stop). By default the nudge asks the agent to ask the user first,
// since feedback transmits task and repository metadata; config.AutoFeedback
// lets a user opt into silent auto-submission instead.

type hookPromptPayload struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
	CWD       string `json:"cwd"`
}

type hookStopPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	StopHookActive bool   `json:"stop_hook_active"`
}

func hookStateDir() string {
	return filepath.Join(RepoGuideDir(), "hook-state")
}

func hookStateFile(sessionID, name string) string {
	return filepath.Join(hookStateDir(), sessionID+"-"+name)
}

func hookStateExists(sessionID, name string) bool {
	if sessionID == "" {
		return false
	}
	_, err := os.Stat(hookStateFile(sessionID, name))
	return err == nil
}

func markHookState(sessionID, name string) {
	if sessionID == "" {
		return
	}
	_ = os.MkdirAll(hookStateDir(), 0o755)
	_ = os.WriteFile(hookStateFile(sessionID, name), nil, 0o644)
}

// RunPromptHook implements the UserPromptSubmit hook: on the first prompt of
// a session in a RepoGuide-activated repo, print the RepoGuide MCP workflow
// instruction so Claude calls repoguide_get_repo_experience for the task.
// Silently no-ops on anything unexpected — a hook must never block typing.
func RunPromptHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	var payload hookPromptPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return nil
	}
	if payload.CWD != "" {
		cwd = payload.CWD
	}
	if payload.SessionID == "" || strings.TrimSpace(payload.Prompt) == "" {
		return nil
	}
	repoID, err := GitRepoID(cwd)
	if err != nil || repoID == "" {
		return nil
	}
	// ponytail: re-emit until the agent actually calls a RepoGuide tool, rather
	// than once per session. A session whose first prompt is trivial ("hi", a
	// git question) used to burn the only shot and never route again.
	if hookStateExists(payload.SessionID, "prompted") && repoToolUsedThisSession(repoID, payload.SessionID) {
		return nil
	}
	// The "prompted" marker doubles as the session baseline for
	// repoToolUsedThisSession, so only stamp it once — rewriting it would move
	// the mtime forward and hide tool use from earlier in the same session.
	if !hookStateExists(payload.SessionID, "prompted") {
		markHookState(payload.SessionID, "prompted")
	}
	_, _ = io.WriteString(stdout, AgentInstructionBriefFor(repoID))
	return nil
}

// RunGeminiPromptHook emits Gemini CLI's structured BeforeAgent response. The
// hook protocol accepts context only as JSON, unlike Claude and Codex hooks.
func RunGeminiPromptHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	var payload hookPromptPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return nil
	}
	if payload.CWD != "" {
		cwd = payload.CWD
	}
	if payload.SessionID == "" || strings.TrimSpace(payload.Prompt) == "" {
		return nil
	}
	repoID, err := GitRepoID(cwd)
	if err != nil || repoID == "" {
		return nil
	}
	result := map[string]any{
		"hookSpecificOutput": map[string]any{
			"additionalContext": AgentInstructionBriefFor(repoID),
		},
	}
	return json.NewEncoder(stdout).Encode(result)
}

// repoToolUsedThisSession reports whether a RepoGuide MCP tool was called in
// this repo since this session's first prompt. Claude Code and Codex never
// forward the hook session_id to the MCP server, so tool use is marked per
// repo and correlated with the session by comparing marker mtimes.
func repoToolUsedThisSession(repoID, sessionID string) bool {
	used, err := os.Stat(hookStateFile(repoID, "tool-used"))
	if err != nil {
		return false
	}
	prompted, err := os.Stat(hookStateFile(sessionID, "prompted"))
	if err != nil {
		return false
	}
	return !used.ModTime().Before(prompted.ModTime())
}

// RunStopHook implements the Claude Code and Codex Stop hook: once per
// session, if a RepoGuide MCP tool was actually called earlier in the
// session, nudge the agent once to offer feedback before it stops. The
// stop_hook_active guard means this never fires twice in a row, so it can
// never hang a session — it is a single reminder, not an enforcement gate.
//
// Stop fires at the end of every turn, not just when the session truly ends
// — e.g. delegating to a background Task/Agent tool also stops the main
// turn. Nudging on that very first Stop (right after the routing call) reads
// as nonsense: feedback about work that hasn't happened yet. So the first
// Stop of a session is only ever recorded, never nudged; the nudge waits for
// a later Stop, by which point real work has had a chance to happen.
func RunStopHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	var payload hookStopPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return nil
	}
	if payload.CWD != "" {
		cwd = payload.CWD
	}
	if payload.StopHookActive || payload.SessionID == "" {
		return nil
	}
	repoID, err := GitRepoID(cwd)
	if err != nil || repoID == "" {
		return nil
	}
	if !repoToolUsedThisSession(repoID, payload.SessionID) {
		return nil
	}
	if !hookStateExists(payload.SessionID, "stop-seen") {
		markHookState(payload.SessionID, "stop-seen")
		return nil
	}
	if hookStateExists(payload.SessionID, "feedback-asked") {
		return nil
	}
	markHookState(payload.SessionID, "feedback-asked")
	auto := config.AutoFeedback()
	// hookSpecificOutput.additionalContext (not decision:"block") keeps the
	// turn going without Claude Code labeling it a "Stop hook error" — the
	// old decision:"block" form rendered as an error banner to the user even
	// with suppressOutput set, which is what this replaced.
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "Stop",
			"additionalContext": AgentFeedbackHookReasonFor(repoID, auto),
		},
	})
	_, _ = stdout.Write(out)
	return nil
}

// RunGeminiStopHook implements Gemini CLI's AfterAgent hook: the same
// one-time feedback nudge as RunStopHook, using Gemini's decision:"deny"
// instead of Claude/Codex's decision:"block". stop_hook_active still guards
// against firing twice in a row.
func RunGeminiStopHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	var payload hookStopPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return nil
	}
	if payload.CWD != "" {
		cwd = payload.CWD
	}
	if payload.StopHookActive || payload.SessionID == "" {
		return nil
	}
	repoID, err := GitRepoID(cwd)
	if err != nil || repoID == "" {
		return nil
	}
	if !repoToolUsedThisSession(repoID, payload.SessionID) {
		return nil
	}
	if !hookStateExists(payload.SessionID, "stop-seen") {
		markHookState(payload.SessionID, "stop-seen")
		return nil
	}
	if hookStateExists(payload.SessionID, "feedback-asked") {
		return nil
	}
	markHookState(payload.SessionID, "feedback-asked")
	out, _ := json.Marshal(map[string]any{
		"decision": "deny",
		"reason":   AgentFeedbackHookReasonFor(repoID, config.AutoFeedback()),
	})
	_, _ = stdout.Write(out)
	return nil
}

const (
	claudeHookPromptMarker = "mcp hook prompt"
	claudeHookStopMarker   = "mcp hook stop"
)

func claudeSettingsLocalPath(repoPath string) string {
	return filepath.Join(repoPath, ".claude", "settings.local.json")
}

func claudeHookCommand(binPath, event string) map[string]any {
	name := "RepoGuide hook"
	switch event {
	case "prompt":
		name = "RepoGuide task routing"
	case "stop":
		name = "RepoGuide feedback reminder"
	}
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"name":    name,
				"type":    "command",
				"command": "\"" + binPath + "\" mcp hook " + event,
				"timeout": 5,
			},
		},
	}
}

// InstallClaudeCodeHooks wires RepoGuide's UserPromptSubmit and Stop hooks into
// repoPath/.claude/settings.local.json, preserving any settings already there.
func InstallClaudeCodeHooks(repoPath, binPath string) error {
	path := claudeSettingsLocalPath(repoPath)
	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	hooks, _ := raw["hooks"].(map[string]any)
	if hooks == nil {
		hooks = make(map[string]any)
	}
	// Drop any prior RepoGuide prompt hook (this binary's or another install's)
	// but keep hooks the user configured on the same event.
	hooks["UserPromptSubmit"] = append(
		filterOutHookMarker(hooks["UserPromptSubmit"], claudeHookPromptMarker),
		claudeHookCommand(binPath, "prompt"),
	)
	// Drop any prior RepoGuide Stop hook (this binary's or another install's)
	// but keep hooks the user configured on the same event.
	hooks["Stop"] = append(
		filterOutHookMarker(hooks["Stop"], claudeHookStopMarker),
		claudeHookCommand(binPath, "stop"),
	)
	raw["hooks"] = hooks
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeJSON(path, raw)
}

// RemoveClaudeCodeHooks removes only the RepoGuide-managed hook entries from
// repoPath/.claude/settings.local.json, leaving any of the user's own hooks
// in place. Deletes the file if RepoGuide's entries were all it contained.
func RemoveClaudeCodeHooks(repoPath string) error {
	path := claudeSettingsLocalPath(repoPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	hooks, _ := raw["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	if kept := filterOutHookMarker(hooks["UserPromptSubmit"], claudeHookPromptMarker); len(kept) > 0 {
		hooks["UserPromptSubmit"] = kept
	} else {
		delete(hooks, "UserPromptSubmit")
	}
	if kept := filterOutHookMarker(hooks["Stop"], claudeHookStopMarker); len(kept) > 0 {
		hooks["Stop"] = kept
	} else {
		delete(hooks, "Stop")
	}
	if len(hooks) == 0 {
		delete(raw, "hooks")
	} else {
		raw["hooks"] = hooks
	}
	if len(raw) == 0 {
		return os.Remove(path)
	}
	return writeJSON(path, raw)
}

// filterOutHookMarker drops hook groups whose command contains marker,
// keeping any other hooks a user configured on the same event.
func filterOutHookMarker(entries any, marker string) []any {
	list, _ := entries.([]any)
	var kept []any
	for _, e := range list {
		group, ok := e.(map[string]any)
		if !ok {
			kept = append(kept, e)
			continue
		}
		hookList, _ := group["hooks"].([]any)
		var keptHooks []any
		for _, h := range hookList {
			hm, ok := h.(map[string]any)
			if !ok {
				keptHooks = append(keptHooks, h)
				continue
			}
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, marker) {
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 {
			continue
		}
		group["hooks"] = keptHooks
		kept = append(kept, group)
	}
	return kept
}

// migrateClaudeMDBlock strips the old static RepoGuide blocks from CLAUDE.md,
// now that Claude Code gets the same instructions via hooks instead.
func migrateClaudeMDBlock(repoPath string) {
	path := filepath.Join(repoPath, "CLAUDE.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	cleaned := removeFeedbackInstruction(removeMCPInstruction(string(data)))
	if cleaned == string(data) {
		return
	}
	if strings.TrimSpace(cleaned) == "" {
		_ = os.Remove(path)
		return
	}
	_ = os.WriteFile(path, []byte(cleaned), 0o644)
}
