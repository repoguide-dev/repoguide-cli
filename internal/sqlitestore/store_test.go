package sqlitestore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/repoguide/repoguide-core/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "repoguide-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	st, err := Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRepos(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	repo := &model.Repo{RepoID: "r1", RepoRoot: "/tmp/r1", RepoName: "r1", ActivatedAt: time.Now()}
	if err := st.Repos().Upsert(ctx, repo); err != nil {
		t.Fatal(err)
	}

	got, err := st.Repos().Get(ctx, "r1")
	if err != nil || got == nil || got.RepoName != "r1" {
		t.Fatalf("Get: %v %v", got, err)
	}

	list, err := st.Repos().List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %v", list, err)
	}

	if err := st.Repos().Delete(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	after, _ := st.Repos().Get(ctx, "r1")
	if after != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestEvents(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	events := []model.RepoSessionEvents{
		{ID: "s1", UpdatedAt: time.Now(), Events: []model.SessionEvent{{Kind: "prompt", Text: "hello"}}},
	}
	if err := st.Events().Put(ctx, "r1", events); err != nil {
		t.Fatal(err)
	}

	got, err := st.Events().Get(ctx, "r1")
	if err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("Get events: %v %v", got, err)
	}

	sess, err := st.Events().GetSession(ctx, "r1", "s1")
	if err != nil || len(sess) != 1 {
		t.Fatalf("GetSession: %v %v", sess, err)
	}
}

func TestTopics(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	topics := []model.TopicContext{{ID: "t1", Name: "Alpha"}}
	if err := st.Topics().PutTopics(ctx, "r1", topics); err != nil {
		t.Fatal(err)
	}

	got, err := st.Topics().GetTopics(ctx, "r1")
	if err != nil || len(got) != 1 || got[0].Name != "Alpha" {
		t.Fatalf("GetTopics: %v %v", got, err)
	}

	topic, err := st.Topics().GetTopic(ctx, "r1", "t1")
	if err != nil || topic == nil || topic.Name != "Alpha" {
		t.Fatalf("GetTopic: %v %v", topic, err)
	}
}

func TestFiles(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	files := []model.FileSummary{{Path: "main.go", Classification: []string{"entry_point"}}}
	if err := st.Topics().PutFiles(ctx, "r1", files); err != nil {
		t.Fatal(err)
	}

	got, err := st.Topics().GetFile(ctx, "r1", "main.go")
	if err != nil || got == nil || len(got.Classification) == 0 {
		t.Fatalf("GetFile: %v %v", got, err)
	}
}

func TestJobs(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	job := &model.ContextPatchJob{JobID: "j1", RepoID: "r1", Type: model.PatchJobTypeTopicPatch, TopicID: "t1"}
	if err := st.Jobs().Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}

	pending, err := st.Jobs().ListPending(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending: %v %v", pending, err)
	}

	claimed, err := st.Jobs().ClaimNext(ctx, "r1")
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext: %v %v", claimed, err)
	}

	if err := st.Jobs().UpdateStatus(ctx, "j1", model.PatchJobStatusDone, 0); err != nil {
		t.Fatal(err)
	}

	after, _ := st.Jobs().ListPending(ctx)
	if len(after) != 0 {
		t.Fatal("expected no pending jobs after done")
	}
}

func TestClassifyJobsPreserveRetryCountAcrossListAndClaim(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	job := &model.ContextPatchJob{
		JobID:       "j1",
		RepoID:      "r1",
		Type:        model.PatchJobTypeClassifyFeedback,
		FeedbackIDs: []string{"fb1"},
	}
	if err := st.ClassifyJobs().Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := st.ClassifyJobs().UpdateStatus(ctx, "j1", model.PatchJobStatusRetry, 2); err != nil {
		t.Fatal(err)
	}

	pending, err := st.ClassifyJobs().ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("ListPending len = %d, want 1", len(pending))
	}
	if pending[0].RetryCount != 2 {
		t.Fatalf("pending retry_count = %d, want 2", pending[0].RetryCount)
	}
	if pending[0].Status != model.PatchJobStatusRetry {
		t.Fatalf("pending status = %q, want %q", pending[0].Status, model.PatchJobStatusRetry)
	}

	claimed, err := st.ClassifyJobs().ClaimNext(ctx, "r1")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNext returned nil job")
	}
	if claimed.RetryCount != 2 {
		t.Fatalf("claimed retry_count = %d, want 2", claimed.RetryCount)
	}
	if claimed.Status != model.PatchJobStatusRetry {
		t.Fatalf("claimed status = %q, want %q", claimed.Status, model.PatchJobStatusRetry)
	}
}

func TestFeedback(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	fb := &model.MCPFeedback{RepoID: "r1", Stars: 4, Helpfulness: "high"}
	saved, err := st.Feedback().PutFeedback(ctx, fb)
	if err != nil || saved.FeedbackID == "" {
		t.Fatalf("PutFeedback: %v %v", saved, err)
	}

	got, err := st.Feedback().GetFeedback(ctx, saved.FeedbackID)
	if err != nil || got == nil || got.Stars != 4 {
		t.Fatalf("GetFeedback: %v %v", got, err)
	}

	unproc, err := st.Feedback().ListUnprocessedFeedback(ctx, "r1")
	if err != nil || len(unproc) != 1 {
		t.Fatalf("ListUnprocessed: %v %v", unproc, err)
	}
}

func TestActionableFeedbackReturnsNewestFirst(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	for _, feedback := range []*model.MCPFeedback{
		{FeedbackID: "older", RepoID: "r1", Task: "old", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{FeedbackID: "newer", RepoID: "r1", Task: "new", CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if _, err := st.Feedback().PutFeedback(ctx, feedback); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Feedback().ListActionableFeedback(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].FeedbackID != "newer" || got[1].FeedbackID != "older" {
		t.Fatalf("feedback order = %#v", got)
	}
}

func TestFeedbackProcessingClaimLifecycle(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	fb, err := st.Feedback().PutFeedback(ctx, &model.MCPFeedback{RepoID: "r1", Helpfulness: "low"})
	if err != nil {
		t.Fatalf("PutFeedback: %v", err)
	}

	claimed, err := st.Feedback().ClaimFeedbackProcessingJob(ctx, fb.FeedbackID, "job-1")
	if err != nil || !claimed {
		t.Fatalf("ClaimFeedbackProcessingJob: claimed=%v err=%v", claimed, err)
	}

	claimed, err = st.Feedback().ClaimFeedbackProcessingJob(ctx, fb.FeedbackID, "job-2")
	if err != nil || claimed {
		t.Fatalf("second ClaimFeedbackProcessingJob: claimed=%v err=%v", claimed, err)
	}

	got, err := st.Feedback().GetFeedback(ctx, fb.FeedbackID)
	if err != nil || got == nil || got.ProcessingJobID != "job-1" {
		t.Fatalf("GetFeedback after claim: %#v err=%v", got, err)
	}

	if err := st.Feedback().ClearFeedbackProcessingJob(ctx, fb.FeedbackID, "job-2"); err != nil {
		t.Fatalf("ClearFeedbackProcessingJob wrong job: %v", err)
	}
	got, _ = st.Feedback().GetFeedback(ctx, fb.FeedbackID)
	if got.ProcessingJobID != "job-1" {
		t.Fatalf("processing job cleared by wrong job id: %#v", got)
	}

	if err := st.Feedback().ClearFeedbackProcessingJob(ctx, fb.FeedbackID, "job-1"); err != nil {
		t.Fatalf("ClearFeedbackProcessingJob: %v", err)
	}
	got, _ = st.Feedback().GetFeedback(ctx, fb.FeedbackID)
	if got.ProcessingJobID != "" {
		t.Fatalf("processing job not cleared: %#v", got)
	}
}
