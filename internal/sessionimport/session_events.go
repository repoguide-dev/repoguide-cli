package sessionimport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	analysispkg "github.com/repoguide/repoguide-cli/internal/session"
)

var (
	uuidPattern      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	longTokenPattern = regexp.MustCompile(`\S{17,}`)
)

func redactSecrets(text string) string {
	text = uuidPattern.ReplaceAllString(text, "[id]")
	return longTokenPattern.ReplaceAllStringFunc(text, func(tok string) string {
		for _, r := range tok {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' && r != '/' {
				return "[redacted]"
			}
		}
		return tok
	})
}

func redactEvents(events []SessionEvent) {
	_ = events
}

const (
	sessionEventLogVersion  = 5
	sessionEventLogFileName = "events.json"
	sessionAnalysisFileName = "analysis.json"
)

type SessionArtifacts struct {
	SessionID      string          `json:"sessionId"`
	EventsPath     string          `json:"eventsPath"`
	AnalysisPath   string          `json:"analysisPath"`
	EventLog       SessionEventLog `json:"eventLog"`
	Analysis       SessionAnalysis `json:"analysis"`
	EventsCached   bool            `json:"eventsCached"`
	AnalysisCached bool            `json:"analysisCached"`
}

type SessionArtifactWarmResult struct {
	Agent        string `json:"agent"`
	SessionCount int    `json:"sessionCount"`
}

type CachedSessionAnalysis struct {
	AnalysisPath string          `json:"analysisPath"`
	Analysis     SessionAnalysis `json:"analysis"`
}

func BuildSessionArtifacts(session SessionSummary) (SessionArtifacts, error) {
	info, err := os.Stat(session.Path)
	if err != nil {
		return SessionArtifacts{}, err
	}

	source := SessionArtifactSource{
		Agent:           session.Agent,
		Path:            session.Path,
		FileModUnixNano: info.ModTime().UnixNano(),
		FileSize:        info.Size(),
	}

	dir := filepath.Join(RepoGuideDir(), "sessions", session.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SessionArtifacts{}, err
	}

	eventsPath := filepath.Join(dir, sessionEventLogFileName)
	eventLog, eventsCached, err := loadOrBuildEventLog(session, source, eventsPath)
	if err != nil {
		return SessionArtifacts{}, err
	}

	analysisPath := filepath.Join(dir, sessionAnalysisFileName)
	analysis, analysisCached, err := analysispkg.LoadOrBuild(eventLog, analysisPath)
	if err != nil {
		return SessionArtifacts{}, err
	}

	return SessionArtifacts{
		SessionID:      session.ID,
		EventsPath:     eventsPath,
		AnalysisPath:   analysisPath,
		EventLog:       eventLog,
		Analysis:       analysis,
		EventsCached:   eventsCached,
		AnalysisCached: analysisCached,
	}, nil
}

func LoadCachedSessionAnalysis(session SessionSummary) (CachedSessionAnalysis, bool, error) {
	info, err := os.Stat(session.Path)
	if err != nil {
		return CachedSessionAnalysis{}, false, err
	}

	source := SessionArtifactSource{
		Agent:           session.Agent,
		Path:            session.Path,
		FileModUnixNano: info.ModTime().UnixNano(),
		FileSize:        info.Size(),
	}

	analysisPath := filepath.Join(RepoGuideDir(), "sessions", session.ID, sessionAnalysisFileName)
	analysis, err := analysispkg.Read(analysisPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CachedSessionAnalysis{}, false, nil
		}
		return CachedSessionAnalysis{}, false, err
	}
	if analysis.Version != analysispkg.SessionAnalysisVersion || !analysispkg.SameSessionArtifactSource(analysis.Source, source) {
		return CachedSessionAnalysis{}, false, nil
	}

	return CachedSessionAnalysis{
		AnalysisPath: analysisPath,
		Analysis:     analysis,
	}, true, nil
}

func loadOrBuildEventLog(session SessionSummary, source SessionArtifactSource, path string) (SessionEventLog, bool, error) {
	if cached, err := readSessionEventLog(path); err == nil {
		if cached.Version == sessionEventLogVersion && analysispkg.SameSessionArtifactSource(cached.Source, source) {
			return cached, true, nil
		}
	}

	events, err := buildSessionEvents(session)
	if err != nil {
		return SessionEventLog{}, false, err
	}
	redactEvents(events)

	log := SessionEventLog{
		Version:     sessionEventLogVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Source:      source,
		Session:     session,
		Events:      events,
	}
	if err := writeJSON(path, log); err != nil {
		return SessionEventLog{}, false, err
	}
	return log, false, nil
}

func readSessionEventLog(path string) (SessionEventLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionEventLog{}, err
	}
	var out SessionEventLog
	if err := json.Unmarshal(data, &out); err != nil {
		return SessionEventLog{}, err
	}
	return out, nil
}

func buildSessionEvents(session SessionSummary) ([]SessionEvent, error) {
	switch session.Agent {
	case "codex":
		return buildCodexSessionEvents(session.Path)
	case "claude":
		return buildClaudeSessionEvents(session.Path)
	case "cursor":
		return buildCursorSessionEvents(session.Path)
	case "opencode":
		return buildOpencodeSessionEvents(session.Path)
	case "copilot":
		return buildCopilotSessionEvents(session.Path)
	case "gemini":
		return buildGeminiSessionEvents(session.Path)
	default:
		return nil, fmt.Errorf("unsupported agent %q", session.Agent)
	}
}

func WarmRepoSessionArtifacts(repoFilter string, progress func(current, total int, label string)) ([]SessionArtifactWarmResult, error) {
	if strings.TrimSpace(repoFilter) == "" {
		return nil, nil
	}

	if sessions, ok, err := loadRepoSessionSummaries(repoFilter); err != nil {
		return nil, err
	} else if ok {
		return warmSessionArtifactsFromSummaries(sessions, progress)
	}

	pagesByAgent := make(map[string]SessionPage, len(SupportedSessionAgents()))
	totalSessions := 0
	for _, agent := range SupportedSessionAgents() {
		page, err := LoadSessionPage(agent, 0, -1, SessionLoadOptions{Repo: repoFilter})
		if err != nil {
			if err.Error() == noSessionsError(agent).Error() {
				continue
			}
			return nil, err
		}
		pagesByAgent[agent] = page
		totalSessions += len(page.Sessions)
	}

	if totalSessions == 0 {
		if progress != nil {
			progress(0, 0, "No repo sessions found to analyze")
		}
		return nil, nil
	}

	results := make([]SessionArtifactWarmResult, 0, len(pagesByAgent))
	completed := 0
	for _, agent := range SupportedSessionAgents() {
		page, ok := pagesByAgent[agent]
		if !ok || len(page.Sessions) == 0 {
			continue
		}
		for _, session := range page.Sessions {
			if progress != nil {
				progress(completed+1, totalSessions, fmt.Sprintf("Analyzing %s session %s", displayAgentName(agent), session.ID))
			}
			if _, err := BuildSessionArtifacts(session); err != nil {
				return nil, err
			}
			completed++
		}
		results = append(results, SessionArtifactWarmResult{
			Agent:        agent,
			SessionCount: len(page.Sessions),
		})
	}

	return results, nil
}

func warmSessionArtifactsFromSummaries(sessions []SessionSummary, progress func(current, total int, label string)) ([]SessionArtifactWarmResult, error) {
	if len(sessions) == 0 {
		if progress != nil {
			progress(0, 0, "No repo sessions found to analyze")
		}
		return nil, nil
	}

	countsByAgent := make(map[string]int, len(SupportedSessionAgents()))
	for i, session := range sessions {
		if progress != nil {
			progress(i+1, len(sessions), fmt.Sprintf("Analyzing %s session %s", displayAgentName(session.Agent), session.ID))
		}
		if _, err := BuildSessionArtifacts(session); err != nil {
			return nil, err
		}
		countsByAgent[session.Agent]++
	}

	results := make([]SessionArtifactWarmResult, 0, len(countsByAgent))
	for _, agent := range SupportedSessionAgents() {
		if countsByAgent[agent] == 0 {
			continue
		}
		results = append(results, SessionArtifactWarmResult{
			Agent:        agent,
			SessionCount: countsByAgent[agent],
		})
	}
	return results, nil
}
