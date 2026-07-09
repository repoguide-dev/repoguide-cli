package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code hooks that replace the static CLAUDE.md instruction blocks with
// dynamic prompt injection: repoguide_get_repo_experience is suggested as soon
// as the user states a task (UserPromptSubmit), and repoguide_record_feedback
// is requested once before the agent finishes a session that used it (Stop).

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
	if hookStateExists(payload.SessionID, "prompted") {
		return nil
	}
	repoID, err := GitRepoID(cwd)
	if err != nil || repoID == "" {
		return nil
	}
	markHookState(payload.SessionID, "prompted")
	_, _ = io.WriteString(stdout, AgentInstructionBriefFor(repoID))
	return nil
}

// repoToolUsedThisSession reports whether a RepoGuide MCP tool was called in
// this repo since this session's first prompt. Claude Code never forwards the
// hook session_id to the MCP server, so tool use is marked per repo and
// correlated with the session by comparing marker mtimes.
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

// RunStopHook implements the Stop hook: once per session, if a RepoGuide MCP
// tool was actually called earlier in the session, block the agent from
// stopping once so it can call repoguide_record_feedback first.
func RunStopHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	var payload hookStopPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return nil
	}
	if payload.CWD != "" {
		cwd = payload.CWD
	}
	// stop_hook_active means this Stop hook already fired and blocked once
	// this turn; never block twice in a row or Claude Code loops forever.
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
	if hookStateExists(payload.SessionID, "feedback-asked") {
		return nil
	}
	markHookState(payload.SessionID, "feedback-asked")
	out, _ := json.Marshal(map[string]any{
		"decision": "block",
		"reason":   AgentFeedbackHookReasonFor(repoID),
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
	hooks["UserPromptSubmit"] = []any{claudeHookCommand(binPath, "prompt")}
	hooks["Stop"] = []any{claudeHookCommand(binPath, "stop")}
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
