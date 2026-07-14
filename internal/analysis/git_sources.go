package analysis

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func recentGitCommitSources(repoRoot string, limit int) []RepoAnalysisSource {
	if strings.TrimSpace(repoRoot) == "" || limit <= 0 {
		return nil
	}
	cmd := exec.Command("git", "-C", repoRoot, "log", "-n", strconv.Itoa(limit), "--format=%x1e%H%x1f%an%x1f%aI%x1f%s", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var sources []RepoAnalysisSource
	for _, record := range strings.Split(string(out), "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		lines := strings.Split(record, "\n")
		fields := strings.Split(lines[0], "\x1f")
		if len(fields) != 4 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		source := RepoAnalysisSource{
			ID: "commit:" + fields[0], SourceType: "commit", AuthorID: fields[1],
			Title: fields[3], Prompts: []string{fields[3]}, Timestamp: fields[2],
		}
		for _, path := range lines[1:] {
			if path = strings.TrimSpace(path); path != "" {
				source.ChangedFiles = append(source.ChangedFiles, filepath.ToSlash(path))
			}
		}
		sources = append(sources, source)
	}
	return sources
}
