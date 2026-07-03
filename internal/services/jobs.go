package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/repoguide/repoguide-cli/internal/ai"
	"github.com/repoguide/repoguide-cli/internal/analysis"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

type JobService struct {
	store    storecontract.Store
	ai       ai.LLM
	services *Services
}

// DrainPending claims and executes all pending patch jobs (not classify).
func (s *JobService) DrainPending(ctx context.Context) {
	s.drainFromStore(ctx, s.store.Jobs(), s.execute)
}

// DrainClassifyPending claims and executes all pending feedback classification jobs.
func (s *JobService) DrainClassifyPending(ctx context.Context) {
	s.drainFromStore(ctx, s.store.ClassifyJobs(), s.executeClassify)
}

// RunStartupMaintenance executes the local/background startup pipeline in the
// same order as the backend flow: classify feedback, derive follow-up patch
// jobs from classified-but-unprocessed feedback, then drain patch jobs.
func (s *JobService) RunStartupMaintenance(ctx context.Context) {
	_ = s.store.Jobs().ResetRunning(ctx)
	_ = s.store.Jobs().ResetErrors(ctx)
	_ = s.store.ClassifyJobs().ResetRunning(ctx)
	_ = s.store.ClassifyJobs().ResetErrors(ctx)
	s.EnqueuePendingClassification(ctx)
	s.DrainClassifyPending(ctx)
	s.EnqueueStartupFollowups(ctx)
	s.DrainPending(ctx)
}

// EnqueuePendingClassification scans local repos for unclassified feedback that
// is not already covered by a queued or running classify job.
func (s *JobService) EnqueuePendingClassification(ctx context.Context) {
	repos, err := s.store.Repos().List(ctx)
	if err != nil {
		slog.Warn("enqueue classify pending: list repos failed", "err", err)
		return
	}
	pending, err := s.store.ClassifyJobs().ListPending(ctx)
	if err != nil {
		slog.Warn("enqueue classify pending: list jobs failed", "err", err)
		return
	}
	active := map[string]bool{}
	for _, job := range pending {
		for _, feedbackID := range job.FeedbackIDs {
			active[repoFeedbackKey(job.RepoID, feedbackID)] = true
		}
	}
	for _, repo := range repos {
		feedbacks, err := s.store.Feedback().ListUnprocessedFeedback(ctx, repo.RepoID)
		if err != nil {
			slog.Warn("enqueue classify pending: list feedback failed", "repo_id", repo.RepoID, "err", err)
			continue
		}
		for _, fb := range feedbacks {
			if fb.Classification != nil {
				continue
			}
			key := repoFeedbackKey(repo.RepoID, fb.FeedbackID)
			if active[key] {
				continue
			}
			job := &model.ContextPatchJob{
				JobID:       randID(),
				RepoID:      repo.RepoID,
				Type:        model.PatchJobTypeClassifyFeedback,
				FeedbackIDs: []string{fb.FeedbackID},
				Status:      model.PatchJobStatusQueued,
				Timestamp:   time.Now().UTC(),
			}
			if err := s.store.ClassifyJobs().Enqueue(ctx, job); err != nil {
				slog.Warn("enqueue classify pending: enqueue failed", "repo_id", repo.RepoID, "feedback_id", fb.FeedbackID, "err", err)
			} else {
				active[key] = true
			}
		}
	}
}

// EnqueueStartupFollowups scans local repos for already-classified feedback that
// still lacks a follow-up patch job, then enqueues the supported patch jobs.
func (s *JobService) EnqueueStartupFollowups(ctx context.Context) {
	repos, err := s.store.Repos().List(ctx)
	if err != nil {
		slog.Warn("enqueue startup followups: list repos failed", "err", err)
		return
	}
	pending, err := s.store.Jobs().ListPending(ctx)
	if err != nil {
		slog.Warn("enqueue startup followups: list jobs failed", "err", err)
		return
	}

	type topicCandidate struct {
		topicID string
		ids     []string
	}
	activeRepoPatch := map[string]bool{}
	activeTopicPatch := map[string]bool{}
	for _, job := range pending {
		switch job.Type {
		case model.PatchJobTypeRepoPatch:
			activeRepoPatch[job.RepoID] = true
		case model.PatchJobTypeTopicPatch:
			activeTopicPatch[job.RepoID+"\x00"+job.TopicID] = true
		}
	}

	for _, repo := range repos {
		feedbacks, err := s.store.Feedback().ListActionableFeedback(ctx, repo.RepoID)
		if err != nil {
			slog.Warn("enqueue startup followups: list feedback failed", "repo_id", repo.RepoID, "err", err)
			continue
		}

		bestTopic := topicCandidate{}
		byTopic := map[string][]string{}
		lowStarCount := 0
		var allUnprocessedIDs []string

		for _, fb := range feedbacks {
			if fb.Classification == nil || fb.ProcessedAt != nil {
				continue
			}
			allUnprocessedIDs = append(allUnprocessedIDs, fb.FeedbackID)

			if isNewTopicClassification(fb.Classification) {
				if fb.ProcessingJobID != "" {
					continue
				}
				job := &model.ContextPatchJob{
					JobID:       randID(),
					RepoID:      repo.RepoID,
					Type:        model.PatchJobTypeNewTopic,
					FeedbackIDs: []string{fb.FeedbackID},
					Status:      model.PatchJobStatusQueued,
					Timestamp:   time.Now().UTC(),
				}
				claimed, err := s.store.Feedback().ClaimFeedbackProcessingJob(ctx, fb.FeedbackID, job.JobID)
				if err != nil {
					slog.Warn("enqueue startup followups: claim new topic feedback failed", "repo_id", repo.RepoID, "feedback_id", fb.FeedbackID, "err", err)
				} else if !claimed {
					continue
				} else if err := s.store.Jobs().Enqueue(ctx, job); err != nil {
					_ = s.store.Feedback().ClearFeedbackProcessingJob(ctx, fb.FeedbackID, job.JobID)
					slog.Warn("enqueue startup followups: enqueue new topic failed", "repo_id", repo.RepoID, "feedback_id", fb.FeedbackID, "err", err)
				}
			}

			if fb.Stars >= 1 && fb.Stars <= 3 {
				lowStarCount++
			}
			if fb.Classification.Kind == model.FeedbackKindPatchTopic && fb.TopicID != "" {
				byTopic[fb.TopicID] = append(byTopic[fb.TopicID], fb.FeedbackID)
			}
		}

		for topicID, ids := range byTopic {
			if len(ids) > len(bestTopic.ids) || (len(ids) == len(bestTopic.ids) && (bestTopic.topicID == "" || topicID < bestTopic.topicID)) {
				bestTopic = topicCandidate{topicID: topicID, ids: ids}
			}
		}

		if bestTopic.topicID != "" && !activeTopicPatch[repo.RepoID+"\x00"+bestTopic.topicID] {
			job := &model.ContextPatchJob{
				JobID:       randID(),
				RepoID:      repo.RepoID,
				Type:        model.PatchJobTypeTopicPatch,
				TopicID:     bestTopic.topicID,
				FeedbackIDs: bestTopic.ids,
				Status:      model.PatchJobStatusQueued,
				Timestamp:   time.Now().UTC(),
			}
			if err := s.store.Jobs().Enqueue(ctx, job); err != nil {
				slog.Warn("enqueue startup followups: enqueue topic patch failed", "repo_id", repo.RepoID, "topic_id", bestTopic.topicID, "err", err)
			}
		}

		if lowStarCount >= 5 && len(allUnprocessedIDs) > 0 && !activeRepoPatch[repo.RepoID] {
			job := &model.ContextPatchJob{
				JobID:       randID(),
				RepoID:      repo.RepoID,
				Type:        model.PatchJobTypeRepoPatch,
				FeedbackIDs: allUnprocessedIDs,
				Status:      model.PatchJobStatusQueued,
				Timestamp:   time.Now().UTC(),
			}
			if err := s.store.Jobs().Enqueue(ctx, job); err != nil {
				slog.Warn("enqueue startup followups: enqueue repo patch failed", "repo_id", repo.RepoID, "err", err)
			}
		}
	}
}

func isNewTopicClassification(classification *model.FeedbackClassification) bool {
	return classification != nil && classification.Kind == model.FeedbackKindTopicCandidate
}

func repoFeedbackKey(repoID, feedbackID string) string {
	return repoID + "\x00" + feedbackID
}

func (s *JobService) drainFromStore(ctx context.Context, js interface {
	ListPending(context.Context) ([]model.ContextPatchJob, error)
	UpdateStatus(context.Context, string, string, int) error
}, run func(context.Context, *model.ContextPatchJob) error) {
	jobs, err := js.ListPending(ctx)
	if err != nil {
		slog.Warn("drainFromStore: list jobs failed", "err", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	slog.Info("drainFromStore: processing jobs", "count", len(jobs))
	for _, j := range jobs {
		if err := run(ctx, &j); err != nil {
			retryCount := j.RetryCount + 1
			status := model.PatchJobStatusRetry
			if retryCount >= model.PatchJobMaxRetries {
				status = model.PatchJobStatusError
			}
			slog.Warn(
				"job failed",
				"job_id", j.JobID,
				"type", j.Type,
				"repo_id", j.RepoID,
				"topic_id", j.TopicID,
				"feedback_ids", j.FeedbackIDs,
				"retry_count", j.RetryCount,
				"next_status", status,
				"next_retry_count", retryCount,
				"err", err,
			)
			_ = js.UpdateStatus(ctx, j.JobID, status, retryCount)
		}
	}
}

func (s *JobService) execute(ctx context.Context, j *model.ContextPatchJob) error {
	slog.Info(
		"job starting",
		"job_id", j.JobID,
		"type", j.Type,
		"repo_id", j.RepoID,
		"topic_id", j.TopicID,
		"feedback_ids", j.FeedbackIDs,
		"retry_count", j.RetryCount,
	)
	if err := s.store.Jobs().UpdateStatus(ctx, j.JobID, model.PatchJobStatusRunning, j.RetryCount); err != nil {
		return err
	}
	var (
		err      error
		newTopic model.TopicContext
	)
	switch j.Type {
	case model.PatchJobTypeNewTopic:
		newTopic, err = s.createNewTopic(ctx, j)
	case model.PatchJobTypeTopicPatch:
		err = s.patchTopic(ctx, j)
	case model.PatchJobTypeRepoPatch:
		err = s.patchRepo(ctx, j)
	default:
		slog.Warn(
			"job type unsupported by generic worker",
			"job_id", j.JobID,
			"type", j.Type,
			"repo_id", j.RepoID,
			"topic_id", j.TopicID,
			"feedback_ids", j.FeedbackIDs,
			"expected_types", []string{model.PatchJobTypeTopicPatch, model.PatchJobTypeRepoPatch},
		)
		err = fmt.Errorf("unknown job type %q", j.Type)
	}
	if err != nil {
		return err
	}
	if j.Type == model.PatchJobTypeNewTopic {
		if err := s.store.Jobs().MarkNewTopicDone(ctx, j.JobID, newTopic.ID, newTopic.Name); err != nil {
			return err
		}
		slog.Info("job done", "job_id", j.JobID, "type", j.Type, "new_topic_id", newTopic.ID)
		return nil
	}
	if err := s.store.Jobs().UpdateStatus(ctx, j.JobID, model.PatchJobStatusDone, j.RetryCount); err != nil {
		return err
	}
	slog.Info("job done", "job_id", j.JobID, "type", j.Type)
	return nil
}

func (s *JobService) executeClassify(ctx context.Context, j *model.ContextPatchJob) error {
	slog.Info(
		"classify job starting",
		"job_id", j.JobID,
		"repo_id", j.RepoID,
		"topic_id", j.TopicID,
		"feedback_ids", j.FeedbackIDs,
		"retry_count", j.RetryCount,
	)
	if err := s.store.ClassifyJobs().UpdateStatus(ctx, j.JobID, model.PatchJobStatusRunning, j.RetryCount); err != nil {
		return err
	}
	feedbackID := ""
	if len(j.FeedbackIDs) > 0 {
		feedbackID = j.FeedbackIDs[0]
	}
	if feedbackID == "" {
		slog.Warn(
			"classify job missing feedback ID",
			"job_id", j.JobID,
			"repo_id", j.RepoID,
			"topic_id", j.TopicID,
			"feedback_ids", j.FeedbackIDs,
		)
		return s.store.ClassifyJobs().UpdateStatus(ctx, j.JobID, model.PatchJobStatusError, j.RetryCount)
	}
	if err := s.services.Feedback.ClassifyFeedback(ctx, j.RepoID, feedbackID); err != nil {
		slog.Warn(
			"classify feedback call failed",
			"job_id", j.JobID,
			"repo_id", j.RepoID,
			"feedback_id", feedbackID,
			"err", err,
		)
		return err
	}
	if err := s.store.ClassifyJobs().UpdateStatus(ctx, j.JobID, model.PatchJobStatusDone, j.RetryCount); err != nil {
		return err
	}
	slog.Info("classify job done", "job_id", j.JobID, "feedback_id", feedbackID)
	return nil
}

func (s *JobService) patchTopic(ctx context.Context, j *model.ContextPatchJob) error {
	topic, err := s.store.Topics().GetTopic(ctx, j.RepoID, j.TopicID)
	if err != nil || topic == nil {
		return fmt.Errorf("topic %s not found", j.TopicID)
	}
	feedbacks, err := s.store.Feedback().ListFeedbackByIDs(ctx, j.FeedbackIDs)
	if err != nil {
		return err
	}
	fbPtrs := make([]*model.MCPFeedback, len(feedbacks))
	for i := range feedbacks {
		fbPtrs[i] = &feedbacks[i]
	}
	pending, err := s.store.Suggestions().ListPending(ctx, j.RepoID, j.TopicID)
	if err != nil {
		slog.Warn("patchTopic: list pending suggestions failed (continuing)", "err", err)
	}

	curation, _, err := s.ai.CurateTopicContext(ctx, topic, fbPtrs, pending)
	if err != nil {
		return err
	}

	// Persist new suggestions; auto-accept confidence==5.
	now := time.Now().UTC()
	var toApply []ai.TopicCurationSuggestion
	var toSave []model.TopicPatchSuggestion
	for _, ns := range curation.NewSuggestions {
		status := model.TopicPatchSuggestionPending
		if ns.Confidence >= 5 {
			status = model.TopicPatchSuggestionApplied
			toApply = append(toApply, ns)
		}
		toSave = append(toSave, model.TopicPatchSuggestion{
			SuggestionID:        randID(),
			RepoID:              j.RepoID,
			TopicID:             j.TopicID,
			Status:              status,
			CreatedAt:           now,
			UpdatedAt:           now,
			Kind:                ns.Kind,
			TargetField:         ns.TargetField,
			Path:                ns.Path,
			Value:               ns.Value,
			Claim:               ns.Claim,
			EvidenceFeedbackIDs: ns.EvidenceFeedbackIDs,
			Confidence:          ns.Confidence,
			Reason:              ns.Reason,
		})
	}
	if len(toSave) > 0 {
		if err := s.store.Suggestions().Save(ctx, j.RepoID, toSave); err != nil {
			slog.Warn("patchTopic: save suggestions failed", "err", err)
		}
	}

	// Process decisions on existing pending suggestions.
	for _, d := range curation.SuggestionDecisions {
		switch d.Decision {
		case "accept":
			existing, err := s.store.Suggestions().GetByID(ctx, d.SuggestionID)
			if err != nil || existing == nil {
				continue
			}
			toApply = append(toApply, ai.TopicCurationSuggestion{
				Kind:                existing.Kind,
				TargetField:         existing.TargetField,
				Path:                existing.Path,
				Value:               existing.Value,
				Claim:               existing.Claim,
				EvidenceFeedbackIDs: existing.EvidenceFeedbackIDs,
				Confidence:          5,
				Reason:              existing.Reason,
			})
			_ = s.store.Suggestions().UpdateStatus(ctx, d.SuggestionID, model.TopicPatchSuggestionApplied, 5)
		case "reject":
			_ = s.store.Suggestions().UpdateStatus(ctx, d.SuggestionID, model.TopicPatchSuggestionRejected, d.Confidence)
		}
		// keep_pending: no action needed
	}

	if len(toApply) == 0 {
		return s.store.Feedback().MarkFeedbacksProcessed(ctx, j.JobID, j.FeedbackIDs)
	}
	updated := ai.ApplyTopicCuration(*topic, toApply)
	topics, err := s.store.Topics().GetTopics(ctx, j.RepoID)
	if err != nil {
		return err
	}
	replaced := false
	for i := range topics {
		if topics[i].ID == updated.ID {
			topics[i] = updated
			replaced = true
			break
		}
	}
	if !replaced {
		topics = append(topics, updated)
	}
	if err := s.store.Topics().PutTopics(ctx, j.RepoID, topics); err != nil {
		return err
	}
	return s.store.Feedback().MarkFeedbacksProcessed(ctx, j.JobID, j.FeedbackIDs)
}

func (s *JobService) patchRepo(ctx context.Context, j *model.ContextPatchJob) error {
	entry, err := s.store.Topics().GetRepoContext(ctx, j.RepoID)
	if err != nil || entry == nil {
		return fmt.Errorf("repo context not found")
	}
	feedbacks, err := s.store.Feedback().ListFeedbackByIDs(ctx, j.FeedbackIDs)
	if err != nil {
		return err
	}
	fbPtrs := make([]*model.MCPFeedback, len(feedbacks))
	for i := range feedbacks {
		fbPtrs[i] = &feedbacks[i]
	}
	patch, _, err := s.ai.PatchRepoContext(ctx, entry.Content, fbPtrs, nil)
	if err != nil {
		return err
	}
	updated := ai.ApplyRepoContextPatch(entry.Content, patch)
	if err := s.store.Topics().PutRepoContext(ctx, j.RepoID, updated, entry.BundleID); err != nil {
		return err
	}
	return s.store.Feedback().MarkFeedbacksProcessed(ctx, j.JobID, j.FeedbackIDs)
}

func (s *JobService) createNewTopic(ctx context.Context, j *model.ContextPatchJob) (model.TopicContext, error) {
	feedbacks, err := s.store.Feedback().ListFeedbackByIDs(ctx, j.FeedbackIDs)
	if err != nil {
		return model.TopicContext{}, err
	}
	if len(feedbacks) == 0 {
		return model.TopicContext{}, fmt.Errorf("no feedbacks found for ids %v", j.FeedbackIDs)
	}
	fb := feedbacks[0]

	repo, err := s.store.Repos().Get(ctx, j.RepoID)
	if err != nil {
		return model.TopicContext{}, err
	}
	repoRoot := ""
	if repo != nil {
		repoRoot = repo.RepoRoot
	}
	events, err := s.store.Events().Get(ctx, j.RepoID)
	if err != nil {
		return model.TopicContext{}, err
	}
	bundle, err := analysis.BuildRepoAnalysis(repoRoot, events)
	if err != nil {
		return model.TopicContext{}, err
	}
	sessionEvents, err := s.store.Events().GetSession(ctx, j.RepoID, fb.SessionID)
	if err != nil {
		return model.TopicContext{}, err
	}
	topics, err := s.store.Topics().GetTopics(ctx, j.RepoID)
	if err != nil {
		return model.TopicContext{}, err
	}
	newTopic, skipReason, _, err := s.ai.GenerateTopicContextFromFeedback(ctx, newTopicSuggestedName(fb), newTopicFeedbackText(fb), bundle, sessionEvents, topics)
	if err != nil {
		return model.TopicContext{}, err
	}
	if newTopic == nil {
		slog.Info("createNewTopic: skipped", "job_id", j.JobID, "reason", skipReason)
		if err := s.store.Feedback().MarkFeedbacksProcessed(ctx, j.JobID, j.FeedbackIDs); err != nil {
			return model.TopicContext{}, err
		}
		return model.TopicContext{}, nil
	}
	topics = append(topics, *newTopic)
	if err := s.store.Topics().PutTopics(ctx, j.RepoID, topics); err != nil {
		return model.TopicContext{}, err
	}
	if err := s.store.Feedback().MarkFeedbacksProcessed(ctx, j.JobID, j.FeedbackIDs); err != nil {
		return model.TopicContext{}, err
	}
	return *newTopic, nil
}

func newTopicSuggestedName(fb model.MCPFeedback) string {
	if fb.MissingContext != "" {
		return fb.MissingContext
	}
	return fb.Task
}

func newTopicFeedbackText(fb model.MCPFeedback) string {
	parts := make([]string, 0, 3)
	if fb.MissingContext != "" {
		parts = append(parts, "Missing context: "+fb.MissingContext)
	}
	if fb.WhatWentWrong != "" {
		parts = append(parts, "What went wrong: "+fb.WhatWentWrong)
	}
	if fb.Quote != "" {
		parts = append(parts, "Quote: "+fb.Quote)
	}
	return strings.Join(parts, "\n")
}

func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
