package session

import "time"

type SessionSummary struct {
	ID              string
	Agent           string
	Name            string
	Cwd             string
	Model           string
	Timestamp       time.Time
	Path            string
	RepoID          string
	RepoName        string
	RepoRoot        string
	RepoRelativeCwd string
	RepoInitialized bool
	UsedRepoGuide   bool
	CostUSD         float64
	ReadFileCount   int
	EditFileCount   int
}

type SessionArtifactSource struct {
	Agent           string `json:"agent"`
	Path            string `json:"path"`
	FileModUnixNano int64  `json:"fileModUnixNano"`
	FileSize        int64  `json:"fileSize"`
}

type SessionEventLog struct {
	Version     int                   `json:"version"`
	GeneratedAt string                `json:"generatedAt"`
	Source      SessionArtifactSource `json:"source"`
	Session     SessionSummary        `json:"session"`
	Events      []SessionEvent        `json:"events"`
}

type SessionEvent struct {
	Index       int               `json:"index"`
	Timestamp   string            `json:"timestamp,omitempty"`
	Kind        string            `json:"kind"`
	Role        string            `json:"role,omitempty"`
	Text        string            `json:"text,omitempty"`
	Model       string            `json:"model,omitempty"`
	ToolName    string            `json:"toolName,omitempty"`
	ToolCallID  string            `json:"toolCallId,omitempty"`
	IsError     bool              `json:"isError,omitempty"`
	TokenUsage  *TokenUsage       `json:"tokenUsage,omitempty"`
	ReadPaths   []string          `json:"readPaths,omitempty"`
	WritePaths  []string          `json:"writePaths,omitempty"`
	Command     []string          `json:"command,omitempty"`
	CommandText string            `json:"commandText,omitempty"`
	SearchQuery string            `json:"searchQuery,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type TokenUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	Cumulative       bool  `json:"cumulative"`
}

type SessionAnalysis struct {
	Version      int                   `json:"version"`
	GeneratedAt  string                `json:"generatedAt"`
	Source       SessionArtifactSource `json:"source"`
	Session      SessionSummary        `json:"session"`
	ModelPricing *ModelPricing         `json:"modelPricing,omitempty"`
	Metrics      SessionMetrics        `json:"metrics"`
}

type PromptBlock struct {
	Text                 string         `json:"text"`
	FullText             string         `json:"fullText,omitempty"`
	ReadsBeforeFirstEdit int            `json:"readsBeforeFirstEdit"`
	EditCount            int            `json:"editCount,omitempty"`
	ReadFiles            []string       `json:"readFiles,omitempty"`
	EditedFiles          []string       `json:"editedFiles,omitempty"`
	ReadFileCountsBefore map[string]int `json:"readFileCountsBefore,omitempty"`
	ReadFileCountsAfter  map[string]int `json:"readFileCountsAfter,omitempty"`
	Searches             []SearchTrace  `json:"searches,omitempty"`
	DeadEndSearches      int            `json:"deadEndSearches,omitempty"`
	TokenUsage           *TokenUsage    `json:"tokenUsage,omitempty"`
	DurationSec          float64        `json:"durationSec,omitempty"`
}

type SearchTrace struct {
	Query           string   `json:"query"`
	ReadFiles       []string `json:"readFiles,omitempty"`
	ReadsBeforeEdit int      `json:"readsBeforeEdit,omitempty"`
	EditTarget      string   `json:"editTarget,omitempty"`
	FoundViaSearch  bool     `json:"foundViaSearch,omitempty"`
}

type SessionMetrics struct {
	EventCount            int               `json:"eventCount"`
	UserPromptCount       int               `json:"userPromptCount"`
	AssistantMessageCount int               `json:"assistantMessageCount"`
	TurnCount             int               `json:"turnCount"`
	ToolCallCount         int               `json:"toolCallCount"`
	ReadFileCount         int               `json:"readFileCount"`
	EditedFileCount       int               `json:"editedFileCount"`
	ReadFiles             []string          `json:"readFiles,omitempty"`
	EditedFiles           []string          `json:"editedFiles,omitempty"`
	ReadFileCounts        map[string]int    `json:"readFileCounts,omitempty"`
	EditFileCounts        map[string]int    `json:"editFileCounts,omitempty"`
	PromptBlocks          []PromptBlock     `json:"promptBlocks,omitempty"`
	TokenUsage            *TokenUsage       `json:"tokenUsage,omitempty"`
	ContextStats          *ContextStats     `json:"contextStats,omitempty"`
	ExplorationStats      *ExplorationStats `json:"explorationStats,omitempty"`
	FailureStats          *FailureStats     `json:"failureStats,omitempty"`
	EstimatedCostUSD      float64           `json:"estimatedCostUsd,omitempty"`
}

type FailureStats struct {
	FailedToolCalls    int `json:"failedToolCalls"`
	RecoveredFailures  int `json:"recoveredFailures"`
	UnresolvedFailures int `json:"unresolvedFailures"`
}

type ExplorationStats struct {
	ToolCallsBeforeFirstEdit int         `json:"toolCallsBeforeFirstEdit"`
	FilesReadBeforeFirstEdit int         `json:"filesReadBeforeFirstEdit"`
	SearchesBeforeFirstEdit  int         `json:"searchesBeforeFirstEdit"`
	CostBeforeFirstEditUSD   float64     `json:"costBeforeFirstEditUsd,omitempty"`
	TokensBeforeFirstEdit    *TokenUsage `json:"tokensBeforeFirstEdit,omitempty"`
}

type ContextStats struct {
	MaxEffectiveInputTokens    int64   `json:"maxEffectiveInputTokens"`
	AvgEffectiveInputTokens    float64 `json:"avgEffectiveInputTokens"`
	PeakContextBeforeFirstEdit int64   `json:"peakContextBeforeFirstEdit"`
	CacheHitRatio              float64 `json:"cacheHitRatio"`
	ContextPressure            string  `json:"contextPressure"`
	CacheReuse                 string  `json:"cacheReuse"`
}

type ModelPricing struct {
	Model                string  `json:"model"`
	InputPerMTokUSD      float64 `json:"inputPerMillionTokensUsd"`
	OutputPerMTokUSD     float64 `json:"outputPerMillionTokensUsd"`
	CacheReadPerMTokUSD  float64 `json:"cacheReadPerMillionTokensUsd"`
	CacheWritePerMTokUSD float64 `json:"cacheWritePerMillionTokensUsd"`
	ContextWindowTokens  int64   `json:"contextWindowTokens,omitempty"`
}
