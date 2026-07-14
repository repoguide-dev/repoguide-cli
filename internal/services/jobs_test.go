package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/repoguide/repoguide-cli/internal/ai"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

// ── fakeLLM ───────────────────────────────────────────────────────────────────

type fakeLLM struct {
	curateResult *ai.TopicCuration
	curateErr    error
}

func (f *fakeLLM) CurateTopicContext(_ context.Context, _ *model.TopicContext, _ []*model.MCPFeedback, _ []model.TopicPatchSuggestion, _ []ai.TopicCurationSession) (*ai.TopicCuration, ai.Usage, error) {
	return f.curateResult, ai.Usage{}, f.curateErr
}
func (f *fakeLLM) DeriveTopicsAndGenerateContext(_ context.Context, _ contracts.RepoAnalysisBundle, _ map[string]string) ([]model.TopicContext, ai.Usage, error) {
	return nil, ai.Usage{}, nil
}
func (f *fakeLLM) GenerateRepoContext(_ context.Context, _ contracts.RepoAnalysisBundle, _ map[string]string) (string, ai.Usage, error) {
	return "", ai.Usage{}, nil
}
func (f *fakeLLM) GenerateTopicContextFromFeedback(_ context.Context, _, _ string, _ contracts.RepoAnalysisBundle, _ []model.SessionEvent, _ []model.TopicContext) (*model.TopicContext, string, ai.Usage, error) {
	return nil, "", ai.Usage{}, nil
}
func (f *fakeLLM) SelectTopic(_ context.Context, _ string, _ []ai.TopicSummary, _ string, _ []string, _, _ []ai.TopicRoutingExample) (ai.SelectTopicResult, ai.Usage, error) {
	return ai.SelectTopicResult{}, ai.Usage{}, nil
}
func (f *fakeLLM) SelectAdvice(_ context.Context, _ string, _ model.TopicContext, _ []ai.AdviceItem, _ ai.SelectionBudget, _, _ []ai.TopicRoutingExample, _ []model.MCPFeedback) (ai.AdviceSelectionResponse, ai.Usage, error) {
	return ai.AdviceSelectionResponse{}, ai.Usage{}, nil
}
func (f *fakeLLM) ClassifyFeedback(_ context.Context, _ *model.MCPFeedback) (*model.FeedbackClassification, ai.Usage, error) {
	return nil, ai.Usage{}, nil
}
func (f *fakeLLM) PatchRepoContext(_ context.Context, _ string, _ []*model.MCPFeedback, _ []ai.RepoContextSession, _ bool) (*ai.RepoContextPatch, ai.Usage, error) {
	return nil, ai.Usage{}, nil
}

// ── fakeSuggestionStoreWithState ─────────────────────────────────────────────

type trackingSuggestionStore struct {
	saved    []model.TopicPatchSuggestion
	pending  []model.TopicPatchSuggestion
	statuses map[string]string // suggestion_id → final status
	evidence map[string][]string
}

func newTrackingStore(pending []model.TopicPatchSuggestion) *trackingSuggestionStore {
	return &trackingSuggestionStore{
		pending:  pending,
		statuses: map[string]string{},
		evidence: map[string][]string{},
	}
}

func (s *trackingSuggestionStore) Save(_ context.Context, _ string, suggestions []model.TopicPatchSuggestion) error {
	s.saved = append(s.saved, suggestions...)
	return nil
}
func (s *trackingSuggestionStore) ListPending(_ context.Context, _, _ string) ([]model.TopicPatchSuggestion, error) {
	return s.pending, nil
}
func (s *trackingSuggestionStore) GetByID(_ context.Context, id string) (*model.TopicPatchSuggestion, error) {
	for i := range s.pending {
		if s.pending[i].SuggestionID == id {
			return &s.pending[i], nil
		}
	}
	return nil, nil
}
func (s *trackingSuggestionStore) UpdateStatus(_ context.Context, id, status string, _ int, evidence []string) error {
	s.statuses[id] = status
	s.evidence[id] = append([]string(nil), evidence...)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func jsonVal(v string) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func newJobSvc(t *testing.T, llm *fakeLLM, sugg storecontract.SuggestionStore, topicStore storecontract.TopicStore) *JobService {
	t.Helper()
	fs := newFakeStore()
	return &JobService{
		store: &storeWithSuggestions{fakeStore: fs, sugg: sugg, topics: topicStore},
		ai:    llm,
	}
}

// storeWithSuggestions wraps fakeStore but overrides Suggestions and Topics.
type storeWithSuggestions struct {
	*fakeStore
	sugg   storecontract.SuggestionStore
	topics storecontract.TopicStore
}

func (s *storeWithSuggestions) Suggestions() storecontract.SuggestionStore { return s.sugg }
func (s *storeWithSuggestions) Topics() storecontract.TopicStore {
	if s.topics != nil {
		return s.topics
	}
	return s.fakeStore.Topics()
}

// ── tests ─────────────────────────────────────────────────────────────────────

func setupTopicStore(repoID string, topics ...model.TopicContext) *fakeTopicStore {
	fs := newFakeStore()
	fs.topics[repoID] = topics
	return &fakeTopicStore{s: fs}
}

func setupFeedback(fs *fakeStore, ids ...string) {
	for _, id := range ids {
		fs.feedback[id] = &model.MCPFeedback{FeedbackID: id, RepoID: "r1"}
	}
}

func TestPatchTopicKeepsFirstSessionConfidence5Pending(t *testing.T) {
	ft := setupTopicStore("r1", model.TopicContext{ID: "t1", Name: "T1"})
	setupFeedback(ft.s, "fb1")

	sugg := newTrackingStore(nil)
	llm := &fakeLLM{curateResult: &ai.TopicCuration{
		TopicID: "t1",
		NewSuggestions: []ai.TopicCurationSuggestion{
			{Kind: "add_when_to_use", Value: jsonVal("ci pipeline"), Confidence: 5},
		},
	}}

	svc := newJobSvc(t, llm, sugg, ft)
	if err := svc.patchTopic(context.Background(), &model.ContextPatchJob{
		RepoID: "r1", TopicID: "t1", FeedbackIDs: []string{"fb1"},
	}); err != nil {
		t.Fatal(err)
	}

	if len(sugg.saved) != 1 {
		t.Fatalf("want 1 saved suggestion, got %d", len(sugg.saved))
	}
	if sugg.saved[0].Status != model.TopicPatchSuggestionPending {
		t.Errorf("want status pending, got %s", sugg.saved[0].Status)
	}
	if contains(ft.s.topics["r1"][0].WhenToUse, "ci pipeline") {
		t.Errorf("first-session candidate must not update topic: WhenToUse=%v", ft.s.topics["r1"][0].WhenToUse)
	}
}

func TestPatchTopicKeepsPendingBelowConfidence5(t *testing.T) {
	ft := setupTopicStore("r1", model.TopicContext{ID: "t1", Name: "T1"})
	setupFeedback(ft.s, "fb1")

	sugg := newTrackingStore(nil)
	llm := &fakeLLM{curateResult: &ai.TopicCuration{
		TopicID: "t1",
		NewSuggestions: []ai.TopicCurationSuggestion{
			{Kind: "add_when_to_use", Value: jsonVal("maybe"), Confidence: 3},
		},
	}}

	svc := newJobSvc(t, llm, sugg, ft)
	_ = svc.patchTopic(context.Background(), &model.ContextPatchJob{
		RepoID: "r1", TopicID: "t1", FeedbackIDs: []string{"fb1"},
	})

	if len(sugg.saved) != 1 || sugg.saved[0].Status != model.TopicPatchSuggestionPending {
		t.Errorf("want 1 pending suggestion, got %+v", sugg.saved)
	}
	// topic must not be rewritten when nothing was applied
	if contains(ft.s.topics["r1"][0].WhenToUse, "maybe") {
		t.Error("topic should not be updated for pending-only suggestions")
	}
}

func TestPatchTopicAcceptDecisionAppliesAndMarksApplied(t *testing.T) {
	ft := setupTopicStore("r1", model.TopicContext{ID: "t1", Name: "T1"})
	setupFeedback(ft.s, "fb1")

	existing := model.TopicPatchSuggestion{
		SuggestionID: "sug1", TopicID: "t1",
		Status: model.TopicPatchSuggestionPending,
		Kind:   "add_when_to_use", Value: jsonVal("debug flow"),
	}
	sugg := newTrackingStore([]model.TopicPatchSuggestion{existing})
	llm := &fakeLLM{curateResult: &ai.TopicCuration{
		TopicID: "t1",
		SuggestionDecisions: []ai.TopicSuggestionDecision{
			{SuggestionID: "sug1", Decision: "accept", Confidence: 5, SupportedByFeedbackIDs: []string{"fb1"}},
		},
	}}

	svc := newJobSvc(t, llm, sugg, ft)
	if err := svc.patchTopic(context.Background(), &model.ContextPatchJob{
		RepoID: "r1", TopicID: "t1", FeedbackIDs: []string{"fb1"},
	}); err != nil {
		t.Fatal(err)
	}

	if sugg.statuses["sug1"] != model.TopicPatchSuggestionApplied {
		t.Errorf("want status applied, got %s", sugg.statuses["sug1"])
	}
	if !contains(ft.s.topics["r1"][0].WhenToUse, "debug flow") {
		t.Errorf("topic not updated: %v", ft.s.topics["r1"][0].WhenToUse)
	}
	if !contains(sugg.evidence["sug1"], "fb1") {
		t.Errorf("corroborating evidence not persisted: %v", sugg.evidence["sug1"])
	}
}

func TestPatchTopicAcceptDecisionWithoutNewEvidenceStaysPending(t *testing.T) {
	ft := setupTopicStore("r1", model.TopicContext{ID: "t1", Name: "T1"})
	setupFeedback(ft.s, "fb1")

	existing := model.TopicPatchSuggestion{
		SuggestionID: "sug1", TopicID: "t1",
		Status: model.TopicPatchSuggestionPending,
		Kind:   "add_when_to_use", Value: jsonVal("debug flow"),
	}
	sugg := newTrackingStore([]model.TopicPatchSuggestion{existing})
	llm := &fakeLLM{curateResult: &ai.TopicCuration{
		TopicID: "t1",
		SuggestionDecisions: []ai.TopicSuggestionDecision{
			{SuggestionID: "sug1", Decision: "accept", Confidence: 5},
		},
	}}

	svc := newJobSvc(t, llm, sugg, ft)
	if err := svc.patchTopic(context.Background(), &model.ContextPatchJob{
		RepoID: "r1", TopicID: "t1", FeedbackIDs: []string{"fb1"},
	}); err != nil {
		t.Fatal(err)
	}

	if got := sugg.statuses["sug1"]; got != "" {
		t.Errorf("candidate status changed without new evidence: %q", got)
	}
	if contains(ft.s.topics["r1"][0].WhenToUse, "debug flow") {
		t.Error("candidate applied without new evidence")
	}
}

func TestPatchTopicRejectDecisionMarksRejectedNoApply(t *testing.T) {
	ft := setupTopicStore("r1", model.TopicContext{ID: "t1", Name: "T1"})
	setupFeedback(ft.s, "fb1")

	existing := model.TopicPatchSuggestion{
		SuggestionID: "sug2", TopicID: "t1",
		Status: model.TopicPatchSuggestionPending,
		Kind:   "add_when_to_use", Value: jsonVal("wrong thing"),
	}
	sugg := newTrackingStore([]model.TopicPatchSuggestion{existing})
	llm := &fakeLLM{curateResult: &ai.TopicCuration{
		TopicID: "t1",
		SuggestionDecisions: []ai.TopicSuggestionDecision{
			{SuggestionID: "sug2", Decision: "reject", Confidence: 1},
		},
	}}

	svc := newJobSvc(t, llm, sugg, ft)
	_ = svc.patchTopic(context.Background(), &model.ContextPatchJob{
		RepoID: "r1", TopicID: "t1", FeedbackIDs: []string{"fb1"},
	})

	if sugg.statuses["sug2"] != model.TopicPatchSuggestionRejected {
		t.Errorf("want status rejected, got %s", sugg.statuses["sug2"])
	}
	if contains(ft.s.topics["r1"][0].WhenToUse, "wrong thing") {
		t.Error("rejected suggestion should not be applied")
	}
}

func TestExecuteClassifyMarksMissingFeedbackIDAsError(t *testing.T) {
	fs := newFakeStore()
	svcs := newTestSvcs(fs, nil)

	job := &model.ContextPatchJob{
		JobID:   "job-1",
		RepoID:  "r1",
		Type:    model.PatchJobTypeClassifyFeedback,
		Status:  model.PatchJobStatusQueued,
		TopicID: "t1",
	}
	fs.jobState[job.JobID] = *job

	if err := svcs.Jobs.executeClassify(context.Background(), job); err != nil {
		t.Fatalf("executeClassify returned err: %v", err)
	}

	got := fs.jobState[job.JobID]
	if got.Status != model.PatchJobStatusError {
		t.Fatalf("job status = %q, want %q", got.Status, model.PatchJobStatusError)
	}
	if got.RetryCount != 0 {
		t.Fatalf("job retry_count = %d, want 0", got.RetryCount)
	}
}

func TestEnqueueStartupFollowups(t *testing.T) {
	fs := newFakeStore()
	fs.repos["r1"] = &model.Repo{RepoID: "r1"}
	fs.feedback["fb1"] = &model.MCPFeedback{
		FeedbackID: "fb1",
		RepoID:     "r1",
		TopicID:    "t1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchTopic,
		},
	}
	fs.feedback["fb2"] = &model.MCPFeedback{
		FeedbackID: "fb2",
		RepoID:     "r1",
		TopicID:    "t1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchTopic,
		},
		Stars: 2,
	}
	fs.feedback["fb3"] = &model.MCPFeedback{
		FeedbackID: "fb3",
		RepoID:     "r1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchRepo,
		},
		Stars: 1,
	}
	fs.feedback["fb4"] = &model.MCPFeedback{
		FeedbackID: "fb4",
		RepoID:     "r1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchRepo,
		},
		Stars: 3,
	}
	fs.feedback["fb5"] = &model.MCPFeedback{
		FeedbackID: "fb5",
		RepoID:     "r1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchRepo,
		},
		Stars: 2,
	}
	fs.feedback["fb6"] = &model.MCPFeedback{
		FeedbackID: "fb6",
		RepoID:     "r1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchRepo,
		},
		Stars: 1,
	}
	fs.feedback["fb_new"] = &model.MCPFeedback{
		FeedbackID: "fb_new",
		RepoID:     "r1",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindTopicCandidate,
		},
	}
	fs.feedback["fb_done"] = &model.MCPFeedback{
		FeedbackID: "fb_done",
		RepoID:     "r1",
		TopicID:    "t2",
		Classification: &model.FeedbackClassification{
			Kind: model.FeedbackKindPatchTopic,
		},
		ProcessedAt: func() *time.Time {
			now := time.Now().UTC()
			return &now
		}(),
	}
	svc := &JobService{store: fs}
	svc.EnqueueStartupFollowups(context.Background())

	if len(fs.jobs) != 3 {
		t.Fatalf("expected 3 startup jobs, got %d", len(fs.jobs))
	}
	var gotTopicPatch, gotRepoPatch, gotNewTopic bool
	for _, job := range fs.jobs {
		switch job.Type {
		case model.PatchJobTypeTopicPatch:
			gotTopicPatch = job.TopicID == "t1"
		case model.PatchJobTypeRepoPatch:
			gotRepoPatch = true
		case model.PatchJobTypeNewTopic:
			gotNewTopic = len(job.FeedbackIDs) == 1 && job.FeedbackIDs[0] == "fb_new"
		}
	}
	if !gotTopicPatch || !gotRepoPatch || !gotNewTopic {
		t.Fatalf("startup jobs missing expected coverage: %+v", fs.jobs)
	}
}

func contains(slice []string, v string) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}
