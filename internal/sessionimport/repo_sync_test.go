package sessionimport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCloudClientRegisterRepo(t *testing.T) {
	var got struct {
		RepoID   string `json:"repo_id"`
		RepoRoot string `json:"repo_root"`
		RepoName string `json:"repo_name"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/repos" {
			t.Fatalf("path = %s, want /api/repos", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	if err := client.RegisterRepo("repo_123", "/tmp/project"); err != nil {
		t.Fatalf("RegisterRepo returned error: %v", err)
	}
	if got.RepoID != "repo_123" || got.RepoRoot != "/tmp/project" || got.RepoName != "project" {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestCloudClientDeleteRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/repos/repo_123" {
			t.Fatalf("path = %s, want /api/repos/repo_123", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	if err := client.DeleteRepo("repo_123"); err != nil {
		t.Fatalf("DeleteRepo returned error: %v", err)
	}
}

func TestCloudClientSkipsWithoutToken(t *testing.T) {
	client := CloudClient{}
	if err := client.RegisterRepo("repo_123", "/tmp/project"); err != nil {
		t.Fatalf("RegisterRepo returned error: %v", err)
	}
	if err := client.DeleteRepo("repo_123"); err != nil {
		t.Fatalf("DeleteRepo returned error: %v", err)
	}
}

func TestCloudClientAnalyzeTimeout(t *testing.T) {
	client := CloudClient{}
	if got := client.httpClient().Timeout; got != defaultBackendTimeout {
		t.Fatalf("httpClient timeout = %v, want %v", got, defaultBackendTimeout)
	}
	if got := client.analyzeHTTPClient().Timeout; got != analyzeBackendTimeout {
		t.Fatalf("analyzeHTTPClient timeout = %v, want %v", got, analyzeBackendTimeout)
	}

	custom := &http.Client{Timeout: 3 * time.Second}
	client = CloudClient{Client: custom}
	if got := client.analyzeHTTPClient(); got != custom {
		t.Fatal("analyzeHTTPClient should reuse custom client")
	}
}

func TestCloudClientUploadRepoEvents(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoRoot := filepath.Join(homeDir, "work", "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}

	repo := RepoConfig{RepoID: "repo_123", RepoRoot: repoRoot}
	repoDir := filepath.Join(RepoGuideDir(), "repos", repo.RepoID)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo dir): %v", err)
	}
	if err := writeJSON(filepath.Join(repoDir, "repo.json"), repo); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "20", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(session dir): %v", err)
	}
	raw := []byte("{\"timestamp\":\"2026-06-20T12:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"session_1\",\"cwd\":\"" + repoRoot + "\"}}\n" +
		"{\"timestamp\":\"2026-06-20T12:00:01Z\",\"type\":\"turn_context\",\"payload\":{\"cwd\":\"" + repoRoot + "\",\"model\":\"gpt-5\"}}\n" +
		"{\"timestamp\":\"2026-06-20T12:00:02Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"inspect repo\"}]}}\n")
	if err := os.WriteFile(sessionPath, raw, 0o644); err != nil {
		t.Fatalf("WriteFile(session): %v", err)
	}

	var gotEvents []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/repos/repo_123/events/" {
			t.Fatalf("path = %s, want /api/repos/repo_123/events/", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll(body): %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("zip.NewReader: %v", err)
		}
		if len(zr.File) != 1 {
			t.Fatalf("zip entries = %d, want 1", len(zr.File))
		}
		rc, err := zr.File[0].Open()
		if err != nil {
			t.Fatalf("Open(zip entry): %v", err)
		}
		defer rc.Close()
		var payload struct {
			Events []map[string]any `json:"events"`
		}
		if err := json.NewDecoder(rc).Decode(&payload); err != nil {
			t.Fatalf("Decode zip payload: %v", err)
		}
		gotEvents = payload.Events
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	if err := client.UploadRepoEvents(repo.RepoID, repo.RepoRoot); err != nil {
		t.Fatalf("UploadRepoEvents returned error: %v", err)
	}
	if len(gotEvents) != 3 || gotEvents[2]["text"] != "inspect repo" {
		t.Fatalf("unexpected uploaded events: %#v", gotEvents)
	}
}

func TestCloudClientUploadRepoEventsReturnsBackendError(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoRoot := filepath.Join(homeDir, "work", "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}
	repo := RepoConfig{RepoID: "repo_123", RepoRoot: repoRoot}
	repoDir := filepath.Join(RepoGuideDir(), "repos", repo.RepoID)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo dir): %v", err)
	}
	if err := writeJSON(filepath.Join(repoDir, "repo.json"), repo); err != nil {
		t.Fatalf("writeJSON(repo): %v", err)
	}

	sessionPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "06", "20", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(session dir): %v", err)
	}
	raw := []byte("{\"timestamp\":\"2026-06-20T12:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"session_1\",\"cwd\":\"" + repoRoot + "\"}}\n" +
		"{\"timestamp\":\"2026-06-20T12:00:01Z\",\"type\":\"turn_context\",\"payload\":{\"cwd\":\"" + repoRoot + "\",\"model\":\"gpt-5\"}}\n")
	if err := os.WriteFile(sessionPath, raw, 0o644); err != nil {
		t.Fatalf("WriteFile(session): %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid events zip"}`))
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	err := client.UploadRepoEvents(repo.RepoID, repo.RepoRoot)
	if err == nil {
		t.Fatal("UploadRepoEvents returned nil error")
	}
	if !strings.Contains(err.Error(), "invalid events zip") {
		t.Fatalf("error = %q, want backend message", err)
	}
}
