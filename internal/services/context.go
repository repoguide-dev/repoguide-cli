package services

import (
	"context"
	"fmt"

	"github.com/repoguide/repoguide-cli/internal/ai"
	"github.com/repoguide/repoguide-cli/internal/analysis"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

type ContextService struct {
	store storecontract.Store
	ai    ai.LLM
}

// RebuildRepoContext re-derives repo context from events and stores it.
func (s *ContextService) RebuildRepoContext(ctx context.Context, repoID string) error {
	events, err := s.store.Events().Get(ctx, repoID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	repo, err := s.store.Repos().Get(ctx, repoID)
	if err != nil {
		return fmt.Errorf("load repo: %w", err)
	}
	repoRoot := ""
	if repo != nil {
		repoRoot = repo.RepoRoot
	}
	// ponytail: CLI's local event store is per-developer; team aggregation happens in cloud.
	bundle, err := analysis.BuildRepoAnalysis(repoRoot, events, false)
	if err != nil {
		return fmt.Errorf("build bundle: %w", err)
	}
	docs, _ := s.store.Events().GetDocs(ctx, repoID)
	content, _, err := s.ai.GenerateRepoContext(ctx, bundle, docs)
	if err != nil {
		return fmt.Errorf("AI repo context: %w", err)
	}
	return s.store.Topics().PutRepoContext(ctx, repoID, content, "")
}

// GetRepoContext returns the stored repo context for a repo.
func (s *ContextService) GetRepoContext(ctx context.Context, repoID string) (*model.RepoContextEntry, error) {
	return s.store.Topics().GetRepoContext(ctx, repoID)
}
