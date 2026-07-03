package sessionimport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const repoDiscoveryMaxDepth = 4

// DiscoverGitRepoRoots returns git repositories found from RepoGuide state,
// agent session history, the current directory, and a bounded scan of likely
// locations under HOME.
func DiscoverGitRepoRoots() ([]string, error) {
	seen := make(map[string]string)
	add := func(path string) {
		root := verifiedGitRoot(path)
		if root == "" {
			return
		}
		seen[normalizePathForCompare(root)] = root
	}

	if repos, err := ListConfiguredRepos(); err == nil {
		for _, r := range repos {
			add(r.RepoRoot)
		}
	}
	if repos, err := LoadAllSessionRepoRoots(); err == nil {
		for _, r := range repos {
			add(r)
		}
	}
	if cur, err := CurrentRepoRoot(); err == nil {
		add(cur)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return sortedRepoRoots(seen), nil
	}
	for _, root := range repoDiscoveryRoots(homeDir) {
		if err := discoverReposUnder(root, homeDir, seen); err != nil {
			continue
		}
	}

	return sortedRepoRoots(seen), nil
}

func repoDiscoveryRoots(homeDir string) []string {
	candidates := []string{
		homeDir,
		filepath.Join(homeDir, "Desktop"),
		filepath.Join(homeDir, "Documents"),
		filepath.Join(homeDir, "Code"),
		filepath.Join(homeDir, "Projects"),
		filepath.Join(homeDir, "Workspace"),
		filepath.Join(homeDir, "workspace"),
		filepath.Join(homeDir, "Work"),
		filepath.Join(homeDir, "work"),
		filepath.Join(homeDir, "dev"),
		filepath.Join(homeDir, "src"),
	}
	seen := make(map[string]struct{}, len(candidates))
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		normalized := normalizePathForCompare(candidate)
		if _, ok := seen[normalized]; ok {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[normalized] = struct{}{}
		roots = append(roots, candidate)
	}
	return roots
}

func discoverReposUnder(root string, homeDir string, seen map[string]string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		if name == ".git" {
			parent := filepath.Dir(path)
			if discovered := verifiedGitRoot(parent); discovered != "" {
				seen[normalizePathForCompare(discovered)] = discovered
			}
			return filepath.SkipDir
		}

		if path != root {
			if shouldSkipRepoDiscoveryDir(name) {
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
		}

		if repoDiscoveryDepth(homeDir, path) >= repoDiscoveryMaxDepth {
			return filepath.SkipDir
		}
		return nil
	})
}

func repoDiscoveryDepth(homeDir, path string) int {
	rel, err := filepath.Rel(homeDir, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

func verifiedGitRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	root, err := gitOutputAt(path, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ""
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return ""
	}
	return root
}

func sortedRepoRoots(seen map[string]string) []string {
	out := make([]string, 0, len(seen))
	for _, root := range seen {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func shouldSkipRepoDiscoveryDir(name string) bool {
	switch name {
	case ".cache", ".codex", ".cursor", ".repoguide", ".npm", ".Trash", ".yarn", "Library", "Applications",
		"node_modules", "vendor", "dist", "build", "target", ".venv", "venv", "__pycache__":
		return true
	default:
		return false
	}
}
