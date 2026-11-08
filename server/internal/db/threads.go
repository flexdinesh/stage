package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
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
		nullIfEmpty(t.CreationSource), nullIfEmpty(t.Link), created, updated,
	}
}

const threadUpsertList = `
INSERT INTO threads (t3_thread_id, t3_project_id, title, status, settled, settled_at,
  provider_instance_id, model, runtime_mode, interaction_mode, parent_thread_id,
  relationship, creation_source, link, created_at, updated_at, synced_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16, now())
ON CONFLICT (t3_thread_id) DO UPDATE SET
  t3_project_id = EXCLUDED.t3_project_id, title = EXCLUDED.title, status = EXCLUDED.status,
  settled = EXCLUDED.settled, settled_at = EXCLUDED.settled_at,
  provider_instance_id = EXCLUDED.provider_instance_id, model = EXCLUDED.model,
  runtime_mode = EXCLUDED.runtime_mode, interaction_mode = EXCLUDED.interaction_mode,
  parent_thread_id = EXCLUDED.parent_thread_id, relationship = EXCLUDED.relationship,
  creation_source = EXCLUDED.creation_source, link = COALESCE(EXCLUDED.link, threads.link),
  created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at, synced_at = now()`

// UpsertThreads mirrors t3_thread_list items. Detail-only columns
// (branch, worktree_path, pending_request_count) are left untouched.
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
	args := append(threadArgs(d.Thread), nullIfEmpty(d.Branch), nullIfEmpty(d.WorktreePath), d.PendingRequestCount)
	_, err := s.pool.Exec(ctx, `
INSERT INTO threads (t3_thread_id, t3_project_id, title, status, settled, settled_at,
  provider_instance_id, model, runtime_mode, interaction_mode, parent_thread_id,
  relationship, creation_source, link, created_at, updated_at, branch, worktree_path,
  pending_request_count, synced_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19, now())
ON CONFLICT (t3_thread_id) DO UPDATE SET
  t3_project_id = EXCLUDED.t3_project_id, title = EXCLUDED.title, status = EXCLUDED.status,
  settled = EXCLUDED.settled, settled_at = EXCLUDED.settled_at,
  provider_instance_id = EXCLUDED.provider_instance_id, model = EXCLUDED.model,
  runtime_mode = EXCLUDED.runtime_mode, interaction_mode = EXCLUDED.interaction_mode,
  parent_thread_id = EXCLUDED.parent_thread_id, relationship = EXCLUDED.relationship,
  creation_source = EXCLUDED.creation_source, link = COALESCE(EXCLUDED.link, threads.link),
  created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at,
  branch = EXCLUDED.branch, worktree_path = EXCLUDED.worktree_path,
  pending_request_count = EXCLUDED.pending_request_count, synced_at = now()`, args...)
	return err
}

const threadSelect = `
SELECT th.t3_thread_id, th.t3_project_id, p.id, p.title, th.title, th.status, th.settled,
  th.settled_at, th.pending_request_count, th.provider_instance_id, th.model, th.runtime_mode,
  th.interaction_mode, th.branch, th.worktree_path, th.parent_thread_id, th.relationship,
  th.creation_source, tk.id, th.link, th.created_at, th.updated_at, th.synced_at
FROM threads th
LEFT JOIN projects p ON p.t3_project_id = th.t3_project_id
LEFT JOIN tasks tk ON tk.t3_thread_id = th.t3_thread_id`

func scanThread(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(&t.T3ThreadID, &t.T3ProjectID, &t.ProjectID, &t.ProjectTitle, &t.Title, &t.Status,
		&t.Settled, &t.SettledAt, &t.PendingRequestCount, &t.ProviderInstanceID, &t.Model, &t.RuntimeMode,
		&t.InteractionMode, &t.Branch, &t.WorktreePath, &t.ParentThreadID, &t.Relationship,
		&t.CreationSource, &t.TaskID, &t.Link, &t.CreatedAt, &t.UpdatedAt, &t.SyncedAt)
	if err != nil {
		return Thread{}, notFound(err)
	}
	t.Source = SourceT3
	if t.TaskID != nil {
		t.Source = SourceApp
	}
	return t, nil
}

func (s *Store) GetThread(ctx context.Context, id string) (Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, threadSelect+` WHERE th.t3_thread_id = $1`, id))
}

type ThreadFilter struct {
	ProjectID *int64
	Statuses  []string
	Settled   *bool
	Provider  string
	Source    string // app | t3 | ""
	Q         string
	From, To  *time.Time
	Limit     int
	Offset    int
}

// ListThreads returns a page of mirrored threads (createdAt desc) and the
// total match count.
func (s *Store) ListThreads(ctx context.Context, f ThreadFilter) ([]Thread, int, error) {
	var w where
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
	if f.From != nil {
		w.add("th.created_at >= " + w.arg(*f.From))
	}
	if f.To != nil {
		w.add("th.created_at < " + w.arg(*f.To))
	}
	var total int
	countSQL := `SELECT count(*) FROM threads th
LEFT JOIN projects p ON p.t3_project_id = th.t3_project_id
LEFT JOIN tasks tk ON tk.t3_thread_id = th.t3_thread_id` + w.sql()
	if err := s.pool.QueryRow(ctx, countSQL, w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := threadSelect + w.sql() + ` ORDER BY th.created_at DESC, th.t3_thread_id DESC LIMIT ` +
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
