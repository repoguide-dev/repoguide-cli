package contracts

import (
	"context"
	"time"

	"github.com/repoguide/repoguide-core/model"
)

// Store is the top-level storage contract used by the CLI runtime.
type Store interface {
	Repos() RepoStore
	Sessions() SessionStore
	Events() EventStore
	Topics() TopicStore
	Feedback() FeedbackStore
	Jobs() JobStore
	ClassifyJobs() ClassifyJobStore
	Suggestions() SuggestionStore
}

type RepoStore interface {
	Upsert(ctx context.Context, repo *model.Repo) error
	Get(ctx context.Context, repoID string) (*model.Repo, error)
	Delete(ctx context.Context, repoID string) error
	List(ctx context.Context) ([]*model.Repo, error)
}

type SessionStore interface {
	LatestUpdatedAt(ctx context.Context, repoID string) (time.Time, error)
}

type EventStore interface {
	Put(ctx context.Context, repoID string, events []model.RepoSessionEvents) error
	Get(ctx context.Context, repoID string) ([]model.RepoSessionEvents, error)
	GetSession(ctx context.Context, repoID, sessionID string) ([]model.SessionEvent, error)
	GetDocs(ctx context.Context, repoID string) (map[string]string, error)
	PutDocs(ctx context.Context, repoID string, docs map[string]string) error
}

type TopicStore interface {
	PutTopics(ctx context.Context, repoID string, topics []model.TopicContext) error
	GetTopics(ctx context.Context, repoID string) ([]model.TopicContext, error)
	GetTopic(ctx context.Context, repoID, topicID string) (*model.TopicContext, error)
	PutFiles(ctx context.Context, repoID string, files []model.FileSummary) error
	GetFile(ctx context.Context, repoID, path string) (*model.FileSummary, error)
	PutSearchData(ctx context.Context, repoID string, data model.BundleSearchData) error
	GetSearchData(ctx context.Context, repoID string) (*model.BundleSearchData, error)
	PutRepoContext(ctx context.Context, repoID, content, bundleID string) error
	GetRepoContext(ctx context.Context, repoID string) (*model.RepoContextEntry, error)
}

type FeedbackStore interface {
	CreateMCPCall(ctx context.Context, call *model.MCPCall) (*model.MCPCall, error)
	GetMCPCall(ctx context.Context, callID string) (*model.MCPCall, error)
	PutFeedback(ctx context.Context, fb *model.MCPFeedback) (*model.MCPFeedback, error)
	GetFeedback(ctx context.Context, feedbackID string) (*model.MCPFeedback, error)
	ListUnprocessedFeedback(ctx context.Context, repoID string) ([]model.MCPFeedback, error)
	ListActionableFeedback(ctx context.Context, repoID string) ([]model.MCPFeedback, error)
	ListFeedbackByIDs(ctx context.Context, feedbackIDs []string) ([]model.MCPFeedback, error)
	SetFeedbackClassification(ctx context.Context, feedbackID string, c *model.FeedbackClassification) error
	ClaimFeedbackProcessingJob(ctx context.Context, feedbackID, jobID string) (bool, error)
	ClearFeedbackProcessingJob(ctx context.Context, feedbackID, jobID string) error
	MarkFeedbacksProcessed(ctx context.Context, jobID string, feedbackIDs []string) error
}

type SuggestionStore interface {
	Save(ctx context.Context, repoID string, suggestions []model.TopicPatchSuggestion) error
	ListPending(ctx context.Context, repoID, topicID string) ([]model.TopicPatchSuggestion, error)
	GetByID(ctx context.Context, suggestionID string) (*model.TopicPatchSuggestion, error)
	UpdateStatus(ctx context.Context, suggestionID, status string, confidence int, evidenceFeedbackIDs []string) error
}

type JobStore interface {
	Enqueue(ctx context.Context, job *model.ContextPatchJob) error
	ClaimNext(ctx context.Context, repoID string) (*model.ContextPatchJob, error)
	UpdateStatus(ctx context.Context, jobID, status string, retryCount int) error
	ListPending(ctx context.Context) ([]model.ContextPatchJob, error)
	ResetRunning(ctx context.Context) error
	ResetErrors(ctx context.Context) error
	MarkNewTopicDone(ctx context.Context, jobID, topicID, topicName string) error
}

type ClassifyJobStore interface {
	Enqueue(ctx context.Context, job *model.ContextPatchJob) error
	ClaimNext(ctx context.Context, repoID string) (*model.ContextPatchJob, error)
	UpdateStatus(ctx context.Context, jobID, status string, retryCount int) error
	ListPending(ctx context.Context) ([]model.ContextPatchJob, error)
	ResetRunning(ctx context.Context) error
	ResetErrors(ctx context.Context) error
}

// JobDispatcher schedules background work without coupling to a specific queue.
type JobDispatcher interface {
	Enqueue(jobType, repoID, topicID string) error
}
