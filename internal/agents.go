package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Status int

const (
	OK      Status = iota // ✓ found with full data
	Partial               // ? found but limited
	Missing               // ✗ not found
)

type Agent struct {
	Name    string
	Status  Status
	Details []string
}

// Checker is a named task that returns an Agent - used by the progress runner.
type Checker struct {
	Label string
	Check func() Agent
}

func Checkers() []Checker {
	return []Checker{
		{"Claude Code", checkClaudeCode},
		{"Codex", checkCodex},
		{"Aider", checkAider},
		{"Cursor", checkCursor},
		{"Windsurf", checkWindsurf},
	}
}

func Detect() []Agent {
	return DetectWithProgress(nil)
}

func DetectWithProgress(progress func(current, total int, label string)) []Agent {
	checkers := Checkers()
	out := make([]Agent, len(checkers))
	for i, c := range checkers {
		if progress != nil {
			progress(i+1, len(checkers), c.Label)
		}
		out[i] = c.Check()
	}
	return out
}

func home(parts ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, parts...)...)
}

func countFiles(dir, pattern string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(matches)
}

func checkClaudeCode() Agent {
	path := home(".claude", "projects")
	if _, err := os.Stat(path); err != nil {
		return Agent{"Claude Code", Missing, []string{"Not found"}}
	}
	sessions, _ := filepath.Glob(filepath.Join(path, "*", "*.jsonl"))
	return Agent{"Claude Code", OK, []string{
		fmt.Sprintf("Sessions: %d", len(sessions)),
		"Path: ~/.claude/projects",
		"Quality: full event stream",
	}}
}

func checkCodex() Agent {
	index := home(".codex", "session_index.jsonl")
	if _, err := os.Stat(index); err != nil {
		return Agent{"Codex", Missing, []string{"Not found"}}
	}
	data, _ := os.ReadFile(index)
	active := strings.Count(string(data), "\n")
	archived := countFiles(home(".codex", "archived_sessions"), "*")
	return Agent{"Codex", OK, []string{
		fmt.Sprintf("Sessions: %d", active+archived),
		"Path: ~/.codex",
		"Quality: full event stream",
	}}
}

func checkAider() Agent {
	if _, err := os.Stat(home(".aider.input.history")); err != nil {
		return Agent{"Aider", Missing, []string{"Not found"}}
	}
	n := countFiles(home(), "*/.aider.chat.history.md")
	s := OK
	if n == 0 {
		s = Partial
	}
	return Agent{"Aider", s, []string{
		fmt.Sprintf("Files: %d", n),
		"Quality: chat history",
	}}
}

func checkCursor() Agent {
	wsPath := home("Library", "Application Support", "Cursor", "User", "workspaceStorage")
	if _, err := os.Stat(wsPath); err != nil {
		return Agent{"Cursor", Missing, []string{"Not found"}}
	}
	dbs, _ := filepath.Glob(filepath.Join(wsPath, "*", "state.vscdb"))
	s := OK
	if len(dbs) == 0 {
		s = Partial
	}
	return Agent{"Cursor", s, []string{
		fmt.Sprintf("Databases: %d", len(dbs)),
		"Quality: inspectable SQLite",
	}}
}

func checkWindsurf() Agent {
	for _, p := range []string{
		home("Library", "Application Support", "Windsurf"),
		home(".codeium", "windsurf"),
	} {
		if _, err := os.Stat(p); err == nil {
			short := strings.Replace(p, home(), "~", 1)
			return Agent{"Windsurf", OK, []string{
				"Path: " + short,
				"Quality: inspectable SQLite",
			}}
		}
	}
	return Agent{"Windsurf", Missing, []string{"Not found"}}
}
