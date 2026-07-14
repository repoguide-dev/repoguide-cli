package services

import (
	"context"
	"fmt"

	"github.com/repoguide/repoguide-cli/internal/ai"
	"github.com/repoguide/repoguide-cli/internal/analysis"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

type TopicService struct {
	store storecontract.Store
	ai    ai.LLM
	jobs  storecontract.JobDispatcher
}

// BuildTopics runs repo analysis + AI topic extraction and stores results.
func (s *TopicService) BuildTopics(ctx context.Context, repoID string) error {
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
	existing, _ := s.store.Topics().GetTopics(ctx, repoID)
	topics, _, err := s.ai.DeriveTopicsAndGenerateContext(ctx, bundle, docs, existing)
	if err != nil {
		return fmt.Errorf("AI topics: %w", err)
	}
	if err := s.store.Topics().PutTopics(ctx, repoID, topics); err != nil {
		return fmt.Errorf("store topics: %w", err)
	}
	files := extractFileSummaries(bundle)
	if err := s.store.Topics().PutFiles(ctx, repoID, files); err != nil {
		return fmt.Errorf("store files: %w", err)
	}
	search := extractSearchData(bundle)
	return s.store.Topics().PutSearchData(ctx, repoID, search)
}

func (s *TopicService) GetTopics(ctx context.Context, repoID string) ([]model.TopicContext, error) {
	return s.store.Topics().GetTopics(ctx, repoID)
}

func (s *TopicService) GetTopic(ctx context.Context, repoID, topicID string) (*model.TopicContext, error) {
	return s.store.Topics().GetTopic(ctx, repoID, topicID)
}

func (s *TopicService) GetFile(ctx context.Context, repoID, path string) (*model.FileSummary, error) {
	return s.store.Topics().GetFile(ctx, repoID, path)
}

func (s *TopicService) GetSearchData(ctx context.Context, repoID string) (*model.BundleSearchData, error) {
	return s.store.Topics().GetSearchData(ctx, repoID)
}

// extractFileSummaries converts bundle file stats to FileSummary records.
func extractFileSummaries(bundle contracts.RepoAnalysisBundle) []model.FileSummary {
	out := make([]model.FileSummary, 0, len(bundle.Files))
	for _, f := range bundle.Files {
		fs := model.FileSummary{Path: f.Path, Classification: f.Classification}
		for _, link := range f.RelatedTests {
			fs.RelatedTests = append(fs.RelatedTests, link.Path)
		}
		fs.ReadBeforeEditOf = f.ReadBeforeEditOf
		out = append(out, fs)
	}
	return out
}

// extractSearchData converts bundle discoverability data to BundleSearchData.
func extractSearchData(bundle contracts.RepoAnalysisBundle) model.BundleSearchData {
	d := bundle.Discoverability
	out := model.BundleSearchData{
		SearchHeavyTargets: make([]model.BundleSearchTarget, 0, len(d.SearchHeavyTargets)),
		AmbiguousSearches:  make([]model.BundleAmbiguousSearch, 0, len(d.AmbiguousSearches)),
	}
	for _, t := range d.SearchHeavyTargets {
		queries := make([]string, 0, len(t.TopQueries))
		for _, q := range t.TopQueries {
			queries = append(queries, q.Query)
		}
		out.SearchHeavyTargets = append(out.SearchHeavyTargets, model.BundleSearchTarget{
			Path:                   t.Path,
			FoundViaSearchSessions: t.FoundViaSearchSessions,
			TopQueries:             queries,
		})
	}
	for _, a := range d.AmbiguousSearches {
		out.AmbiguousSearches = append(out.AmbiguousSearches, model.BundleAmbiguousSearch{
			Query:               a.Query,
			Searches:            a.Searches,
			DistinctEditTargets: a.DistinctEditTargets,
		})
	}
	return out
}
