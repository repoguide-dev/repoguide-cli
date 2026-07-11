package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAgentMemoryDocsIncludesRepoLocalProviderMemory(t *testing.T) {
	repoRoot := t.TempDir()
	claudeDir := filepath.Join(repoRoot, ".claude", "memory")
	codexDir := filepath.Join(repoRoot, ".codex", "memories")
	for _, dir := range []string{claudeDir, codexDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "MEMORY.md"), []byte("claude fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "repo.md"), []byte("codex fact"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := readAgentMemoryDocs(repoRoot)
	if docs["claude-memory/MEMORY.md"] != "claude fact" {
		t.Fatalf("claude memory missing: %#v", docs)
	}
	if docs["codex-memory/repo.md"] != "codex fact" {
		t.Fatalf("codex memory missing: %#v", docs)
	}
}

func TestReadHintFileDocsIncludesMemoryWithoutConfiguredHints(t *testing.T) {
	repoRoot := t.TempDir()
	dir := filepath.Join(repoRoot, ".claude", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("remember me"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := readHintFileDocsFromConfig(RepoConfig{}, repoRoot)
	if docs["claude-memory/MEMORY.md"] != "remember me" {
		t.Fatalf("memory not packaged as docs: %#v", docs)
	}
}

func TestScopedCodexMemoryDirsNeverIncludesGlobalRoots(t *testing.T) {
	repoRoot := filepath.Join(string(filepath.Separator), "work", "repo")
	codexHome := filepath.Join(string(filepath.Separator), "home", "me", ".codex")
	dirs := scopedCodexMemoryDirs(repoRoot, codexHome)
	if len(dirs) != 2 {
		t.Fatalf("got %d dirs, want 2: %#v", len(dirs), dirs)
	}
	for _, dir := range dirs {
		if dir == filepath.Join(codexHome, "memory") || dir == filepath.Join(codexHome, "memories") {
			t.Fatalf("global Codex memory root must not be collected: %s", dir)
		}
	}
	wantSuffix := strings.ReplaceAll(filepath.Clean(repoRoot), string(filepath.Separator), "-")
	for _, dir := range dirs {
		if filepath.Base(dir) != wantSuffix {
			t.Fatalf("memory directory %q is not scoped to %q", dir, repoRoot)
		}
	}
}
