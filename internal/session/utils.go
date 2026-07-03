package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var searchToolNames = map[string]bool{
	"WebSearch": true,
	"WebFetch":  true,
	"Grep":      true,
}

var searchShellCmds = map[string]bool{
	"grep": true,
	"rg":   true,
	"ag":   true,
	"ack":  true,
	"find": true,
}

var shellSearchQueryPattern = regexp.MustCompile(`(?:^|[\s;&|])(?:rg|grep|ag|ack)\s+(?:-[^\s]+\s+)*(?:"([^"]+)"|'([^']+)'|([^\s]+))`)

func SameSessionArtifactSource(a, b SessionArtifactSource) bool {
	return a.Agent == b.Agent &&
		a.Path == b.Path &&
		a.FileModUnixNano == b.FileModUnixNano &&
		a.FileSize == b.FileSize
}

func isSearchEvent(event SessionEvent) bool {
	if searchToolNames[event.ToolName] {
		return true
	}
	if event.CommandText == "" {
		return false
	}
	fields := strings.Fields(event.CommandText)
	if len(fields) == 0 {
		return false
	}
	return searchShellCmds[fields[0]]
}

func searchEventQuery(event SessionEvent) string {
	query := strings.TrimSpace(event.SearchQuery)
	if query != "" {
		return query
	}
	return shellSearchQuery(event.CommandText)
}

func shellSearchQuery(command string) string {
	if match := shellSearchQueryPattern.FindStringSubmatch(command); len(match) > 0 {
		for _, candidate := range match[1:] {
			if candidate != "" {
				return candidate
			}
		}
	}
	fields := strings.Fields(command)
	for i, field := range fields {
		name := filepath.Base(strings.Trim(field, "'\""))
		if !searchShellCmds[name] {
			continue
		}
		for j := i + 1; j < len(fields); j++ {
			candidate := strings.Trim(fields[j], "'\"")
			if candidate == "" || strings.HasPrefix(candidate, "-") {
				continue
			}
			return candidate
		}
	}
	return strings.TrimSpace(command)
}

func truncatePrompt(text string) string {
	if len(text) <= 80 {
		return text
	}
	return text[:77] + "..."
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func addTokenUsage(dst *TokenUsage, usage TokenUsage) {
	dst.InputTokens += usage.InputTokens
	dst.OutputTokens += usage.OutputTokens
	dst.CacheReadTokens += usage.CacheReadTokens
	dst.CacheWriteTokens += usage.CacheWriteTokens
}

func tokenUsageDelta(prev *TokenUsage, next TokenUsage) TokenUsage {
	if prev == nil {
		return TokenUsage{
			InputTokens:      next.InputTokens,
			OutputTokens:     next.OutputTokens,
			CacheReadTokens:  next.CacheReadTokens,
			CacheWriteTokens: next.CacheWriteTokens,
		}
	}
	return TokenUsage{
		InputTokens:      nonNegativeDelta(next.InputTokens, prev.InputTokens),
		OutputTokens:     nonNegativeDelta(next.OutputTokens, prev.OutputTokens),
		CacheReadTokens:  nonNegativeDelta(next.CacheReadTokens, prev.CacheReadTokens),
		CacheWriteTokens: nonNegativeDelta(next.CacheWriteTokens, prev.CacheWriteTokens),
	}
}

func nonNegativeDelta(next, prev int64) int64 {
	if next <= prev {
		return 0
	}
	return next - prev
}

func hasTokenUsage(usage TokenUsage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheReadTokens > 0 ||
		usage.CacheWriteTokens > 0
}

func effectiveInputTokens(usage TokenUsage) int64 {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
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
