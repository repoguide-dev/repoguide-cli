package sessionimport

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type SessionLoadOptions struct {
	Progress func(current, total int, label string)
	Repo     string
}

type SessionPage struct {
	Sessions []SessionSummary
	Total    int
	Offset   int
	Limit    int
}

type SessionIndexWarmResult struct {
	Agent        string
	Location     string
	SessionCount int
}

type RepoSessionStats struct {
	Repo        RepoConfig
	Configured  bool
	AgentCounts map[string]int
	Total       int
	Online      bool
	LastSynced  time.Time
}

type codexIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

type codexEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

type codexTurnContextPayload struct {
	Cwd   string `json:"cwd"`
	Model string `json:"model"`
}

type codexAITitle struct {
	Type      string `json:"type"`
	AITitle   string `json:"aiTitle"`
	SessionID string `json:"sessionId"`
}

type claudeEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   *claudeMessage  `json:"message"`
	SessionID string          `json:"sessionId"`
	Payload   json.RawMessage `json:"payload"`
}

type claudeMessage struct {
	Model string `json:"model"`
}

type claudeAttachment struct {
	Type string `json:"type"`
}

type sessionIndexFile struct {
	Version   int                 `json:"version"`
	Agent     string              `json:"agent"`
	Location  string              `json:"location"`
	UpdatedAt string              `json:"updatedAt"`
	Entries   []sessionIndexEntry `json:"entries"`
}

type repoSessionIndexFile struct {
	Version   int                 `json:"version"`
	RepoID    string              `json:"repoId"`
	RepoRoot  string              `json:"repoRoot"`
	UpdatedAt string              `json:"updatedAt"`
	Entries   []sessionIndexEntry `json:"entries"`
}

type sessionIndexEntry struct {
	ID              string  `json:"id"`
	Agent           string  `json:"agent"`
	Name            string  `json:"name"`
	Cwd             string  `json:"cwd"`
	Model           string  `json:"model"`
	Timestamp       string  `json:"timestamp"`
	Path            string  `json:"path"`
	RepoID          string  `json:"repoId,omitempty"`
	UsedRepoGuide   bool    `json:"usedRepoGuide,omitempty"`
	FileModUnixNano int64   `json:"fileModUnixNano"`
	FileSize        int64   `json:"fileSize"`
	CostUSD         float64 `json:"costUsd,omitempty"`
	ReadFileCount   int     `json:"readFileCount,omitempty"`
	EditFileCount   int     `json:"editFileCount,omitempty"`
}

type sessionSource struct {
	agent    string
	location string
	paths    []string
}

const sessionIndexVersion = 3

func SupportedSessionAgents() []string {
	return []string{"codex", "claude", "cursor", "opencode", "copilot", "vscode-copilot", "gemini"}
}

func CountSessions(agent string) (int, error) {
	return QuickCountSessions(agent), nil
}

func QuickCountSessions(agent string) int {
	switch strings.ToLower(agent) {
	case "codex":
		return len(append(glob(home(".codex", "sessions", "*", "*", "*", "*.jsonl")), glob(home(".codex", "archived_sessions", "*.jsonl"))...))
	case "claude":
		return len(glob(home(".claude", "projects", "*", "*.jsonl")))
	case "cursor":
		return len(glob(home(".cursor", "projects", "*", "agent-transcripts", "*", "*.jsonl")))
	case "opencode":
		materializeOpencodeDB()
		return len(glob(home(".local", "share", "opencode", "storage", "session", "*", "*.json"))) +
			len(glob(filepath.Join(opencodeDBCacheRoot(), "session", "*", "*.json")))
	case "copilot":
		return len(glob(home(".copilot", "session-state", "*", "events.jsonl")))
	case "vscode-copilot":
		return len(vscodeCopilotSessionPaths())
	case "gemini":
		return len(glob(home(".gemini", "tmp", "*", "chats", "session-*.jsonl")))
	default:
		return 0
	}
}

func LoadSessions(agent string) ([]SessionSummary, error) {
	page, err := LoadSessionPage(agent, 0, -1, SessionLoadOptions{})
	if err != nil {
		return nil, err
	}
	return page.Sessions, nil
}

func LoadSessionPage(agent string, offset, limit int, opts SessionLoadOptions) (SessionPage, error) {
	repos, _ := ListConfiguredRepos()
	annotator := newSessionAnnotator(repos)

	source, err := sessionSourceForAgent(agent)
	if err != nil {
		return SessionPage{}, err
	}
	if len(source.paths) == 0 {
		return SessionPage{}, noSessionsError(agent)
	}

	entries, err := syncSessionIndex(source, annotator, opts)
	if err != nil {
		return SessionPage{}, err
	}
	entries = filterSessionEntriesByRepo(entries, repos, opts.Repo)

	total := len(entries)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit < 0 || offset+limit > total {
		limit = total - offset
	}

	summaries := make([]SessionSummary, 0, limit)
	for _, entry := range entries[offset : offset+limit] {
		summaries = append(summaries, entry.toSummary(repos))
	}

	return SessionPage{
		Sessions: summaries,
		Total:    total,
		Offset:   offset,
		Limit:    limit,
	}, nil
}

func LoadRepoSessionStats() ([]RepoSessionStats, error) {
	repos, err := ListConfiguredRepos()
	if err != nil {
		return nil, err
	}

	statsByID := make(map[string]*RepoSessionStats, len(repos))
	statsByRoot := make(map[string]*RepoSessionStats, len(repos))
	for _, repo := range repos {
		repoCopy := repo
		s := &RepoSessionStats{
			Repo:        repoCopy,
			Configured:  true,
			AgentCounts: make(map[string]int, len(SupportedSessionAgents())),
		}
		statsByID[repo.RepoID] = s
		if repo.RepoRoot != "" {
			statsByRoot[normalizePathForCompare(repo.RepoRoot)] = s
		}
	}

	// keyed by repo root for unconfigured repos discovered from session history
	unconfiguredByRoot := make(map[string]*RepoSessionStats)

	annotator := newSessionAnnotator(repos)
	for _, agent := range SupportedSessionAgents() {
		source, err := sessionSourceForAgent(agent)
		if err != nil || len(source.paths) == 0 {
			continue
		}

		entries, err := syncSessionIndex(source, annotator, SessionLoadOptions{})
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.RepoID != "" {
				stats := statsByID[entry.RepoID]
				if stats == nil {
					continue
				}
				stats.AgentCounts[agent]++
				stats.Total++
				continue
			}
			if entry.Cwd == "" {
				continue
			}
			root := annotator.detectRepoRoot(entry.Cwd)
			if root == "" {
				continue
			}
			if cs := statsByRoot[normalizePathForCompare(root)]; cs != nil {
				cs.AgentCounts[agent]++
				cs.Total++
				continue
			}
			s := unconfiguredByRoot[root]
			if s == nil {
				s = &RepoSessionStats{
					Repo:        RepoConfig{RepoRoot: root},
					Configured:  false,
					AgentCounts: make(map[string]int, len(SupportedSessionAgents())),
				}
				unconfiguredByRoot[root] = s
			}
			s.AgentCounts[agent]++
			s.Total++
		}
	}

	out := make([]RepoSessionStats, 0, len(statsByID)+len(unconfiguredByRoot))
	for _, stats := range unconfiguredByRoot {
		for _, agent := range SupportedSessionAgents() {
			if _, ok := stats.AgentCounts[agent]; !ok {
				stats.AgentCounts[agent] = 0
			}
		}
		out = append(out, *stats)
	}
	for _, stats := range statsByID {
		for _, agent := range SupportedSessionAgents() {
			if _, ok := stats.AgentCounts[agent]; !ok {
				stats.AgentCounts[agent] = 0
			}
		}
		out = append(out, *stats)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Configured != out[j].Configured {
			return out[i].Configured
		}
		return out[i].Repo.RepoRoot < out[j].Repo.RepoRoot
	})
	return out, nil
}

// LoadAllSessionRepoRoots returns unique repo roots found across all session
// entries, regardless of whether they've been initialized with repoguide.
// Only paths that exist on disk and are git repos are included.
func LoadAllSessionRepoRoots() ([]string, error) {
	repos, _ := ListConfiguredRepos()
	annotator := newSessionAnnotator(repos)
	reposByID := make(map[string]RepoConfig, len(repos))
	for _, r := range repos {
		reposByID[r.RepoID] = r
	}
	seen := make(map[string]struct{})
	var out []string
	for _, agent := range SupportedSessionAgents() {
		source, err := sessionSourceForAgent(agent)
		if err != nil || len(source.paths) == 0 {
			continue
		}
		entries, err := syncSessionIndex(source, annotator, SessionLoadOptions{})
		if err != nil {
			continue
		}
		for _, e := range entries {
			var root string
			if e.RepoID != "" {
				if r, ok := reposByID[e.RepoID]; ok {
					root = r.RepoRoot
				}
			} else if e.Cwd != "" {
				root = annotator.detectRepoRoot(e.Cwd)
			}
			if root == "" {
				continue
			}
			if _, ok := seen[root]; ok {
				continue
			}
			seen[root] = struct{}{}
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				continue
			}
			if _, err := gitOutput("-C", root, "rev-parse", "--show-toplevel"); err != nil {
				continue
			}
			out = append(out, root)
		}
	}
	sort.Strings(out)
	return out, nil
}

func WarmSessionCaches(opts SessionLoadOptions) ([]SessionIndexWarmResult, error) {
	repos, _ := ListConfiguredRepos()
	annotator := newSessionAnnotator(repos)

	sources := make([]sessionSource, 0, len(SupportedSessionAgents()))
	totalWork := 0
	for _, agent := range SupportedSessionAgents() {
		source, err := sessionSourceForAgent(agent)
		if err != nil || len(source.paths) == 0 {
			continue
		}
		sources = append(sources, source)
		totalWork += len(source.paths)
	}

	results := make([]SessionIndexWarmResult, 0, len(sources))
	completed := 0
	repoEntries := make(map[string][]sessionIndexEntry, len(repos))
	for _, source := range sources {
		entries, err := syncSessionIndex(source, annotator, SessionLoadOptions{
			Progress: func(current, total int, label string) {
				if opts.Progress == nil {
					return
				}
				opts.Progress(completed+current, totalWork, label)
			},
		})
		if err != nil {
			return nil, err
		}
		mergeRepoSessionEntries(repoEntries, repos, entries)
		completed += len(source.paths)
		results = append(results, SessionIndexWarmResult{
			Agent:        source.agent,
			Location:     source.location,
			SessionCount: len(entries),
		})
	}

	if opts.Progress != nil && totalWork == 0 {
		opts.Progress(0, 0, "No local session files found")
	}

	if err := writeRepoSessionIndexes(repos, repoEntries); err != nil {
		return nil, err
	}

	return results, nil
}

func sessionSourceForAgent(agent string) (sessionSource, error) {
	switch strings.ToLower(agent) {
	case "codex":
		return sessionSource{
			agent:    "codex",
			location: home(".codex"),
			paths: append(
				glob(home(".codex", "sessions", "*", "*", "*", "*.jsonl")),
				glob(home(".codex", "archived_sessions", "*.jsonl"))...,
			),
		}, nil
	case "claude":
		return sessionSource{
			agent:    "claude",
			location: home(".claude", "projects"),
			paths:    glob(home(".claude", "projects", "*", "*.jsonl")),
		}, nil
	case "cursor":
		return sessionSource{
			agent:    "cursor",
			location: home(".cursor", "projects"),
			paths:    glob(home(".cursor", "projects", "*", "agent-transcripts", "*", "*.jsonl")),
		}, nil
	case "opencode":
		materializeOpencodeDB()
		return sessionSource{
			agent:    "opencode",
			location: home(".local", "share", "opencode"),
			paths: append(
				glob(home(".local", "share", "opencode", "storage", "session", "*", "*.json")),
				glob(filepath.Join(opencodeDBCacheRoot(), "session", "*", "*.json"))...,
			),
		}, nil
	case "copilot":
		return sessionSource{
			agent:    "copilot",
			location: home(".copilot", "session-state"),
			paths:    glob(home(".copilot", "session-state", "*", "events.jsonl")),
		}, nil
	case "vscode-copilot":
		return sessionSource{
			agent:    "vscode-copilot",
			location: "VS Code workspaceStorage",
			paths:    vscodeCopilotSessionPaths(),
		}, nil
	case "gemini":
		return sessionSource{
			agent:    "gemini",
			location: home(".gemini", "tmp"),
			paths:    glob(home(".gemini", "tmp", "*", "chats", "session-*.jsonl")),
		}, nil
	default:
		return sessionSource{}, fmt.Errorf("unsupported agent %q", agent)
	}
}

func LoadAllAgentsSessionPage(offset, limit int, opts SessionLoadOptions) (SessionPage, error) {
	repos, _ := ListConfiguredRepos()
	annotator := newSessionAnnotator(repos)

	var all []sessionIndexEntry
	for _, agent := range SupportedSessionAgents() {
		source, err := sessionSourceForAgent(agent)
		if err != nil || len(source.paths) == 0 {
			continue
		}
		entries, err := syncSessionIndex(source, annotator, opts)
		if err != nil {
			continue
		}
		all = append(all, filterSessionEntriesByRepo(entries, repos, opts.Repo)...)
	}

	sort.Slice(all, func(i, j int) bool {
		return parseTime(all[i].Timestamp).After(parseTime(all[j].Timestamp))
	})

	total := len(all)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit < 0 || offset+limit > total {
		limit = total - offset
	}

	summaries := make([]SessionSummary, 0, limit)
	for _, entry := range all[offset : offset+limit] {
		summaries = append(summaries, entry.toSummary(repos))
	}
	return SessionPage{Sessions: summaries, Total: total, Offset: offset, Limit: limit}, nil
}

func noSessionsError(agent string) error {
	switch strings.ToLower(agent) {
	case "codex":
		return errors.New("no Codex sessions found")
	case "claude":
		return errors.New("no Claude Code sessions found")
	case "cursor":
		return errors.New("no Cursor sessions found")
	case "opencode":
		return errors.New("no OpenCode sessions found")
	case "copilot":
		return errors.New("no GitHub Copilot sessions found")
	case "vscode-copilot":
		return errors.New("no VS Code Copilot Chat sessions found")
	case "gemini":
		return errors.New("no Gemini CLI sessions found")
	default:
		return errors.New("no sessions found")
	}
}

func syncSessionIndex(source sessionSource, annotator *sessionAnnotator, opts SessionLoadOptions) ([]sessionIndexEntry, error) {
	cachePath := sessionIndexPath(source.agent, source.location)
	cache, _ := readSessionIndex(cachePath)
	cacheByPath := make(map[string]sessionIndexEntry, len(cache.Entries))
	for _, entry := range cache.Entries {
		cacheByPath[entry.Path] = entry
	}

	indexByID := map[string]codexIndexEntry{}
	if source.agent == "codex" {
		indexEntries, _ := readCodexIndex(home(".codex", "session_index.jsonl"))
		indexByID = make(map[string]codexIndexEntry, len(indexEntries))
		for _, entry := range indexEntries {
			indexByID[entry.ID] = entry
		}
	}

	sort.Slice(source.paths, func(i, j int) bool {
		return source.paths[i] > source.paths[j]
	})

	entries := make([]sessionIndexEntry, 0, len(source.paths))
	total := len(source.paths)
	for i, path := range source.paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		if opts.Progress != nil {
			opts.Progress(i+1, total, fmt.Sprintf("Syncing %s session index", displayAgentName(source.agent)))
		}

		cached, ok := cacheByPath[path]
		if ok && cached.FileModUnixNano == info.ModTime().UnixNano() && cached.FileSize == info.Size() {
			cached = refreshCachedSessionEntry(cached, annotator)
			entries = append(entries, cached)
			continue
		}

		session, err := readSession(source.agent, path, indexByID)
		if err != nil {
			continue
		}
		annotator.annotate(&session)
		entries = append(entries, entryFromSession(session, info))
	}

	sort.Slice(entries, func(i, j int) bool {
		return parseTime(entries[i].Timestamp).After(parseTime(entries[j].Timestamp))
	})

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		_ = writeJSON(cachePath, sessionIndexFile{
			Version:   sessionIndexVersion,
			Agent:     source.agent,
			Location:  source.location,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Entries:   entries,
		})
	}

	return entries, nil
}

func refreshCachedSessionEntry(entry sessionIndexEntry, annotator *sessionAnnotator) sessionIndexEntry {
	session := SessionSummary{
		ID:        entry.ID,
		Agent:     entry.Agent,
		Name:      entry.Name,
		Cwd:       entry.Cwd,
		Model:     entry.Model,
		Timestamp: parseTime(entry.Timestamp),
		Path:      entry.Path,
	}
	annotator.annotate(&session)
	entry.RepoID = session.RepoID
	return entry
}

func sessionIndexPath(agent, location string) string {
	sum := sha1.Sum([]byte(location))
	return filepath.Join(
		home(".repoguide", "cache", "sessions"),
		fmt.Sprintf("%s-%s.json", agent, hex.EncodeToString(sum[:])),
	)
}

func readSessionIndex(path string) (sessionIndexFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionIndexFile{}, err
	}
	var index sessionIndexFile
	if err := json.Unmarshal(data, &index); err != nil {
		return sessionIndexFile{}, err
	}
	if index.Version != sessionIndexVersion {
		return sessionIndexFile{}, errors.New("stale session index")
	}
	return index, nil
}

func readRepoSessionIndex(path string) (repoSessionIndexFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return repoSessionIndexFile{}, err
	}
	var index repoSessionIndexFile
	if err := json.Unmarshal(data, &index); err != nil {
		return repoSessionIndexFile{}, err
	}
	if index.Version != sessionIndexVersion {
		return repoSessionIndexFile{}, errors.New("stale repo session index")
	}
	return index, nil
}

func entryFromSession(session SessionSummary, info os.FileInfo) sessionIndexEntry {
	entry := sessionIndexEntry{
		ID:              session.ID,
		Agent:           session.Agent,
		Name:            session.Name,
		Cwd:             session.Cwd,
		Model:           session.Model,
		Timestamp:       session.Timestamp.UTC().Format(time.RFC3339Nano),
		Path:            session.Path,
		RepoID:          session.RepoID,
		UsedRepoGuide:   session.UsedRepoGuide,
		FileModUnixNano: info.ModTime().UnixNano(),
		FileSize:        info.Size(),
	}
	if cached, ok, _ := LoadCachedSessionAnalysis(session); ok {
		entry.CostUSD = cached.Analysis.Metrics.EstimatedCostUSD
		entry.ReadFileCount = cached.Analysis.Metrics.ReadFileCount
		entry.EditFileCount = cached.Analysis.Metrics.EditedFileCount
	}
	return entry
}

func (e sessionIndexEntry) toSummary(repos []RepoConfig) SessionSummary {
	s := SessionSummary{
		ID:            e.ID,
		Agent:         e.Agent,
		Name:          e.Name,
		Cwd:           e.Cwd,
		Model:         e.Model,
		Timestamp:     parseTime(e.Timestamp),
		Path:          e.Path,
		RepoID:        e.RepoID,
		UsedRepoGuide: e.UsedRepoGuide,
		CostUSD:       e.CostUSD,
		ReadFileCount: e.ReadFileCount,
		EditFileCount: e.EditFileCount,
	}
	for _, r := range repos {
		if r.RepoID == e.RepoID && e.RepoID != "" {
			s.RepoRoot = r.RepoRoot
			s.RepoName = filepath.Base(r.RepoRoot)
			s.RepoInitialized = true
			if rel, err := filepath.Rel(normalizePathForCompare(r.RepoRoot), normalizePathForCompare(e.Cwd)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				s.RepoRelativeCwd = filepath.ToSlash(rel)
			}
			break
		}
	}
	return s
}

func readSession(agent, path string, indexByID map[string]codexIndexEntry) (SessionSummary, error) {
	switch agent {
	case "codex":
		return readCodexSession(path, indexByID)
	case "claude":
		return readClaudeSession(path)
	case "cursor":
		return readCursorSession(path)
	case "opencode":
		return readOpencodeSession(path)
	case "copilot":
		return readCopilotSession(path)
	case "vscode-copilot":
		return readVSCodeCopilotSession(path)
	case "gemini":
		return readGeminiSession(path)
	default:
		return SessionSummary{}, fmt.Errorf("unsupported agent %q", agent)
	}
}

func loadRepoSessionSummaries(repoFilter string) ([]SessionSummary, bool, error) {
	repo, ok, err := resolveConfiguredRepo(repoFilter)
	if err != nil || !ok {
		return nil, false, err
	}

	index, err := readRepoSessionIndex(repoSessionIndexPath(repo))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	summaries := make([]SessionSummary, 0, len(index.Entries))
	for _, entry := range index.Entries {
		summaries = append(summaries, entry.toSummary([]RepoConfig{repo}))
	}
	return summaries, true, nil
}

func resolveConfiguredRepo(repoFilter string) (RepoConfig, bool, error) {
	repoFilter = strings.TrimSpace(repoFilter)
	if repoFilter == "" {
		return RepoConfig{}, false, nil
	}

	repos, err := ListConfiguredRepos()
	if err != nil {
		return RepoConfig{}, false, err
	}
	filterPath := normalizePathForCompare(repoFilter)
	for _, repo := range repos {
		if strings.EqualFold(repo.RepoID, repoFilter) ||
			strings.EqualFold(filepath.Base(repo.RepoRoot), repoFilter) ||
			normalizePathForCompare(repo.RepoRoot) == filterPath {
			return repo, true, nil
		}
	}
	return RepoConfig{}, false, nil
}

func mergeRepoSessionEntries(dst map[string][]sessionIndexEntry, repos []RepoConfig, entries []sessionIndexEntry) {
	for _, entry := range entries {
		if entry.RepoID == "" {
			continue
		}
		for _, repo := range repos {
			if repo.RepoID == entry.RepoID {
				dst[repo.RepoID] = append(dst[repo.RepoID], entry)
				break
			}
		}
	}
}

func writeRepoSessionIndexes(repos []RepoConfig, repoEntries map[string][]sessionIndexEntry) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, repo := range repos {
		entries := append([]sessionIndexEntry(nil), repoEntries[repo.RepoID]...)
		sort.Slice(entries, func(i, j int) bool {
			return parseTime(entries[i].Timestamp).After(parseTime(entries[j].Timestamp))
		})

		path := repoSessionIndexPath(repo)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeJSON(path, repoSessionIndexFile{
			Version:   sessionIndexVersion,
			RepoID:    repo.RepoID,
			RepoRoot:  repo.RepoRoot,
			UpdatedAt: now,
			Entries:   entries,
		}); err != nil {
			return err
		}
	}
	return nil
}

func repoSessionIndexPath(repo RepoConfig) string {
	return filepath.Join(RepoGuideDir(), "repos", repo.RepoID, "cache", "sessions.json")
}

func filterSessionEntriesByRepo(entries []sessionIndexEntry, repos []RepoConfig, repoFilter string) []sessionIndexEntry {
	repoFilter = strings.TrimSpace(repoFilter)
	if repoFilter == "" {
		return entries
	}

	matchedIDs := matchingConfiguredRepoIDs(repos, repoFilter)

	// Build matched roots for CWD-based fallback (entries cached before repo registration have empty RepoID)
	matchedRoots := make(map[string]struct{})
	for _, repo := range repos {
		if _, ok := matchedIDs[repo.RepoID]; ok && repo.RepoRoot != "" {
			matchedRoots[normalizePathForCompare(repo.RepoRoot)] = struct{}{}
		}
	}
	// The filter itself may be a root path (covers unconfigured repos too)
	matchedRoots[normalizePathForCompare(repoFilter)] = struct{}{}

	filtered := make([]sessionIndexEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.RepoID != "" {
			if _, ok := matchedIDs[entry.RepoID]; ok {
				filtered = append(filtered, entry)
			}
			continue
		}
		if entry.Cwd == "" {
			continue
		}
		cwd := normalizePathForCompare(entry.Cwd)
		for root := range matchedRoots {
			if cwd == root || strings.HasPrefix(cwd, root+"/") {
				filtered = append(filtered, entry)
				break
			}
		}
	}
	return filtered
}

func matchingConfiguredRepoIDs(repos []RepoConfig, repoFilter string) map[string]struct{} {
	repoFilter = strings.TrimSpace(repoFilter)
	filterPath := normalizePathForCompare(repoFilter)
	ids := make(map[string]struct{})
	for _, repo := range repos {
		if strings.EqualFold(repo.RepoID, repoFilter) ||
			strings.EqualFold(filepath.Base(repo.RepoRoot), repoFilter) ||
			normalizePathForCompare(repo.RepoRoot) == filterPath {
			ids[repo.RepoID] = struct{}{}
		}
	}
	return ids
}

func readCodexIndex(path string) ([]codexIndexEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []codexIndexEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry codexIndexEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil && entry.ID != "" {
			out = append(out, entry)
		}
	}
	return out, scanner.Err()
}

func readCodexSession(path string, indexByID map[string]codexIndexEntry) (SessionSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return SessionSummary{}, err
	}
	defer file.Close()

	session := SessionSummary{
		Agent: "codex",
		Path:  path,
	}
	pendingRepoGuideCalls := map[string]bool{}
	if info, err := os.Stat(path); err == nil {
		session.Timestamp = info.ModTime()
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var env codexEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}

		switch env.Type {
		case "session_meta":
			var payload codexSessionMetaPayload
			if err := json.Unmarshal(env.Payload, &payload); err == nil {
				if session.ID == "" {
					session.ID = payload.ID
				}
				if session.Cwd == "" {
					session.Cwd = payload.Cwd
				}
			}
		case "turn_context":
			var payload codexTurnContextPayload
			if err := json.Unmarshal(env.Payload, &payload); err == nil {
				if session.Model == "" {
					session.Model = payload.Model
				}
				if session.Cwd == "" {
					session.Cwd = payload.Cwd
				}
			}
		case "ai-title":
			var title codexAITitle
			if err := json.Unmarshal(line, &title); err == nil && session.Name == "" {
				session.Name = title.AITitle
			}
		case "response_item":
			var payload struct {
				Type   string          `json:"type"`
				Name   string          `json:"name"`
				CallID string          `json:"call_id"`
				Output json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(env.Payload, &payload); err == nil {
				switch {
				case payload.Type == "function_call" && isRepoGuideUnderstandTaskToolName(payload.Name) && payload.CallID != "":
					pendingRepoGuideCalls[payload.CallID] = true
				case payload.Type == "function_call_output" && pendingRepoGuideCalls[payload.CallID]:
					if repoGuideResultHasExperience(toolResultText(payload.Output)) {
						session.UsedRepoGuide = true
					}
				}
			}
		}

		if session.ID != "" && session.Cwd != "" && session.Model != "" && session.Name != "" && session.UsedRepoGuide {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return SessionSummary{}, err
	}

	if session.ID == "" {
		session.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if idx, ok := indexByID[session.ID]; ok {
		if session.Name == "" {
			session.Name = idx.ThreadName
		}
		if t := parseTime(idx.UpdatedAt); !t.IsZero() {
			session.Timestamp = t
		}
	}
	if session.Name == "" {
		session.Name = "(untitled)"
	}
	return session, nil
}

func readClaudeSession(path string) (SessionSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return SessionSummary{}, err
	}
	defer file.Close()

	session := SessionSummary{
		Agent: "claude",
		Path:  path,
		ID:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
	}
	pendingRepoGuideCalls := map[string]bool{}
	if info, err := os.Stat(path); err == nil {
		session.Timestamp = info.ModTime()
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var env claudeEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}

		if session.Cwd == "" && env.Cwd != "" {
			session.Cwd = env.Cwd
		}
		if session.Model == "" && env.Message != nil && env.Message.Model != "" {
			session.Model = env.Message.Model
		}

		switch env.Type {
		case "ai-title":
			var title struct {
				AITitle string `json:"aiTitle"`
			}
			if err := json.Unmarshal(line, &title); err == nil && session.Name == "" {
				session.Name = title.AITitle
			}
		case "attachment":
			var payload claudeAttachment
			if err := json.Unmarshal(env.Payload, &payload); err == nil && session.Name == "" && payload.Type == "summary" {
				session.Name = "Summary session"
			}
		case "assistant":
			var payload struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Name string `json:"name"`
						ID   string `json:"id"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(line, &payload); err == nil {
				for _, item := range payload.Message.Content {
					if item.Type == "tool_use" && isRepoGuideUnderstandTaskToolName(item.Name) && item.ID != "" {
						pendingRepoGuideCalls[item.ID] = true
					}
				}
			}
		case "user":
			if session.UsedRepoGuide || len(pendingRepoGuideCalls) == 0 {
				break
			}
			var payload struct {
				Message struct {
					Content []struct {
						Type      string          `json:"type"`
						ToolUseID string          `json:"tool_use_id"`
						IsError   bool            `json:"is_error"`
						Content   json.RawMessage `json:"content"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(line, &payload); err == nil {
				for _, item := range payload.Message.Content {
					if item.Type != "tool_result" || !pendingRepoGuideCalls[item.ToolUseID] || item.IsError {
						continue
					}
					if repoGuideResultHasExperience(toolResultText(item.Content)) {
						session.UsedRepoGuide = true
						break
					}
				}
			}
		}

		if session.Name != "" && session.Cwd != "" && session.Model != "" && session.UsedRepoGuide {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return SessionSummary{}, err
	}

	if session.Name == "" {
		session.Name = "(untitled)"
	}
	return session, nil
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

type sessionAnnotator struct {
	repos     []RepoConfig
	repoCache map[string]string
	mu        sync.Mutex
}

func newSessionAnnotator(repos []RepoConfig) *sessionAnnotator {
	return &sessionAnnotator{
		repos:     repos,
		repoCache: make(map[string]string),
	}
}

func (a *sessionAnnotator) annotate(session *SessionSummary) {
	cwd := strings.TrimSpace(session.Cwd)
	if cwd == "" {
		return
	}

	bestRoot := ""
	for _, repo := range a.repos {
		root := strings.TrimSpace(repo.RepoRoot)
		if root == "" || !pathContains(root, cwd) {
			continue
		}
		if len(root) > len(bestRoot) {
			bestRoot = root
			session.RepoID = repo.RepoID
			session.RepoName = filepath.Base(root)
			session.RepoRoot = root
			session.RepoInitialized = true
		}
	}

	if session.RepoRoot == "" {
		repoRoot := a.detectRepoRoot(cwd)
		if repoRoot == "" {
			return
		}
		session.RepoName = filepath.Base(repoRoot)
		session.RepoRoot = repoRoot
	}

	if rel, err := filepath.Rel(normalizePathForCompare(session.RepoRoot), normalizePathForCompare(cwd)); err == nil && rel != "." {
		session.RepoRelativeCwd = filepath.ToSlash(rel)
	}
}

func (a *sessionAnnotator) detectRepoRoot(start string) string {
	a.mu.Lock()
	if repoRoot, ok := a.repoCache[start]; ok {
		a.mu.Unlock()
		return repoRoot
	}
	a.mu.Unlock()

	dir := start
	visited := make([]string, 0, 8)
	for {
		if dir == "" || dir == "." || dir == string(filepath.Separator) {
			a.cacheRepoRoots("", visited)
			return ""
		}
		visited = append(visited, dir)
		a.mu.Lock()
		if repoRoot, ok := a.repoCache[dir]; ok {
			a.mu.Unlock()
			a.cacheRepoRoots(repoRoot, visited)
			return repoRoot
		}
		a.mu.Unlock()
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			a.cacheRepoRoots(dir, visited)
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			a.cacheRepoRoots("", visited)
			return ""
		}
		dir = parent
	}
}

func (a *sessionAnnotator) cacheRepoRoots(repoRoot string, paths []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, path := range paths {
		a.repoCache[path] = repoRoot
	}
}

func pathContains(root, target string) bool {
	root = normalizePathForCompare(root)
	target = normalizePathForCompare(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func pathsEquivalent(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return normalizePathForCompare(a) == normalizePathForCompare(b)
}

func normalizePathForCompare(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = resolved
	}
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	return cleaned
}

func displayAgentName(agent string) string {
	switch strings.ToLower(agent) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "cursor":
		return "Cursor"
	case "opencode":
		return "OpenCode"
	case "copilot":
		return "GitHub Copilot"
	case "vscode-copilot":
		return "VS Code Copilot Chat"
	case "gemini":
		return "Gemini CLI"
	default:
		return agent
	}
}

// toolResultText flattens a tool_result content field (plain string, or a list
// of {type: "text", text} blocks) into one string.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			parts = append(parts, b.Text)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// repoGuideResultHasExperience reports whether a repoguide_get_repo_experience
// tool result carried topic-specific guidance — the topic-list disambiguation
// response and errors don't count as "used RepoGuide".
func repoGuideResultHasExperience(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.Contains(text, "Task maps to multiple topics") {
		return false
	}
	// ponytail: substring match on the known error prefix; parse structured errors if formats multiply
	if strings.Contains(text, "understand-task failed") {
		return false
	}
	return true
}

func isRepoGuideUnderstandTaskToolName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "repoguide_get_repo_experience", "mcp__repoguide__repoguide_get_repo_experience":
		return true
	default:
		return strings.Contains(name, "repoguide")
	}
}
