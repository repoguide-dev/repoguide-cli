package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
)

// readAgentMemoryDocs collects provider-managed memory that is relevant to the
// current repository. These documents travel in docs.json beside session data,
// so both local and cloud analysis use the same inputs.
func readAgentMemoryDocs(repoRoot string) map[string]string {
	docs := map[string]string{}
	for _, dir := range scopedCodexMemoryDirs(repoRoot, home(".codex")) {
		addMemoryTree(docs, "codex-memory", dir)
	}
	addMemoryTree(docs, "codex-memory", filepath.Join(repoRoot, ".codex", "memories"))
	addMemoryTree(docs, "codex-memory", filepath.Join(repoRoot, ".codex", "memory"))
	addMemoryTree(docs, "claude-memory", filepath.Join(repoRoot, ".claude", "memory"))

	// Claude stores project memory below a directory whose name is the absolute
	// project path with path separators encoded as '-'. Match the canonical and
	// lexical spellings because symlinks can make either one appear in sessions.
	for _, root := range uniquePaths(repoRoot) {
		encoded := strings.ReplaceAll(filepath.Clean(root), string(filepath.Separator), "-")
		addMemoryTree(docs, "claude-memory", filepath.Join(home(".claude", "projects"), encoded, "memory"))
	}
	return docs
}

// scopedCodexMemoryDirs deliberately does not return ~/.codex/memory or
// ~/.codex/memories themselves: those roots may contain memories from many
// repositories. Only the absolute-path-encoded directory for this sync is read.
func scopedCodexMemoryDirs(repoRoot, codexHome string) []string {
	var dirs []string
	for _, root := range uniquePaths(repoRoot) {
		encoded := strings.ReplaceAll(filepath.Clean(root), string(filepath.Separator), "-")
		dirs = append(dirs,
			filepath.Join(codexHome, "memory", encoded),
			filepath.Join(codexHome, "memories", encoded),
		)
	}
	return dirs
}

func uniquePaths(path string) []string {
	out := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		out = append(out, resolved)
	}
	return out
}

func addMemoryTree(docs map[string]string, label, root string) {
	info, err := os.Stat(root)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if data, readErr := os.ReadFile(root); readErr == nil {
			docs[label+"/"+filepath.Base(root)] = string(data)
		}
		return
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".md" && ext != ".txt" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			docs[label+"/"+filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
}
