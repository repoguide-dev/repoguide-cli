package internal

// This file exports a handful of small helpers that are otherwise unexported
// in this package, so that internal/repo, internal/sessionimport, and
// internal/mcp (all of which import this root package) can use them without
// duplicating their logic. See init.go, agents.go, and hooks.go for the
// underlying implementations.

// Home joins parts onto the current user's home directory.
func Home(parts ...string) string { return home(parts...) }

// GitOutput runs `git <args...>` and returns trimmed stdout.
func GitOutput(args ...string) (string, error) { return gitOutput(args...) }

// GitOutputAt runs `git -C repoRoot <args...>` and returns trimmed stdout.
func GitOutputAt(repoRoot string, args ...string) (string, error) {
	return gitOutputAt(repoRoot, args...)
}

// ReadHooksPath returns the configured core.hooksPath for the current repo.
func ReadHooksPath() string { return readHooksPath() }

// ReadHooksPathAt returns the configured core.hooksPath for repoRoot.
func ReadHooksPathAt(repoRoot string) string { return readHooksPathAt(repoRoot) }

// DetectHookStatus reports the install status of managed hooks for the current repo.
func DetectHookStatus() []HookStatus { return detectHookStatus() }

// DetectHookStatusAt reports the install status of managed hooks for repoRoot.
func DetectHookStatusAt(repoRoot string) []HookStatus { return detectHookStatusAt(repoRoot) }

// CommitHooksEnabled reports whether commit hooks are enabled per cfg.
func CommitHooksEnabled(cfg RepoConfig) bool { return commitHooksEnabled(cfg) }

// Glob returns filepath.Glob(pattern), discarding the error.
func Glob(pattern string) []string { return glob(pattern) }

// WriteJSON marshals value as indented JSON and writes it to path.
func WriteJSON(path string, value any) error { return writeJSON(path, value) }

// RepoguideVersion is the current on-disk config schema version.
const RepoguideVersion = repoguideVersion

// HookPreCommit and HookPostCommit are the managed git hook names.
const (
	HookPreCommit  = hookPreCommit
	HookPostCommit = hookPostCommit
)

// GitHookFilePath returns the path to the given git hook file for repoRoot.
func GitHookFilePath(repoRoot, hookName string) (string, error) {
	return gitHookFilePath(repoRoot, hookName)
}

// HookFileContainsManagedBlock reports whether the hook file at path contains
// the repoguide managed commit hook block.
func HookFileContainsManagedBlock(path string) bool { return hookFileContainsManagedBlock(path) }

// GitShowIndexFile returns the staged (index) contents of path in repoRoot.
func GitShowIndexFile(repoRoot, path string) (string, error) {
	return gitShowIndexFile(repoRoot, path)
}
