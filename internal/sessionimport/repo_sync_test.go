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
		RepoURL  string `json:"repo_url"`
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
	if got.RepoURL != "" {
		t.Fatalf("RepoURL = %q, want empty for non-git repo", got.RepoURL)
	}
}

func TestCloudClientRegisterRepoIncludesGitHubOrigin(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "remote", "add", "origin", "git@github.com:acme/widgets.git")

	var got struct {
		RepoURL string `json:"repo_url"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	if err := client.RegisterRepo("repo_123", repoRoot); err != nil {
		t.Fatalf("RegisterRepo returned error: %v", err)
	}
	if got.RepoURL != "https://github.com/acme/widgets" {
		t.Fatalf("RepoURL = %q, want https://github.com/acme/widgets", got.RepoURL)
	}
}

func TestGithubRepoURL(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	if got := githubRepoURL(repoRoot); got != "https://github.com/acme/widgets" {
		t.Fatalf("githubRepoURL() = %q, want https://github.com/acme/widgets", got)
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

func TestCloudClientGetMeReturnsUnauthorizedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "stale-token"}
	_, err := client.GetMe()
	if err == nil {
		t.Fatal("GetMe returned nil error")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("error = %q, want invalid token message", err)
	}
}

func TestCloudClientRefreshAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/auth/refresh" {
			t.Fatalf("path = %s, want /api/auth/refresh", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer stale-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AuthSessionResponse{
			Token: "fresh-token",
			Email: "dev@repoguide.test",
		})
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "stale-token"}
	resp, err := client.RefreshAuthToken()
	if err != nil {
		t.Fatalf("RefreshAuthToken returned error: %v", err)
	}
	if resp.Token != "fresh-token" || resp.Email != "dev@repoguide.test" {
		t.Fatalf("unexpected refresh response: %#v", resp)
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

func TestCloudClientRejectsUnverifiedBackendHost(t *testing.T) {
	client := CloudClient{BaseURL: "https://example.com", Token: "test-token"}
	err := client.RegisterRepo("repo_123", "/tmp/project")
	if err == nil {
		t.Fatal("RegisterRepo returned nil error")
	}
	if !strings.Contains(err.Error(), "host is not allowed") {
		t.Fatalf("error = %q, want host rejection", err)
	}
}

func TestCloudClientRejectsUnexpectedRequestPath(t *testing.T) {
	client := CloudClient{BaseURL: "https://repoguide.dev", Token: "test-token"}
	if _, err := client.newRequest(http.MethodGet, "https://example.com/api/repos/repo_123", nil); err == nil {
		t.Fatal("absolute URL path should be rejected")
	}
	if _, err := client.newRequest(http.MethodGet, "/api/admin/repos", nil); err == nil {
		t.Fatal("unexpected backend path should be rejected")
	}
}

func TestCloudClientRejectsRedirectToUnverifiedHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/api/limits", http.StatusFound)
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	_, err := client.GetLimits()
	if err == nil {
		t.Fatal("GetLimits returned nil error")
	}
	if !strings.Contains(err.Error(), "host is not allowed") {
		t.Fatalf("error = %q, want redirect host rejection", err)
	}
}

func TestCloudClientEscapesMCPSearchQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/repos/repo_123/mcp/search" {
			t.Fatalf("path = %s, want /api/repos/repo_123/mcp/search", r.URL.Path)
		}
		if got := r.URL.Query().Get("topic_id"); got != "topic&1" {
			t.Fatalf("topic_id = %q, want escaped value round trip", got)
		}
		if got := r.URL.Query().Get("query"); got != "hello world & tests" {
			t.Fatalf("query = %q, want escaped value round trip", got)
		}
		_ = json.NewEncoder(w).Encode(MCPSearchContext{})
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "test-token"}
	if _, err := client.GetMCPSearchContext("repo_123", "topic&1", "hello world & tests"); err != nil {
		t.Fatalf("GetMCPSearchContext returned error: %v", err)
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
	gotClient := client.analyzeHTTPClient()
	if gotClient == custom {
		t.Fatal("analyzeHTTPClient should wrap custom client with backend redirect validation")
	}
	if gotClient.Timeout != custom.Timeout {
		t.Fatalf("custom client timeout = %v, want %v", gotClient.Timeout, custom.Timeout)
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/api/limits", nil)
	if err := gotClient.CheckRedirect(req, nil); err == nil {
		t.Fatal("custom client redirect policy should reject unverified hosts")
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

	var progressCurrent, progressTotal int
	client := CloudClient{
		BaseURL: server.URL,
		Token:   "test-token",
		Progress: func(current, total int, _ string) {
			progressCurrent, progressTotal = current, total
		},
	}
	if err := client.UploadRepoEvents(repo.RepoID, repo.RepoRoot); err != nil {
		t.Fatalf("UploadRepoEvents returned error: %v", err)
	}
	if progressCurrent != 1 || progressTotal != 1 {
		t.Fatalf("progress = %d/%d, want 1/1", progressCurrent, progressTotal)
	}
	if len(gotEvents) != 3 || gotEvents[2]["text"] != "inspect repo" {
		t.Fatalf("unexpected uploaded events: %#v", gotEvents)
	}
}

func TestBuildRepoPathAliases(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "dev@example.com")
	runGit(t, repoRoot, "config", "user.name", "Dev")

	oldPath := filepath.Join(repoRoot, "pkg", "old_name.go")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(oldPath, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old): %v", err)
	}
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "add old name")

	midPath := filepath.Join(repoRoot, "pkg", "mid_name.go")
	if err := os.Rename(oldPath, midPath); err != nil {
		t.Fatalf("Rename old->mid: %v", err)
	}
	runGit(t, repoRoot, "add", "-A")
	runGit(t, repoRoot, "commit", "-m", "rename to mid")

	finalPath := filepath.Join(repoRoot, "pkg", "final_name.go")
	if err := os.Rename(midPath, finalPath); err != nil {
		t.Fatalf("Rename mid->final: %v", err)
	}
	runGit(t, repoRoot, "add", "-A")
	runGit(t, repoRoot, "commit", "-m", "rename to final")

	aliases := buildRepoPathAliases(repoRoot)
	if got := aliases["pkg/old_name.go"]; got != "pkg/final_name.go" {
		t.Fatalf("old alias = %q, want final", got)
	}
	if got := aliases["pkg/mid_name.go"]; got != "pkg/final_name.go" {
		t.Fatalf("mid alias = %q, want final", got)
	}
	if _, ok := aliases["pkg/final_name.go"]; ok {
		t.Fatalf("final path should not be emitted as alias: %#v", aliases)
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

func TestSanitizeEventLogForUploadRespectsRawPromptsFalse(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := writeJSON(filepath.Join(RepoGuideDir(), "config.json"), GlobalConfig{
		Privacy: PrivacyDefaults{RawPrompts: false},
	}); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	log := SessionEventLog{Events: []SessionEvent{
		{Kind: "prompt", Text: "token=sk-live-abcdefghijklmnopqrstuvwxyz"},
		{Kind: "assistant_message", Text: "use password=hunter2"},
	}}
	data, err := json.Marshal(log)
	if err != nil {
		t.Fatalf("Marshal event log: %v", err)
	}
	out, err := sanitizeEventLogForUpload(data, RepoConfig{})
	if err != nil {
		t.Fatalf("sanitizeEventLogForUpload: %v", err)
	}
	var got SessionEventLog
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("Unmarshal sanitized log: %v", err)
	}
	if got.Events[0].Text != "[redacted prompt]" {
		t.Fatalf("prompt text = %q", got.Events[0].Text)
	}
	if got.Events[1].Text != "use password=[redacted]" {
		t.Fatalf("assistant text = %q", got.Events[1].Text)
	}
}

func TestSafeHintFilePathStaysInRepoAndAllowsOnlyTextMarkdown(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "notes.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile notes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "secret.json"), []byte("no"), 0o644); err != nil {
		t.Fatalf("WriteFile json: %v", err)
	}
	outside := filepath.Join(filepath.Dir(repoRoot), "outside.md")
	if err := os.WriteFile(outside, []byte("no"), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repoRoot, "linked.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, ok := safeHintFilePath(repoRoot, "README.md"); !ok {
		t.Fatal("README.md should be allowed")
	}
	if _, ok := safeHintFilePath(repoRoot, "notes.txt"); !ok {
		t.Fatal("notes.txt should be allowed")
	}
	for _, name := range []string{"secret.json", "../outside.md", "/tmp/outside.md", "linked.md"} {
		if path, ok := safeHintFilePath(repoRoot, name); ok {
			t.Fatalf("%s resolved to %s, want rejected", name, path)
		}
	}
}

func TestIsAllowedBackendRequestPath(t *testing.T) {
	// Every path the CloudClient actually builds must be allowed.
	allowed := []string{
		"/version", "/api/limits", "/api/auth/me", "/api/auth/refresh",
		"/api/repos", "/api/repos/abc", "/api/repos/abc/events/",
		"/api/repos/abc/mcp-calls", "/api/repos/abc/mcp/topics",
		"/api/repos/abc/mcp/topics/t1", "/api/repos/abc/mcp/understand-task",
		"/api/repos/abc/mcp/feedback", "/api/repos/abc/mcp/search",
		"/api/teams", "/api/teams/join", "/api/teams/t1",
		"/api/teams/t1/repos", "/api/teams/t1/members", "/api/teams/t1/invites",
		"/api/teams/t1/repos/r1/merge", "/api/teams/t1/repos/r1/connect",
	}
	for _, p := range allowed {
		if !isAllowedBackendRequestPath(p) {
			t.Errorf("%s should be allowed", p)
		}
	}
	for _, p := range []string{"/", "/admin", "/api/teams/", "/api/admin/users", "/api/repos/abc/secrets", "/api/teams/t1/invites/i1"} {
		if isAllowedBackendRequestPath(p) {
			t.Errorf("%s should be rejected", p)
		}
	}
}
