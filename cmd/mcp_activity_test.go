package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	mcpinternal "github.com/repoguide/repoguide-cli/internal/mcp"
)

func TestRunMCPActivityPrintsRecordedCalls(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", homeDir)

	if err := mcpinternal.AppendMCPActivity(mcpinternal.MCPActivityRecord{
		Timestamp: "2026-06-20T10:00:00Z",
		Repo:      "/repo-a",
		Command:   "repoguide_list_topics",
		Inputs:    map[string]any{"task": "mock server"},
	}); err != nil {
		t.Fatalf("AppendMCPActivity #1: %v", err)
	}
	if err := mcpinternal.AppendMCPActivity(mcpinternal.MCPActivityRecord{
		Timestamp: "2026-06-20T11:00:00Z",
		Repo:      "/repo-b",
		Command:   "repoguide_get_bootstrap_context",
		Inputs:    map[string]any{"task": "mock server", "topic_id": "cli"},
	}); err != nil {
		t.Fatalf("AppendMCPActivity #2: %v", err)
	}

	var out bytes.Buffer
	mcpActivityCmd.SetOut(&out)
	mcpActivityCmd.SetErr(&out)
	if err := mcpActivityCmd.Flags().Set("limit", "1"); err != nil {
		t.Fatalf("Set(limit): %v", err)
	}
	defer mcpActivityCmd.Flags().Set("limit", "20")

	if err := runMCPActivity(mcpActivityCmd, nil); err != nil {
		t.Fatalf("runMCPActivity: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "RepoGuide MCP Activity") {
		t.Fatalf("expected title in output, got %q", output)
	}
	if !strings.Contains(output, "repoguide_get_bootstrap") {
		t.Fatalf("expected newest command in output, got %q", output)
	}
	if strings.Contains(output, "repoguide_list_topics") {
		t.Fatalf("expected limit=1 to suppress older entry, got %q", output)
	}
}
