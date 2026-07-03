package services

import (
	"context"

	"github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

type RepoService struct {
	store contracts.Store
}

func (s *RepoService) Upsert(ctx context.Context, repo *model.Repo) error {
	return s.store.Repos().Upsert(ctx, repo)
}

func (s *RepoService) Get(ctx context.Context, repoID string) (*model.Repo, error) {
	return s.store.Repos().Get(ctx, repoID)
}

func (s *RepoService) Delete(ctx context.Context, repoID string) error {
	return s.store.Repos().Delete(ctx, repoID)
}

func (s *RepoService) List(ctx context.Context) ([]*model.Repo, error) {
	return s.store.Repos().List(ctx)
}
