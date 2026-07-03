package mcp

import (
	"path/filepath"
	"testing"
)

func TestAppendAndLoadMCPActivity(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", homeDir)

	if err := AppendMCPActivity(MCPActivityRecord{
		Timestamp: "2026-06-20T10:00:00Z",
		Repo:      "/repo-a",
		Command:   "repoguide_list_topics",
		Inputs:    map[string]any{"task": "task a"},
	}); err != nil {
		t.Fatalf("AppendMCPActivity #1: %v", err)
	}
	if err := AppendMCPActivity(MCPActivityRecord{
		Timestamp: "2026-06-20T11:00:00Z",
		Repo:      "/repo-b",
		Command:   "repoguide_get_bootstrap_context",
		Inputs:    map[string]any{"task": "task b", "topic_id": "cli"},
	}); err != nil {
		t.Fatalf("AppendMCPActivity #2: %v", err)
	}

	records, err := LoadMCPActivity()
	if err != nil {
		t.Fatalf("LoadMCPActivity: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if records[0].CallID == "" || records[1].CallID == "" {
		t.Fatalf("expected call IDs to be populated: %+v", records)
	}
	if records[0].Repo != "/repo-b" || records[0].Command != "repoguide_get_bootstrap_context" {
		t.Fatalf("unexpected newest record: %+v", records[0])
	}
	if records[1].Repo != "/repo-a" || records[1].Command != "repoguide_list_topics" {
		t.Fatalf("unexpected oldest record: %+v", records[1])
	}
}

func TestLatestUnderstandTaskTopicIDMatchesRepo(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", homeDir)

	records := []MCPActivityRecord{
		{
			Timestamp: "2026-06-20T09:00:00Z",
			Repo:      "/repo-a",
			Command:   "repoguide_get_repo_experience",
			Response:  map[string]any{"topic_id": "topic-old"},
		},
		{
			Timestamp: "2026-06-20T10:00:00Z",
			Repo:      "/repo-b",
			Command:   "repoguide_get_repo_experience",
			Response:  map[string]any{"topic_id": "topic-other"},
		},
		{
			Timestamp: "2026-06-20T11:00:00Z",
			Repo:      "/repo-a",
			Command:   "repoguide_record_feedback",
			Inputs:    map[string]any{"topic_id": "ignore-feedback"},
		},
		{
			Timestamp: "2026-06-20T12:00:00Z",
			Repo:      "/repo-a",
			Command:   "repoguide_get_repo_experience",
			Inputs:    map[string]any{"repo_id": "repo-a"},
			Response:  map[string]any{"topic_id": "topic-new"},
		},
	}
	for _, record := range records {
		if err := AppendMCPActivity(record); err != nil {
			t.Fatalf("AppendMCPActivity: %v", err)
		}
	}

	if got := LatestUnderstandTaskTopicID("", "/repo-a"); got != "topic-new" {
		t.Fatalf("LatestUnderstandTaskTopicID(repo-a path) = %q, want topic-new", got)
	}
	if got := LatestUnderstandTaskTopicID("repo-a", ""); got != "topic-new" {
		t.Fatalf("LatestUnderstandTaskTopicID(repo-a id) = %q, want topic-new", got)
	}
	if got := LatestUnderstandTaskTopicID("", "/repo-missing"); got != "" {
		t.Fatalf("LatestUnderstandTaskTopicID(missing repo) = %q, want empty", got)
	}
}
