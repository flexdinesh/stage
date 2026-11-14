package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/stage/server/internal/t3"
)

// threadArgs are the list-level mirror columns shared by both upserts.
func threadArgs(t t3.Thread) []any {
	created, updated := t.CreatedAt, t.UpdatedAt
	if created.IsZero() {
		created = time.Now()
	}
	if updated.IsZero() {
		updated = created
	}
	return []any{
		t.ThreadID, t.ProjectID, t.Title, string(t.Status), t.Settled, t.SettledAt,
		nullIfEmpty(t.ProviderInstanceID), nullIfEmpty(t.Model),
		nullIfEmpty(string(t.RuntimeMode)), nullIfEmpty(string(t.InteractionMode)),
		nullIfEmpty(t.ParentThreadID), nullIfEmpty(t.RelationshipToParent),
		nullIfEmpty(t.CreationSource), nullIfEmpty(t.Link), created, updated, nullIfEmpty(t.LatestRunID), t.Archived,
		nonNilPRs(t.PullRequests),
	}
}

func nonNilPRs(prs []t3.PullRequest) []t3.PullRequest {
	if prs == nil {
		return []t3.PullRequest{}
	}
	return prs
}

// maxStoredRuns caps threads.recent_runs (t3_thread_read runLimit).
const maxStoredRuns = 50

// threadRuns maps recentRuns (newest first) to the stored shape.
func threadRuns(runs []t3.Run) []ThreadRun {
	out := make([]ThreadRun, 0, min(len(runs), maxStoredRuns))
	for _, r := range runs[:min(len(runs), maxStoredRuns)] {
		out = append(out, ThreadRun{
			RunID: r.RunID, Ordinal: r.Ordinal, Status: string(r.Status),
			ProviderInstanceID: nullIfEmpty(r.ProviderInstanceID), Model: nullIfEmpty(r.Model),
			ExecutedModel: nullIfEmpty(r.ExecutedModel), RequestedAt: r.RequestedAt,
			StartedAt: r.StartedAt, CompletedAt: r.CompletedAt,
		})
	}
	return out
}

const threadUpsertList = `
INSERT INTO threads (t3_thread_id, t3_project_id, title, status, settled, settled_at,
  provider_instance_id, model, runtime_mode, interaction_mode, parent_thread_id,
  relationship, creation_source, link, created_at, updated_at, latest_run_id, archived, pull_requests, synced_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19, now())
ON CONFLICT (t3_thread_id) DO UPDATE SET
  t3_project_id = EXCLUDED.t3_project_id, title = EXCLUDED.title, status = EXCLUDED.status,
  settled = EXCLUDED.settled, settled_at = EXCLUDED.settled_at,
  provider_instance_id = EXCLUDED.provider_instance_id, model = EXCLUDED.model,
  runtime_mode = EXCLUDED.runtime_mode, interaction_mode = EXCLUDED.interaction_mode,
  parent_thread_id = EXCLUDED.parent_thread_id, relationship = EXCLUDED.relationship,
  creation_source = EXCLUDED.creation_source, link = COALESCE(EXCLUDED.link, threads.link),
  created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at,
  latest_run_id = EXCLUDED.latest_run_id, pull_requests = EXCLUDED.pull_requests,
  active_run_id = CASE WHEN EXCLUDED.status IN ('completed','failed','cancelled','interrupted','rolled_back','idle') THEN NULL ELSE threads.active_run_id END,
  latest_run_status = CASE WHEN EXCLUDED.latest_run_id IS DISTINCT FROM threads.latest_run_id THEN NULL ELSE threads.latest_run_status END,
  synced_at = now() WHERE EXCLUDED.updated_at >= threads.updated_at`

// UpsertThreads mirrors t3_thread_list items (pull requests included).
// Detail-only columns (branch, worktree_path, pending_request_count,
// recent_runs, run_count) are left untouched.
func (s *Store) UpsertThreads(ctx context.Context, threads []t3.Thread) error {
	if len(threads) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, t := range threads {
		if t.ThreadID == "" {
			continue
		}
		batch.Queue(threadUpsertList, threadArgs(t)...)
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// UpsertThreadDetail mirrors a t3_thread_read result (all columns).
func (s *Store) UpsertThreadDetail(ctx context.Context, d t3.ThreadDetail) error {
	var latest string
	if len(d.RecentRuns) > 0 {
		latest = string(d.RecentRuns[0].Status)
	}
	start, end := RunSpan(d)
	args := append(threadArgs(d.Thread), nullIfEmpty(d.Branch), nullIfEmpty(d.WorktreePath), d.PendingRequestCount,
		nullIfEmpty(d.ActiveRunID), nullIfEmpty(latest), start, end, threadRuns(d.RecentRuns), d.RunCount)
	_, err := s.pool.Exec(ctx, `
INSERT INTO threads (t3_thread_id, t3_project_id, title, status, settled, settled_at,
  provider_instance_id, model, runtime_mode, interaction_mode, parent_thread_id,
  relationship, creation_source, link, created_at, updated_at, latest_run_id, archived, pull_requests, branch, worktree_path,
  pending_request_count, active_run_id, latest_run_status, run_started_at, run_ended_at, recent_runs, run_count, synced_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28, now())
ON CONFLICT (t3_thread_id) DO UPDATE SET
  t3_project_id = EXCLUDED.t3_project_id, title = EXCLUDED.title, status = EXCLUDED.status,
  settled = EXCLUDED.settled, settled_at = EXCLUDED.settled_at,
  provider_instance_id = EXCLUDED.provider_instance_id, model = EXCLUDED.model,
  runtime_mode = EXCLUDED.runtime_mode, interaction_mode = EXCLUDED.interaction_mode,
  parent_thread_id = EXCLUDED.parent_thread_id, relationship = EXCLUDED.relationship,
  creation_source = EXCLUDED.creation_source, link = COALESCE(EXCLUDED.link, threads.link),
  created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at,
  branch = EXCLUDED.branch, worktree_path = EXCLUDED.worktree_path,
  pending_request_count = EXCLUDED.pending_request_count, archived = EXCLUDED.archived,
  active_run_id = EXCLUDED.active_run_id, latest_run_id = EXCLUDED.latest_run_id,
  latest_run_status = EXCLUDED.latest_run_status,
  run_started_at = EXCLUDED.run_started_at, run_ended_at = EXCLUDED.run_ended_at,
  pull_requests = EXCLUDED.pull_requests, recent_runs = EXCLUDED.recent_runs, run_count = EXCLUDED.run_count, synced_at = now()
WHERE EXCLUDED.updated_at >= threads.updated_at`, args...)
	return err
}

// RunSpan is a thread's elapsed run span from recentRuns (newest first):
// the oldest run's start (requestedAt when it never started) to the newest
// run's completion. When runCount exceeds the runs read, the first run is
// missing, so the start falls back to the thread's createdAt. nil, nil
// without runs; end is nil while the newest run is incomplete.
func RunSpan(d t3.ThreadDetail) (start, end *time.Time) {
	if len(d.RecentRuns) == 0 {
		return nil, nil
	}
	end = d.RecentRuns[0].CompletedAt
	oldest := d.RecentRuns[len(d.RecentRuns)-1]
	switch {
	case d.RunCount > len(d.RecentRuns) && !d.CreatedAt.IsZero():
		start = &d.CreatedAt
	case oldest.StartedAt != nil:
		start = oldest.StartedAt
	case !oldest.RequestedAt.IsZero():
		start = &oldest.RequestedAt
	}
	return start, end
}

const threadSelect = `
SELECT th.t3_thread_id, th.t3_project_id, p.id, p.title, th.title, th.status, th.settled,
  th.settled_at, th.pending_request_count, th.provider_instance_id, th.model, th.runtime_mode,
  th.interaction_mode, th.branch, th.worktree_path, th.parent_thread_id, th.relationship,
  th.creation_source, tk.id, th.link, th.created_at, th.updated_at, th.synced_at, th.archived, th.active_run_id, th.latest_run_id,
  th.run_started_at, th.run_ended_at, th.last_word_preview, th.last_word_at, th.run_count, th.pull_requests, ` + threadStateSQL + `
FROM threads th
LEFT JOIN projects p ON p.t3_project_id = th.t3_project_id
LEFT JOIN tasks tk ON tk.t3_thread_id = th.t3_thread_id`

func scanThread(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(&t.T3ThreadID, &t.T3ProjectID, &t.ProjectID, &t.ProjectTitle, &t.Title, &t.Status,
		&t.Settled, &t.SettledAt, &t.PendingRequestCount, &t.ProviderInstanceID, &t.Model, &t.RuntimeMode,
		&t.InteractionMode, &t.Branch, &t.WorktreePath, &t.ParentThreadID, &t.Relationship,
		&t.CreationSource, &t.TaskID, &t.Link, &t.CreatedAt, &t.UpdatedAt, &t.SyncedAt, &t.Archived, &t.ActiveRunID, &t.LatestRunID,
		&t.RunStartedAt, &t.RunEndedAt, &t.LastWordPreview, &t.LastWordAt, &t.RunCount, &t.PullRequests, &t.State)
	if err != nil {
		return Thread{}, notFound(err)
	}
	t.PullRequests = nonNilPRs(t.PullRequests)
	t.Source = SourceT3
	if t.TaskID != nil {
		t.Source = SourceApp
	}
	return t, nil
}

// ThreadRuns returns a thread's mirrored recent runs (newest first, at most
// 50); [] when unread. ErrNotFound for an unknown thread.
func (s *Store) ThreadRuns(ctx context.Context, id string) ([]ThreadRun, error) {
	var runs []ThreadRun
	if err := s.pool.QueryRow(ctx, `SELECT recent_runs FROM threads WHERE t3_thread_id = $1`, id).Scan(&runs); err != nil {
		return nil, notFound(err)
	}
	if runs == nil {
		runs = []ThreadRun{}
	}
	return runs, nil
}

func (s *Store) GetThread(ctx context.Context, id string) (Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, threadSelect+` WHERE th.t3_thread_id = $1`, id))
}

type ThreadFilter struct {
	ProjectID    *int64
	States       []string
	Active       bool
	SortActivity bool
	Parent       *string
	Statuses     []string
	Settled      *bool
	Provider     string
	Source       string // app | t3 | ""
	Q            string
	// IDs keeps these T3 thread ids only (nil = no id filter).
	IDs      []string
	From, To *time.Time
	// ExcludeSubagents keeps root threads only (parent_thread_id IS NULL).
	ExcludeSubagents bool
	// NeedsInput keeps external threads needing input (see NeedsInputCount).
	NeedsInput bool
	// Running keeps external threads observed running (see externalRunning).
	Running bool
	// SettledFrom keeps external threads settled at or after it (see
	// externalSettled), newest settle first.
	SettledFrom *time.Time
	Limit       int
	Offset      int
}

// ListThreads returns a page of mirrored threads (createdAt desc) and the
// total match count.
func (s *Store) ListThreads(ctx context.Context, f ThreadFilter) ([]Thread, int, error) {
	var w where
	if len(f.States) > 0 {
		w.add("(" + threadStateSQL + ") = ANY(" + w.arg(f.States) + ")")
	}
	if f.Active {
		w.add("NOT th.archived AND (" + threadStateSQL + ") IN ('running','needs_input')")
	}
	if f.Parent != nil {
		w.add("th.parent_thread_id = " + w.arg(*f.Parent))
	}
	if f.ProjectID != nil {
		w.add("p.id = " + w.arg(*f.ProjectID))
	}
	if len(f.Statuses) > 0 {
		w.add("th.status = ANY(" + w.arg(f.Statuses) + ")")
	}
	if f.Settled != nil {
		w.add("th.settled = " + w.arg(*f.Settled))
	}
	if f.Provider != "" {
		w.add("th.provider_instance_id = " + w.arg(f.Provider))
	}
	switch f.Source {
	case SourceApp:
		w.add("tk.id IS NOT NULL")
	case SourceT3:
		w.add("tk.id IS NULL")
	}
	if f.Q != "" {
		w.add("th.title ILIKE " + w.arg(likePattern(f.Q)))
	}
	if f.IDs != nil {
		w.add("th.t3_thread_id = ANY(" + w.arg(f.IDs) + ")")
	}
	if f.From != nil {
		w.add("th.created_at >= " + w.arg(*f.From))
	}
	if f.To != nil {
		w.add("th.created_at < " + w.arg(*f.To))
	}
	if f.ExcludeSubagents {
		w.add("th.parent_thread_id IS NULL")
	}
	if f.NeedsInput {
		w.add(externalNeedsInput)
	}
	if f.Running {
		w.add(externalRunning)
	}
	if f.SettledFrom != nil {
		w.add(externalSettled + " AND th.settled_at >= " + w.arg(*f.SettledFrom))
	}
	var total int
	countSQL := `SELECT count(*) FROM threads th
LEFT JOIN projects p ON p.t3_project_id = th.t3_project_id
LEFT JOIN tasks tk ON tk.t3_thread_id = th.t3_thread_id` + w.sql()
	if err := s.pool.QueryRow(ctx, countSQL, w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "th.created_at"
	switch {
	case f.SettledFrom != nil:
		order = "th.settled_at"
	case f.SortActivity:
		order = "th.updated_at"
	}
	q := threadSelect + w.sql() + ` ORDER BY ` + order + ` DESC, th.t3_thread_id DESC LIMIT ` +
		w.arg(f.Limit) + ` OFFSET ` + w.arg(f.Offset)
	rows, err := s.pool.Query(ctx, q, w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// The mirror's state follows T3 independently of frozen legacy task state.
// Input, activity, and failure outrank a stale settled flag.
const threadStateSQL = `CASE
 WHEN th.pending_request_count > 0 THEN 'needs_input'
 WHEN th.active_run_id IS NOT NULL OR COALESCE(NULLIF(th.status,'idle'),th.latest_run_status) IN ('preparing','queued','starting','running','waiting') THEN 'running'
 WHEN COALESCE(NULLIF(th.status,'idle'),th.latest_run_status) = 'failed' THEN 'failed'
 WHEN COALESCE(NULLIF(th.status,'idle'),th.latest_run_status) = 'interrupted' THEN 'interrupted'
 WHEN COALESCE(NULLIF(th.status,'idle'),th.latest_run_status) IN ('cancelled','rolled_back') THEN 'cancelled'
 WHEN th.settled THEN 'settled'
 WHEN th.status IN ('idle','completed') THEN 'done'
 ELSE 'running' END`

// threadPassedSQL: a session no longer blocks tasks waiting on it (link
// rules): settled in T3 with no pending input or active run. Uses the raw
// settled flag, so a settled session whose last run was cancelled passes.
const threadPassedSQL = `(th.settled AND th.pending_request_count = 0 AND th.active_run_id IS NULL
 AND COALESCE(NULLIF(th.status,'idle'),th.latest_run_status,'') NOT IN ('preparing','queued','starting','running','waiting'))`
