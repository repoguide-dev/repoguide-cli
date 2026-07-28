package session

import (
	"encoding/json"
	"os"
	"time"
)

// Analyses are built from cached event logs, so a change to how events are
// parsed only reaches the stats once this is bumped too — bump both whenever
// sessionEventLogVersion changes, or callers keep serving the stale analysis.
const SessionAnalysisVersion = 12

func LoadOrBuild(eventLog SessionEventLog, path string) (SessionAnalysis, bool, error) {
	if cached, err := Read(path); err == nil {
		if cached.Version == SessionAnalysisVersion && SameSessionArtifactSource(cached.Source, eventLog.Source) {
			return cached, true, nil
		}
	}

	out := SessionAnalysis{
		Version:     SessionAnalysisVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Source:      eventLog.Source,
		Session:     eventLog.Session,
		Metrics:     AnalyzeSessionEventLog(eventLog),
	}
	if price, ok := lookupModelPricing(eventLog.Session.Model); ok {
		priceCopy := price
		out.ModelPricing = &priceCopy
		out.Metrics.EstimatedCostUSD = estimateCostUSD(priceCopy, out.Metrics.TokenUsage)
		if es := out.Metrics.ExplorationStats; es != nil {
			es.CostBeforeFirstEditUSD = estimateCostUSD(priceCopy, es.TokensBeforeFirstEdit)
		}
	}
	if err := writeJSON(path, out); err != nil {
		return SessionAnalysis{}, false, err
	}
	return out, false, nil
}

func Read(path string) (SessionAnalysis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionAnalysis{}, err
	}
	var out SessionAnalysis
	if err := json.Unmarshal(data, &out); err != nil {
		return SessionAnalysis{}, err
	}
	return out, nil
}
