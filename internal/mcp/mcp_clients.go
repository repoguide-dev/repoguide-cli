package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// These identifiers are overridden by the local development build. Keeping
// them distinct lets the local and released CLIs be installed side by side.
var mcpServerName = "repoguide"
var repoguideMarketplace = "repoguide"
var mcpPluginName = "repoguide"

type MCPClientResult struct {
	Name       string
	Detected   bool
	Configured bool
	ConfigPath string // empty if configured via CLI
	Err        error
}

type mcpClientDef struct {
	name         string
	detect       func() bool
	isConfigured func() bool
	install      func(string, bool) (string, error)
}

func allMCPClientDefs() []mcpClientDef {
	return []mcpClientDef{
		{"Claude Code", detectClaudeCodeMCP, isClaudePluginConfigured, func(bin string, _ bool) (string, error) {
			_ = RemoveViaMCPRemove("claude") // migrate away from old mcp-add entry
			return InstallClaudePlugin(bin)
		}},
		{"Codex", detectCodexMCP, isCodexPluginConfigured, func(bin string, installHooks bool) (string, error) {
			_ = RemoveViaMCPRemove("codex") // migrate away from old mcp-add entry
			return "", installCodexPlugin(bin, installHooks)
		}},
		{"Cursor", detectCursorMCP, isCursorMCPConfigured, func(bin string, _ bool) (string, error) {
			path := cursorMCPConfigPath()
			return path, patchMCPJSON(path, bin)
		}},
		{"OpenCode", detectOpencodeMCP, isOpencodeMCPConfigured, func(bin string, _ bool) (string, error) {
			path := opencodeConfigPath()
			return path, patchOpencodeMCPJSON(path, bin)
		}},
		{"GitHub Copilot", detectCopilotMCP, isCopilotMCPConfigured, func(bin string, _ bool) (string, error) {
			path := copilotMCPConfigPath()
			return path, patchCopilotMCPJSON(path, bin)
		}},
		{"Gemini CLI", detectGeminiMCP, isGeminiMCPConfigured, func(bin string, _ bool) (string, error) {
			path := geminiMCPConfigPath()
			return path, patchGeminiMCPJSON(path, bin)
		}},
	}
}

func isClaudePluginConfigured() bool {
	_, err := os.Stat(home(".claude", "skills", mcpPluginName, ".mcp.json"))
	return err == nil
}

func isCodexPluginConfigured() bool {
	// Check for the repoguide marketplace entry in codex config.
	data, err := os.ReadFile(home(".codex", "config.toml"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"`+mcpPluginName+`@`+repoguideMarketplace+`"`)
}

func isCursorMCPConfigured() bool {
	data, err := os.ReadFile(cursorMCPConfigPath())
	if err != nil {
		return false
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	servers, _ := raw["mcpServers"].(map[string]any)
	_, ok := servers[mcpServerName]
	return ok
}

func isOpencodeMCPConfigured() bool {
	data, err := os.ReadFile(opencodeConfigPath())
	if err != nil {
		return false
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	servers, _ := raw["mcp"].(map[string]any)
	_, ok := servers[mcpServerName]
	return ok
}

func isCopilotMCPConfigured() bool {
	data, err := os.ReadFile(copilotMCPConfigPath())
	if err != nil {
		return false
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	servers, _ := raw["mcpServers"].(map[string]any)
	_, ok := servers[mcpServerName]
	return ok
}

func isGeminiMCPConfigured() bool {
	data, err := os.ReadFile(geminiMCPConfigPath())
	if err != nil {
		return false
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	servers, _ := raw["mcpServers"].(map[string]any)
	_, ok := servers[mcpServerName]
	return ok
}

// DetectMCPClients returns names of installed MCP-capable clients.
func DetectMCPClients() []string {
	var detected []string
	for _, c := range allMCPClientDefs() {
		if c.detect() {
			detected = append(detected, c.name)
		}
	}
	return detected
}

// DetectConfiguredMCPClients returns names of clients that are installed AND
// already have RepoGuide configured.
func DetectConfiguredMCPClients() []string {
	var out []string
	for _, c := range allMCPClientDefs() {
		if c.detect() && c.isConfigured() {
			out = append(out, c.name)
		}
	}
	return out
}

// InstallMCPClients detects installed MCP-capable clients and configures the
// repoguide MCP server in each. Returns one result per detected client.
// If nothing is detected, fallback contains a JSON config block to paste manually.
func InstallMCPClients() ([]MCPClientResult, string) {
	return InstallSelectedMCPClients(DetectMCPClients(), true)
}

// InstallSelectedMCPClients installs only the named clients.
func InstallSelectedMCPClients(names []string, installHooks bool) ([]MCPClientResult, string) {
	self := RepoGuideBinaryPath()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	results := make([]MCPClientResult, 0, len(names))
	for _, c := range allMCPClientDefs() {
		if !nameSet[c.name] {
			continue
		}
		configPath, err := c.install(self, installHooks)
		results = append(results, MCPClientResult{
			Name:       c.name,
			Detected:   true,
			Configured: err == nil,
			ConfigPath: configPath,
			Err:        err,
		})
	}

	fallback := ""
	if len(results) == 0 {
		fallback = fallbackMCPBlock(self)
	}
	return results, fallback
}

func RepoGuideBinaryPath() string {
	if self, err := os.Executable(); err == nil {
		return self
	}
	if p, err := exec.LookPath("repoguide"); err == nil {
		return p
	}
	return "repoguide"
}

// InstallViaMCPAdd runs `<cli> mcp add --transport stdio repoguide -- <bin> mcp serve`
func InstallViaMCPAdd(cli, binPath string) error {
	cmd := exec.Command(cli, "mcp", "add", "--transport", "stdio", mcpServerName, "--", binPath, "mcp", "serve")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// InstallClaudePlugin creates ~/.claude/skills/repoguide/ as a skills-dir plugin
// with a .claude-plugin/plugin.json manifest and .mcp.json server config.
func InstallClaudePlugin(binPath string) (string, error) {
	dir := home(".claude", "skills", mcpPluginName)
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		return dir, err
	}
	manifest := map[string]any{
		"$schema":     "https://anthropic.com/claude-code/plugin.schema.json",
		"name":        mcpPluginName,
		"version":     "1.0.2",
		"description": "Repo-specific context routing. Calls repoguide_get_repo_experience once per task/session, not once per message.",
		"author": map[string]any{
			"name": "RepoGuide",
			"url":  "https://repoguide.dev",
		},
	}
	if err := writeJSON(filepath.Join(dir, ".claude-plugin", "plugin.json"), manifest); err != nil {
		return dir, err
	}
	mcp := map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{
				"command": binPath,
				"args":    []string{"mcp", "serve"},
			},
		},
	}
	return dir, writeJSON(filepath.Join(dir, ".mcp.json"), mcp)
}

func uninstallClaudePlugin() error {
	return os.RemoveAll(home(".claude", "skills", mcpPluginName))
}

const legacyHindsightMarketplace = "hindsight"

// installCodexPlugin writes a local marketplace at ~/.repoguide/codex-marketplace/,
// registers it with codex, then installs the repoguide plugin from it.
func installCodexPlugin(binPath string, installHooks bool) error {
	marketDir := filepath.Join(RepoGuideDir(), "codex-marketplace")
	pluginDir := filepath.Join(marketDir, "plugins", mcpPluginName)

	if err := os.MkdirAll(filepath.Join(marketDir, ".agents", "plugins"), 0o755); err != nil {
		return err
	}
	marketplace := codexMarketplaceManifest()
	if err := writeJSON(filepath.Join(marketDir, ".agents", "plugins", "marketplace.json"), marketplace); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(pluginDir, ".codex-plugin"), 0o755); err != nil {
		return err
	}
	plugin := codexPluginManifest()
	if err := writeJSON(filepath.Join(pluginDir, ".codex-plugin", "plugin.json"), plugin); err != nil {
		return err
	}
	mcp := codexPluginMCPConfig(binPath)
	if err := writeJSON(filepath.Join(pluginDir, ".mcp.json"), mcp); err != nil {
		return err
	}
	if installHooks {
		if err := os.MkdirAll(filepath.Join(pluginDir, "hooks"), 0o755); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(pluginDir, "hooks", "hooks.json"), codexPluginHooksConfig(binPath)); err != nil {
			return err
		}
	} else {
		_ = os.RemoveAll(filepath.Join(pluginDir, "hooks"))
	}

	removeLegacyCodexPlugin()

	// Register marketplace (idempotent — ignore error if already present).
	_ = exec.Command("codex", "plugin", "marketplace", "add", marketDir).Run()

	// Codex does not reload an already-installed plugin when its local
	// marketplace files change. Remove it first so a repeated `mcp install`
	// picks up the current MCP command and hook definition. This is especially
	// important for the local build, whose plugin and binary are named
	// repoguide-local and must not keep using the released CLI's hook command.
	_ = exec.Command("codex", "plugin", "remove", mcpPluginName+"@"+repoguideMarketplace).Run()

	cmd := exec.Command("codex", "plugin", "add", mcpPluginName+"@"+repoguideMarketplace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func uninstallCodexPlugin() error {
	_ = exec.Command("codex", "plugin", "remove", mcpPluginName+"@"+repoguideMarketplace).Run()
	removeLegacyCodexPlugin()
	cmd := exec.Command("codex", "plugin", "marketplace", "remove", repoguideMarketplace)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func codexMarketplaceManifest() map[string]any {
	return map[string]any{
		"name": repoguideMarketplace,
		"interface": map[string]any{
			"displayName": "RepoGuide",
		},
		"plugins": []map[string]any{{
			"name": mcpPluginName,
			"source": map[string]any{
				"source": "local",
				"path":   "./plugins/" + mcpPluginName,
			},
			"policy": map[string]any{
				"installation":   "AVAILABLE",
				"authentication": "ON_INSTALL",
				"products":       []string{"CODEX"},
			},
			"category": "Engineering",
		}},
	}
}

func codexPluginManifest() map[string]any {
	return map[string]any{
		"name":        mcpPluginName,
		"version":     "1.0.2",
		"description": "Repo-specific context routing for Codex.",
		"author": map[string]any{
			"name": "RepoGuide",
			"url":  "https://repoguide.dev",
		},
		"homepage":   "https://repoguide.dev",
		"repository": "https://repoguide.dev",
		"keywords":   []string{"mcp", "context", "routing", "repository"},
		"interface": map[string]any{
			"displayName":       "RepoGuide",
			"shortDescription":  "Repo-specific context routing",
			"longDescription":   "RepoGuide gives Codex repository-specific routing context so the model can start in the right files, avoid known dead ends, and use the right tests for the task.",
			"developerName":     "RepoGuide",
			"category":          "Engineering",
			"capabilities":      []string{"Interactive", "Read"},
			"websiteURL":        "https://repoguide.dev",
			"privacyPolicyURL":  "https://repoguide.dev/privacy",
			"termsOfServiceURL": "https://repoguide.dev/terms",
			"brandColor":        "#0F766E",
		},
	}
}

func codexPluginMCPConfig(binPath string) map[string]any {
	return map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{
				"command": binPath,
				"args":    []string{"mcp", "serve"},
			},
		},
	}
}

func codexPluginHooksConfig(binPath string) map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []map[string]any{{
				"hooks": []map[string]any{{
					"name":    "RepoGuide task routing",
					"type":    "command",
					"command": "\"" + binPath + "\" mcp hook prompt",
					"timeout": 5,
				}},
			}},
		},
	}
}

func removeLegacyCodexPlugin() {
	_ = exec.Command("codex", "plugin", "remove", legacyHindsightMarketplace+"@"+legacyHindsightMarketplace).Run()
	_ = exec.Command("codex", "plugin", "marketplace", "remove", legacyHindsightMarketplace).Run()
}

// --- Claude Code ---

func detectClaudeCodeMCP() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// --- Codex ---

func detectCodexMCP() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// --- Cursor ---

func detectCursorMCP() bool {
	for _, p := range []string{
		home("Library", "Application Support", "Cursor"),
		home(".cursor"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func cursorMCPConfigPath() string {
	return home(".cursor", "mcp.json")
}

func patchMCPJSON(path, binPath string) error {
	// read existing as raw map to preserve unknown fields
	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	servers, _ := raw["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers[mcpServerName] = map[string]any{
		"command": binPath,
		"args":    []string{"mcp", "serve"},
	}
	raw["mcpServers"] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// --- OpenCode ---

func detectOpencodeMCP() bool {
	if _, err := exec.LookPath("opencode"); err == nil {
		return true
	}
	for _, p := range []string{
		home(".config", "opencode"),
		home(".local", "share", "opencode"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func opencodeConfigPath() string {
	return home(".config", "opencode", "opencode.json")
}

// patchOpencodeMCPJSON registers repoguide under the "mcp" key of OpenCode's
// config (https://opencode.ai/docs/mcp-servers/) - OpenCode has no separate
// plugin-manifest format for MCP servers, so this entry is the install.
func patchOpencodeMCPJSON(path, binPath string) error {
	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	servers, _ := raw["mcp"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers[mcpServerName] = map[string]any{
		"type":    "local",
		"command": []string{binPath, "mcp", "serve"},
		"enabled": true,
	}
	raw["mcp"] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// --- GitHub Copilot ---

func detectCopilotMCP() bool {
	if _, err := exec.LookPath("copilot"); err == nil {
		return true
	}
	_, err := os.Stat(home(".copilot"))
	return err == nil
}

func copilotMCPConfigPath() string {
	return home(".copilot", "mcp-config.json")
}

// patchCopilotMCPJSON registers repoguide under the "mcpServers" key of Copilot
// CLI's config (https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers).
func patchCopilotMCPJSON(path, binPath string) error {
	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	servers, _ := raw["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers[mcpServerName] = map[string]any{
		"type":    "local",
		"command": binPath,
		"args":    []string{"mcp", "serve"},
		"tools":   []string{"*"},
	}
	raw["mcpServers"] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func unpatchCopilotMCPJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if servers, ok := raw["mcpServers"].(map[string]any); ok {
		delete(servers, mcpServerName)
		if len(servers) == 0 {
			delete(raw, "mcpServers")
		}
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// --- Gemini CLI ---

func detectGeminiMCP() bool {
	if _, err := exec.LookPath("gemini"); err == nil {
		return true
	}
	_, err := os.Stat(home(".gemini"))
	return err == nil
}

func geminiMCPConfigPath() string {
	return home(".gemini", "settings.json")
}

// patchGeminiMCPJSON registers repoguide under the "mcpServers" key of Gemini
// CLI's settings.json (https://github.com/google-gemini/gemini-cli mcp-server docs).
func patchGeminiMCPJSON(path, binPath string) error {
	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	servers, _ := raw["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers[mcpServerName] = map[string]any{
		"command": binPath,
		"args":    []string{"mcp", "serve"},
	}
	raw["mcpServers"] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func unpatchGeminiMCPJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if servers, ok := raw["mcpServers"].(map[string]any); ok {
		delete(servers, mcpServerName)
		if len(servers) == 0 {
			delete(raw, "mcpServers")
		}
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func unpatchOpencodeMCPJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if servers, ok := raw["mcp"].(map[string]any); ok {
		delete(servers, mcpServerName)
		if len(servers) == 0 {
			delete(raw, "mcp")
		}
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// UninstallMCPClients removes the repoguide MCP server from every detected client.
func UninstallMCPClients() []MCPClientResult {
	type clientDef struct {
		name      string
		detect    func() bool
		uninstall func() error
	}

	clients := []clientDef{
		{"Claude Code", detectClaudeCodeMCP, func() error {
			_ = os.RemoveAll(home(".claude", "skills", mcpPluginName))
			_ = RemoveViaMCPRemove("claude") // also clean up any old mcp-add entry
			return nil
		}},
		{"Codex", detectCodexMCP, func() error {
			if err := uninstallCodexPlugin(); err != nil {
				// fall back to legacy mcp-remove if plugin wasn't installed
				return RemoveViaMCPRemove("codex")
			}
			return nil
		}},
		{"Cursor", detectCursorMCP, func() error {
			return unpatchMCPJSON(cursorMCPConfigPath())
		}},
		{"OpenCode", detectOpencodeMCP, func() error {
			return unpatchOpencodeMCPJSON(opencodeConfigPath())
		}},
		{"GitHub Copilot", detectCopilotMCP, func() error {
			return unpatchCopilotMCPJSON(copilotMCPConfigPath())
		}},
		{"Gemini CLI", detectGeminiMCP, func() error {
			return unpatchGeminiMCPJSON(geminiMCPConfigPath())
		}},
	}

	results := make([]MCPClientResult, 0, len(clients))
	for _, c := range clients {
		if !c.detect() {
			continue
		}
		err := c.uninstall()
		results = append(results, MCPClientResult{
			Name:       c.name,
			Detected:   true,
			Configured: err == nil,
			Err:        err,
		})
	}
	return results
}

func RemoveViaMCPRemove(cli string) error {
	cmd := exec.Command(cli, "mcp", "remove", mcpServerName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func unpatchMCPJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if servers, ok := raw["mcpServers"].(map[string]any); ok {
		delete(servers, mcpServerName)
		if len(servers) == 0 {
			delete(raw, "mcpServers")
		}
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}

// --- Fallback ---

// AddClaudeCodePermission adds mcp__repoguide__repoguide_get_repo_experience to
// the allow list in ~/.claude/settings.json so Claude Code never prompts for it.
func AddClaudeCodePermission() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude", "settings.json")

	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	perms, _ := raw["permissions"].(map[string]any)
	if perms == nil {
		perms = make(map[string]any)
	}
	allow, _ := perms["allow"].([]any)
	const perm = "mcp__repoguide__repoguide_get_repo_experience"
	for _, v := range allow {
		if s, ok := v.(string); ok && s == perm {
			return nil
		}
	}
	perms["allow"] = append(allow, perm)
	raw["permissions"] = perms

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// AddClaudeCodeFeedbackPermission adds mcp__repoguide__repoguide_record_feedback to
// the allow list in ~/.claude/settings.json so Claude Code never prompts for it.
func AddClaudeCodeFeedbackPermission() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude", "settings.json")

	raw := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	perms, _ := raw["permissions"].(map[string]any)
	if perms == nil {
		perms = make(map[string]any)
	}
	allow, _ := perms["allow"].([]any)
	const perm = "mcp__repoguide__repoguide_record_feedback"
	for _, v := range allow {
		if s, ok := v.(string); ok && s == perm {
			return nil
		}
	}
	perms["allow"] = append(allow, perm)
	raw["permissions"] = perms

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func fallbackMCPBlock(binPath string) string {
	entry := map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{
				"command": binPath,
				"args":    []string{"mcp", "serve"},
			},
		},
	}
	data, _ := json.MarshalIndent(entry, "", "  ")
	return string(data)
}
