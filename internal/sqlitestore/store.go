// Package sqlitestore implements core/contracts.Store on top of a local SQLite database.
// All state that was previously sent to the backend (topics, files, feedback,
// MCP calls, repo context) now lives in ~/.repoguide/repoguide.db.
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/repoguide/repoguide-cli/internal/store"
	"github.com/repoguide/repoguide-core/model"
	_ "modernc.org/sqlite"
)

// Store implements core/contracts.Store.
type Store struct{ db *sql.DB }

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_busy_timeout=5000&_fk=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	return s, s.migrate()
}

func (s *Store) Close() error { return s.db.Close() }

// LocalStats returns aggregate token totals and agent versions from local data.
type LocalStats struct {
	AgentVersions []string
	InputTokens   int64
	OutputTokens  int64
	SessionCount  int
}

func (s *Store) LocalStats(ctx context.Context) (LocalStats, error) {
	var ls LocalStats

	// Agent versions from MCP calls
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT agent_name, agent_version FROM mcp_calls WHERE agent_name != '' ORDER BY agent_name`)
	if err == nil {
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var name, version string
			if rows.Scan(&name, &version) == nil && !seen[name+version] {
				seen[name+version] = true
				v := name
				if version != "" {
					v += " " + version
				}
				ls.AgentVersions = append(ls.AgentVersions, v)
			}
		}
	}

	// Token totals from session events
	erows, err := s.db.QueryContext(ctx, `SELECT events_json FROM session_events`)
	if err != nil {
		return ls, nil // ponytail: best-effort, don't fail doctor on missing data
	}
	defer erows.Close()
	for erows.Next() {
		var raw string
		if erows.Scan(&raw) != nil {
			continue
		}
		var session model.RepoSessionEvents
		if json.Unmarshal([]byte(raw), &session) != nil {
			continue
		}
		ls.SessionCount++
		// Find last cumulative token_usage; fall back to summing incrementals
		var lastCumulative *model.TokenUsage
		var incremental model.TokenUsage
		for _, ev := range session.Events {
			if ev.TokenUsage == nil {
				continue
			}
			if ev.TokenUsage.Cumulative {
				lastCumulative = ev.TokenUsage
			} else {
				incremental.InputTokens += ev.TokenUsage.InputTokens
				incremental.OutputTokens += ev.TokenUsage.OutputTokens
			}
		}
		if lastCumulative != nil {
			ls.InputTokens += lastCumulative.InputTokens
			ls.OutputTokens += lastCumulative.OutputTokens
		} else {
			ls.InputTokens += incremental.InputTokens
			ls.OutputTokens += incremental.OutputTokens
		}
	}
	return ls, nil
}

// ── Store interface ───────────────────────────────────────────────────────────

func (s *Store) Repos() contracts.RepoStore        { return &repoStore{s.db} }
func (s *Store) Sessions() contracts.SessionStore  { return &sessionStore{s.db} }
func (s *Store) Events() contracts.EventStore      { return &eventStore{s.db} }
func (s *Store) Topics() contracts.TopicStore      { return &topicStore{s.db} }
func (s *Store) Feedback() contracts.FeedbackStore { return &feedbackStore{s.db} }
func (s *Store) Jobs() contracts.JobStore          { return &tableJobStore{s.db, "jobs"} }
func (s *Store) ClassifyJobs() contracts.ClassifyJobStore {
	return &tableJobStore{s.db, "feedback_classify_jobs"}
}
func (s *Store) Suggestions() contracts.SuggestionStore { return &suggestionStore{s.db} }

// ── Schema ────────────────────────────────────────────────────────────────────

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS repos (
    repo_id      TEXT PRIMARY KEY,
    repo_root    TEXT NOT NULL,
    repo_name    TEXT NOT NULL,
    user_id      TEXT NOT NULL DEFAULT '',
    activated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS session_events (
    repo_id     TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    events_json TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repo_id, session_id)
);
CREATE TABLE IF NOT EXISTS repo_docs (
    repo_id  TEXT NOT NULL,
    name     TEXT NOT NULL,
    content  TEXT NOT NULL,
    PRIMARY KEY (repo_id, name)
);
CREATE TABLE IF NOT EXISTS topics (
    repo_id     TEXT NOT NULL,
    topic_id    TEXT NOT NULL,
    topic_json  TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repo_id, topic_id)
);
CREATE TABLE IF NOT EXISTS files (
    repo_id      TEXT NOT NULL,
    path         TEXT NOT NULL,
    context_json TEXT NOT NULL,
    PRIMARY KEY (repo_id, path)
);
CREATE TABLE IF NOT EXISTS search_data (
    repo_id     TEXT PRIMARY KEY,
    search_json TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS repo_context (
    repo_id    TEXT PRIMARY KEY,
    content    TEXT NOT NULL,
    bundle_id  TEXT NOT NULL DEFAULT '',
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS mcp_calls (
    call_id       TEXT PRIMARY KEY,
    repo_id       TEXT NOT NULL,
    command       TEXT NOT NULL,
    inputs_json   TEXT,
    response_json TEXT,
    agent_name    TEXT,
    agent_version TEXT,
    session_id    TEXT,
    topic_id      TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS feedback (
    feedback_id   TEXT PRIMARY KEY,
    repo_id       TEXT NOT NULL,
    feedback_json TEXT NOT NULL,
    processed     INTEGER NOT NULL DEFAULT 0,
    job_id        TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS jobs (
    job_id     TEXT PRIMARY KEY,
    repo_id    TEXT NOT NULL,
    job_json   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'QUEUED',
    retry_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS feedback_classify_jobs (
    job_id     TEXT PRIMARY KEY,
    repo_id    TEXT NOT NULL,
    job_json   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'QUEUED',
    retry_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS topic_patch_suggestions (
    suggestion_id TEXT PRIMARY KEY,
    repo_id       TEXT NOT NULL,
    topic_id      TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    suggestion_json TEXT NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
`)
	return err
}

// ── repoStore ────────────────────────────────────────────────────────────────

type repoStore struct{ db *sql.DB }

func (s *repoStore) Upsert(ctx context.Context, r *model.Repo) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO repos (repo_id, repo_root, repo_name, user_id, activated_at, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   repo_root=excluded.repo_root, repo_name=excluded.repo_name,
		   updated_at=excluded.updated_at`,
		r.RepoID, r.RepoRoot, r.RepoName, r.UserID,
		r.ActivatedAt.UTC(), time.Now().UTC(),
	)
	return err
}

func (s *repoStore) Get(ctx context.Context, repoID string) (*model.Repo, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT repo_id, repo_root, repo_name, user_id, activated_at, updated_at FROM repos WHERE repo_id=?`, repoID)
	var r model.Repo
	if err := row.Scan(&r.RepoID, &r.RepoRoot, &r.RepoName, &r.UserID, &r.ActivatedAt, &r.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *repoStore) Delete(ctx context.Context, repoID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE repo_id=?`, repoID)
	return err
}

func (s *repoStore) List(ctx context.Context) ([]*model.Repo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo_id, repo_root, repo_name, user_id, activated_at, updated_at FROM repos ORDER BY repo_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		var r model.Repo
		if err := rows.Scan(&r.RepoID, &r.RepoRoot, &r.RepoName, &r.UserID, &r.ActivatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ── sessionStore ─────────────────────────────────────────────────────────────

type sessionStore struct{ db *sql.DB }

func (s *sessionStore) LatestUpdatedAt(ctx context.Context, repoID string) (time.Time, error) {
	var t time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(updated_at), '1970-01-01') FROM session_events WHERE repo_id=?`, repoID,
	).Scan(&t)
	return t, err
}

// ── eventStore ───────────────────────────────────────────────────────────────

type eventStore struct{ db *sql.DB }

func (s *eventStore) Put(ctx context.Context, repoID string, events []model.RepoSessionEvents) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO session_events (repo_id, session_id, events_json, updated_at)
		 VALUES (?,?,?,?)
		 ON CONFLICT(repo_id, session_id) DO UPDATE SET
		   events_json=excluded.events_json, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range events {
		data, _ := json.Marshal(e)
		_, err = stmt.ExecContext(ctx, repoID, e.ID, string(data), e.UpdatedAt.UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *eventStore) Get(ctx context.Context, repoID string) ([]model.RepoSessionEvents, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT events_json FROM session_events WHERE repo_id=?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RepoSessionEvents
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e model.RepoSessionEvents
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *eventStore) GetSession(ctx context.Context, repoID, sessionID string) ([]model.SessionEvent, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT events_json FROM session_events WHERE repo_id=? AND session_id=?`, repoID, sessionID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e model.RepoSessionEvents
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil, err
	}
	return e.Events, nil
}

func (s *eventStore) GetDocs(ctx context.Context, repoID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, content FROM repo_docs WHERE repo_id=?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			return nil, err
		}
		out[name] = content
	}
	return out, rows.Err()
}

func (s *eventStore) PutDocs(ctx context.Context, repoID string, docs map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, content := range docs {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO repo_docs (repo_id, name, content) VALUES (?,?,?)
			 ON CONFLICT(repo_id, name) DO UPDATE SET content=excluded.content`,
			repoID, name, content,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ── topicStore ───────────────────────────────────────────────────────────────

type topicStore struct{ db *sql.DB }

func (s *topicStore) PutTopics(ctx context.Context, repoID string, topics []model.TopicContext) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range topics {
		data, _ := json.Marshal(t)
		_, err = tx.ExecContext(ctx,
			`INSERT INTO topics (repo_id, topic_id, topic_json, updated_at) VALUES (?,?,?,?)
			 ON CONFLICT(repo_id, topic_id) DO UPDATE SET topic_json=excluded.topic_json, updated_at=excluded.updated_at`,
			repoID, t.ID, string(data), time.Now().UTC(),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *topicStore) GetTopics(ctx context.Context, repoID string) ([]model.TopicContext, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT topic_json FROM topics WHERE repo_id=? ORDER BY topic_id`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TopicContext
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var t model.TopicContext
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *topicStore) GetTopic(ctx context.Context, repoID, topicID string) (*model.TopicContext, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT topic_json FROM topics WHERE repo_id=? AND topic_id=?`, repoID, topicID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t model.TopicContext
	return &t, json.Unmarshal([]byte(raw), &t)
}

func (s *topicStore) PutFiles(ctx context.Context, repoID string, files []model.FileSummary) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range files {
		data, _ := json.Marshal(f)
		_, err = tx.ExecContext(ctx,
			`INSERT INTO files (repo_id, path, context_json) VALUES (?,?,?)
			 ON CONFLICT(repo_id, path) DO UPDATE SET context_json=excluded.context_json`,
			repoID, f.Path, string(data),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *topicStore) GetFile(ctx context.Context, repoID, path string) (*model.FileSummary, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT context_json FROM files WHERE repo_id=? AND path=?`, repoID, path,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f model.FileSummary
	return &f, json.Unmarshal([]byte(raw), &f)
}

func (s *topicStore) PutSearchData(ctx context.Context, repoID string, data model.BundleSearchData) error {
	raw, _ := json.Marshal(data)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO search_data (repo_id, search_json, updated_at) VALUES (?,?,?)
		 ON CONFLICT(repo_id) DO UPDATE SET search_json=excluded.search_json, updated_at=excluded.updated_at`,
		repoID, string(raw), time.Now().UTC(),
	)
	return err
}

func (s *topicStore) GetSearchData(ctx context.Context, repoID string) (*model.BundleSearchData, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT search_json FROM search_data WHERE repo_id=?`, repoID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d model.BundleSearchData
	return &d, json.Unmarshal([]byte(raw), &d)
}

func (s *topicStore) PutRepoContext(ctx context.Context, repoID, content, bundleID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO repo_context (repo_id, content, bundle_id, updated_at) VALUES (?,?,?,?)
		 ON CONFLICT(repo_id) DO UPDATE SET content=excluded.content, bundle_id=excluded.bundle_id, updated_at=excluded.updated_at`,
		repoID, content, bundleID, time.Now().UTC(),
	)
	return err
}

func (s *topicStore) GetRepoContext(ctx context.Context, repoID string) (*model.RepoContextEntry, error) {
	var entry model.RepoContextEntry
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT content, bundle_id, updated_at FROM repo_context WHERE repo_id=?`, repoID,
	).Scan(&entry.Content, &entry.BundleID, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entry.CreatedAt = updatedAt
	entry.ContextID = repoID
	return &entry, nil
}

// ── feedbackStore ─────────────────────────────────────────────────────────────

type feedbackStore struct{ db *sql.DB }

func (s *feedbackStore) CreateMCPCall(ctx context.Context, call *model.MCPCall) (*model.MCPCall, error) {
	if call.CallID == "" {
		call.CallID = uuid.New().String()
	}
	if call.CreatedAt.IsZero() {
		call.CreatedAt = time.Now().UTC()
	}
	inputs, _ := json.Marshal(call.Inputs)
	resp, _ := json.Marshal(call.Response)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_calls (call_id, repo_id, command, inputs_json, response_json, agent_name, agent_version, session_id, topic_id, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		call.CallID, call.RepoID, call.Command, string(inputs), string(resp),
		call.AgentName, call.AgentVersion, call.SessionID, call.TopicID, call.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return call, nil
}

func (s *feedbackStore) GetMCPCall(ctx context.Context, callID string) (*model.MCPCall, error) {
	var call model.MCPCall
	var inputs, resp string
	err := s.db.QueryRowContext(ctx,
		`SELECT call_id, repo_id, command, inputs_json, response_json, agent_name, agent_version, session_id, topic_id, created_at
		 FROM mcp_calls WHERE call_id=?`, callID,
	).Scan(&call.CallID, &call.RepoID, &call.Command, &inputs, &resp,
		&call.AgentName, &call.AgentVersion, &call.SessionID, &call.TopicID, &call.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(inputs), &call.Inputs)
	_ = json.Unmarshal([]byte(resp), &call.Response)
	return &call, nil
}

func (s *feedbackStore) PutFeedback(ctx context.Context, fb *model.MCPFeedback) (*model.MCPFeedback, error) {
	if fb.FeedbackID == "" {
		fb.FeedbackID = uuid.New().String()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now().UTC()
	}
	data, _ := json.Marshal(fb)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback (feedback_id, repo_id, feedback_json, created_at) VALUES (?,?,?,?)
		 ON CONFLICT(feedback_id) DO UPDATE SET feedback_json=excluded.feedback_json`,
		fb.FeedbackID, fb.RepoID, string(data), fb.CreatedAt,
	)
	return fb, err
}

func (s *feedbackStore) GetFeedback(ctx context.Context, feedbackID string) (*model.MCPFeedback, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT feedback_json FROM feedback WHERE feedback_id=?`, feedbackID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fb model.MCPFeedback
	return &fb, json.Unmarshal([]byte(raw), &fb)
}

func (s *feedbackStore) ListUnprocessedFeedback(ctx context.Context, repoID string) ([]model.MCPFeedback, error) {
	return s.listFeedback(ctx, `SELECT feedback_json FROM feedback WHERE repo_id=? AND processed=0`, repoID)
}

func (s *feedbackStore) ListActionableFeedback(ctx context.Context, repoID string) ([]model.MCPFeedback, error) {
	return s.listFeedback(ctx, `SELECT feedback_json FROM feedback WHERE repo_id=? ORDER BY created_at DESC`, repoID)
}

func (s *feedbackStore) ListFeedbackByIDs(ctx context.Context, ids []string) ([]model.MCPFeedback, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.MCPFeedback
	for _, id := range ids {
		fb, err := s.GetFeedback(ctx, id)
		if err != nil {
			return nil, err
		}
		if fb != nil {
			out = append(out, *fb)
		}
	}
	return out, nil
}

func (s *feedbackStore) listFeedback(ctx context.Context, query, repoID string) ([]model.MCPFeedback, error) {
	rows, err := s.db.QueryContext(ctx, query, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.MCPFeedback
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var fb model.MCPFeedback
		if err := json.Unmarshal([]byte(raw), &fb); err != nil {
			return nil, err
		}
		out = append(out, fb)
	}
	return out, rows.Err()
}

func (s *feedbackStore) SetFeedbackClassification(ctx context.Context, feedbackID string, c *model.FeedbackClassification) error {
	fb, err := s.GetFeedback(ctx, feedbackID)
	if err != nil || fb == nil {
		return fmt.Errorf("feedback %s not found", feedbackID)
	}
	fb.Classification = c
	data, _ := json.Marshal(fb)
	_, err = s.db.ExecContext(ctx, `UPDATE feedback SET feedback_json=? WHERE feedback_id=?`, string(data), feedbackID)
	return err
}

func (s *feedbackStore) ClaimFeedbackProcessingJob(ctx context.Context, feedbackID, jobID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT feedback_json FROM feedback WHERE feedback_id=?`, feedbackID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	var fb model.MCPFeedback
	if err := json.Unmarshal([]byte(raw), &fb); err != nil {
		return false, err
	}
	if fb.ProcessedAt != nil || fb.ProcessingJobID != "" {
		return false, nil
	}
	fb.ProcessingJobID = jobID
	data, _ := json.Marshal(fb)
	if _, err := tx.ExecContext(ctx, `UPDATE feedback SET feedback_json=? WHERE feedback_id=?`, string(data), feedbackID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *feedbackStore) ClearFeedbackProcessingJob(ctx context.Context, feedbackID, jobID string) error {
	fb, err := s.GetFeedback(ctx, feedbackID)
	if err != nil || fb == nil {
		return err
	}
	if fb.ProcessedAt != nil || fb.ProcessingJobID != jobID {
		return nil
	}
	fb.ProcessingJobID = ""
	data, _ := json.Marshal(fb)
	_, err = s.db.ExecContext(ctx, `UPDATE feedback SET feedback_json=? WHERE feedback_id=?`, string(data), feedbackID)
	return err
}

func (s *feedbackStore) MarkFeedbacksProcessed(ctx context.Context, jobID string, feedbackIDs []string) error {
	for _, id := range feedbackIDs {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE feedback SET processed=1, job_id=? WHERE feedback_id=?`, jobID, id,
		); err != nil {
			return err
		}
	}
	return nil
}

// ── jobStore ─────────────────────────────────────────────────────────────────

type tableJobStore struct {
	db    *sql.DB
	table string
}

func (s *tableJobStore) Enqueue(ctx context.Context, job *model.ContextPatchJob) error {
	if job.JobID == "" {
		job.JobID = uuid.New().String()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	job.Status = model.PatchJobStatusQueued
	data, _ := json.Marshal(job)
	q := fmt.Sprintf(`INSERT INTO %s (job_id, repo_id, job_json, status, created_at, updated_at) VALUES (?,?,?,?,?,?)`, s.table)
	_, err := s.db.ExecContext(ctx, q, job.JobID, job.RepoID, string(data), job.Status, now, now)
	return err
}

func (s *tableJobStore) ClaimNext(ctx context.Context, repoID string) (*model.ContextPatchJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var jobID, raw, status string
	var retryCount int
	err = tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT job_id, job_json, status, retry_count FROM %s WHERE repo_id=? AND status IN ('QUEUED','RETRY') ORDER BY created_at LIMIT 1`, s.table),
		repoID,
	).Scan(&jobID, &raw, &status, &retryCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET status='RUNNING', updated_at=? WHERE job_id=?`, s.table),
		time.Now().UTC(), jobID,
	); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	var job model.ContextPatchJob
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return nil, err
	}
	job.Status = status
	job.RetryCount = retryCount
	return &job, nil
}

func (s *tableJobStore) UpdateStatus(ctx context.Context, jobID, status string, retryCount int) error {
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET status=?, retry_count=?, updated_at=? WHERE job_id=?`, s.table),
		status, retryCount, time.Now().UTC(), jobID,
	)
	return err
}

func (s *tableJobStore) ListPending(ctx context.Context) ([]model.ContextPatchJob, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT job_json, status, retry_count FROM %s WHERE status IN ('QUEUED','RETRY') ORDER BY created_at`, s.table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ContextPatchJob
	for rows.Next() {
		var raw, status string
		var retryCount int
		if err := rows.Scan(&raw, &status, &retryCount); err != nil {
			return nil, err
		}
		var j model.ContextPatchJob
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return nil, err
		}
		j.Status = status
		j.RetryCount = retryCount
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *tableJobStore) ResetRunning(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET status='QUEUED', updated_at=? WHERE status='RUNNING'`, s.table),
		time.Now().UTC())
	return err
}

func (s *tableJobStore) ResetErrors(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET status='QUEUED', retry_count=0, updated_at=? WHERE status='ERROR'`, s.table),
		time.Now().UTC())
	return err
}

func (s *tableJobStore) MarkNewTopicDone(ctx context.Context, jobID, topicID, topicName string) error {
	var raw string
	if err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT job_json FROM %s WHERE job_id=?`, s.table), jobID,
	).Scan(&raw); err != nil {
		return err
	}
	var j model.ContextPatchJob
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return err
	}
	j.Status = model.PatchJobStatusDone
	j.NewTopicID = topicID
	j.NewTopicName = topicName
	data, _ := json.Marshal(j)
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET status='DONE', job_json=?, updated_at=? WHERE job_id=?`, s.table),
		string(data), time.Now().UTC(), jobID,
	)
	return err
}

// ── suggestionStore ──────────────────────────────────────────────────────────

type suggestionStore struct{ db *sql.DB }

func (s *suggestionStore) Save(ctx context.Context, repoID string, suggestions []model.TopicPatchSuggestion) error {
	now := time.Now().UTC()
	for i := range suggestions {
		if suggestions[i].SuggestionID == "" {
			suggestions[i].SuggestionID = uuid.New().String()
		}
		suggestions[i].CreatedAt = now
		suggestions[i].UpdatedAt = now
		data, _ := json.Marshal(suggestions[i])
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO topic_patch_suggestions (suggestion_id, repo_id, topic_id, status, suggestion_json, created_at, updated_at)
			 VALUES (?,?,?,?,?,?,?)
			 ON CONFLICT(suggestion_id) DO NOTHING`,
			suggestions[i].SuggestionID, repoID, suggestions[i].TopicID,
			suggestions[i].Status, string(data), now, now,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *suggestionStore) ListPending(ctx context.Context, repoID, topicID string) ([]model.TopicPatchSuggestion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT suggestion_json FROM topic_patch_suggestions WHERE repo_id=? AND topic_id=? AND status='pending'`,
		repoID, topicID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TopicPatchSuggestion
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var sug model.TopicPatchSuggestion
		if err := json.Unmarshal([]byte(raw), &sug); err != nil {
			return nil, err
		}
		out = append(out, sug)
	}
	return out, rows.Err()
}

func (s *suggestionStore) GetByID(ctx context.Context, suggestionID string) (*model.TopicPatchSuggestion, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT suggestion_json FROM topic_patch_suggestions WHERE suggestion_id=?`, suggestionID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sug model.TopicPatchSuggestion
	if err := json.Unmarshal([]byte(raw), &sug); err != nil {
		return nil, err
	}
	return &sug, nil
}

func (s *suggestionStore) UpdateStatus(ctx context.Context, suggestionID, status string, confidence int, evidenceFeedbackIDs []string) error {
	now := time.Now().UTC()
	suggestion, err := s.GetByID(ctx, suggestionID)
	if err != nil || suggestion == nil {
		return err
	}
	suggestion.Status = status
	suggestion.Confidence = confidence
	suggestion.UpdatedAt = now
	if len(evidenceFeedbackIDs) > 0 {
		suggestion.EvidenceFeedbackIDs = append([]string(nil), evidenceFeedbackIDs...)
	}
	data, _ := json.Marshal(suggestion)
	_, err = s.db.ExecContext(ctx,
		`UPDATE topic_patch_suggestions
		 SET status=?, updated_at=?, suggestion_json=?
		 WHERE suggestion_id=?`,
		status, now, string(data), suggestionID,
	)
	return err
}
