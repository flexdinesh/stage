package sqlite

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type threads struct{ *runner }

func (t *tx) Threads() store.Threads { return threads{t.r} }

// The mirror's state follows T3 independently of frozen legacy task state.
// Input, activity, and failure outrank a stale settled flag.
const threadStateSQL = `COALESCE(th.execution_state,'done')`

const threadSelect = `
SELECT th.t3_thread_id, th.t3_project_id, COALESCE(tk.project_id,p.id), COALESCE((SELECT title FROM projects WHERE id=tk.project_id),p.title), th.title, th.status, th.settled,
  th.settled_at, th.pending_request_count, th.model, th.runtime_mode,
  th.interaction_mode, th.branch, th.worktree_path, th.parent_thread_id, th.relationship,
  tk.id, th.created_at, th.updated_at, th.synced_at, th.archived, th.active_run_id, th.latest_run_id,
  th.run_started_at, th.run_ended_at, th.last_word_preview, th.last_word_at, ` + threadStateSQL + `,
  COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),0)
FROM threads th
LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),` + legacyT3SQL + `) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
LEFT JOIN tasks tk ON tk.session_id = th.session_id`

func scanThread(row rowScanner) (store.Thread, error) {
	var t store.Thread
	err := row.Scan(&t.T3ThreadID, &t.T3ProjectID, &t.ProjectID, &t.ProjectTitle, &t.Title, &t.Status,
		&t.Settled, &t.SettledAt, &t.PendingRequestCount, &t.Model, &t.RuntimeMode,
		&t.InteractionMode, &t.Branch, &t.WorktreePath, &t.ParentThreadID, &t.Relationship,
		&t.TaskID, &t.CreatedAt, &t.UpdatedAt, &t.SyncedAt, &t.Archived, &t.ActiveRunID, &t.LatestRunID,
		&t.RunStartedAt, &t.RunEndedAt, &t.LastWordPreview, &t.LastWordAt, &t.State, &t.ConnectionID)
	if err != nil {
		return store.Thread{}, notFound(err)
	}
	t.Session = store.SessionRef{ConnectionID: t.ConnectionID, ExternalSessionID: t.T3ThreadID}
	t.Source = store.SourceT3
	if t.TaskID != nil {
		t.Source = store.SourceApp
	}
	return t, nil
}

func (r threads) Get(ctx context.Context, id string) (store.Thread, error) {
	return scanThread(r.QueryRow(ctx, threadSelect+` WHERE th.t3_thread_id = $1`, id))
}

func (r threads) NeedsInputCount(ctx context.Context) (int, error) {
	var n int
	err := r.QueryRow(ctx, `
SELECT (SELECT count(*) FROM tasks WHERE state = 'needs_input')
  + (SELECT count(*) FROM threads th LEFT JOIN tasks tk ON tk.session_id = th.session_id
     WHERE `+externalNeedsInput+` AND NOT EXISTS (SELECT 1 FROM execution_observations o WHERE o.session_id=th.session_id))
  + (SELECT count(*) FROM execution_observations o WHERE json_extract(o.observation,'$.state')='needs_input'
     AND json_extract(o.observation,'$.child')=0 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.session_id=o.session_id))`).Scan(&n)
	return n, err
}

func (r threads) WatchedIDs(ctx context.Context, connection int64) ([]string, error) {
	// Scoped to the connection: a replaced T3's threads are never watched
	// against the T3 that replaced it.
	rows, err := r.Query(ctx, `
SELECT th.t3_thread_id FROM threads th
WHERE (th.observed_state IN ('running', 'needs_input', 'done') OR (`+threadStateSQL+`) IN ('running','needs_input'))
  AND th.relationship IS NOT 'subagent'
  AND (th.session_id IN (SELECT id FROM session_identities WHERE connection_id=$1)
    OR (th.session_id IS NULL AND $1=`+legacyT3SQL+`))
ORDER BY th.created_at`, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r threads) Observations(ctx context.Context, ids []string) (map[string]store.ThreadObservation, error) {
	rows, err := r.Query(ctx, `
SELECT i.id, th.observed_state, EXISTS (SELECT 1 FROM tasks tk WHERE tk.session_id = th.session_id)
FROM (SELECT value AS id FROM json_each($1)) AS i LEFT JOIN threads th ON th.t3_thread_id = i.id`, jsonArray(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]store.ThreadObservation, len(ids))
	for rows.Next() {
		var id string
		var o store.ThreadObservation
		if err := rows.Scan(&id, &o.ObservedState, &o.TaskOwned); err != nil {
			return nil, err
		}
		out[id] = o
	}
	return out, rows.Err()
}

func (r threads) SetObservedState(ctx context.Context, ref store.SessionRef, st store.TaskState) error {
	_, err := r.Exec(ctx, `UPDATE threads SET observed_state = $2 WHERE t3_thread_id = $1
 AND (session_id IN (SELECT id FROM session_identities WHERE connection_id=$3)
   OR (session_id IS NULL AND $3=`+legacyT3SQL+`))`, ref.ExternalSessionID, string(st), ref.ConnectionID)
	return err
}

func (r threads) LastWordCheckedAt(ctx context.Context, id string) (*time.Time, error) {
	var at *time.Time
	err := r.QueryRow(ctx, `SELECT last_word_checked_at FROM threads WHERE t3_thread_id = $1`, id).Scan(&at)
	return at, notFound(err)
}

func (r threads) SetLastWord(ctx context.Context, id string, checked time.Time, lw *store.LastWordRow) error {
	if lw == nil {
		_, err := r.Exec(ctx, `
UPDATE threads SET last_word_checked_at = $2
WHERE t3_thread_id = $1 AND (last_word_checked_at IS NULL OR last_word_checked_at <= $2)`, id, checked)
		return err
	}
	_, err := r.Exec(ctx, `
UPDATE threads SET last_word_text = $3, last_word_preview = $4, last_word_truncated = $5,
  last_word_run_id = $6, last_word_at = $7, last_word_checked_at = $2
WHERE t3_thread_id = $1 AND (last_word_checked_at IS NULL OR last_word_checked_at <= $2)`,
		id, checked, lw.Text, lw.Preview, lw.Truncated, lw.RunID, lw.At)
	return err
}

func (r threads) LastWord(ctx context.Context, id string) (*string, store.LastWord, error) {
	var (
		text *string
		lw   store.LastWord
	)
	err := r.QueryRow(ctx, `
SELECT last_word_text, last_word_truncated, last_word_run_id, last_word_at
FROM threads WHERE t3_thread_id = $1`, id).Scan(&text, &lw.Truncated, &lw.RunID, &lw.At)
	return text, lw, notFound(err)
}
