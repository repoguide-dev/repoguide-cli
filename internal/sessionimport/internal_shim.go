package sessionimport

import (
	internalpkg "github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/repo"
)

// This file re-exposes root-package ("internal") and internal/repo types and
// helpers under the unqualified names sessions.go, session_events.go,
// repo_sync.go, and repo_discovery.go already use, so those files (originally
// written as part of package internal, or as part of package repo) did not
// need per-call-site edits when they were split into this package.

type SessionSummary = internalpkg.SessionSummary
type SessionArtifactSource = internalpkg.SessionArtifactSource
type SessionEventLog = internalpkg.SessionEventLog
type SessionEvent = internalpkg.SessionEvent
type TokenUsage = internalpkg.TokenUsage
type SessionAnalysis = internalpkg.SessionAnalysis
type PromptBlock = internalpkg.PromptBlock
type SearchTrace = internalpkg.SearchTrace
type SessionMetrics = internalpkg.SessionMetrics
type FailureStats = internalpkg.FailureStats
type ExplorationStats = internalpkg.ExplorationStats
type ContextStats = internalpkg.ContextStats
type ModelPricing = internalpkg.ModelPricing
type RepoConfig = repo.RepoConfig

func AnalyzeSessionEventLog(eventLog SessionEventLog) SessionMetrics {
	return internalpkg.AnalyzeSessionEventLog(eventLog)
}

func EstimateCostUSD(pricing ModelPricing, usage *TokenUsage) float64 {
	return internalpkg.EstimateCostUSD(pricing, usage)
}

func ReadSessionAnalysis(path string) (SessionAnalysis, error) {
	return internalpkg.ReadSessionAnalysis(path)
}

func CurrentRepoRoot() (string, error) { return internalpkg.CurrentRepoRoot() }

func gitOutputAt(repoRoot string, args ...string) (string, error) {
	return internalpkg.GitOutputAt(repoRoot, args...)
}

func gitOutput(args ...string) (string, error) { return internalpkg.GitOutput(args...) }

func home(parts ...string) string { return internalpkg.Home(parts...) }

func glob(pattern string) []string { return internalpkg.Glob(pattern) }

func writeJSON(path string, value any) error { return internalpkg.WriteJSON(path, value) }

func ListConfiguredRepos() ([]RepoConfig, error) { return repo.ListConfiguredRepos() }

func RepoIsLocalModeByID(repoID string) bool { return repo.RepoIsLocalModeByID(repoID) }

func RepoGuideDir() string { return repo.RepoGuideDir() }

func LoadRepoConfigFile(storeDir string) (RepoConfig, error) {
	return repo.LoadRepoConfigFile(storeDir)
}

func SaveRepoConfigFile(storeDir string, cfg RepoConfig) error {
	return repo.SaveRepoConfigFile(storeDir, cfg)
}
