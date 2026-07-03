package repo

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const repoguideIgnoreFileName = ".gitignore"

type repoguideIgnoreRule struct {
	pattern       string
	negated       bool
	directoryOnly bool
	rooted        bool
	hasSlash      bool
}

type repoguideIgnoreMatcher struct {
	rules []repoguideIgnoreRule
}

type cachedRepoguideIgnoreMatcher struct {
	modTime time.Time
	size    int64
	matcher *repoguideIgnoreMatcher
}

var repoguideIgnoreCache struct {
	sync.Mutex
	byRepoRoot map[string]cachedRepoguideIgnoreMatcher
}

func removeRepoGuideArtifacts(_ string) error { return nil }

func loadRepoguideIgnoreMatcher(repoRoot string) (*repoguideIgnoreMatcher, error) {
	ignorePath := filepath.Join(repoRoot, repoguideIgnoreFileName)
	info, err := os.Stat(ignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	repoguideIgnoreCache.Lock()
	defer repoguideIgnoreCache.Unlock()

	if repoguideIgnoreCache.byRepoRoot == nil {
		repoguideIgnoreCache.byRepoRoot = map[string]cachedRepoguideIgnoreMatcher{}
	}
	if cached, ok := repoguideIgnoreCache.byRepoRoot[repoRoot]; ok && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.matcher, nil
	}

	data, err := os.ReadFile(ignorePath)
	if err != nil {
		return nil, err
	}
	matcher := parseRepoguideIgnore(string(data))
	repoguideIgnoreCache.byRepoRoot[repoRoot] = cachedRepoguideIgnoreMatcher{
		modTime: info.ModTime(),
		size:    info.Size(),
		matcher: matcher,
	}
	return matcher, nil
}

func pathIgnoredByRepoGuide(repoRoot, relPath string, isDir bool) bool {
	matcher, err := loadRepoguideIgnoreMatcher(repoRoot)
	if err != nil || matcher == nil {
		return false
	}
	return matcher.Match(relPath, isDir)
}

func parseRepoguideIgnore(contents string) *repoguideIgnoreMatcher {
	lines := strings.Split(contents, "\n")
	rules := make([]repoguideIgnoreRule, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		rule := repoguideIgnoreRule{}
		if strings.HasPrefix(line, "!") {
			rule.negated = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		}
		line = filepath.ToSlash(line)
		if strings.HasPrefix(line, "/") {
			rule.rooted = true
			line = strings.TrimPrefix(line, "/")
		}
		if strings.HasSuffix(line, "/") {
			rule.directoryOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		line = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(line)), "./")
		if line == "." || line == "" {
			continue
		}
		if line == "" {
			continue
		}
		rule.pattern = line
		rule.hasSlash = strings.Contains(line, "/")
		rules = append(rules, rule)
	}
	return &repoguideIgnoreMatcher{rules: rules}
}

func (m *repoguideIgnoreMatcher) Match(relPath string, isDir bool) bool {
	relPath = strings.Trim(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relPath)), "./"), "/")
	if relPath == "" || relPath == "." {
		return false
	}

	ignored := false
	for _, rule := range m.rules {
		if rule.matches(relPath, isDir) {
			ignored = !rule.negated
		}
	}
	return ignored
}

func (r repoguideIgnoreRule) matches(relPath string, isDir bool) bool {
	if r.directoryOnly {
		for _, candidate := range directoryCandidates(relPath, isDir) {
			if r.matchCandidate(candidate) {
				return true
			}
		}
		return false
	}
	return r.matchCandidate(relPath)
}

func (r repoguideIgnoreRule) matchCandidate(candidate string) bool {
	candidate = strings.Trim(candidate, "/")
	if candidate == "" {
		return false
	}

	if r.hasSlash {
		for _, value := range pathMatchCandidates(candidate, r.rooted) {
			if ok, _ := path.Match(r.pattern, value); ok {
				return true
			}
		}
		return false
	}

	for _, value := range segmentMatchCandidates(candidate, r.rooted) {
		if ok, _ := path.Match(r.pattern, value); ok {
			return true
		}
	}
	return false
}

func directoryCandidates(relPath string, isDir bool) []string {
	parts := strings.Split(relPath, "/")
	limit := len(parts)
	if !isDir {
		limit--
	}
	out := make([]string, 0, limit)
	for i := 1; i <= limit; i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}

func pathMatchCandidates(candidate string, rooted bool) []string {
	if rooted {
		return []string{candidate}
	}

	parts := strings.Split(candidate, "/")
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		out = append(out, strings.Join(parts[i:], "/"))
	}
	return out
}

func segmentMatchCandidates(candidate string, rooted bool) []string {
	if rooted {
		return []string{path.Base(candidate)}
	}

	parts := strings.Split(candidate, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, part)
	}
	return out
}
