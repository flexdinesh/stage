package sqlite

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type threads struct{ *runner }

func (t *tx) Threads() store.Threads { return threads{t.r} }

// The mirror's state follows T3 independently of frozen legacy task state.
// Input, activity, and failure outrank a stale settled flag.
const threadStateSQL = `COALESCE(th.execution_state,'done')`

// externalRunning: external threads observed running (dashboard,
// threads?running=1). Same scope as externalNeedsInput.
const externalRunning = `th.observed_state = 'running'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// externalSettled: external threads whose mirror state is settled
// (dashboard, threads?settledFrom=). Same scope as externalNeedsInput;
// archived threads included.
const externalSettled = `(` + threadStateSQL + `) = 'settled'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

func nonNilPRs(prs []execution.Artifact) []execution.Artifact {
	if prs == nil {
		return []execution.Artifact{}
	}
	return prs
}

const threadSelect = `
SELECT th.t3_thread_id, th.t3_project_id, COALESCE(tk.project_id,p.id), COALESCE((SELECT title FROM projects WHERE id=tk.project_id),p.title), th.title, th.status, th.settled,
  th.settled_at, th.pending_request_count, min(th.pending_question_count, th.pending_request_count), th.provider_instance_id, th.model, th.runtime_mode,
  th.interaction_mode, th.branch, th.worktree_path, th.parent_thread_id, th.relationship,
  th.creation_source, tk.id, th.link, th.created_at, th.updated_at, th.synced_at, th.archived, th.active_run_id, th.latest_run_id,
  th.run_started_at, th.run_ended_at, th.last_word_preview, th.last_word_at, th.run_count, th.pull_requests, ` + threadStateSQL + `,
  COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),0)
FROM threads th
LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),` + legacyT3SQL + `) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
LEFT JOIN tasks tk ON tk.session_id = th.session_id`

func scanThread(row rowScanner) (store.Thread, error) {
	var t store.Thread
	err := row.Scan(&t.T3ThreadID, &t.T3ProjectID, &t.ProjectID, &t.ProjectTitle, &t.Title, &t.Status,
		&t.Settled, &t.SettledAt, &t.PendingRequestCount, &t.PendingQuestionCount, &t.ProviderInstanceID, &t.Model, &t.RuntimeMode,
		&t.InteractionMode, &t.Branch, &t.WorktreePath, &t.ParentThreadID, &t.Relationship,
		&t.CreationSource, &t.TaskID, &t.Link, &t.CreatedAt, &t.UpdatedAt, &t.SyncedAt, &t.Archived, &t.ActiveRunID, &t.LatestRunID,
		&t.RunStartedAt, &t.RunEndedAt, &t.LastWordPreview, &t.LastWordAt, &t.RunCount, &t.PullRequests, &t.State, &t.ConnectionID)
	if err != nil {
		return store.Thread{}, notFound(err)
	}
	t.PullRequests = nonNilPRs(t.PullRequests)
	t.Session = store.SessionRef{ConnectionID: t.ConnectionID, ExternalSessionID: t.T3ThreadID}
	t.Source = store.SourceT3
	if t.TaskID != nil {
		t.Source = store.SourceApp
	}
	return t, nil
}

func (r threads) Runs(ctx context.Context, id string) ([]store.ThreadRun, error) {
	var runs []store.ThreadRun
	if err := r.QueryRow(ctx, `SELECT recent_runs FROM threads WHERE t3_thread_id = $1`, id).Scan(&runs); err != nil {
		return nil, notFound(err)
	}
	return runs, nil
}

func (r threads) Get(ctx context.Context, id string) (store.Thread, error) {
	return scanThread(r.QueryRow(ctx, threadSelect+` WHERE th.t3_thread_id = $1`, id))
}

func (r threads) List(ctx context.Context, f store.ThreadFilter) ([]store.Thread, int, error) {
	var w where
	if len(f.States) > 0 {
		w.add("(" + threadStateSQL + ") IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.States)) + "))")
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
		w.add("th.status IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.Statuses)) + "))")
	}
	if f.Settled != nil {
		w.add("th.settled = " + w.arg(*f.Settled))
	}
	if f.Provider != "" {
		w.add("th.provider_instance_id = " + w.arg(f.Provider))
	}
	switch f.Source {
	case store.SourceApp:
		w.add("tk.id IS NOT NULL")
	case store.SourceT3:
		w.add("tk.id IS NULL")
	}
	if f.Q != "" {
		w.add(likeSQL("th.title", w.arg(likePattern(f.Q))))
	}
	if f.IDs != nil {
		w.add("th.t3_thread_id IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.IDs)) + "))")
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
LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),` + legacyT3SQL + `) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
LEFT JOIN tasks tk ON tk.session_id = th.session_id` + w.sql()
	if err := r.QueryRow(ctx, countSQL, w.args...).Scan(&total); err != nil {
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
	rows, err := r.Query(ctx, q, w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []store.Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func (r threads) SetPendingQuestionCount(ctx context.Context, id string, n int) error {
	_, err := r.Exec(ctx, `UPDATE threads SET pending_question_count = $2 WHERE t3_thread_id = $1`, id, n)
	return err
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
SELECT i.id, th.observed_state, EXISTS (SELECT 1 FROM tasks tk WHERE tk.connection_id=`+legacyT3SQL+` AND tk.t3_thread_id = i.id)
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
  last_word_run_id = $6, last_word_item_id = $7, last_word_at = $8, last_word_checked_at = $2
WHERE t3_thread_id = $1 AND (last_word_checked_at IS NULL OR last_word_checked_at <= $2)`,
		id, checked, lw.Text, lw.Preview, lw.Truncated, lw.RunID, lw.ItemID, lw.At)
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

type stats struct{ *runner }

func (t *tx) Stats() store.Stats { return stats{t.r} }

func (r stats) ThreadTimes(ctx context.Context, from, to time.Time, excludeSubagents bool) ([]time.Time, error) {
	rows, err := r.Query(ctx, `
SELECT created_at FROM threads WHERE created_at >= $1 AND created_at < $2
  AND (NOT $3 OR parent_thread_id IS NULL)`, from, to, excludeSubagents)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[time.Time])
}

func (r stats) ThreadsByProject(ctx context.Context, from, to time.Time, excludeSubagents bool) ([]store.ProjectCount, error) {
	rows, err := r.Query(ctx, `
SELECT p.id, th.t3_project_id, COALESCE(p.title, th.t3_project_id), count(*) AS n
FROM threads th LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),`+legacyT3SQL+`) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
WHERE th.created_at >= $1 AND th.created_at < $2
  AND (NOT $3 OR th.parent_thread_id IS NULL)
GROUP BY p.id, th.t3_project_id, p.title ORDER BY n DESC, th.t3_project_id`, from, to, excludeSubagents)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.ProjectCount, error) {
		var c store.ProjectCount
		err := r.Scan(&c.ProjectID, &c.T3ProjectID, &c.Title, &c.Count)
		return c, err
	})
}

func (r stats) ThreadsByStatus(ctx context.Context, from, to time.Time, excludeSubagents bool) ([]store.StatusCount, error) {
	rows, err := r.Query(ctx, `
SELECT status, count(*) AS n FROM threads WHERE created_at >= $1 AND created_at < $2
  AND (NOT $3 OR parent_thread_id IS NULL)
GROUP BY status ORDER BY n DESC, status`, from, to, excludeSubagents)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.StatusCount, error) {
		var c store.StatusCount
		err := r.Scan(&c.Status, &c.Count)
		return c, err
	})
}

func (r stats) TasksByState(ctx context.Context) ([]store.StateCount, error) {
	rows, err := r.Query(ctx, `SELECT state, count(*) FROM tasks GROUP BY state ORDER BY state`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.StateCount, error) {
		var c store.StateCount
		err := r.Scan(&c.State, &c.Count)
		return c, err
	})
}
