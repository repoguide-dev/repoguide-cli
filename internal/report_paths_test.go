package internal

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRepoReportPathUsesDateFilename(t *testing.T) {
	storeDir := filepath.Join("/tmp", "repo_123")
	at := time.Date(2026, time.June, 19, 13, 5, 0, 0, time.UTC)

	got := RepoReportPath(storeDir, "ai-analysis", at, ".md")
	want := filepath.Join(storeDir, "reports", "ai-analysis", "2026-06-19.md")
	if got != want {
		t.Fatalf("RepoReportPath() = %q, want %q", got, want)
	}
}

func TestRepoReportPathWithSuffixUsesDateAndSuffixFilename(t *testing.T) {
	storeDir := filepath.Join("/tmp", "repo_123")
	at := time.Date(2026, time.June, 19, 13, 5, 0, 0, time.UTC)

	got := RepoReportPathWithSuffix(storeDir, "ai-analysis", at, "from-360d_to-0d", ".md")
	want := filepath.Join(storeDir, "reports", "ai-analysis", "2026-06-19_from-360d_to-0d.md")
	if got != want {
		t.Fatalf("RepoReportPathWithSuffix() = %q, want %q", got, want)
	}
}
