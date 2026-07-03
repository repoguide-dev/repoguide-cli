package mcp

import "testing"

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
