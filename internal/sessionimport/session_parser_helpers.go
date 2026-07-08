package sessionimport

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// sedScriptPattern matches sed/perl-style scripts like s/a/b/g or y/a/b/ so they
// aren't misparsed as file paths when quote-stripped from a Bash command.
var sedScriptPattern = regexp.MustCompile(`^[a-zA-Z]?[/#|,:].+[/#|,:]`)

func derivePathsFromShell(command string) ([]string, []string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, nil
	}
	readSet := map[string]struct{}{}
	writeSet := map[string]struct{}{}
	// apply_patch uses a heredoc; extract paths from *** Add/Update/Delete File: lines
	if strings.Contains(command, "apply_patch") {
		for _, line := range strings.Split(command, "\n") {
			line = strings.TrimSpace(line)
			for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: "} {
				if strings.HasPrefix(line, prefix) {
					if p := filepath.Clean(strings.TrimPrefix(line, prefix)); p != "." {
						writeSet[p] = struct{}{}
					}
				}
			}
		}
	}
	segments := splitShellSegments(command)
	for _, segment := range segments {
		tokens := strings.Fields(segment)
		if len(tokens) == 0 {
			continue
		}
		isRead := shellSegmentLooksReadOnly(tokens)
		isWrite := shellSegmentLooksWrite(tokens)
		isSed := tokens[0] == "sed"
		for _, token := range tokens {
			if isSed && sedScriptPattern.MatchString(strings.Trim(token, "\"'`")) {
				continue
			}
			path := normalizePathToken(token)
			if path == "" {
				continue
			}
			if isRead {
				readSet[path] = struct{}{}
			}
			if isWrite {
				writeSet[path] = struct{}{}
			}
		}
	}
	return sortedKeys(readSet), sortedKeys(writeSet)
}

func splitShellSegments(command string) []string {
	// ponytail: simple split on shell operators; | is included to prevent pipelines from contaminating write detection
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	parts := strings.Split(replacer.Replace(command), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func shellSegmentLooksReadOnly(tokens []string) bool {
	readCommands := map[string]struct{}{
		"cat": {}, "sed": {}, "head": {}, "tail": {}, "ls": {}, "find": {}, "rg": {}, "git": {}, "wc": {},
	}
	_, ok := readCommands[tokens[0]]
	return ok
}

func shellSegmentLooksWrite(tokens []string) bool {
	// ponytail: only explicit write commands; dropped > redirect detection - too many false positives from 2>/dev/null
	writeCommands := map[string]struct{}{
		"mv": {}, "cp": {}, "tee": {}, "touch": {}, "mkdir": {}, "apply_patch": {}, "gofmt": {},
	}
	if _, ok := writeCommands[tokens[0]]; ok {
		return true
	}
	// sed -i writes in place
	if tokens[0] == "sed" && len(tokens) > 1 {
		for _, t := range tokens[1:] {
			if t == "-i" || strings.HasPrefix(t, "-i") {
				return true
			}
		}
	}
	return false
}

func normalizePathToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, "\"'`()[]{}:,")
	if token == "" || token == "." || strings.HasPrefix(token, "-") || strings.ContainsAny(token, "*|\\><") {
		return ""
	}
	if strings.Contains(token, "=") && !strings.Contains(token, "/") {
		return ""
	}
	if !(strings.Contains(token, "/") || strings.Contains(token, ".")) {
		return ""
	}
	if strings.HasPrefix(token, "$") || strings.HasPrefix(token, "#") {
		return ""
	}
	cleaned := filepath.Clean(token)
	if cleaned == "." || strings.HasPrefix(cleaned, "/dev/") || strings.HasPrefix(cleaned, "/proc/") || strings.HasPrefix(cleaned, "/sys/") {
		return ""
	}
	return cleaned
}

func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// diffLineCounts gives a coarse added/removed line count for a whole-block replacement.
// ponytail: block-level counts (whole old/new strings), not a real line-diff; upgrade to a
// line-level diff (e.g. myers) if per-line precision matters.
func diffLineCounts(oldStr, newStr string) (added, removed int) {
	return countLines(newStr), countLines(oldStr)
}

// countPatchLines counts +/- content lines inside an apply_patch-style hunk body.
func countPatchLines(command string) (added, removed int) {
	if !strings.Contains(command, "apply_patch") {
		return 0, 0
	}
	for _, line := range strings.Split(command, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "***") || strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}

func normalizePaths(paths []string) []string {
	set := map[string]struct{}{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			set[filepath.Clean(path)] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func joinContentTexts(items []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func nestedJSONRawString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return jsonString(value[key])
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func numberSessionEvents(events []SessionEvent) {
	for i := range events {
		events[i].Index = i
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func joinCommand(command []string) string {
	return strings.Join(command, " ")
}
