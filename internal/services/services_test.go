package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

// ── Fake store ────────────────────────────────────────────────────────────────

type fakeStore struct {
	repos    map[string]*model.Repo
	topics   map[string][]model.TopicContext
	files    map[string]*model.FileSummary // "repoID:path" → FileSummary
	search   map[string]*model.BundleSearchData
	repoCtx  map[string]*model.RepoContextEntry
	events   map[string][]model.RepoSessionEvents
	docs     map[string]map[string]string
	feedback map[string]*model.MCPFeedback
	calls    map[string]*model.MCPCall
	jobs     []model.ContextPatchJob
	jobState map[string]model.ContextPatchJob
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		repos:    map[string]*model.Repo{},
		topics:   map[string][]model.TopicContext{},
		files:    map[string]*model.FileSummary{},
		search:   map[string]*model.BundleSearchData{},
		repoCtx:  map[string]*model.RepoContextEntry{},
		events:   map[string][]model.RepoSessionEvents{},
		docs:     map[string]map[string]string{},
		feedback: map[string]*model.MCPFeedback{},
		calls:    map[string]*model.MCPCall{},
		jobState: map[string]model.ContextPatchJob{},
	}
}

func (f *fakeStore) Repos() contracts.RepoStore               { return &fakeRepoStore{f} }
func (f *fakeStore) Sessions() contracts.SessionStore         { return &fakeSessionStore{f} }
func (f *fakeStore) Events() contracts.EventStore             { return &fakeEventStore{f} }
func (f *fakeStore) Topics() contracts.TopicStore             { return &fakeTopicStore{f} }
func (f *fakeStore) Feedback() contracts.FeedbackStore        { return &fakeFeedbackStore{f} }
func (f *fakeStore) Jobs() contracts.JobStore                 { return &fakeJobStore{f} }
func (f *fakeStore) ClassifyJobs() contracts.ClassifyJobStore { return &fakeJobStore{f} }
func (f *fakeStore) Suggestions() contracts.SuggestionStore   { return &fakeSuggestionStore{} }

type fakeSuggestionStore struct{}

func (s *fakeSuggestionStore) Save(_ context.Context, _ string, _ []model.TopicPatchSuggestion) error {
	return nil
}
func (s *fakeSuggestionStore) ListPending(_ context.Context, _, _ string) ([]model.TopicPatchSuggestion, error) {
	return nil, nil
}
func (s *fakeSuggestionStore) GetByID(_ context.Context, _ string) (*model.TopicPatchSuggestion, error) {
	return nil, nil
}
func (s *fakeSuggestionStore) UpdateStatus(_ context.Context, _, _ string, _ int, _ []string) error {
	return nil
}

// ── RepoStore ──────────────────────────────────────────────────────────────────

type fakeRepoStore struct{ s *fakeStore }

func (r *fakeRepoStore) Upsert(_ context.Context, repo *model.Repo) error {
	r.s.repos[repo.RepoID] = repo
	return nil
}
func (r *fakeRepoStore) Get(_ context.Context, repoID string) (*model.Repo, error) {
	repo, ok := r.s.repos[repoID]
	if !ok {
		return nil, nil
	}
	return repo, nil
}
func (r *fakeRepoStore) Delete(_ context.Context, repoID string) error {
	delete(r.s.repos, repoID)
	return nil
}
func (r *fakeRepoStore) List(_ context.Context) ([]*model.Repo, error) {
	out := make([]*model.Repo, 0, len(r.s.repos))
	for _, v := range r.s.repos {
		out = append(out, v)
	}
	return out, nil
}

// ── SessionStore ───────────────────────────────────────────────────────────────

type fakeSessionStore struct{ s *fakeStore }

func (ss *fakeSessionStore) LatestUpdatedAt(_ context.Context, repoID string) (time.Time, error) {
	evs := ss.s.events[repoID]
	var t time.Time
	for _, e := range evs {
		if e.UpdatedAt.After(t) {
			t = e.UpdatedAt
		}
	}
	return t, nil
}

// ── EventStore ─────────────────────────────────────────────────────────────────

type fakeEventStore struct{ s *fakeStore }

func (e *fakeEventStore) Put(_ context.Context, repoID string, events []model.RepoSessionEvents) error {
	e.s.events[repoID] = events
	return nil
}
func (e *fakeEventStore) Get(_ context.Context, repoID string) ([]model.RepoSessionEvents, error) {
	return e.s.events[repoID], nil
}
func (e *fakeEventStore) GetSession(_ context.Context, repoID, sessionID string) ([]model.SessionEvent, error) {
	for _, ev := range e.s.events[repoID] {
		if ev.ID == sessionID {
			return ev.Events, nil
		}
	}
	return nil, nil
}
func (e *fakeEventStore) GetDocs(_ context.Context, repoID string) (map[string]string, error) {
	return e.s.docs[repoID], nil
}
func (e *fakeEventStore) PutDocs(_ context.Context, repoID string, docs map[string]string) error {
	e.s.docs[repoID] = docs
	return nil
}

// ── TopicStore ─────────────────────────────────────────────────────────────────

type fakeTopicStore struct{ s *fakeStore }

func (t *fakeTopicStore) PutTopics(_ context.Context, repoID string, topics []model.TopicContext) error {
	t.s.topics[repoID] = topics
	return nil
}
func (t *fakeTopicStore) GetTopics(_ context.Context, repoID string) ([]model.TopicContext, error) {
	return t.s.topics[repoID], nil
}
func (t *fakeTopicStore) GetTopic(_ context.Context, repoID, topicID string) (*model.TopicContext, error) {
	for _, tc := range t.s.topics[repoID] {
		if tc.ID == topicID {
			cp := tc
			return &cp, nil
		}
	}
	return nil, nil
}
func (t *fakeTopicStore) PutFiles(_ context.Context, repoID string, files []model.FileSummary) error {
	for i := range files {
		cp := files[i]
		t.s.files[repoID+":"+files[i].Path] = &cp
	}
	return nil
}
func (t *fakeTopicStore) GetFile(_ context.Context, repoID, path string) (*model.FileSummary, error) {
	return t.s.files[repoID+":"+path], nil
}
func (t *fakeTopicStore) PutSearchData(_ context.Context, repoID string, data model.BundleSearchData) error {
	cp := data
	t.s.search[repoID] = &cp
	return nil
}
func (t *fakeTopicStore) GetSearchData(_ context.Context, repoID string) (*model.BundleSearchData, error) {
	return t.s.search[repoID], nil
}
func (t *fakeTopicStore) PutRepoContext(_ context.Context, repoID, content, bundleID string) error {
	t.s.repoCtx[repoID] = &model.RepoContextEntry{Content: content, BundleID: bundleID}
	return nil
}
func (t *fakeTopicStore) GetRepoContext(_ context.Context, repoID string) (*model.RepoContextEntry, error) {
	return t.s.repoCtx[repoID], nil
}

// ── FeedbackStore ──────────────────────────────────────────────────────────────

type fakeFeedbackStore struct{ s *fakeStore }

func (f *fakeFeedbackStore) CreateMCPCall(_ context.Context, call *model.MCPCall) (*model.MCPCall, error) {
	f.s.calls[call.CallID] = call
	return call, nil
}
func (f *fakeFeedbackStore) GetMCPCall(_ context.Context, callID string) (*model.MCPCall, error) {
	return f.s.calls[callID], nil
}
func (f *fakeFeedbackStore) PutFeedback(_ context.Context, fb *model.MCPFeedback) (*model.MCPFeedback, error) {
	if fb.FeedbackID == "" {
		fb.FeedbackID = "fb-" + fb.Task[:min(8, len(fb.Task))]
	}
	cp := *fb
	f.s.feedback[fb.FeedbackID] = &cp
	return &cp, nil
}
func (f *fakeFeedbackStore) GetFeedback(_ context.Context, id string) (*model.MCPFeedback, error) {
	return f.s.feedback[id], nil
}
func (f *fakeFeedbackStore) ListUnprocessedFeedback(_ context.Context, repoID string) ([]model.MCPFeedback, error) {
	var out []model.MCPFeedback
	for _, fb := range f.s.feedback {
		if fb.RepoID == repoID && fb.ProcessedAt == nil {
			out = append(out, *fb)
		}
	}
	return out, nil
}
func (f *fakeFeedbackStore) ListActionableFeedback(_ context.Context, repoID string) ([]model.MCPFeedback, error) {
	var out []model.MCPFeedback
	for _, fb := range f.s.feedback {
		if fb.RepoID == repoID {
			out = append(out, *fb)
		}
	}
	return out, nil
}
func (f *fakeFeedbackStore) ListFeedbackByIDs(_ context.Context, ids []string) ([]model.MCPFeedback, error) {
	var out []model.MCPFeedback
	for _, id := range ids {
		if fb := f.s.feedback[id]; fb != nil {
			out = append(out, *fb)
		}
	}
	return out, nil
}
func (f *fakeFeedbackStore) SetFeedbackClassification(_ context.Context, id string, c *model.FeedbackClassification) error {
	if fb := f.s.feedback[id]; fb != nil {
		fb.Classification = c
	}
	return nil
}
func (f *fakeFeedbackStore) ClaimFeedbackProcessingJob(_ context.Context, feedbackID, jobID string) (bool, error) {
	if fb := f.s.feedback[feedbackID]; fb != nil {
		if fb.ProcessedAt != nil || fb.ProcessingJobID != "" {
			return false, nil
		}
		fb.ProcessingJobID = jobID
		return true, nil
	}
	return false, nil
}
func (f *fakeFeedbackStore) ClearFeedbackProcessingJob(_ context.Context, feedbackID, jobID string) error {
	if fb := f.s.feedback[feedbackID]; fb != nil && fb.ProcessedAt == nil && fb.ProcessingJobID == jobID {
		fb.ProcessingJobID = ""
	}
	return nil
}
func (f *fakeFeedbackStore) MarkFeedbacksProcessed(_ context.Context, jobID string, ids []string) error {
	now := time.Now().UTC()
	for _, id := range ids {
		if fb := f.s.feedback[id]; fb != nil {
			fb.ProcessedAt = &now
			fb.ProcessingJobID = jobID
		}
	}
	return nil
}

// ── JobStore ───────────────────────────────────────────────────────────────────

type fakeJobStore struct{ s *fakeStore }

func (j *fakeJobStore) Enqueue(_ context.Context, job *model.ContextPatchJob) error {
	j.s.jobs = append(j.s.jobs, *job)
	j.s.jobState[job.JobID] = *job
	return nil
}
func (j *fakeJobStore) ClaimNext(_ context.Context, repoID string) (*model.ContextPatchJob, error) {
	return nil, nil
}
func (j *fakeJobStore) UpdateStatus(_ context.Context, jobID, status string, retryCount int) error {
	job := j.s.jobState[jobID]
	job.JobID = jobID
	job.Status = status
	job.RetryCount = retryCount
	j.s.jobState[jobID] = job
	return nil
}
func (j *fakeJobStore) ListPending(_ context.Context) ([]model.ContextPatchJob, error) {
	return j.s.jobs, nil
}
func (j *fakeJobStore) ResetRunning(_ context.Context) error { return nil }
func (j *fakeJobStore) ResetErrors(_ context.Context) error  { return nil }
func (j *fakeJobStore) MarkNewTopicDone(_ context.Context, jobID, topicID, topicName string) error {
	return nil
}

// ── Fake dispatcher ────────────────────────────────────────────────────────────

type fakeDispatcher struct{ jobs []string }

func (d *fakeDispatcher) Enqueue(jobType, repoID, topicID string) error {
	d.jobs = append(d.jobs, jobType+":"+repoID+":"+topicID)
	return nil
}

// ── Tests ──────────────────────────────────────────────────────────────────────

func newTestSvcs(s *fakeStore, disp contracts.JobDispatcher) *Services {
	return New(Deps{Store: s, Jobs: disp})
}

func TestRepoServiceCRUD(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	repo := &model.Repo{RepoID: "r1", RepoName: "test"}
	if err := svc.Repos.Upsert(ctx, repo); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Repos.Get(ctx, "r1")
	if err != nil || got == nil || got.RepoName != "test" {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}

	list, err := svc.Repos.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(list))
	}

	if err := svc.Repos.Delete(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.Repos.Get(ctx, "r1")
	if after != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestFeedbackServicePutFeedbackDoesNotEnqueueJobs(t *testing.T) {
	fs := newFakeStore()
	disp := &fakeDispatcher{}
	svc := newTestSvcs(fs, disp)
	ctx := context.Background()

	fb := &model.MCPFeedback{RepoID: "r1", Task: "add feature", Helpfulness: "high", Stars: 5}
	created, err := svc.Feedback.PutFeedback(ctx, fb)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || created.FeedbackID == "" {
		t.Fatalf("expected created feedback id, got %+v", created)
	}
	if len(disp.jobs) != 0 {
		t.Fatalf("expected no dispatched jobs, got %v", disp.jobs)
	}
}

func TestFeedbackServiceNoDispatcherIsNoop(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	fb := &model.MCPFeedback{RepoID: "r1", Task: "fix bug", Helpfulness: "low", Stars: 2}
	if _, err := svc.Feedback.PutFeedback(ctx, fb); err != nil {
		t.Fatal(err)
	}
}

func TestMCPServiceListTopics(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	_ = fs.Topics().PutTopics(ctx, "r1", []model.TopicContext{
		{ID: "t1", Name: "Auth"},
		{ID: "t2", Name: "Sessions"},
	})

	topics, err := svc.MCP.ListTopics(ctx, "r1")
	if err != nil || len(topics) != 2 {
		t.Fatalf("ListTopics: err=%v len=%d", err, len(topics))
	}
}

func TestMCPServiceGetTopicContextNotFound(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	got, err := svc.MCP.GetTopicContext(ctx, "r1", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for missing topic")
	}
}

func TestMCPServiceGetFileContextRoundTrip(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	_ = fs.Topics().PutFiles(ctx, "r1", []model.FileSummary{
		{Path: "pkg/auth.go", Classification: []string{"high_context"}},
	})

	fc, err := svc.MCP.GetFileContext(ctx, "r1", "pkg/auth.go")
	if err != nil || fc == nil || fc.Path != "pkg/auth.go" {
		t.Fatalf("GetFileContext: err=%v fc=%v", err, fc)
	}
}

func TestRenderTestContextIncludesSignalAndFiles(t *testing.T) {
	topic := &model.TopicContext{
		Name: "Auth Flow",
		Tests: model.TopicTests{
			Signal:    "tests as spec",
			StartWith: []string{"auth_test.go"},
			Notes:     []string{"run with -race"},
		},
		ImportantFiles: model.TopicImportantFiles{
			TestFiles: []string{"pkg/auth_test.go"},
		},
	}

	out := RenderTestContext(topic, []string{"pkg/auth.go"})
	if !strings.Contains(out, "tests as spec") {
		t.Errorf("missing signal in output: %q", out)
	}
	if !strings.Contains(out, "auth_test.go") {
		t.Errorf("missing start_with file: %q", out)
	}
	if !strings.Contains(out, "-race") {
		t.Errorf("missing note: %q", out)
	}
}

func TestRenderFileContextIncludesClassification(t *testing.T) {
	fc := &model.FileSummary{
		Path:             "backend/db/db.go",
		Classification:   []string{"high_context", "schema_sensitive"},
		ReadBeforeEditOf: []string{"backend/server/repo_handlers.go"},
		RelatedTests:     []string{"backend/db/db_test.go"},
	}

	out := RenderFileContext(fc)
	if !strings.Contains(out, "high_context") {
		t.Errorf("missing classification: %q", out)
	}
	if !strings.Contains(out, "repo_handlers.go") {
		t.Errorf("missing read_before_edit: %q", out)
	}
	if !strings.Contains(out, "db_test.go") {
		t.Errorf("missing related test: %q", out)
	}
}

func TestRenderSearchContextNoFriction(t *testing.T) {
	sc := &model.BundleSearchData{}
	out := RenderSearchContext("", sc)
	if !strings.Contains(out, "No search friction patterns recorded") {
		t.Errorf("expected no-friction message: %q", out)
	}
}

func TestRenderSearchContextSanitizesRegexMeta(t *testing.T) {
	sc := &model.BundleSearchData{
		SearchHeavyTargets: []model.BundleSearchTarget{
			// reQueryMeta strips \^$[]{}
			{Path: "pkg/parser.go", TopQueries: []string{"foo[bar]", "a{b}c"}},
		},
	}
	out := RenderSearchContext("t1", sc)
	if strings.Contains(out, "[") || strings.Contains(out, "{") {
		t.Errorf("regex meta chars not stripped: %q", out)
	}
}

func TestSessionServicePutAndGetEvents(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	events := []model.RepoSessionEvents{
		{ID: "s1", Agent: "claude", Events: []model.SessionEvent{{Index: 1, Kind: "prompt", Text: "hello"}}},
	}
	if err := svc.Sessions.PutEvents(ctx, "r1", events); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Sessions.GetEvents(ctx, "r1")
	if err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("GetEvents: err=%v got=%v", err, got)
	}
}

func TestFeedbackClassifyFailsWhenNotFound(t *testing.T) {
	fs := newFakeStore()
	svc := newTestSvcs(fs, nil)
	ctx := context.Background()

	err := svc.Feedback.ClassifyFeedback(ctx, "r1", "nonexistent")
	if !errors.Is(err, nil) && err == nil {
		t.Fatal("expected error")
	}
	// fb == nil case returns an error
	if err == nil {
		t.Fatal("expected error for missing feedback")
	}
}
