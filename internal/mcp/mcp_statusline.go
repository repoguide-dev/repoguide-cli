package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
)

// Claude Code statusLine badge: chains onto whatever statusLine command is
// already configured (e.g. ponytail's) and appends a [REPOGUIDE] tag whenever
// the current repo has RepoGuide activated.

const statuslineChainMarker = "mcp statusline"

// RunStatuslineHook runs the previously-configured statusLine command (if
// any, base64-encoded in chainB64) with the original stdin, then appends the
// RepoGuide badge if the repo at the payload's cwd is RepoGuide-activated.
func RunStatuslineHook(stdin io.Reader, stdout io.Writer, chainB64 string) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return nil
	}

	if prevCmd, decErr := base64.StdEncoding.DecodeString(chainB64); decErr == nil && len(prevCmd) > 0 {
		c := exec.Command("sh", "-c", string(prevCmd))
		c.Stdin = bytes.NewReader(input)
		if out, runErr := c.Output(); runErr == nil {
			_, _ = stdout.Write(out)
		}
	}

	var payload struct {
		CWD       string `json:"cwd"`
		Workspace struct {
			CurrentDir string `json:"current_dir"`
		} `json:"workspace"`
	}
	if json.Unmarshal(input, &payload) != nil {
		return nil
	}
	cwd := payload.CWD
	if cwd == "" {
		cwd = payload.Workspace.CurrentDir
	}
	if cwd == "" {
		return nil
	}
	repoID, err := GitRepoID(cwd)
	if err != nil || repoID == "" {
		return nil
	}
	label := "RepoGuide Enabled"
	if planLabel := statuslinePlanLabel(cwd); planLabel != "" {
		label += " (" + planLabel + ")"
	}
	_, _ = io.WriteString(stdout, " \033[38;5;75m"+label+" ✓\033[0m")
	return nil
}

func statuslinePlanLabel(repoRoot string) string {
	if repopkg.RepoIsLocalMode(repoRoot) {
		return "Offline"
	}
	token, ok := clientauth.Load()
	if !ok {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(token.Plan)) {
	case "PRO":
		return "Pro"
	case "TEAM", "ADMIN":
		return "Team"
	default:
		return ""
	}
}

func statuslineCandidatePaths(repoPath string) []string {
	return []string{
		claudeSettingsLocalPath(repoPath),
		filepath.Join(repoPath, ".claude", "settings.json"),
		home(".claude", "settings.json"),
	}
}

// currentStatuslineCommand returns whatever statusLine command Claude Code
// would currently run for repoPath, checked in the same precedence Claude
// Code applies (project-local, project-shared, then user-global).
func currentStatuslineCommand(repoPath string) string {
	for _, path := range statuslineCandidatePaths(repoPath) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		sl, _ := raw["statusLine"].(map[string]any)
		if cmd, _ := sl["command"].(string); cmd != "" {
			return cmd
		}
	}
	return ""
}

// InstallClaudeCodeStatusline chains a [REPOGUIDE] badge onto whatever
// statusLine command is already configured, without touching that command's
// own file. No-op if already chained (idempotent across repeated installs).
func InstallClaudeCodeStatusline(repoPath, binPath string) error {
	prev := currentStatuslineCommand(repoPath)
	if strings.Contains(prev, statuslineChainMarker) {
		return nil
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(prev))
	command := "\"" + binPath + "\" mcp statusline --chain \"" + encoded + "\""

	path := claudeSettingsLocalPath(repoPath)
	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	raw["statusLine"] = map[string]any{
		"type":    "command",
		"command": command,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeJSON(path, raw)
}

// RemoveClaudeCodeStatusline removes the statusLine entry from
// repoPath/.claude/settings.local.json, but only if it's RepoGuide's own
// chained command - never a statusLine the user configured directly there.
func RemoveClaudeCodeStatusline(repoPath string) error {
	path := claudeSettingsLocalPath(repoPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw := make(map[string]any)
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	sl, _ := raw["statusLine"].(map[string]any)
	if sl == nil {
		return nil
	}
	cmd, _ := sl["command"].(string)
	if !strings.Contains(cmd, statuslineChainMarker) {
		return nil
	}
	delete(raw, "statusLine")
	if len(raw) == 0 {
		return os.Remove(path)
	}
	return writeJSON(path, raw)
}
