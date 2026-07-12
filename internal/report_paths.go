package internal

import (
	"errors"
	"path/filepath"
	"time"
)

// ErrRepoNotInitialized mirrors internal/repo.ErrRepoNotInitialized. It is
// duplicated here (rather than imported) because report_paths.go lives in
// the root "internal" package, which must not import internal/repo (that
// package imports the root package, so the reverse import would create a
// cycle). CurrentRepoStore below is a small self-contained helper, not a
// caller of internal/repo.DetectLocalSetup.
var ErrRepoNotInitialized = errors.New("repoguide is not initialized for this repository")

func CurrentRepoRoot() (string, error) {
	root, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return root, nil
}

// CurrentRepoStore resolves the RepoConfig and store directory for the
// current Git repository, using the same git-config-based lookup as
// internal/repo.DetectLocalSetup.
func CurrentRepoStore() (RepoConfig, string, error) {
	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil || repoRoot == "" {
		return RepoConfig{}, "", ErrNotGitRepo
	}
	repoID, _ := gitOutput("config", "--get", "repoguide.repoId")
	if repoID == "" {
		return RepoConfig{}, "", ErrRepoNotInitialized
	}
	storeDir := filepath.Join(RepoGuideDir(), "repos", repoID)
	return RepoConfig{
		RepoID:   repoID,
		RepoRoot: repoRoot,
	}, storeDir, nil
}

func RepoReportPath(storeDir, category string, at time.Time, ext string) string {
	return RepoReportPathWithSuffix(storeDir, category, at, "", ext)
}

func RepoReportPathWithSuffix(storeDir, category string, at time.Time, suffix, ext string) string {
	if at.IsZero() {
		at = time.Now()
	}
	name := at.Format("2006-01-02")
	if suffix != "" {
		name += "_" + suffix
	}
	return filepath.Join(storeDir, "reports", category, name+ext)
}
