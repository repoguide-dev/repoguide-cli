package analysis

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/repoguide/repoguide-core/model"
)

// WorstSessionCase is the single most navigation-heavy session found (by
// worstSessionCandidate's score) across all sessions, regardless of which
// file it ended up editing or whether that edit matches any detected
// recurring pattern - it exists to turn "N sessions matched a pattern" into
// one relatable, evidenced example, not to prove the pattern itself.
type WorstSessionCase struct {
	SessionTitle  string   `json:"session_title,omitempty"`
	Prompt        string   `json:"prompt,omitempty"` // fallback opening ask when no usable session title exists
	FilesRead     int      `json:"files_read"`       // distinct real files read before the first edit
	Searches      int      `json:"searches"`         // search tool/shell calls before the first edit
	FilesReopened int      `json:"files_reopened"`   // files read more than once before the first edit
	Detour        []string `json:"detour,omitempty"` // chronological, deduped files read before the edit (tail-capped)
	EditedFile    string   `json:"edited_file"`
	Score         int      `json:"score"` // FilesRead + FilesReopened + Searches; higher = more navigation for one edit
}

const worstSessionDetourCap = 4

// promptPreambleCutPattern drops everything up to and including the first
// closing tag of a known auto-injected wrapper block (environment context,
// tool-injected repo instructions like AGENTS.md, etc.), whether or not the
// matching opening tag is present in this text - upstream capture sometimes
// redacts the opener but leaves the closer.
var promptPreambleCutPattern = regexp.MustCompile(`(?is)^.*?</\s*(?:environment_context|instructions|system-reminder|context)\s*>\s*`)

// wrapperTagPairPattern strips complete wrapper blocks appearing anywhere
// else in the text (e.g. a second injected block after the real ask).
// Go's RE2 doesn't support backreferences, so this doesn't require the
// closing tag to match the opening one - acceptable for stripping wrapper
// noise, where any of these tags closing is enough of a signal.
var wrapperTagPairPattern = regexp.MustCompile(`(?is)<(?:environment_context|instructions|system-reminder|context)\b[^>]*>.*?</\s*(?:environment_context|instructions|system-reminder|context)\s*>`)

// agentsInstructionsHeaderPattern matches the "# AGENTS.md instructions for
// <path>" header some agents prepend before injecting repo instructions.
var agentsInstructionsHeaderPattern = regexp.MustCompile(`(?im)^#.*instructions for .*$`)

// terminalSessionMetadataPattern catches synthetic session labels captured as
// user prompts by some terminal integrations, for example:
// "/Users/alex/repo zsh 2026-05-29 Europe/Berlin".
var terminalSessionMetadataPattern = regexp.MustCompile(`(?i)^[/~].+\s(?:zsh|bash|fish|pwsh)\s\d{4}-\d{2}-\d{2}\s\S+/\S+$`)

// cleanPromptText removes tool/system-injected preamble (environment
// context, injected AGENTS.md/instructions content) so the case study shows
// what the user actually asked, not what the harness prepended to it.
func cleanPromptText(text string) string {
	cleaned := promptPreambleCutPattern.ReplaceAllString(text, "")
	cleaned = wrapperTagPairPattern.ReplaceAllString(cleaned, " ")
	cleaned = agentsInstructionsHeaderPattern.ReplaceAllString(cleaned, " ")
	cleaned = xmlTagPattern.ReplaceAllString(cleaned, " ")
	cleaned = strings.ReplaceAll(cleaned, "[redacted]", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(cleaned), " "))
}

func usableCaseStudyText(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && !terminalSessionMetadataPattern.MatchString(text)
}

func sessionCaseStudyTitle(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "(untitled)") || !usableCaseStudyText(name) {
		return ""
	}
	return truncatePromptPreview(name, 160)
}

// FindWorstSession scans every stored session for the one whose first edit
// was preceded by the most reads/reopens/searches (by a simple additive
// score). It's picked from the whole session set, not just sessions that
// match a detected recurring pattern - a session doesn't need to have been
// fixable by prior evidence to be worth showing as a concrete example.
func FindWorstSession(repoRoot string, stored []model.RepoSessionEvents) (WorstSessionCase, bool) {
	var best WorstSessionCase
	found := false
	for _, s := range stored {
		cand, ok := worstSessionCandidate(repoRoot, s)
		if !ok {
			continue
		}
		if !found || cand.Score > best.Score {
			best = cand
			found = true
		}
	}
	return best, found
}

func worstSessionCandidate(repoRoot string, s model.RepoSessionEvents) (WorstSessionCase, bool) {
	sessionTitle := sessionCaseStudyTitle(s.Name)
	var prompt string
	seen := map[string]struct{}{}
	readCounts := map[string]int{}
	var detour []string
	searches := 0
	editedFile := ""
	done := false

	for _, ev := range s.Events {
		if sessionTitle == "" && prompt == "" && ev.Kind == "prompt" {
			if cleaned := cleanPromptText(ev.Text); usableCaseStudyText(cleaned) {
				prompt = truncatePromptPreview(cleaned, 300)
			}
		}
		if done {
			continue
		}
		if ev.Kind == "tool_call" && isSearchEvent(ev) {
			searches++
		}
		for _, raw := range ev.ReadPaths {
			path := repoRelPath(repoRoot, raw)
			if path == "" || !realFileExists(repoRoot, path) {
				continue
			}
			readCounts[path]++
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				detour = append(detour, path)
			}
		}
		for _, raw := range ev.WritePaths {
			path := repoRelPath(repoRoot, raw)
			if path == "" {
				continue
			}
			editedFile = path
			done = true
			break
		}
	}

	if editedFile == "" {
		return WorstSessionCase{}, false
	}

	reopens := 0
	for _, c := range readCounts {
		if c > 1 {
			reopens++
		}
	}
	if len(detour) > worstSessionDetourCap {
		// Keep the files closest to the edit - most relevant to "what led here".
		detour = detour[len(detour)-worstSessionDetourCap:]
	}

	return WorstSessionCase{
		SessionTitle:  sessionTitle,
		Prompt:        prompt,
		FilesRead:     len(seen),
		Searches:      searches,
		FilesReopened: reopens,
		Detour:        detour,
		EditedFile:    editedFile,
		Score:         len(seen) + reopens + searches,
	}, true
}

// realFileExists guards against non-path strings (e.g. a search regex like
// "rate.?limit" that ends up in a ReadPaths list) being displayed as if they
// were a file the agent read.
func realFileExists(repoRoot, relPath string) bool {
	info, err := os.Stat(filepath.Join(repoRoot, relPath))
	return err == nil && !info.IsDir()
}
