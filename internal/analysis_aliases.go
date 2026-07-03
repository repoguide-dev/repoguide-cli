package internal

import analysispkg "github.com/repoguide/repoguide-cli/internal/session"

type SessionSummary = analysispkg.SessionSummary
type SessionArtifactSource = analysispkg.SessionArtifactSource
type SessionEventLog = analysispkg.SessionEventLog
type SessionEvent = analysispkg.SessionEvent
type TokenUsage = analysispkg.TokenUsage
type SessionAnalysis = analysispkg.SessionAnalysis
type PromptBlock = analysispkg.PromptBlock
type SearchTrace = analysispkg.SearchTrace
type SessionMetrics = analysispkg.SessionMetrics
type FailureStats = analysispkg.FailureStats
type ExplorationStats = analysispkg.ExplorationStats
type ContextStats = analysispkg.ContextStats
type ModelPricing = analysispkg.ModelPricing

func AnalyzeSessionEventLog(eventLog SessionEventLog) SessionMetrics {
	return analysispkg.AnalyzeSessionEventLog(eventLog)
}

func EstimateCostUSD(pricing ModelPricing, usage *TokenUsage) float64 {
	return analysispkg.EstimateCostUSD(pricing, usage)
}

func ReadSessionAnalysis(path string) (SessionAnalysis, error) {
	return analysispkg.Read(path)
}
