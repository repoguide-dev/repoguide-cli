package services

import (
	"context"
	"time"

	"github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

type SessionService struct {
	store contracts.Store
}

func (s *SessionService) PutEvents(ctx context.Context, repoID string, events []model.RepoSessionEvents) error {
	return s.store.Events().Put(ctx, repoID, events)
}

func (s *SessionService) GetEvents(ctx context.Context, repoID string) ([]model.RepoSessionEvents, error) {
	return s.store.Events().Get(ctx, repoID)
}

func (s *SessionService) LatestUpdatedAt(ctx context.Context, repoID string) (time.Time, error) {
	return s.store.Sessions().LatestUpdatedAt(ctx, repoID)
}

func (s *SessionService) GetDocs(ctx context.Context, repoID string) (map[string]string, error) {
	return s.store.Events().GetDocs(ctx, repoID)
}

func (s *SessionService) PutDocs(ctx context.Context, repoID string, docs map[string]string) error {
	return s.store.Events().PutDocs(ctx, repoID, docs)
}
