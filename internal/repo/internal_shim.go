package repo

import (
	"path/filepath"
	"strings"

	internalpkg "github.com/repoguide/repoguide-cli/internal"
)

// This file re-exposes root-package ("internal") types and helpers under the
// unqualified names repo_store.go and repoguide_ignore.go already use, so
// those files (originally written as part of package internal) did not need
// per-call-site edits when they were split into this package.

type RepoConfig = internalpkg.RepoConfig
type HookStatus = internalpkg.HookStatus

var ErrNotGitRepo = internalpkg.ErrNotGitRepo

func home(parts ...string) string { return internalpkg.Home(parts...) }

func gitOutput(args ...string) (string, error) { return internalpkg.GitOutput(args...) }

func gitOutputAt(repoRoot string, args ...string) (string, error) {
	return internalpkg.GitOutputAt(repoRoot, args...)
}

func readHooksPath() string { return internalpkg.ReadHooksPath() }

func detectHookStatus() []HookStatus { return internalpkg.DetectHookStatus() }

func commitHooksEnabled(cfg RepoConfig) bool { return internalpkg.CommitHooksEnabled(cfg) }

func LoadRepoConfigFile(storeDir string) (RepoConfig, error) {
	return internalpkg.LoadRepoConfigFile(storeDir)
}

func SaveRepoConfigFile(storeDir string, cfg RepoConfig) error {
	return internalpkg.SaveRepoConfigFile(storeDir, cfg)
}

func RepoGuideDir() string { return internalpkg.RepoGuideDir() }

func DirSize(path string) (int64, bool, error) { return internalpkg.DirSize(path) }

func RemoveManagedCommitHooks(repoRoot string) error {
	return internalpkg.RemoveManagedCommitHooks(repoRoot)
}

func writeJSON(path string, value any) error { return internalpkg.WriteJSON(path, value) }

type GlobalConfig = internalpkg.GlobalConfig

const repoguideVersion = internalpkg.RepoguideVersion

func SetManagedCommitHooks(storeDir, repoRoot string, enabled bool) ([]HookStatus, error) {
	return internalpkg.SetManagedCommitHooks(storeDir, repoRoot, enabled)
}

const (
	hookPreCommit  = internalpkg.HookPreCommit
	hookPostCommit = internalpkg.HookPostCommit
)

func gitHookFilePath(repoRoot, hookName string) (string, error) {
	return internalpkg.GitHookFilePath(repoRoot, hookName)
}

func hookFileContainsManagedBlock(path string) bool {
	return internalpkg.HookFileContainsManagedBlock(path)
}

// normalizePathForCompare is duplicated from internal/sessionimport/sessions.go
// (a trivial, self-contained path-cleaning helper) because internal/repo must
// not import internal/sessionimport: sessionimport already imports repo (for
// ListConfiguredRepos), so the reverse import would create a cycle.
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
