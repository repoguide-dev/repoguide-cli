package analysis

import (
	"time"

	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

const bundleVersion = 10

type sessionMetrics struct {
	UserPromptCount  int
	ToolCallCount    int
	ReadFileCounts   map[string]int
	EditFileCounts   map[string]int
	PromptBlocks     []promptBlock
	TokenUsage       *model.TokenUsage
	EstimatedCostUSD float64
}

type promptBlock struct {
	ReadsBeforeFirstEdit int
	EditedFiles          []string
	ReadFiles            []string
	Searches             []searchTrace
	DeadEndSearches      int
	TokenUsage           *model.TokenUsage
}

type searchTrace struct {
	Query           string
	ReadFiles       []string
	ReadsBeforeEdit int
	EditTarget      string
	FoundViaSearch  bool
}

type backendSession struct {
	id        string
	agent     string
	name      string
	timestamp time.Time
	metrics   sessionMetrics
	events    []model.SessionEvent
}

type fileAccum struct {
	path                      string
	kind                      string
	sessionSet                map[string]struct{}
	editSessionSet            map[string]struct{}
	reads                     int
	edits                     int
	contextTokens             int64
	firstSeen                 time.Time
	lastSeen                  time.Time
	readsBeforeFirstEditSum   float64
	readsBeforeFirstEditN     int
	promptsBeforeFirstEditSum float64
	promptsBeforeFirstEditN   int
	contextBeforeFirstEditSum float64
	contextBeforeFirstEditN   int
	testReads                 int
	testEdits                 int
	testReadBeforeEdit        int
	sourceEditWithoutTestRead int
	sourceEditWithoutTestEdit int
	relatedTests              map[string]*relatedTestAccum
	readBeforeEditTargets     map[string]int
	commonlySeenWith          map[string]int
	foundViaSearchSessions    map[string]struct{}
	searchesBeforeEditSum     int
	readsAfterSearchSum       int
	searchQueries             map[string]int
}

type searchAccum struct {
	query       string
	searches    int
	readTargets map[string]struct{}
	editTargets map[string]struct{}
}

type subsystemAccum struct {
	name                    string
	pathSet                 map[string]struct{}
	sessionSet              map[string]struct{}
	reads                   int
	edits                   int
	sourceReads             int
	sourceEdits             int
	testReads               int
	testEdits               int
	contextTokens           int64
	costUSD                 float64
	readsBeforeFirstEditSum float64
	readsBeforeFirstEditN   int
	fileScores              map[string]float64
	sourceEditSessionSet    map[string]struct{}
	testTouchedSessionSet   map[string]struct{}
}

type traceAccum struct {
	pattern    []string
	sessions   int
	costSum    float64
	contextSum int64
}

type expensiveEditAccum struct {
	target                    string
	sessionSet                map[string]struct{}
	costSum                   float64
	contextSum                int64
	readsBeforeFirstEditSum   float64
	promptsBeforeFirstEditSum float64
	precedingReadSessions     map[string]int
	relatedSubsystems         map[string]int
}

type readBeforeEditAccum struct {
	source     string
	target     string
	sessionSet map[string]struct{}
	costSum    float64
	contextSum int64
}

type lifecycleAccum struct {
	file       string
	chain      string
	sessions   int
	costSum    float64
	contextSum int64
}

type relatedTestAccum struct {
	path  string
	reads int
	edits int
}

type testSignalsAccum struct {
	sourceEditSessions    map[string]struct{}
	sessionsWithTestReads map[string]struct{}
	sessionsWithTestEdits map[string]struct{}
	testsAsSpec           map[string]*testRelationAccum
	sourceAndTestCoEdit   map[string]*testRelationAccum
	testFriction          map[string]*testFileAccum
	testChurn             map[string]*testFileAccum
}

type testRelationAccum struct {
	source     string
	test       string
	sessionSet map[string]struct{}
}

type testFileAccum struct {
	test          string
	sessionSet    map[string]struct{}
	reads         int
	edits         int
	sourceEdits   int
	contextTokens int64
}

type RepoAnalysisBundle = contracts.RepoAnalysisBundle
type RepoAnalysisRepo = contracts.RepoAnalysisRepo
type RepoAnalysisSummary = contracts.RepoAnalysisSummary
type RepoAnalysisSession = contracts.RepoAnalysisSession
type SessionInteraction = contracts.SessionInteraction
type RepoAnalysisFile = contracts.RepoAnalysisFile
type RepoAnalysisFileLink = contracts.RepoAnalysisFileLink
type RepoAnalysisSessionRef = contracts.RepoAnalysisSessionRef
type RepoAnalysisSubsystem = contracts.RepoAnalysisSubsystem
type RepoAnalysisTestSignals = contracts.RepoAnalysisTestSignals
type RepoAnalysisTestSummary = contracts.RepoAnalysisTestSummary
type RepoAnalysisTestRelation = contracts.RepoAnalysisTestRelation
type RepoAnalysisTestFile = contracts.RepoAnalysisTestFile
type RepoAnalysisRelation = contracts.RepoAnalysisRelation
type RepoAnalysisRelationshipGroup = contracts.RepoAnalysisRelationshipGroup
type RepoAnalysisTrace = contracts.RepoAnalysisTrace
type RepoAnalysisPathCount = contracts.RepoAnalysisPathCount
type RepoAnalysisNameCount = contracts.RepoAnalysisNameCount
type RepoAnalysisDiscoverability = contracts.RepoAnalysisDiscoverability
type RepoAnalysisSearchTarget = contracts.RepoAnalysisSearchTarget
type RepoAnalysisAmbiguousSearch = contracts.RepoAnalysisAmbiguousSearch
type RepoAnalysisQueryCount = contracts.RepoAnalysisQueryCount
type RepoAnalysisDoc = contracts.RepoAnalysisDoc
