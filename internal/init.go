package internal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	repoguideVersion = 1
	hookPreCommit    = "pre-commit"
	hookPostCommit   = "post-commit"
)

var ErrNotGitRepo = errors.New("no Git repository found")

type InitOptions struct {
	Force bool
	Mode  string // "local" or "online"
	// RepoID adopts an existing identity instead of minting a random one. It is
	// used when the caller has already found this directory registered under a
	// known ID (see CloudClient.FindRepoIDForRoot) - local state having been
	// purged does not mean the repository is new. Ignored when git config or the
	// local store already knows this repo.
	RepoID string
}

type InitResult struct {
	RepoRoot           string
	RepoID             string
	StoreDir           string
	AlreadyInit        bool
	GlobalConfig       string
	Hooks              []HookStatus
	HooksPath          string
	HooksPathCustom    bool
	CommitHooksEnabled bool
	Agents             []DetectedSource
}

type HookStatus struct {
	Name      string
	Installed bool
	Skipped   bool
}

type GlobalConfig struct {
	Version    int             `json:"version"`
	Privacy    PrivacyDefaults `json:"privacy"`
	Agreements Agreements      `json:"agreements"`
	OptIns     OptIns          `json:"optIns"`
	CreatedAt  string          `json:"createdAt"`
}

type PrivacyDefaults struct {
	RawPrompts   any    `json:"rawPrompts"`
	FileContents string `json:"fileContents"`
}

type Agreements struct {
	TermsAccepted   bool   `json:"termsAccepted"`
	TermsAcceptedAt string `json:"termsAcceptedAt,omitempty"`
}

type OptIns struct {
	Analytics bool `json:"analytics"`
}

type RepoCommitHooksConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type RepoConfig struct {
	Version        int    `json:"version"`
	RepoID         string `json:"repoId"`
	RepoRoot       string `json:"repoRoot"`
	InitializedAt  string `json:"initializedAt"`
	Mode           string `json:"mode,omitempty"` // "local" or "online"
	TeamID         string `json:"teamId,omitempty"`
	LocalAIBackend string `json:"localAiBackend,omitempty"`
	// Branch is the branch RepoGuide filters repo-experience/topic file paths
	// against. For online-mode repos this is a cache of the cloud-authoritative
	// setting (see CloudClient.UpdateRepoBranch); for local-mode repos (no
	// cloud) it's the only copy. Empty means "main".
	Branch       string                `json:"branch,omitempty"`
	HintFiles    []string              `json:"hintFiles,omitempty"`
	CommitHooks  RepoCommitHooksConfig `json:"commitHooks,omitempty"`
	LastSyncedAt *time.Time            `json:"lastSyncedAt,omitempty"`
}

// hintFileCandidates are the well-known repo guidance files RepoGuide can read.
var hintFileCandidates = []string{
	"AGENTS.md", "CLAUDE.md", "README.md", "STRUCTURE.md", "CONTRIBUTING.md",
}

// ScanHintFiles returns which candidate guidance files exist in repoRoot.
func ScanHintFiles(repoRoot string) []string {
	var found []string
	for _, name := range hintFileCandidates {
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// LoadRepoConfigFile reads the repo.json from storeDir.
func LoadRepoConfigFile(storeDir string) (RepoConfig, error) {
	data, err := os.ReadFile(filepath.Join(storeDir, "repo.json"))
	if err != nil {
		return RepoConfig{}, err
	}
	var cfg RepoConfig
	return cfg, json.Unmarshal(data, &cfg)
}

// SaveRepoConfigFile writes cfg to storeDir/repo.json.
func SaveRepoConfigFile(storeDir string, cfg RepoConfig) error {
	return writeJSON(filepath.Join(storeDir, "repo.json"), cfg)
}

func globalConfigPath() string { return filepath.Join(RepoGuideDir(), "config.json") }

func LoadGlobalConfig() (GlobalConfig, error) {
	data, err := os.ReadFile(globalConfigPath())
	if err != nil {
		return GlobalConfig{}, err
	}
	var cfg GlobalConfig
	return cfg, json.Unmarshal(data, &cfg)
}

func SaveGlobalConfig(cfg GlobalConfig) error {
	return writeJSON(globalConfigPath(), cfg)
}

type DetectedSource struct {
	Name        string
	Count       int
	Unit        string
	Found       bool
	DisplayName string
}

func InitRepo(opts InitOptions) (InitResult, error) {
	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil || repoRoot == "" {
		return InitResult{}, ErrNotGitRepo
	}

	existingRepoID, _ := gitOutput("config", "--get", "repoguide.repoId")
	storeRoot := RepoGuideDir()

	// If git config has no ID, check local store by repo_root to avoid creating duplicates.
	if existingRepoID == "" {
		if repos, _ := listConfiguredRepos(); len(repos) > 0 {
			norm := normalizePathForCompare(repoRoot)
			for _, r := range repos {
				if normalizePathForCompare(r.RepoRoot) == norm && r.RepoID != "" {
					existingRepoID = r.RepoID
					// Restore git config silently - it was likely cleared by re-clone or worktree setup.
					_ = writeGitConfig(existingRepoID)
					break
				}
			}
		}
	}

	if existingRepoID != "" && !opts.Force {
		storeDir := filepath.Join(storeRoot, "repos", existingRepoID)
		hooksPath := readHooksPath()
		cfg, _ := LoadRepoConfigFile(storeDir)
		return InitResult{
			RepoRoot:           repoRoot,
			RepoID:             existingRepoID,
			StoreDir:           storeDir,
			AlreadyInit:        true,
			GlobalConfig:       filepath.Join(storeRoot, "config.json"),
			Hooks:              detectHookStatus(),
			HooksPath:          hooksPath,
			HooksPathCustom:    hooksPath != "",
			CommitHooksEnabled: commitHooksEnabled(cfg),
			Agents:             DetectInitSources(repoRoot),
		}, nil
	}

	repoID := existingRepoID
	if repoID == "" {
		repoID = strings.TrimSpace(opts.RepoID)
	}
	if repoID == "" {
		repoID, err = generateRepoID(repoRoot)
		if err != nil {
			return InitResult{}, err
		}
	}

	storeDir := filepath.Join(storeRoot, "repos", repoID)
	if err := ensureStore(storeRoot, storeDir, repoRoot, repoID, opts.Mode); err != nil {
		return InitResult{}, err
	}

	if err := writeGitConfig(repoID); err != nil {
		return InitResult{}, err
	}

	cfg, err := LoadRepoConfigFile(storeDir)
	if err != nil {
		return InitResult{}, err
	}
	if existingRepoID == "" {
		setCommitHooksEnabled(&cfg, false)
		if err := SaveRepoConfigFile(storeDir, cfg); err != nil {
			return InitResult{}, err
		}
	}

	hooksPath := readHooksPath()
	hooks := detectHookStatus()

	return InitResult{
		RepoRoot:           repoRoot,
		RepoID:             repoID,
		StoreDir:           storeDir,
		GlobalConfig:       filepath.Join(storeRoot, "config.json"),
		Hooks:              hooks,
		HooksPath:          hooksPath,
		HooksPathCustom:    hooksPath != "",
		CommitHooksEnabled: commitHooksEnabled(cfg),
		Agents:             DetectInitSources(repoRoot),
	}, nil
}

// InitEphemeralRepo registers repoRoot inside the current RepoGuideDir()
// (typically a temp directory the caller set via SetRepoGuideDirOverride)
// so session data can be read without a prior `repoguide init`. Unlike
// InitRepo, it never touches the repo's real git config.
func InitEphemeralRepo(repoRoot string) (string, error) {
	repoID, err := generateRepoID(repoRoot)
	if err != nil {
		return "", err
	}
	storeRoot := RepoGuideDir()
	storeDir := filepath.Join(storeRoot, "repos", repoID)
	if err := ensureStore(storeRoot, storeDir, repoRoot, repoID, "local"); err != nil {
		return "", err
	}
	return repoID, nil
}

func ensureStore(storeRoot, storeDir, repoRoot, repoID, mode string) error {
	dirs := []string{
		storeRoot,
		filepath.Join(storeRoot, "repos"),
		storeDir,
		filepath.Join(storeDir, "reports"),
		filepath.Join(storeDir, "cache"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	cfgPath := filepath.Join(storeRoot, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := writeJSON(cfgPath, GlobalConfig{
			Version: repoguideVersion,
			Privacy: PrivacyDefaults{
				RawPrompts:   "yes",
				FileContents: "never_stored",
			},
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
	}

	if err := writeJSON(filepath.Join(storeDir, "repo.json"), RepoConfig{
		Version:       repoguideVersion,
		RepoID:        repoID,
		RepoRoot:      repoRoot,
		InitializedAt: time.Now().UTC().Format(time.RFC3339),
		Mode:          mode,
		CommitHooks:   RepoCommitHooksConfig{Enabled: boolPtr(false)},
	}); err != nil {
		return err
	}

	return nil
}

func writeGitConfig(repoID string) error {
	entries := [][]string{
		{"config", "repoguide.enabled", "true"},
		{"config", "repoguide.repoId", repoID},
		{"config", "repoguide.version", fmt.Sprintf("%d", repoguideVersion)},
	}
	for _, args := range entries {
		if _, err := gitOutput(args...); err != nil {
			return err
		}
	}
	return nil
}

func detectHookStatus() []HookStatus {
	hooksDir, err := gitOutput("rev-parse", "--git-path", "hooks")
	if err != nil {
		return nil
	}
	hooks := []HookStatus{
		{Name: hookPreCommit},
		{Name: hookPostCommit},
	}
	for i, hook := range hooks {
		if hookFileContainsManagedBlock(filepath.Join(hooksDir, hook.Name)) {
			hooks[i].Installed = true
			hooks[i].Skipped = false
		} else {
			hooks[i].Skipped = true
		}
	}
	return hooks
}

func readHooksPath() string {
	value, _ := gitOutput("config", "--get", "core.hooksPath")
	return value
}

func DetectInitSources(repoRoot string) []DetectedSource {
	return []DetectedSource{
		detectClaudeSource(),
		detectCodexSource(),
		detectAiderSource(repoRoot),
		detectCursorSource(),
	}
}

func detectClaudeSource() DetectedSource {
	sessions := glob(home(".claude", "projects", "*", "*.jsonl"))
	return DetectedSource{
		Name:        "Claude Code",
		Count:       len(sessions),
		Unit:        "sessions",
		Found:       len(sessions) > 0,
		DisplayName: "Claude Code",
	}
}

func detectCodexSource() DetectedSource {
	paths := append(glob(home(".codex", "sessions", "*", "*", "*", "*.jsonl")), glob(home(".codex", "archived_sessions", "*.jsonl"))...)
	return DetectedSource{
		Name:        "Codex CLI",
		Count:       len(paths),
		Unit:        "sessions",
		Found:       len(paths) > 0,
		DisplayName: "Codex CLI",
	}
}

func detectAiderSource(repoRoot string) DetectedSource {
	patterns := []string{
		filepath.Join(repoRoot, ".aider*"),
		filepath.Join(repoRoot, ".aider.chat.history.md"),
	}
	found := false
	for _, pattern := range patterns {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			found = true
			break
		}
	}
	return DetectedSource{
		Name:        "Aider",
		Count:       0,
		Unit:        "repo files",
		Found:       found,
		DisplayName: "Aider",
	}
}

func detectCursorSource() DetectedSource {
	dbs := glob(home("Library", "Application Support", "Cursor", "User", "workspaceStorage", "*", "state.vscdb"))
	return DetectedSource{
		Name:        "Cursor",
		Count:       len(dbs),
		Unit:        "workspaces found",
		Found:       len(dbs) > 0,
		DisplayName: "Cursor",
	}
}

func generateRepoID(repoRoot string) (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "repo_" + repoNameSlug(repoRoot) + "_" + hex.EncodeToString(buf), nil
}

func repoNameSlug(repoRoot string) string {
	if out, err := gitOutputAt(repoRoot, "remote", "get-url", "origin"); err == nil && out != "" {
		// extract last path component of URL or path, strip .git
		name := filepath.Base(strings.TrimSuffix(strings.TrimSpace(out), ".git"))
		if s := slugify(name); s != "" {
			return s
		}
	}
	return slugify(filepath.Base(repoRoot))
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prev := '_'
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prev = r
		} else if prev != '_' {
			b.WriteByte('_')
			prev = '_'
		}
	}
	return strings.Trim(b.String(), "_")
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOutputAt(repoRoot string, args ...string) (string, error) {
	return gitOutput(append([]string{"-C", repoRoot}, args...)...)
}

// KnownFilesForBranch lists every file that exists on branch in repoRoot, so
// callers can filter stale (renamed/moved/deleted) paths out of learned
// topic/session data. Falls back to origin/<branch> if the local ref doesn't
// resolve (e.g. a fresh clone with a different branch checked out). Returns
// nil - "unknown, don't filter" - rather than an error, since this is always
// a best-effort enrichment, never a required input.
func KnownFilesForBranch(repoRoot, branch string) []string {
	branch = strings.TrimSpace(branch)
	if repoRoot == "" || branch == "" {
		return nil
	}
	ref := branch
	if _, err := gitOutputAt(repoRoot, "rev-parse", "--verify", "--quiet", ref); err != nil {
		ref = "origin/" + branch
		if _, err := gitOutputAt(repoRoot, "rev-parse", "--verify", "--quiet", ref); err != nil {
			return nil
		}
	}
	out, err := gitOutputAt(repoRoot, "ls-tree", "-r", "--name-only", ref)
	if err != nil || out == "" {
		return nil
	}
	return append(strings.Split(out, "\n"), submoduleFiles(repoRoot, ref)...)
}

// submoduleFiles lists the files inside each submodule of ref, prefixed with
// the submodule's path in the superproject. ls-tree stops at a gitlink, so
// without this an umbrella repo reports only its own top-level files and every
// path inside a submodule looks deleted - which would make callers filter away
// the entire codebase. Reads each submodule's checked-out worktree rather than
// its recorded commit: close enough for an existence check, and it costs one
// git call per submodule instead of a tree walk.
func submoduleFiles(repoRoot, ref string) []string {
	out, err := gitOutputAt(repoRoot, "ls-tree", "-r", "-d", "--format=%(objecttype) %(path)", ref)
	if err != nil || out == "" {
		return nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		objectType, path, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || objectType != "commit" {
			continue
		}
		sub, err := gitOutputAt(filepath.Join(repoRoot, path), "ls-files")
		if err != nil || sub == "" {
			continue
		}
		for _, f := range strings.Split(sub, "\n") {
			if f != "" {
				files = append(files, path+"/"+f)
			}
		}
	}
	return files
}

// listConfiguredRepos and normalizePathForCompare are duplicated here (from
// internal/repo/repo_store.go and internal/sessionimport/sessions.go,
// respectively) because init.go lives in the root "internal" package, which
// must not import internal/repo or internal/sessionimport (both of those
// packages import the root package for RepoConfig/HookStatus/etc, so a
// root -> repo or root -> sessionimport import would create a cycle). This
// is only used for duplicate-repoID detection during init, so a small
// self-contained copy is preferable to restructuring the package graph.
func listConfiguredRepos() ([]RepoConfig, error) {
	root := filepath.Join(RepoGuideDir(), "repos")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	repos := make([]RepoConfig, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "repo.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg RepoConfig
		if err := json.Unmarshal(data, &cfg); err != nil || cfg.RepoID == "" {
			continue
		}
		repos = append(repos, cfg)
	}
	return repos, nil
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

func GitRepoID(repoRoot string) (string, error) {
	return gitOutputAt(repoRoot, "config", "--get", "repoguide.repoId")
}

// InitRepoAt initializes repoguide for the repo at the given path.
// Equivalent to running `repoguide init` from that directory.
func InitRepoAt(repoRoot string, opts InitOptions) (InitResult, error) {
	root, err := gitOutputAt(repoRoot, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return InitResult{}, ErrNotGitRepo
	}

	existingRepoID, _ := gitOutputAt(root, "config", "--get", "repoguide.repoId")
	storeRoot := RepoGuideDir()

	if existingRepoID == "" {
		if repos, _ := listConfiguredRepos(); len(repos) > 0 {
			norm := normalizePathForCompare(root)
			for _, r := range repos {
				if normalizePathForCompare(r.RepoRoot) == norm && r.RepoID != "" {
					existingRepoID = r.RepoID
					_, _ = gitOutputAt(root, "config", "repoguide.enabled", "true")
					_, _ = gitOutputAt(root, "config", "repoguide.repoId", existingRepoID)
					_, _ = gitOutputAt(root, "config", "repoguide.version", fmt.Sprintf("%d", repoguideVersion))
					break
				}
			}
		}
	}

	if existingRepoID != "" && !opts.Force {
		storeDir := filepath.Join(storeRoot, "repos", existingRepoID)
		cfg, _ := LoadRepoConfigFile(storeDir)
		hooksPath := readHooksPathAt(root)
		return InitResult{
			RepoRoot:           root,
			RepoID:             existingRepoID,
			StoreDir:           storeDir,
			AlreadyInit:        true,
			GlobalConfig:       filepath.Join(storeRoot, "config.json"),
			Hooks:              detectHookStatusAt(root),
			HooksPath:          hooksPath,
			HooksPathCustom:    hooksPath != "",
			CommitHooksEnabled: commitHooksEnabled(cfg),
			Agents:             DetectInitSources(root),
		}, nil
	}

	repoID := existingRepoID
	if repoID == "" {
		repoID = strings.TrimSpace(opts.RepoID)
	}
	if repoID == "" {
		repoID, err = generateRepoID(root)
		if err != nil {
			return InitResult{}, err
		}
	}

	storeDir := filepath.Join(storeRoot, "repos", repoID)
	if err := ensureStore(storeRoot, storeDir, root, repoID, opts.Mode); err != nil {
		return InitResult{}, err
	}

	if _, err := gitOutputAt(root, "config", "repoguide.enabled", "true"); err != nil {
		return InitResult{}, err
	}
	if _, err := gitOutputAt(root, "config", "repoguide.repoId", repoID); err != nil {
		return InitResult{}, err
	}
	if _, err := gitOutputAt(root, "config", "repoguide.version", fmt.Sprintf("%d", repoguideVersion)); err != nil {
		return InitResult{}, err
	}

	cfg, err := LoadRepoConfigFile(storeDir)
	if err != nil {
		return InitResult{}, err
	}
	if existingRepoID == "" {
		setCommitHooksEnabled(&cfg, false)
		if err := SaveRepoConfigFile(storeDir, cfg); err != nil {
			return InitResult{}, err
		}
	}

	hooksPath := readHooksPathAt(root)

	return InitResult{
		RepoRoot:           root,
		RepoID:             repoID,
		StoreDir:           storeDir,
		GlobalConfig:       filepath.Join(storeRoot, "config.json"),
		Hooks:              detectHookStatusAt(root),
		HooksPath:          hooksPath,
		HooksPathCustom:    hooksPath != "",
		CommitHooksEnabled: commitHooksEnabled(cfg),
		Agents:             DetectInitSources(root),
	}, nil
}

// LinkRepoAt binds an existing git checkout to a specific RepoGuide repo ID.
// It is used for shared/team repos where the cloud repo already exists.
func LinkRepoAt(repoRoot, repoID, mode string, teamID ...string) (InitResult, error) {
	root, err := gitOutputAt(repoRoot, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return InitResult{}, ErrNotGitRepo
	}
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return InitResult{}, fmt.Errorf("repo ID required")
	}
	storeRoot := RepoGuideDir()
	storeDir := filepath.Join(storeRoot, "repos", repoID)

	if err := ensureStore(storeRoot, storeDir, root, repoID, mode); err != nil {
		return InitResult{}, err
	}
	if _, err := gitOutputAt(root, "config", "repoguide.enabled", "true"); err != nil {
		return InitResult{}, err
	}
	if _, err := gitOutputAt(root, "config", "repoguide.repoId", repoID); err != nil {
		return InitResult{}, err
	}
	if _, err := gitOutputAt(root, "config", "repoguide.version", fmt.Sprintf("%d", repoguideVersion)); err != nil {
		return InitResult{}, err
	}
	cfg, err := LoadRepoConfigFile(storeDir)
	if err != nil {
		return InitResult{}, err
	}
	setCommitHooksEnabled(&cfg, false)
	if len(teamID) > 0 {
		cfg.TeamID = strings.TrimSpace(teamID[0])
	}
	if err := SaveRepoConfigFile(storeDir, cfg); err != nil {
		return InitResult{}, err
	}
	hooksPath := readHooksPathAt(root)
	return InitResult{
		RepoRoot:           root,
		RepoID:             repoID,
		StoreDir:           storeDir,
		GlobalConfig:       filepath.Join(storeRoot, "config.json"),
		Hooks:              detectHookStatusAt(root),
		HooksPath:          hooksPath,
		HooksPathCustom:    hooksPath != "",
		CommitHooksEnabled: commitHooksEnabled(cfg),
		Agents:             DetectInitSources(root),
	}, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func boolPtr(v bool) *bool { return &v }

func commitHooksEnabled(cfg RepoConfig) bool {
	return cfg.CommitHooks.Enabled != nil && *cfg.CommitHooks.Enabled
}

func setCommitHooksEnabled(cfg *RepoConfig, enabled bool) {
	cfg.CommitHooks.Enabled = boolPtr(enabled)
}
