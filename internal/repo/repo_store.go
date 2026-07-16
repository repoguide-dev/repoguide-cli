package repo

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

type LocalSetupStatus struct {
	RepoGuideDir       string
	LocalStorageBytes  int64
	LocalStorageFound  bool
	LocalStorageError  error
	InGitRepo          bool
	RepoRoot           string
	Initialized        bool
	RepoID             string
	StoreDir           string
	IsLocalMode        bool
	Hooks              []HookStatus
	HooksPath          string
	HooksPathCustom    bool
	CommitHooksEnabled bool
}

var ErrRepoNotInitialized = errors.New("repoguide is not initialized for this repository")

type RemoveResult struct {
	RepoRoot string
	RepoID   string
	StoreDir string
	Mode     string // "local" or ""
}

type RemoveAllResult struct {
	RepoGuideDir  string
	RemovedRepos  []RepoConfig
	SkippedRepos  []RepoConfig
	CurrentRepo   string
	CurrentRepoID string
}

func DetectLocalSetup() LocalSetupStatus {
	storeRoot := RepoGuideDir()
	storeBytes, storeFound, storeErr := DirSize(storeRoot)
	status := LocalSetupStatus{
		RepoGuideDir:      storeRoot,
		LocalStorageBytes: storeBytes,
		LocalStorageFound: storeFound,
		LocalStorageError: storeErr,
	}

	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil || repoRoot == "" {
		return status
	}

	repoID, _ := gitOutput("config", "--get", "repoguide.repoId")
	hooksPath := readHooksPath()

	status.InGitRepo = true
	status.RepoRoot = repoRoot
	status.RepoID = repoID
	status.Hooks = detectHookStatus()
	status.HooksPath = hooksPath
	status.HooksPathCustom = hooksPath != ""

	if repoID != "" {
		status.Initialized = true
		status.StoreDir = filepath.Join(storeRoot, "repos", repoID)
		status.CommitHooksEnabled = false
		if cfg, err := LoadRepoConfigFile(status.StoreDir); err == nil {
			status.IsLocalMode = cfg.Mode == "local"
			status.CommitHooksEnabled = commitHooksEnabled(cfg)
		}
	} else {
		status.CommitHooksEnabled = false
	}

	return status
}

func ListConfiguredRepos() ([]RepoConfig, error) {
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
		var repo RepoConfig
		if err := json.Unmarshal(data, &repo); err != nil || repo.RepoID == "" {
			continue
		}
		repos = append(repos, repo)
	}

	sort.Slice(repos, func(i, j int) bool {
		return repos[i].RepoRoot < repos[j].RepoRoot
	})

	// Dedup by RepoRoot: multiple store dirs can exist for the same path if the
	// repo was re-initialized. Keep the entry whose repoId matches git config;
	// fall back to keeping the first (earliest-sorted) entry.
	byRoot := make(map[string]int, len(repos)) // normalized path → index in out
	out := make([]RepoConfig, 0, len(repos))
	for _, r := range repos {
		norm := normalizePathForCompare(r.RepoRoot)
		if norm == "" {
			out = append(out, r)
			continue
		}
		idx, seen := byRoot[norm]
		if !seen {
			byRoot[norm] = len(out)
			out = append(out, r)
			continue
		}
		// prefer the repoId currently set in git config at that path
		if gitID, _ := gitOutputAt(r.RepoRoot, "config", "--get", "repoguide.repoId"); gitID == r.RepoID {
			out[idx] = r
		}
	}

	return out, nil
}

// IsRepoInitialized returns true if repoRoot has been initialized with repoguide init.
func IsRepoInitialized(repoRoot string) bool {
	id, err := gitOutputAt(repoRoot, "config", "--get", "repoguide.repoId")
	return err == nil && id != ""
}

// RepoIsLocalMode returns true if repoRoot was initialized with local (SQLite) mode.
func RepoIsLocalMode(repoRoot string) bool {
	id, err := gitOutputAt(repoRoot, "config", "--get", "repoguide.repoId")
	if err != nil || id == "" {
		return false
	}
	cfg, err := LoadRepoConfigFile(filepath.Join(RepoGuideDir(), "repos", id))
	return err == nil && cfg.Mode == "local"
}

// RepoIsLocalModeByID returns true if the repo with the given ID is configured
// for local (SQLite) mode.
func RepoIsLocalModeByID(repoID string) bool {
	repos, err := ListConfiguredRepos()
	if err != nil {
		return false
	}
	for _, r := range repos {
		if r.RepoID == repoID {
			return r.Mode == "local"
		}
	}
	return false
}

// HasLocalModeRepos returns true if any configured repo uses local (SQLite) mode.
func HasLocalModeRepos() bool {
	repos, _ := ListConfiguredRepos()
	for _, r := range repos {
		if r.Mode == "local" {
			return true
		}
	}
	return false
}

func RemoveTrackedRepoAt(repoRoot string) (RemoveResult, error) {
	repoID, err := gitOutputAt(repoRoot, "config", "--get", "repoguide.repoId")
	if err != nil || repoID == "" {
		return RemoveResult{}, ErrRepoNotInitialized
	}
	storeDir := filepath.Join(RepoGuideDir(), "repos", repoID)
	cfg, _ := LoadRepoConfigFile(storeDir)
	if err := RemoveManagedCommitHooks(repoRoot); err != nil {
		return RemoveResult{}, err
	}
	if err := unsetRepoConfig(repoRoot); err != nil {
		return RemoveResult{}, err
	}
	if err := removeRepoGuideArtifacts(repoRoot); err != nil {
		return RemoveResult{}, err
	}
	if err := os.RemoveAll(storeDir); err != nil {
		return RemoveResult{}, err
	}
	return RemoveResult{RepoRoot: repoRoot, RepoID: repoID, StoreDir: storeDir, Mode: cfg.Mode}, nil
}

func RemoveTrackedRepo() (RemoveResult, error) {
	status := DetectLocalSetup()
	if !status.InGitRepo {
		return RemoveResult{}, ErrNotGitRepo
	}
	if !status.Initialized || status.RepoID == "" {
		return RemoveResult{}, ErrRepoNotInitialized
	}

	modeCfg, _ := LoadRepoConfigFile(status.StoreDir)
	result := RemoveResult{
		RepoRoot: status.RepoRoot,
		RepoID:   status.RepoID,
		StoreDir: status.StoreDir,
		Mode:     modeCfg.Mode,
	}

	if err := RemoveManagedCommitHooks(status.RepoRoot); err != nil {
		return RemoveResult{}, err
	}
	if err := unsetRepoConfig(status.RepoRoot); err != nil {
		return RemoveResult{}, err
	}
	if err := removeRepoGuideArtifacts(status.RepoRoot); err != nil {
		return RemoveResult{}, err
	}

	if err := os.RemoveAll(result.StoreDir); err != nil {
		return RemoveResult{}, err
	}

	return result, nil
}

func RemoveAllTrackedData() (RemoveAllResult, error) {
	status := DetectLocalSetup()
	repos, err := ListConfiguredRepos()
	if err != nil {
		return RemoveAllResult{}, err
	}

	result := RemoveAllResult{
		RepoGuideDir: RepoGuideDir(),
		RemovedRepos: make([]RepoConfig, 0, len(repos)),
		SkippedRepos: make([]RepoConfig, 0),
	}

	if status.InGitRepo && status.Initialized {
		result.CurrentRepo = status.RepoRoot
		result.CurrentRepoID = status.RepoID

		found := false
		for _, repo := range repos {
			if repo.RepoRoot == status.RepoRoot {
				found = true
				break
			}
		}
		if !found {
			repos = append(repos, RepoConfig{
				RepoID:   status.RepoID,
				RepoRoot: status.RepoRoot,
			})
		}
	}

	for _, repo := range repos {
		if repo.RepoRoot == "" {
			result.SkippedRepos = append(result.SkippedRepos, repo)
			continue
		}
		if err := RemoveManagedCommitHooks(repo.RepoRoot); err != nil {
			if errors.Is(err, ErrNotGitRepo) {
				result.SkippedRepos = append(result.SkippedRepos, repo)
				continue
			}
			return RemoveAllResult{}, err
		}
		if err := unsetRepoConfig(repo.RepoRoot); err != nil {
			if errors.Is(err, ErrNotGitRepo) {
				result.SkippedRepos = append(result.SkippedRepos, repo)
				continue
			}
			return RemoveAllResult{}, err
		}
		if err := removeRepoGuideArtifacts(repo.RepoRoot); err != nil {
			return RemoveAllResult{}, err
		}
		result.RemovedRepos = append(result.RemovedRepos, repo)
	}

	if err := removeAllWithRetry(result.RepoGuideDir); err != nil {
		return RemoveAllResult{}, err
	}

	return result, nil
}

// removeAllWithRetry handles a concurrent MCP or hook process creating a
// directory between RemoveAll's traversal and its final unlink operation.
func removeAllWithRetry(path string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = os.RemoveAll(path)
		if err == nil || !isDirectoryNotEmpty(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return err
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY)
}

func unsetRepoConfig(repoRoot string) error {
	if repoRoot == "" {
		return ErrNotGitRepo
	}
	if _, err := gitOutput("-C", repoRoot, "rev-parse", "--show-toplevel"); err != nil {
		return ErrNotGitRepo
	}
	for _, key := range []string{"repoguide.enabled", "repoguide.repoId", "repoguide.version"} {
		cmd := exec.Command("git", "-C", repoRoot, "config", "--unset-all", key)
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 5 {
				return err
			}
		}
	}
	return nil
}
