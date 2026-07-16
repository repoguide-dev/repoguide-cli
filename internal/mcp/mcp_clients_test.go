package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexMarketplaceManifestIncludesCodexProductAndPlugin(t *testing.T) {
	manifest := codexMarketplaceManifest()

	if got := manifest["name"]; got != repoguideMarketplace {
		t.Fatalf("marketplace name = %v, want %q", got, repoguideMarketplace)
	}

	iface, ok := manifest["interface"].(map[string]any)
	if !ok {
		t.Fatalf("interface = %T, want map[string]any", manifest["interface"])
	}
	if got := iface["displayName"]; got != "RepoGuide" {
		t.Fatalf("displayName = %v, want RepoGuide", got)
	}

	plugins, ok := manifest["plugins"].([]map[string]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("plugins = %#v, want one plugin entry", manifest["plugins"])
	}
	policy, ok := plugins[0]["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy = %T, want map[string]any", plugins[0]["policy"])
	}
	products, ok := policy["products"].([]string)
	if !ok || len(products) != 1 || products[0] != "CODEX" {
		t.Fatalf("products = %#v, want [CODEX]", policy["products"])
	}
}

func TestCodexPluginManifestIncludesDisplayMetadata(t *testing.T) {
	manifest := codexPluginManifest()

	if got := manifest["name"]; got != "repoguide" {
		t.Fatalf("name = %v, want repoguide", got)
	}
	if got := manifest["homepage"]; got != "https://repoguide.dev" {
		t.Fatalf("homepage = %v, want https://repoguide.dev", got)
	}

	iface, ok := manifest["interface"].(map[string]any)
	if !ok {
		t.Fatalf("interface = %T, want map[string]any", manifest["interface"])
	}
	for key, want := range map[string]any{
		"displayName":       "RepoGuide",
		"developerName":     "RepoGuide",
		"shortDescription":  "Repo-specific context routing",
		"privacyPolicyURL":  "https://repoguide.dev/privacy",
		"termsOfServiceURL": "https://repoguide.dev/terms",
		"brandColor":        "#0F766E",
	} {
		if got := iface[key]; got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}

	caps, ok := iface["capabilities"].([]string)
	if !ok || len(caps) != 2 || caps[0] != "Interactive" || caps[1] != "Read" {
		t.Fatalf("capabilities = %#v, want [Interactive Read]", iface["capabilities"])
	}
}

func TestCodexPluginMCPConfigUsesRepoGuideServer(t *testing.T) {
	cfg := codexPluginMCPConfig("/tmp/repoguide")
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers = %T, want map[string]any", cfg["mcpServers"])
	}
	server, ok := servers[mcpServerName].(map[string]any)
	if !ok {
		t.Fatalf("server = %T, want map[string]any", servers[mcpServerName])
	}
	if got := server["command"]; got != "/tmp/repoguide" {
		t.Fatalf("command = %v, want /tmp/repoguide", got)
	}
	args, ok := server["args"].([]string)
	if !ok || len(args) != 2 || args[0] != "mcp" || args[1] != "serve" {
		t.Fatalf("args = %#v, want [mcp serve]", server["args"])
	}
}

func TestCodexPluginHooksConfigInstallsPromptHookOnly(t *testing.T) {
	cfg := codexPluginHooksConfig("/tmp/repoguide")
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks = %T, want map[string]any", cfg["hooks"])
	}

	groups, ok := hooks["UserPromptSubmit"].([]map[string]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("UserPromptSubmit groups = %#v, want one hook group", hooks["UserPromptSubmit"])
	}
	entries, ok := groups[0]["hooks"].([]map[string]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("UserPromptSubmit hooks = %#v, want one command hook", groups[0]["hooks"])
	}
	if got := entries[0]["name"]; got != "RepoGuide task routing" {
		t.Fatalf("UserPromptSubmit name = %v, want %q", got, "RepoGuide task routing")
	}
	if got := entries[0]["command"]; got != "\"/tmp/repoguide\" mcp hook prompt" {
		t.Fatalf("UserPromptSubmit command = %v, want %q", got, "\"/tmp/repoguide\" mcp hook prompt")
	}
	if got := entries[0]["timeout"]; got != 5 {
		t.Fatalf("UserPromptSubmit timeout = %v, want 5", got)
	}
	if _, ok := hooks["Stop"]; ok {
		t.Fatalf("Stop hook should be omitted for Codex, got %#v", hooks["Stop"])
	}
}

func TestInstallCodexPluginWritesHooksByDefault(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	codexStub := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexStub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex stub): %v", err)
	}

	if err := installCodexPlugin("/tmp/repoguide", true); err != nil {
		t.Fatalf("installCodexPlugin: %v", err)
	}

	path := filepath.Join(homeDir, ".repoguide", "codex-marketplace", "plugins", "repoguide", "hooks", "hooks.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected hooks.json to be written: %v", err)
	}
}

func TestInstallCodexPluginRefreshesExistingPlugin(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	binDir := filepath.Join(tempDir, "bin")
	logPath := filepath.Join(tempDir, "codex.log")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_LOG", logPath)
	codexStub := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexStub, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CODEX_LOG\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex stub): %v", err)
	}

	if err := installCodexPlugin("/tmp/repoguide", true); err != nil {
		t.Fatalf("installCodexPlugin: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(codex log): %v", err)
	}
	log := string(data)
	remove := "plugin remove " + mcpPluginName + "@" + repoguideMarketplace
	add := "plugin add " + mcpPluginName + "@" + repoguideMarketplace
	if !strings.Contains(log, remove) || !strings.Contains(log, add) {
		t.Fatalf("expected plugin refresh commands %q and %q, got %q", remove, add, log)
	}
	if strings.Index(log, remove) > strings.Index(log, add) {
		t.Fatalf("expected plugin removal before re-add, got %q", log)
	}
}

func TestInstallCodexPluginSkipsHooksWhenDisabled(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home): %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	codexStub := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexStub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex stub): %v", err)
	}

	if err := installCodexPlugin("/tmp/repoguide", false); err != nil {
		t.Fatalf("installCodexPlugin: %v", err)
	}

	path := filepath.Join(homeDir, ".repoguide", "codex-marketplace", "plugins", "repoguide", "hooks", "hooks.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected hooks.json to be absent, stat err = %v", err)
	}
}

func TestPatchGeminiRoutingHookAddsBeforeAgentCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := patchGeminiRoutingHook(path, "/tmp/repoguide"); err != nil {
		t.Fatalf("patchGeminiRoutingHook: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "BeforeAgent") || !strings.Contains(string(data), "gemini-prompt") {
		t.Fatalf("expected native Gemini routing hook, got %s", data)
	}
}
