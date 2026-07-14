package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	managedHookStart = "# >>> repoguide managed commit hook >>>"
	managedHookEnd   = "# <<< repoguide managed commit hook <<<"
)

var managedCommitHookFiles = []string{"AGENTS.md", "CLAUDE.md"}

type managedHookState struct {
	Files []string `json:"files"`
}

func SetManagedCommitHooks(storeDir, repoRoot string, enabled bool) ([]HookStatus, error) {
	cfg, err := LoadRepoConfigFile(storeDir)
	if err != nil {
		return nil, err
	}

	setCommitHooksEnabled(&cfg, enabled)
	if enabled {
		if err := installManagedCommitHooks(repoRoot); err != nil {
			return nil, err
		}
	} else {
		if err := uninstallManagedCommitHooks(repoRoot); err != nil {
			return nil, err
		}
	}

	if err := SaveRepoConfigFile(storeDir, cfg); err != nil {
		return nil, err
	}
	return detectHookStatusAt(repoRoot), nil
}

func RemoveManagedCommitHooks(repoRoot string) error {
	if repoRoot == "" {
		return nil
	}
	if _, err := gitOutputAt(repoRoot, "rev-parse", "--show-toplevel"); err != nil {
		return ErrNotGitRepo
	}
	return uninstallManagedCommitHooks(repoRoot)
}

// Managed commit hook design
//
// RepoGuide injects instruction blocks (<!-- repoguide:*-instruction -->) into
// CLAUDE.md and AGENTS.md so that AI agents read them automatically. These blocks
// must not be committed — they're session-local guidance, not project source.
//
// The hook lifecycle keeps the working tree and the commit clean:
//
//  1. pre-commit: unsets --skip-worktree on managed files, then strips injected
//     blocks from the staged index (index-only — working tree is untouched).
//     Aborted commits therefore leave the working-tree file intact.
//     Stripped file names are written to a state file for the post-commit step.
//
//  2. post-commit: reads the state file, sets --skip-worktree on each touched
//     file, then clears the state. --skip-worktree tells git to ignore
//     working-tree differences for those files, so they don't appear as
//     modified in git status even though the working tree still has the blocks.
//
//  3. HideInjectedFiles: called after injection (e.g. mcp fix / mcp install)
//     to immediately hide the blocks from git status without waiting for a commit.
//     It also strips any staged content so no blocks sneak into the index.
//
// When a user genuinely wants to commit changes to CLAUDE.md or AGENTS.md:
//   - git add <file> updates the index normally (skip-worktree does not block add).
//   - pre-commit unsets skip-worktree, strips only the injected blocks from the
//     staged content, and lets the real changes through.
//   - post-commit re-sets skip-worktree.
func RunManagedCommitHook(action string) error {
	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil || repoRoot == "" {
		return nil
	}

	switch action {
	case hookPreCommit:
		return scrubManagedFilesFromIndex(repoRoot)
	case hookPostCommit:
		return applyPostCommitSkipWorktree(repoRoot)
	default:
		return fmt.Errorf("unknown hook action %q", action)
	}
}

// applyPostCommitSkipWorktree reads the hook state written by pre-commit, sets
// --skip-worktree on touched files (so working-tree blocks don't show as dirty),
// then clears the state.
func applyPostCommitSkipWorktree(repoRoot string) error {
	state, err := readManagedHookState(repoRoot)
	if err != nil || len(state.Files) == 0 {
		return clearManagedHookState(repoRoot)
	}
	_ = gitSkipWorktree(repoRoot, true, state.Files...)
	return clearManagedHookState(repoRoot)
}

// HideInjectedFiles strips injected blocks from the index (if staged) and sets
// --skip-worktree so the working-tree copy with blocks doesn't appear as dirty.
func HideInjectedFiles(repoRoot string) {
	_ = scrubManagedFilesFromIndex(repoRoot)
	var tracked []string
	for _, name := range managedCommitHookFiles {
		out, err := gitOutputAt(repoRoot, "ls-files", "--error-unmatch", "--", name)
		if err == nil && strings.TrimSpace(out) != "" {
			tracked = append(tracked, name)
		}
	}
	if len(tracked) > 0 {
		_ = gitSkipWorktree(repoRoot, true, tracked...)
	}
	_ = clearManagedHookState(repoRoot)
}

func gitIsSkipWorktree(repoRoot, name string) bool {
	out, err := gitOutputAt(repoRoot, "ls-files", "-v", "--", name)
	if err != nil {
		return false
	}
	// ls-files -v prefixes skip-worktree files with 'S'
	return strings.HasPrefix(strings.TrimSpace(out), "S ")
}

func gitShowHeadFile(repoRoot, name string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "show", "HEAD:"+name)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func gitSkipWorktree(repoRoot string, skip bool, files ...string) error {
	flag := "--skip-worktree"
	if !skip {
		flag = "--no-skip-worktree"
	}
	args := append([]string{"-C", repoRoot, "update-index", flag, "--"}, files...)
	return exec.Command("git", args...).Run()
}

func hookFileContainsManagedBlock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(data)
	return strings.Contains(text, managedHookStart) && strings.Contains(text, managedHookEnd)
}

func detectHookStatusAt(repoRoot string) []HookStatus {
	hooksDir, err := gitPathAt(repoRoot, "hooks")
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

func installManagedCommitHooks(repoRoot string) error {
	hooksPath := readHooksPathAt(repoRoot)
	if hooksPath != "" {
		return nil
	}
	for _, name := range []string{hookPreCommit, hookPostCommit} {
		path, err := gitHookFilePath(repoRoot, name)
		if err != nil {
			return err
		}
		data, _ := os.ReadFile(path)
		updated := upsertManagedHookBlock(string(data), name, managedHookBinaryPath())
		if err := os.WriteFile(path, []byte(updated), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func uninstallManagedCommitHooks(repoRoot string) error {
	for _, name := range []string{hookPreCommit, hookPostCommit} {
		path, err := gitHookFilePath(repoRoot, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		cleaned, removed := removeManagedHookBlock(string(data))
		if !removed {
			continue
		}
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" || cleaned == "#!/bin/sh" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if !strings.HasSuffix(cleaned, "\n") {
			cleaned += "\n"
		}
		if err := os.WriteFile(path, []byte(cleaned), 0o755); err != nil {
			return err
		}
	}
	return clearManagedHookState(repoRoot)
}

func scrubManagedFilesFromIndex(repoRoot string) error {
	// Auto-stage real changes from skip-worktree managed files so that
	// `git add .` isn't required — the pre-commit hook promotes them transparently.
	if err := promoteSkipWorktreeChanges(repoRoot); err != nil {
		return err
	}

	paths, err := stagedManagedFiles(repoRoot)
	if err != nil || len(paths) == 0 {
		return err
	}

	touched := make([]string, 0, len(paths))
	for _, name := range paths {
		content, err := gitShowIndexFile(repoRoot, name)
		if err != nil {
			return err
		}
		cleaned := stripManagedAgentMessages(content)
		if cleaned == content {
			continue
		}
		if strings.TrimSpace(cleaned) == "" {
			if err := gitUpdateIndexForceRemove(repoRoot, name); err != nil {
				return err
			}
		} else {
			mode, err := gitIndexMode(repoRoot, name)
			if err != nil {
				return err
			}
			blob, err := gitHashObject(repoRoot, cleaned)
			if err != nil {
				return err
			}
			if err := gitUpdateIndex(repoRoot, mode, blob, name); err != nil {
				return err
			}
		}
		touched = append(touched, name)
	}

	if len(touched) == 0 {
		return clearManagedHookState(repoRoot)
	}
	return writeManagedHookState(repoRoot, managedHookState{Files: touched})
}

// promoteSkipWorktreeChanges checks managed files hidden by --skip-worktree.
// If the working tree has real changes beyond injected blocks (compared to HEAD),
// it stages the stripped content and clears skip-worktree so the changes commit.
// On commit abort, the files will appear as staged — run `repoguide mcp fix` to
// re-hide them.
func promoteSkipWorktreeChanges(repoRoot string) error {
	for _, name := range managedCommitHookFiles {
		if !gitIsSkipWorktree(repoRoot, name) {
			continue
		}
		wtContent, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			continue
		}
		stripped := stripManagedAgentMessages(string(wtContent))
		headContent, err := gitShowHeadFile(repoRoot, name)
		if err != nil || stripped == headContent {
			continue // no real changes
		}
		// Real changes exist: stage stripped content and clear skip-worktree.
		mode, err := gitIndexMode(repoRoot, name)
		if err != nil {
			mode = "100644"
		}
		blob, err := gitHashObject(repoRoot, stripped)
		if err != nil {
			continue
		}
		if err := gitUpdateIndex(repoRoot, mode, blob, name); err != nil {
			continue
		}
		_ = gitSkipWorktree(repoRoot, false, name)
	}
	return nil
}

func stagedManagedFiles(repoRoot string) ([]string, error) {
	args := append([]string{"-C", repoRoot, "diff", "--cached", "--name-only", "--diff-filter=ACMR", "--"}, managedCommitHookFiles...)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, name := range managedCommitHookFiles {
			if line == name {
				files = append(files, line)
				break
			}
		}
	}
	return files, nil
}

func stripManagedAgentMessages(content string) string {
	return removeFeedbackInstruction(removeMCPInstruction(content))
}

// removeFeedbackInstruction and removeMCPInstruction are duplicated here (from
// internal/mcp/mcp.go) because hooks.go lives in the root "internal" package,
// which must not import internal/mcp (internal/mcp imports internal/repo,
// which imports the root package, so a root -> mcp import would create a
// cycle). Both copies are small, self-contained string-processing helpers.
func removeFeedbackInstruction(existing string) string {
	const openMarker = "<!-- repoguide:feedback-instruction"
	const closeMarker = "<!-- /repoguide:feedback-instruction -->"

	start := strings.Index(existing, openMarker)
	if start < 0 {
		return existing
	}
	end := strings.Index(existing[start:], closeMarker)
	if end < 0 {
		return existing
	}
	tail := strings.TrimLeft(existing[start+end+len(closeMarker):], "\n")
	head := strings.TrimRight(existing[:start], "\n")
	if strings.TrimSpace(tail) == "" {
		return restoreTrailingNewline(head)
	}
	if head == "" {
		return tail
	}
	return head + "\n\n" + tail
}

func removeMCPInstruction(existing string) string {
	const marker = "## RepoGuide MCP usage"
	const openMarker = "<!-- repoguide:mcp-instruction"
	const closeMarker = "<!-- /repoguide:mcp-instruction -->"

	findStart := func(s string) int {
		if idx := strings.Index(s, openMarker); idx >= 0 {
			if h := strings.LastIndex(s[:idx], "## "); h >= 0 {
				return h
			}
			return idx
		}
		return strings.Index(s, marker)
	}

	start := findStart(existing)
	if start < 0 {
		return existing
	}

	// prefer precise removal using closing sentinel
	if end := strings.Index(existing[start:], closeMarker); end >= 0 {
		tail := existing[start+end+len(closeMarker):]
		tail = strings.TrimLeft(tail, "\n")
		head := strings.TrimRight(existing[:start], "\n")
		if tail == "" {
			return restoreTrailingNewline(head)
		}
		if head == "" {
			return tail
		}
		return head + "\n\n" + tail
	}

	// fallback: remove until next ## heading
	after := existing[start+len(marker):]
	end := strings.Index(after, "\n## ")
	if end < 0 {
		return restoreTrailingNewline(strings.TrimRight(existing[:start], "\n"))
	}
	return existing[:start] + existing[start+len(marker)+end+1:]
}

func restoreTrailingNewline(s string) string {
	if s == "" {
		return s
	}
	return s + "\n"
}

func readManagedHookState(repoRoot string) (managedHookState, error) {
	path, err := gitManagedHookStatePath(repoRoot)
	if err != nil {
		return managedHookState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return managedHookState{}, err
	}
	var state managedHookState
	return state, json.Unmarshal(data, &state)
}

func writeManagedHookState(repoRoot string, state managedHookState) error {
	path, err := gitManagedHookStatePath(repoRoot)
	if err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func clearManagedHookState(repoRoot string) error {
	path, err := gitManagedHookStatePath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func gitManagedHookStatePath(repoRoot string) (string, error) {
	return gitPathAt(repoRoot, "repoguide-managed-hook-state.json")
}

func gitHookFilePath(repoRoot, hookName string) (string, error) {
	return gitPathAt(repoRoot, filepath.Join("hooks", hookName))
}

func readHooksPathAt(repoRoot string) string {
	value, _ := gitOutputAt(repoRoot, "config", "--get", "core.hooksPath")
	return value
}

func upsertManagedHookBlock(existing, hookName, binPath string) string {
	block := managedHookBlock(hookName, binPath)
	if cleaned, removed := removeManagedHookBlock(existing); removed {
		existing = cleaned
	}
	existing = strings.TrimRight(existing, "\n")
	if existing == "" {
		return "#!/bin/sh\n\n" + block + "\n"
	}
	if !strings.HasPrefix(existing, "#!") {
		existing = "#!/bin/sh\n\n" + existing
	}
	return existing + "\n\n" + block + "\n"
}

func removeManagedHookBlock(existing string) (string, bool) {
	start := strings.Index(existing, managedHookStart)
	if start < 0 {
		return existing, false
	}
	end := strings.Index(existing[start:], managedHookEnd)
	if end < 0 {
		return existing, false
	}
	end += start + len(managedHookEnd)
	cleaned := strings.TrimRight(existing[:start], "\n")
	tail := strings.TrimLeft(existing[end:], "\n")
	switch {
	case cleaned == "":
		return tail, true
	case tail == "":
		return cleaned, true
	default:
		return cleaned + "\n\n" + tail, true
	}
}

// managedHookBinaryPath returns the exact executable that installed the hook.
// Local builds use a distinct repoguide-local binary and must not fall back to
// the released `repoguide` found on PATH.
func managedHookBinaryPath() string {
	if self, err := os.Executable(); err == nil {
		return self
	}
	if path, err := exec.LookPath("repoguide"); err == nil {
		return path
	}
	return "repoguide"
}

func managedHookBlock(hookName, binPath string) string {
	quotedBin := strconv.Quote(binPath)
	return strings.Join([]string{
		managedHookStart,
		fmt.Sprintf("if [ -x %s ]; then", quotedBin),
		fmt.Sprintf("  %s repo hook-run %s || exit $?", quotedBin, hookName),
		"fi",
		managedHookEnd,
	}, "\n")
}

func gitShowIndexFile(repoRoot, path string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "show", ":"+path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func gitIndexMode(repoRoot, path string) (string, error) {
	out, err := gitOutputAt(repoRoot, "ls-files", "--stage", "--", path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 1 {
		return "", fmt.Errorf("missing index mode for %s", path)
	}
	return fields[0], nil
}

func gitHashObject(repoRoot, content string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "hash-object", "-w", "--stdin")
	cmd.Stdin = bytes.NewBufferString(content)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitUpdateIndex(repoRoot, mode, blob, path string) error {
	cmd := exec.Command("git", "-C", repoRoot, "update-index", "--cacheinfo", fmt.Sprintf("%s,%s,%s", mode, blob, path))
	return cmd.Run()
}

func gitUpdateIndexForceRemove(repoRoot, path string) error {
	cmd := exec.Command("git", "-C", repoRoot, "update-index", "--force-remove", "--", path)
	return cmd.Run()
}

func gitPathAt(repoRoot, path string) (string, error) {
	resolved, err := gitOutputAt(repoRoot, "rev-parse", "--git-path", path)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(resolved) {
		return resolved, nil
	}
	return filepath.Join(repoRoot, resolved), nil
}
