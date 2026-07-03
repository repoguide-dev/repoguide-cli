package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/repoguide/repoguide-cli/internal/ai"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

type FeedbackService struct {
	store storecontract.Store
	ai    ai.LLM
}

func (s *FeedbackService) CreateMCPCall(ctx context.Context, call *model.MCPCall) (*model.MCPCall, error) {
	return s.store.Feedback().CreateMCPCall(ctx, call)
}

func (s *FeedbackService) PutFeedback(ctx context.Context, fb *model.MCPFeedback) (*model.MCPFeedback, error) {
	created, err := s.store.Feedback().PutFeedback(ctx, fb)
	if err != nil {
		return nil, err
	}
	slog.Info("feedback saved", "feedback_id", created.FeedbackID, "repo_id", created.RepoID, "stars", created.Stars, "helpfulness", created.Helpfulness)
	return created, nil
}

// ClassifyFeedback calls AI to classify a feedback record and stores the result.
func (s *FeedbackService) ClassifyFeedback(ctx context.Context, repoID, feedbackID string) error {
	slog.Info("classifying feedback", "feedback_id", feedbackID, "repo_id", repoID)
	fb, err := s.store.Feedback().GetFeedback(ctx, feedbackID)
	if err != nil {
		slog.Warn("classify feedback: load failed", "feedback_id", feedbackID, "repo_id", repoID, "err", err)
		return fmt.Errorf("load feedback: %w", err)
	}
	if fb == nil {
		slog.Warn("classify feedback: feedback missing", "feedback_id", feedbackID, "repo_id", repoID)
		return fmt.Errorf("feedback %s not found", feedbackID)
	}
	slog.Info("classify feedback: loaded", "feedback_id", feedbackID, "repo_id", repoID, "task_present", fb.Task != "", "topic_id", fb.TopicID, "helpfulness", fb.Helpfulness, "stars", fb.Stars)
	clf, _, err := s.ai.ClassifyFeedback(ctx, fb)
	if err != nil {
		slog.Warn("classify feedback: model call failed", "feedback_id", feedbackID, "repo_id", repoID, "err", err)
		return fmt.Errorf("AI classify: %w", err)
	}
	if clf == nil {
		slog.Warn("classify feedback: model returned nil classification", "feedback_id", feedbackID, "repo_id", repoID)
		return fmt.Errorf("AI classify: nil classification")
	}
	slog.Info("classify feedback: model returned", "feedback_id", feedbackID, "repo_id", repoID, "kind", clf.Kind, "quality", clf.Quality, "severity", clf.Severity)
	if err := s.store.Feedback().SetFeedbackClassification(ctx, feedbackID, clf); err != nil {
		slog.Warn("classify feedback: persist failed", "feedback_id", feedbackID, "repo_id", repoID, "kind", clf.Kind, "err", err)
		return err
	}
	slog.Info("feedback classified", "feedback_id", feedbackID, "kind", clf.Kind, "quality", clf.Quality)
	return nil
}
