package sessionimport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/repoguide/repoguide-cli/internal/services"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

const (
	defaultBackendTimeout = time.Minute
	analyzeBackendTimeout = 5 * time.Minute
	// Cloudflare rejects uploads around 1 MiB before they reach the backend.
	// Keep event archives deliberately small so a large local history can sync.
	maxSessionsPerRepoEventsUpload = 10
)

var verifiedBackendHosts = map[string]struct{}{
	"repoguide.dev":     {},
	"www.repoguide.dev": {},
}

type CloudClient struct {
	BaseURL      string
	Token        string
	Client       *http.Client
	Progress     func(current, total int, label string)
	AgentName    string
	AgentVersion string
	SessionID    string
	// localSvc, when set, is available for routing local-mode repos to SQLite.
	localSvc        *services.Services
	localSvcFactory func(repoID string) *services.Services
}

// NewCloudClient constructs a CloudClient. svc/localSvcFactory wire the
// client for local (SQLite) mode; leave them nil for pure cloud mode. This
// constructor exists because localSvc/localSvcFactory are unexported (callers
// outside this package, e.g. internal/mcp, cannot set them via a struct
// literal).
func NewCloudClient(baseURL, token string, svc *services.Services, localSvcFactory func(repoID string) *services.Services) CloudClient {
	return CloudClient{BaseURL: baseURL, Token: token, localSvc: svc, localSvcFactory: localSvcFactory}
}

var _ contracts.CloudConnector = CloudClient{}

func validateBackendURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", fmt.Errorf("backend URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid backend URL %q", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("backend URL must be an origin without credentials, path, query, or fragment")
	}
	host := normalizedBackendHost(u.Hostname())
	if u.Scheme == "https" && isVerifiedBackendHost(host) {
		return raw, nil
	}
	if (u.Scheme == "http" || u.Scheme == "https") && isLoopbackHost(host) {
		return raw, nil
	}
	return "", fmt.Errorf("backend URL host is not allowed")
}

func normalizedBackendHost(host string) string {
	return strings.Trim(strings.TrimSuffix(strings.ToLower(host), "."), "[]")
}

func isVerifiedBackendHost(host string) bool {
	_, ok := verifiedBackendHosts[host]
	return ok
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c CloudClient) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	baseURL, err := validateBackendURL(c.BaseURL)
	if err != nil {
		return nil, err
	}
	if err := validateBackendRequestPath(path); err != nil {
		return nil, err
	}
	return http.NewRequest(method, baseURL+path, body)
}

func validateBackendRequestPath(path string) error {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fmt.Errorf("invalid backend request path %q", path)
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return fmt.Errorf("backend request path must be absolute-path relative")
	}
	if !isAllowedBackendRequestPath(u.Path) {
		return fmt.Errorf("backend request path is not allowed: %s", u.Path)
	}
	return nil
}

func isAllowedBackendRequestPath(path string) bool {
	switch path {
	case "/version", "/api/limits", "/api/auth/me", "/api/auth/refresh", "/api/repos", "/api/teams", "/api/teams/join":
		return true
	}
	if strings.HasPrefix(path, "/api/teams/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/teams/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			return false
		}
		return len(parts) == 1 ||
			(len(parts) == 2 && (parts[1] == "repos" || parts[1] == "members")) ||
			(len(parts) == 4 && parts[1] == "repos" && parts[3] == "merge")
	}
	if !strings.HasPrefix(path, "/api/repos/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/repos/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	switch parts[1] {
	case "events":
		return len(parts) == 2 || (len(parts) == 3 && parts[2] == "")
	case "analyze":
		return len(parts) == 2
	case "mcp-calls":
		return len(parts) == 2
	case "mcp":
		if len(parts) == 3 && (parts[2] == "topics" || parts[2] == "understand-task" || parts[2] == "feedback" || parts[2] == "search") {
			return true
		}
		return len(parts) == 4 && parts[2] == "topics" && parts[3] != ""
	default:
		return false
	}
}

func validateBackendRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if _, err := validateBackendURL(requestOrigin(req.URL)); err != nil {
		return err
	}
	return validateBackendRequestPath(req.URL.RequestURI())
}

func requestOrigin(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// shouldUseLocal returns true when the given repo should be served from the
// local SQLite store rather than the remote backend.
// In pure local mode (no cloud token) all repos go local.
// In hybrid mode (cloud token present) only repos configured as local go local.
func (c CloudClient) shouldUseLocal(repoID string) bool {
	if c.localSvc == nil {
		return false
	}
	if strings.TrimSpace(c.Token) == "" {
		return true // pure local mode – no cloud credentials
	}
	return RepoIsLocalModeByID(repoID) // ponytail: reads disk on each call; call frequency is low enough
}

type RepoInfo = contracts.RepoInfo

func (c CloudClient) GetRepo(repoID string) (*RepoInfo, error) {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	req, err := c.newRequest(http.MethodGet, "/api/repos/"+url.PathEscape(repoID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend get repo failed", resp)
	}
	var info RepoInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c CloudClient) RegisterRepo(repoID, repoRoot string) error {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil
	}
	repoURL := remoteRepoURL(repoRoot)
	body, err := json.Marshal(map[string]any{
		"repo_id":   repoID,
		"repo_root": repoRoot,
		"repo_name": filepath.Base(repoRoot),
		"repo_url":  repoURL,
	})
	if err != nil {
		return err
	}
	req, err := c.newRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 409 {
		return nil // already registered
	}
	if resp.StatusCode >= 300 {
		return backendRequestError("backend repo registration failed", resp)
	}
	return nil
}

func remoteRepoURL(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(raw, "git@github.com:"):
		path := strings.TrimPrefix(raw, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		path = strings.Trim(path, "/")
		if path == "" {
			return ""
		}
		return "https://github.com/" + path
	case strings.HasPrefix(raw, "https://github.com/"), strings.HasPrefix(raw, "http://github.com/"):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		path := strings.TrimSuffix(u.Path, ".git")
		path = strings.Trim(path, "/")
		if path == "" {
			return ""
		}
		return "https://github.com/" + path
	default:
		return raw
	}
}

func githubRepoURL(repoRoot string) string {
	raw := remoteRepoURL(repoRoot)
	if strings.HasPrefix(raw, "https://github.com/") {
		return raw
	}
	return ""
}

type TeamSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	InviteCode string `json:"invite_code"`
}

type TeamMembership struct {
	Team TeamSummary `json:"team"`
	Role string      `json:"role"`
}

type TeamRepo struct {
	RepoID         string `json:"repo_id"`
	TeamID         string `json:"team_id,omitempty"`
	RepoRoot       string `json:"repo_root,omitempty"`
	RepoName       string `json:"repo_name,omitempty"`
	RepoURL        string `json:"repo_url,omitempty"`
	ConnectCommand string `json:"connect_command,omitempty"`
}

func (c CloudClient) JoinTeam(inviteCode string) (*TeamSummary, error) {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	body, err := json.Marshal(map[string]string{"invite_code": strings.TrimSpace(inviteCode)})
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/teams/join", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("team join failed", resp)
	}
	var payload struct {
		Team TeamSummary `json:"team"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return &payload.Team, nil
}

func (c CloudClient) ListTeamRepos(teamID string) ([]TeamRepo, error) {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	req, err := c.newRequest(http.MethodGet, "/api/teams/"+url.PathEscape(teamID)+"/repos", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("team repo list failed", resp)
	}
	var payload struct {
		Repos []TeamRepo `json:"repos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Repos, nil
}

func (c CloudClient) MergeTeamRepo(teamID, targetRepoID, sourceRepoID string) error {
	body, err := json.Marshal(map[string]string{"source_repo_id": strings.TrimSpace(sourceRepoID)})
	if err != nil {
		return err
	}
	req, err := c.newRequest(http.MethodPost, "/api/teams/"+url.PathEscape(teamID)+"/repos/"+url.PathEscape(targetRepoID)+"/merge", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return backendRequestError("team repo merge failed", resp)
	}
	return nil
}

func (c CloudClient) DeleteRepo(repoID string) error {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(repoID) == "" {
		return nil
	}
	req, err := c.newRequest(http.MethodDelete, "/api/repos/"+url.PathEscape(repoID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return backendRequestError("backend repo deletion failed", resp)
	}
	return nil
}

var (
	lastSync   time.Time
	lastSyncMu sync.Mutex
)

func (c CloudClient) SyncAllRepos() {
	if c.localSvc != nil {
		return // local mode: events are already on disk; no upload needed
	}
	lastSyncMu.Lock()
	if time.Since(lastSync) < time.Minute {
		lastSyncMu.Unlock()
		return
	}
	lastSync = time.Now()
	lastSyncMu.Unlock()

	repos, err := ListConfiguredRepos()
	if err != nil {
		return
	}
	for _, repo := range repos {
		_ = c.UploadRepoEvents(repo.RepoID, repo.RepoRoot)
	}
}

// MarkRepoConnected tells the backend this account's local checkout is now
// linked to a team repo, so it shows as connected immediately instead of
// waiting for the first session upload.
func (c CloudClient) MarkRepoConnected(teamID, repoID string) error {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil
	}
	req, err := c.newRequest(http.MethodPost, "/api/teams/"+url.PathEscape(teamID)+"/repos/"+url.PathEscape(repoID)+"/connect", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return backendRequestError("team repo connect failed", resp)
	}
	return nil
}

func (c CloudClient) UploadRepoEvents(repoID, repoRoot string) error {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(repoID) == "" {
		return nil
	}
	if _, err := WarmSessionCaches(SessionLoadOptions{}); err != nil {
		return err
	}
	if _, err := WarmRepoSessionArtifacts(repoRoot, nil); err != nil {
		return err
	}

	storeDir := filepath.Join(RepoGuideDir(), "repos", repoID)
	cfg, _ := LoadRepoConfigFile(storeDir)

	var since time.Time
	if cfg.LastSyncedAt != nil {
		since = *cfg.LastSyncedAt
	}

	uploads, err := buildRepoEventsUploads(repoID, repoRoot, since)
	if err != nil {
		return err
	}
	if len(uploads) == 0 {
		return nil
	}
	for i, payload := range uploads {
		req, err := c.newRequest(http.MethodPost, "/api/repos/"+url.PathEscape(repoID)+"/events/", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/zip")
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 300 {
			err := backendRequestError("backend repo events upload failed", resp)
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		if c.Progress != nil {
			c.Progress(i+1, len(uploads), "")
		}
	}

	now := time.Now().UTC()
	cfg.LastSyncedAt = &now
	_ = SaveRepoConfigFile(storeDir, cfg)

	return nil
}

func (c CloudClient) TriggerAnalyze(repoID string, force bool) error {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(repoID) == "" {
		return nil
	}
	path := "/api/repos/" + url.PathEscape(repoID) + "/analyze"
	if force {
		path += "?force=true"
	}
	req, err := c.newRequest(http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.analyzeHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return backendRequestError("backend analyze failed", resp)
	}
	return nil
}

func backendRequestError(prefix string, resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("%s: %s", prefix, resp.Status)
	}

	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		return fmt.Errorf("%s: %s (%s)", prefix, payload.Error, resp.Status)
	}
	return fmt.Errorf("%s: %s: %s", prefix, resp.Status, strings.TrimSpace(string(body)))
}

func buildRepoEventsZip(repoID, repoRoot string, since time.Time) ([]byte, int, error) {
	sessions, ok, err := loadRepoSessionSummaries(repoID)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, fmt.Errorf("repo session index not found for %s", repoID)
	}

	return buildRepoEventsZipForSessions(repoID, repoRoot, sessions, true)
}

func buildRepoEventsUploads(repoID, repoRoot string, since time.Time) ([][]byte, error) {
	sessions, ok, err := loadRepoSessionSummaries(repoID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("repo session index not found for %s", repoID)
	}
	if !since.IsZero() {
		filtered := sessions[:0]
		for _, session := range sessions {
			if session.Timestamp.After(since) {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}

	var uploads [][]byte
	for start := 0; start < len(sessions); start += maxSessionsPerRepoEventsUpload {
		end := start + maxSessionsPerRepoEventsUpload
		if end > len(sessions) {
			end = len(sessions)
		}
		payload, _, err := buildRepoEventsZipForSessions(repoID, repoRoot, sessions[start:end], start == 0)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, payload)
	}
	if len(sessions) == 0 {
		payload, n, err := buildRepoEventsZipForSessions(repoID, repoRoot, nil, true)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			uploads = append(uploads, payload)
		}
	}
	return uploads, nil
}

func buildRepoEventsZipForSessions(repoID, repoRoot string, sessions []SessionSummary, includeMetadata bool) ([]byte, int, error) {
	storeDir := filepath.Join(RepoGuideDir(), "repos", repoID)
	cfg, _ := LoadRepoConfigFile(storeDir)
	var n int
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, session := range sessions {
		eventsPath := filepath.Join(RepoGuideDir(), "sessions", session.ID, sessionEventLogFileName)
		data, err := os.ReadFile(eventsPath)
		if err != nil {
			return nil, 0, err
		}
		data, err = sanitizeEventLogForUpload(data, cfg)
		if err != nil {
			return nil, 0, err
		}
		entry, err := zw.Create(filepath.ToSlash(filepath.Join("sessions", session.ID, sessionEventLogFileName)))
		if err != nil {
			return nil, 0, err
		}
		if _, err := entry.Write(data); err != nil {
			return nil, 0, err
		}
		n++
	}

	if includeMetadata {
		if docs := readHintFileDocsFromConfig(cfg, repoRoot); len(docs) > 0 {
			data, err := json.Marshal(docs)
			if err != nil {
				return nil, 0, err
			}
			entry, err := zw.Create("docs.json")
			if err != nil {
				return nil, 0, err
			}
			if _, err := entry.Write(data); err != nil {
				return nil, 0, err
			}
			n++
		}

		aliases := buildRepoPathAliases(repoRoot)
		commits := buildRecentGitCommits(repoRoot, 100)
		if len(aliases) > 0 || len(commits) > 0 {
			data, err := json.Marshal(map[string]any{"path_aliases": aliases, "commits": commits})
			if err != nil {
				return nil, 0, err
			}
			entry, err := zw.Create("git_history.json")
			if err != nil {
				return nil, 0, err
			}
			if _, err := entry.Write(data); err != nil {
				return nil, 0, err
			}
			n++
		}
	}

	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), n, nil
}

type repoGitCommit struct {
	ID           string   `json:"id"`
	Subject      string   `json:"subject"`
	Author       string   `json:"author,omitempty"`
	Timestamp    string   `json:"timestamp,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

func buildRecentGitCommits(repoRoot string, limit int) []repoGitCommit {
	if strings.TrimSpace(repoRoot) == "" || limit <= 0 {
		return nil
	}
	cmd := exec.Command("git", "-C", repoRoot, "log", "-n", strconv.Itoa(limit), "--format=%x1e%H%x1f%an%x1f%aI%x1f%s", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var commits []repoGitCommit
	for _, record := range strings.Split(string(out), "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		lines := strings.Split(record, "\n")
		fields := strings.Split(lines[0], "\x1f")
		if len(fields) != 4 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		commit := repoGitCommit{ID: fields[0], Author: fields[1], Timestamp: fields[2], Subject: fields[3]}
		for _, path := range lines[1:] {
			if path = strings.TrimSpace(path); path != "" {
				commit.ChangedFiles = append(commit.ChangedFiles, filepath.ToSlash(path))
			}
		}
		commits = append(commits, commit)
	}
	return commits
}

func buildRepoPathAliases(repoRoot string) map[string]string {
	cmd := exec.Command("git", "-C", repoRoot, "log", "--all", "--format=", "--name-status", "--find-renames=90%", "--diff-filter=R")
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}

	aliases := map[string]string{}
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line[0] != 'R' {
			continue
		}
		parts := strings.Split(raw, "\t")
		if len(parts) < 3 {
			continue
		}
		from := filepath.ToSlash(strings.TrimSpace(parts[1]))
		to := filepath.ToSlash(strings.TrimSpace(parts[2]))
		if from == "" || to == "" || from == to {
			continue
		}
		target := aliases[to]
		if target == "" {
			target = to
		}
		aliases[from] = target
		aliases[to] = target
	}

	outAliases := make(map[string]string, len(aliases))
	for from, to := range aliases {
		if from != to {
			outAliases[from] = to
		}
	}
	if len(outAliases) == 0 {
		return nil
	}
	return outAliases
}

// ParseRepoEventsForLocal parses local session artifacts for repoID and returns
// them as model.RepoSessionEvents for ingestion into the local SQLite store.
// Mirrors UploadRepoEvents: warms session caches before reading.
func ParseRepoEventsForLocal(repoID, repoRoot string) ([]model.RepoSessionEvents, map[string]string, error) {
	if _, err := WarmSessionCaches(SessionLoadOptions{}); err != nil {
		return nil, nil, err
	}
	if _, err := WarmRepoSessionArtifacts(repoRoot, nil); err != nil {
		return nil, nil, err
	}
	sessions, ok, err := loadRepoSessionSummaries(repoID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, readHintFileDocs(repoID, repoRoot), nil
	}
	out := make([]model.RepoSessionEvents, 0, len(sessions))
	for _, s := range sessions {
		artifacts, err := BuildSessionArtifacts(s)
		if err != nil {
			continue
		}
		data, err := json.Marshal(artifacts.EventLog.Events)
		if err != nil {
			continue
		}
		var events []model.SessionEvent
		if err := json.Unmarshal(data, &events); err != nil {
			continue
		}
		out = append(out, model.RepoSessionEvents{
			Agent:     s.Agent,
			ID:        s.ID,
			Name:      s.Name,
			Events:    events,
			UpdatedAt: s.Timestamp,
		})
	}
	return out, readHintFileDocs(repoID, repoRoot), nil
}

func readHintFileDocs(repoID, repoRoot string) map[string]string {
	storeDir := filepath.Join(RepoGuideDir(), "repos", repoID)
	cfg, err := LoadRepoConfigFile(storeDir)
	if err != nil {
		return nil
	}
	return readHintFileDocsFromConfig(cfg, repoRoot)
}

func readHintFileDocsFromConfig(cfg RepoConfig, repoRoot string) map[string]string {
	docs := make(map[string]string, len(cfg.HintFiles))
	for _, name := range cfg.HintFiles {
		path, ok := safeHintFilePath(repoRoot, name)
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			docs[name] = string(data)
		}
	}
	for name, content := range readAgentMemoryDocs(repoRoot) {
		docs[name] = content
	}
	if len(docs) == 0 {
		return nil
	}
	return docs
}

func safeHintFilePath(repoRoot, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || filepath.IsAbs(name) {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".md" && ext != ".txt" {
		return "", false
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	root, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		root, err = filepath.Abs(repoRoot)
		if err != nil {
			return "", false
		}
	}
	candidate := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return resolved, true
}

func sanitizeEventLogForUpload(data []byte, cfg RepoConfig) ([]byte, error) {
	var log SessionEventLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, err
	}
	events := append([]SessionEvent(nil), log.Events...)
	redactEvents(events)
	if !rawPromptsEnabled(cfg) {
		for i := range events {
			if events[i].Kind == "prompt" {
				events[i].Text = "[redacted prompt]"
				events[i].SearchQuery = ""
			}
		}
	}
	log.Events = events
	return json.MarshalIndent(log, "", "  ")
}

func rawPromptsEnabled(cfg RepoConfig) bool {
	globalCfg, err := LoadGlobalConfig()
	if err != nil {
		return true
	}
	switch v := globalCfg.Privacy.RawPrompts.(type) {
	case bool:
		return v
	case string:
		return rawPromptsStringEnabled(v)
	default:
		return true
	}
}

func rawPromptsStringEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "yes", "true", "on", "raw", "always":
		return true
	case "no", "false", "off", "never", "local_only", "local-only":
		return false
	default:
		return true
	}
}

type LimitInfo = contracts.LimitInfo

type LimitsResponse = contracts.LimitsResponse

func (c CloudClient) GetLimits() (*LimitsResponse, error) {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	req, err := c.newRequest(http.MethodGet, "/api/limits", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend limits failed", resp)
	}
	var out LimitsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MeResponse is the response from GET /api/auth/me.
type MeResponse = contracts.MeResponse

// AuthSessionResponse is returned by auth endpoints that mint a bearer token.
type AuthSessionResponse = contracts.AuthSessionResponse

func (c CloudClient) DeleteMe() error {
	req, err := c.newRequest(http.MethodDelete, "/api/auth/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return backendRequestError("account deletion failed", resp)
	}
	return nil
}

func (c CloudClient) GetMe() (*MeResponse, error) {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("not authenticated")
	}
	req, err := c.newRequest(http.MethodGet, "/api/auth/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend auth check failed", resp)
	}
	var out MeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c CloudClient) RefreshAuthToken() (*AuthSessionResponse, error) {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("not authenticated")
	}
	req, err := c.newRequest(http.MethodPost, "/api/auth/refresh", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend token refresh failed", resp)
	}
	var out AuthSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type MCPTopicSummary = contracts.MCPTopicSummary

type MCPTopicStartFile = contracts.MCPTopicStartFile

type MCPTopicImportantFiles = contracts.MCPTopicImportantFiles

type MCPTopicTests = contracts.MCPTopicTests

type MCPTopicContext = contracts.MCPTopicContext

func (c CloudClient) GetMCPTopics(repoID string) ([]MCPTopicSummary, error) {
	if c.shouldUseLocal(repoID) {
		return c.localGetMCPTopics(repoID)
	}
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	req, err := c.newRequest(http.MethodGet, "/api/repos/"+url.PathEscape(repoID)+"/mcp/topics", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	c.setAgentHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend mcp topics failed", resp)
	}
	var out struct {
		Topics []MCPTopicSummary `json:"topics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Topics, nil
}

type MCPSearchContext = contracts.MCPSearchContext

func (c CloudClient) GetMCPSearchContext(repoID, topicID, query string) (*MCPSearchContext, error) {
	if c.shouldUseLocal(repoID) {
		return c.localGetMCPSearchContext(repoID, topicID, query)
	}
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	values := url.Values{}
	values.Set("topic_id", topicID)
	values.Set("query", query)
	path := "/api/repos/" + url.PathEscape(repoID) + "/mcp/search?" + values.Encode()
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	c.setAgentHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend mcp search context failed", resp)
	}
	var out MCPSearchContext
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c CloudClient) GetMCPTopicContext(repoID, topicID string) (*MCPTopicContext, error) {
	if c.shouldUseLocal(repoID) {
		return c.localGetMCPTopicContext(repoID, topicID)
	}
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	req, err := c.newRequest(http.MethodGet, "/api/repos/"+url.PathEscape(repoID)+"/mcp/topics/"+url.PathEscape(topicID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	c.setAgentHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend mcp topic context failed", resp)
	}
	var ctx MCPTopicContext
	if err := json.NewDecoder(resp.Body).Decode(&ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
}

type MCPUnderstandTaskResult = contracts.MCPUnderstandTaskResult

func (c CloudClient) GetMCPUnderstandTask(repoID, task, topicID string, prompts []string) (*MCPUnderstandTaskResult, error) {
	if c.shouldUseLocal(repoID) {
		return c.localGetMCPUnderstandTask(repoID, task, topicID, prompts)
	}
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	body, _ := json.Marshal(contracts.MCPUnderstandTaskRequest{Task: task, TopicID: topicID, Prompts: prompts})
	req, err := c.newRequest(http.MethodPost, "/api/repos/"+url.PathEscape(repoID)+"/mcp/understand-task", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	c.setAgentHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("understand-task failed", resp)
	}
	var out MCPUnderstandTaskResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type MCPCallCreateRequest = contracts.MCPCallCreateRequest

type MCPCallCreateResponse = contracts.MCPCallCreateResponse

func (c CloudClient) CreateMCPCall(repoID string, reqBody MCPCallCreateRequest) (*MCPCallCreateResponse, error) {
	if c.shouldUseLocal(repoID) {
		return c.localCreateMCPCall(repoID, reqBody)
	}
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(repoID) == "" {
		return nil, nil
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/repos/"+url.PathEscape(repoID)+"/mcp-calls", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	c.setAgentHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, backendRequestError("backend mcp call create failed", resp)
	}
	var out MCPCallCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type MCPFeedbackRequest = contracts.MCPFeedbackRequest

func (c CloudClient) RecordMCPFeedback(repoID string, req MCPFeedbackRequest) error {
	if c.shouldUseLocal(repoID) {
		return c.localRecordMCPFeedback(repoID, req)
	}
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(repoID) == "" {
		return nil
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := c.newRequest(http.MethodPost, "/api/repos/"+url.PathEscape(repoID)+"/mcp/feedback", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAgentHeaders(httpReq)
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return backendRequestError("record feedback failed", resp)
	}
	return nil
}

func (c CloudClient) setAgentHeaders(req *http.Request) {
	if c.AgentName != "" {
		req.Header.Set("X-Agent-Name", c.AgentName)
	}
	if c.AgentVersion != "" {
		req.Header.Set("X-Agent-Version", c.AgentVersion)
	}
	if c.SessionID != "" {
		req.Header.Set("X-Agent-Session-ID", c.SessionID)
	}
}

func (c CloudClient) CheckRequiredVersion() (string, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return "", nil
	}
	req, err := c.newRequest(http.MethodGet, "/version", nil)
	if err != nil {
		return "", err
	}
	client := c.backendHTTPClient(3 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		RequiredCLIVersion string `json:"required_cli_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.RequiredCLIVersion, nil
}

func (c CloudClient) httpClient() *http.Client {
	return c.backendHTTPClient(defaultBackendTimeout)
}

func (c CloudClient) analyzeHTTPClient() *http.Client {
	return c.backendHTTPClient(analyzeBackendTimeout)
}

func (c CloudClient) backendHTTPClient(timeout time.Duration) *http.Client {
	if c.Client != nil {
		return withBackendRedirectPolicy(c.Client)
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: validateBackendRedirect,
	}
}

func withBackendRedirectPolicy(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	out := *client
	original := client.CheckRedirect
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateBackendRedirect(req, via); err != nil {
			return err
		}
		if original != nil {
			return original(req, via)
		}
		return nil
	}
	return &out
}

// ── Local-mode overrides ──────────────────────────────────────────────────────
// These methods are called when localSvc is set (local SQLite mode).
// They call core/services directly and adapt types via JSON round-trip,
// which is safe because the JSON tags match between core/model and the CLI's local types.

func jsonRoundTrip[Src any, Dst any](src Src) (*Dst, error) {
	data, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var dst Dst
	return &dst, json.Unmarshal(data, &dst)
}

func (c CloudClient) localGetMCPTopics(repoID string) ([]MCPTopicSummary, error) {
	topics, err := c.localSvc.MCP.ListTopics(context.Background(), repoID)
	if err != nil {
		return nil, err
	}
	out := make([]MCPTopicSummary, len(topics))
	for i, t := range topics {
		out[i] = MCPTopicSummary{ID: t.ID, Name: t.Name, Summary: t.Summary}
	}
	return out, nil
}

func (c CloudClient) localGetMCPTopicContext(repoID, topicID string) (*MCPTopicContext, error) {
	t, err := c.localSvc.MCP.GetTopicContext(context.Background(), repoID, topicID)
	if err != nil || t == nil {
		return nil, err
	}
	return jsonRoundTrip[model.TopicContext, MCPTopicContext](*t)
}

func (c CloudClient) localGetMCPSearchContext(repoID, topicID, _ string) (*MCPSearchContext, error) {
	sc, err := c.localSvc.MCP.GetSearchContext(context.Background(), repoID, topicID)
	if err != nil || sc == nil {
		return nil, err
	}
	return jsonRoundTrip[model.BundleSearchData, MCPSearchContext](*sc)
}

func (c CloudClient) localGetMCPUnderstandTask(repoID, task, topicID string, prompts []string) (*MCPUnderstandTaskResult, error) {
	svc := c.localSvc
	if c.localSvcFactory != nil {
		if scoped := c.localSvcFactory(repoID); scoped != nil {
			svc = scoped
		}
	}
	if svc == nil {
		return nil, fmt.Errorf("local services not configured")
	}
	out, err := svc.MCP.UnderstandTask(context.Background(), repoID, task, topicID, prompts)
	if err != nil || out == nil {
		return nil, err
	}
	return &MCPUnderstandTaskResult{
		Status:            out.Status,
		Explanation:       out.Explanation,
		TopicID:           out.TopicID,
		MatchConfidence:   out.MatchConfidence,
		ContextText:       out.ContextText,
		SelectedAdvice:    out.SelectedAdvice,
		Reason:            out.Reason,
		Question:          out.Question,
		CandidateTopics:   out.CandidateTopics,
		CandidateTopicIDs: out.CandidateTopicIDs,
	}, nil
}

func (c CloudClient) localCreateMCPCall(repoID string, req MCPCallCreateRequest) (*MCPCallCreateResponse, error) {
	call := &model.MCPCall{
		RepoID:       repoID,
		Command:      req.Command,
		Inputs:       req.Inputs,
		Response:     req.Response,
		AgentName:    req.AgentName,
		AgentVersion: req.AgentVersion,
		SessionID:    req.SessionID,
	}
	created, err := c.localSvc.Feedback.CreateMCPCall(context.Background(), call)
	if err != nil || created == nil {
		return nil, err
	}
	slog.Debug("local MCP call created", "call_id", created.CallID, "command", req.Command, "repo_id", repoID)
	return &MCPCallCreateResponse{CallID: created.CallID, CreatedAt: created.CreatedAt}, nil
}

func (c CloudClient) localRecordMCPFeedback(repoID string, req MCPFeedbackRequest) error {
	slog.Info("recording feedback (local)", "repo_id", repoID, "stars", req.Stars, "helpfulness", req.Helpfulness, "topic_id", req.TopicID)
	fb := &model.MCPFeedback{
		RepoID:              repoID,
		Task:                req.Task,
		Stars:               req.Stars,
		Helpfulness:         req.Helpfulness,
		HelpedWith:          req.HelpedWith,
		Quote:               req.Quote,
		MissingContext:      req.MissingContext,
		WhatWentWrong:       req.WhatWentWrong,
		WhatCouldBeImproved: req.WhatCouldBeImproved,
		AdviceEvaluation:    req.AdviceEvaluation,
		CandidateRule:       req.CandidateRule,
		MCPCallID:           req.MCPCallID,
		TopicID:             req.TopicID,
	}
	_, err := c.localSvc.Feedback.PutFeedback(context.Background(), fb)
	return err
}
