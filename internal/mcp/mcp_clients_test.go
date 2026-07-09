package mcp

import (
	"os"
	"path/filepath"
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

func TestCodexPluginHooksConfigInstallsPromptAndStopHooks(t *testing.T) {
	cfg := codexPluginHooksConfig("/tmp/repoguide")
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks = %T, want map[string]any", cfg["hooks"])
	}
	for event, want := range map[string]struct {
		command string
		name    string
	}{
		"UserPromptSubmit": {
			command: "\"/tmp/repoguide\" mcp hook prompt",
			name:    "RepoGuide task routing",
		},
		"Stop": {
			command: "\"/tmp/repoguide\" mcp hook stop",
			name:    "RepoGuide feedback reminder",
		},
	} {
		groups, ok := hooks[event].([]map[string]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("%s groups = %#v, want one hook group", event, hooks[event])
		}
		entries, ok := groups[0]["hooks"].([]map[string]any)
		if !ok || len(entries) != 1 {
			t.Fatalf("%s hooks = %#v, want one command hook", event, groups[0]["hooks"])
		}
		if got := entries[0]["name"]; got != want.name {
			t.Fatalf("%s name = %v, want %q", event, got, want.name)
		}
		if got := entries[0]["command"]; got != want.command {
			t.Fatalf("%s command = %v, want %q", event, got, want.command)
		}
		if got := entries[0]["timeout"]; got != 5 {
			t.Fatalf("%s timeout = %v, want 5", event, got)
		}
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
