// Package localrunner wires a sqlitestore.Store into the CLI runtime.
package localrunner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/repoguide/repoguide-cli/internal/runtime"
	"github.com/repoguide/repoguide-cli/internal/services"
	"github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
)

// Dispatcher implements services.JobDispatcher by enqueuing directly into the
// local JobStore rather than sending an HTTP request.
type Dispatcher struct {
	store   contracts.Store
	drainFn func() // set after Runtime is constructed to trigger immediate classify drain
}

func (d *Dispatcher) Enqueue(jobType, repoID, topicID string) error {
	job := &model.ContextPatchJob{
		JobID:   uuid.New().String(),
		RepoID:  repoID,
		Type:    jobType,
		TopicID: topicID,
		Status:  model.PatchJobStatusQueued,
	}
	if jobType == model.PatchJobTypeClassifyFeedback {
		job.FeedbackIDs = []string{topicID}
		job.TopicID = ""
		if err := d.store.ClassifyJobs().Enqueue(context.Background(), job); err != nil {
			return err
		}
		slog.Info("classify job enqueued", "job_id", job.JobID, "feedback_id", topicID, "repo_id", repoID)
		if d.drainFn != nil {
			d.drainFn()
		}
		return nil
	}
	if err := d.store.Jobs().Enqueue(context.Background(), job); err != nil {
		return err
	}
	slog.Info("job enqueued", "job_id", job.JobID, "type", jobType, "repo_id", repoID, "topic_id", topicID)
	return nil
}

// Runtime wraps the CLI runtime with a local Dispatcher.
type Runtime struct {
	*runtime.Runtime
}

// New builds a local Runtime using the provided Store and config.
func New(s contracts.Store, cfg runtime.Config) *Runtime {
	d := &Dispatcher{store: s}
	r := runtime.New(s, d, cfg)
	d.drainFn = func() { go r.Services.Jobs.DrainClassifyPending(context.Background()) }
	return &Runtime{r}
}

// DrainJobs processes all pending background jobs synchronously (for --debug use).
func (r *Runtime) DrainJobs(ctx context.Context) {
	r.Services.Jobs.DrainPending(ctx)
}

// DrainClassifyJobs processes all pending feedback classification jobs synchronously.
func (r *Runtime) DrainClassifyJobs(ctx context.Context) {
	r.Services.Jobs.DrainClassifyPending(ctx)
}

// RunStartupMaintenance runs the same startup pipeline used by local CLI mode.
func (r *Runtime) RunStartupMaintenance(ctx context.Context) {
	r.Services.Jobs.RunStartupMaintenance(ctx)
}

// EnsureLocalRepo upserts a repo record in the local store.
func EnsureLocalRepo(ctx context.Context, svc *services.Services, repoID, repoRoot, repoName string) error {
	repo := &model.Repo{
		RepoID:   repoID,
		RepoRoot: repoRoot,
		RepoName: repoName,
	}
	if err := svc.Repos.Upsert(ctx, repo); err != nil {
		return fmt.Errorf("upsert local repo: %w", err)
	}
	slog.Debug("local repo registered", "repo_id", repoID)
	return nil
}
