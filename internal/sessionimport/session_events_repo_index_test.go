package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarmRepoSessionArtifactsUsesRepoSessionIndex(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoRoot := filepath.Join(homeDir, "work", "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}

	repo := RepoConfig{
		RepoID:   "repo_1234",
		RepoRoot: repoRoot,
	}
	if err := os.MkdirAll(filepath.Dir(repoSessionIndexPath(repo)), 0o755); err != nil {
		t.Fatalf("MkdirAll(repo cache): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(RepoGuideDir(), "repos", repo.RepoID), 0o755); err != nil {
		t.Fatalf("MkdirAll(repo dir): %v", err)
	}
	if err := writeJSON(filepath.Join(RepoGuideDir(), "repos", repo.RepoID, "repo.json"), repo); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "18", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(session dir): %v", err)
	}
	raw := strings.Join([]string{
		`{"timestamp":"2026-06-18T12:00:00Z","type":"session_meta","payload":{"id":"codex-session","cwd":"` + repoRoot + `"}}`,
		`{"timestamp":"2026-06-18T12:00:01Z","type":"turn_context","payload":{"cwd":"` + repoRoot + `","model":"gpt-5"}}`,
		`{"timestamp":"2026-06-18T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect repo"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(session): %v", err)
	}

	index := repoSessionIndexFile{
		Version:   sessionIndexVersion,
		RepoID:    repo.RepoID,
		RepoRoot:  repo.RepoRoot,
		UpdatedAt: "2026-06-18T12:00:00Z",
		Entries: []sessionIndexEntry{
			{
				ID:        "codex-session",
				Agent:     "codex",
				Name:      "(untitled)",
				Cwd:       repoRoot,
				Model:     "gpt-5",
				Timestamp: "2026-06-18T12:00:00Z",
				Path:      sessionPath,
				RepoID:    repo.RepoID,
			},
		},
	}
	if err := writeJSON(repoSessionIndexPath(repo), index); err != nil {
		t.Fatalf("writeJSON(repo session index): %v", err)
	}

	results, err := WarmRepoSessionArtifacts(repoRoot, nil)
	if err != nil {
		t.Fatalf("WarmRepoSessionArtifacts: %v", err)
	}
	if len(results) != 1 || results[0].Agent != "codex" || results[0].SessionCount != 1 {
		t.Fatalf("unexpected results: %+v", results)
	}

	eventsPath := filepath.Join(RepoGuideDir(), "sessions", "codex-session", sessionEventLogFileName)
	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("expected events cache at %s: %v", eventsPath, err)
	}
}

func TestWarmSessionCachesWritesRepoSessionIndex(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoRoot := filepath.Join(homeDir, "work", "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}

	repo := RepoConfig{
		RepoID:   "repo_5678",
		RepoRoot: repoRoot,
	}
	if err := os.MkdirAll(filepath.Join(RepoGuideDir(), "repos", repo.RepoID), 0o755); err != nil {
		t.Fatalf("MkdirAll(repo dir): %v", err)
	}
	if err := writeJSON(filepath.Join(RepoGuideDir(), "repos", repo.RepoID, "repo.json"), repo); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "18", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(session dir): %v", err)
	}
	raw := strings.Join([]string{
		`{"timestamp":"2026-06-18T12:00:00Z","type":"session_meta","payload":{"id":"codex-session","cwd":"` + repoRoot + `"}}`,
		`{"timestamp":"2026-06-18T12:00:01Z","type":"turn_context","payload":{"cwd":"` + repoRoot + `","model":"gpt-5"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(session): %v", err)
	}

	if _, err := WarmSessionCaches(SessionLoadOptions{}); err != nil {
		t.Fatalf("WarmSessionCaches: %v", err)
	}

	index, err := readRepoSessionIndex(repoSessionIndexPath(repo))
	if err != nil {
		t.Fatalf("readRepoSessionIndex: %v", err)
	}
	if len(index.Entries) != 1 || index.Entries[0].RepoID != repo.RepoID {
		t.Fatalf("unexpected repo session index entries: %+v", index.Entries)
	}
}

func TestWarmSessionCachesReannotatesCachedEntriesAfterRepoInit(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoRoot := filepath.Join(homeDir, "work", "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "18", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(session dir): %v", err)
	}
	raw := strings.Join([]string{
		`{"timestamp":"2026-06-18T12:00:00Z","type":"session_meta","payload":{"id":"codex-session","cwd":"` + repoRoot + `"}}`,
		`{"timestamp":"2026-06-18T12:00:01Z","type":"turn_context","payload":{"cwd":"` + repoRoot + `","model":"gpt-5"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(session): %v", err)
	}

	if _, err := WarmSessionCaches(SessionLoadOptions{}); err != nil {
		t.Fatalf("initial WarmSessionCaches: %v", err)
	}

	repo := RepoConfig{
		RepoID:   "repo_9012",
		RepoRoot: repoRoot,
	}
	if err := os.MkdirAll(filepath.Join(RepoGuideDir(), "repos", repo.RepoID), 0o755); err != nil {
		t.Fatalf("MkdirAll(repo dir): %v", err)
	}
	if err := writeJSON(filepath.Join(RepoGuideDir(), "repos", repo.RepoID, "repo.json"), repo); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}

	if _, err := WarmSessionCaches(SessionLoadOptions{}); err != nil {
		t.Fatalf("second WarmSessionCaches: %v", err)
	}

	index, err := readRepoSessionIndex(repoSessionIndexPath(repo))
	if err != nil {
		t.Fatalf("readRepoSessionIndex: %v", err)
	}
	if len(index.Entries) != 1 {
		t.Fatalf("expected 1 repo session index entry, got %+v", index.Entries)
	}
	if index.Entries[0].RepoID != repo.RepoID {
		t.Fatalf("expected cached entry to be reannotated with repo %q, got %+v", repo.RepoID, index.Entries[0])
	}
}
