//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type journal struct{ *runner }

func (t *tx) Journal() store.Journal { return journal{t.r} }

const journalColumns = `id, kind, task_id, tree_id, source_task_id, (SELECT external_id FROM session_identities WHERE id=source_session_id), attempt, category, text, event_type, from_tree_id, to_tree_id, created_at, (SELECT jsonb_build_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=source_session_id), created_by_user_id, source_machine_id`

func scanJournal(r rowScanner) (store.JournalEntry, error) {
	var e store.JournalEntry
	err := r.Scan(&e.ID, &e.Kind, &e.TaskID, &e.TreeID, &e.SourceTaskID, &e.SourceThreadID, &e.Attempt, &e.Category, &e.Text, &e.EventType, &e.FromTreeID, &e.ToTreeID, &e.CreatedAt, &e.SourceSession, &e.CreatedBy, &e.SourceMachineID)
	return e, notFound(err)
}

func (r journal) OwnerRevision(ctx context.Context, o store.JournalOwner) (int64, error) {
	var rev int64
	if o.TreeID != nil {
		err := r.QueryRow(ctx, `SELECT membership_revision FROM task_trees WHERE id = $1`, o.TreeID).Scan(&rev)
		return rev, notFound(err)
	}
	err := r.QueryRow(ctx, `SELECT 0 FROM tasks WHERE id = $1`, o.TaskID).Scan(&rev)
	return rev, notFound(err)
}

func (r journal) Get(ctx context.Context, id int64) (store.JournalEntry, error) {
	return scanJournal(r.QueryRow(ctx, `SELECT `+journalColumns+` FROM journal_entries WHERE id = $1`, id))
}

func (r journal) NoteByRequest(ctx context.Context, o store.JournalOwner, requestID string) (store.JournalEntry, error) {
	return scanJournal(r.QueryRow(ctx, `SELECT `+journalColumns+` FROM journal_entries WHERE kind = 'note' AND task_id IS NOT DISTINCT FROM $1::bigint AND tree_id IS NOT DISTINCT FROM $2::bigint AND request_id = $3`, o.TaskID, o.TreeID, requestID))
}

func (r journal) InsertNote(ctx context.Context, o store.JournalOwner, in store.AppendNoteInput, sourceID *int64, createdBy int64, sourceMachine *int64) (store.JournalEntry, error) {
	return scanJournal(r.QueryRow(ctx, `INSERT INTO journal_entries (kind, task_id, tree_id, source_task_id, category, text, request_id,source_session_id,created_by_user_id,source_machine_id) VALUES ('note', $1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+journalColumns, o.TaskID, o.TreeID, in.SourceTaskID, in.Category, in.Text, in.RequestID, sourceID, createdBy, sourceMachine))
}

func (r journal) InsertEvent(ctx context.Context, e store.JournalEvent) error {
	_, err := r.Exec(ctx, `INSERT INTO journal_entries (kind, task_id, tree_id, source_task_id, source_session_id, attempt, event_type, text, from_tree_id, to_tree_id, created_at)
VALUES ('event', $1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10::timestamptz, `+nowSQL+`))`,
		e.TaskID, e.TreeID, e.SourceTaskID, e.SourceSessionID, e.Attempt, e.EventType, e.Text, e.FromTreeID, e.ToTreeID, e.At)
	return err
}

func (r journal) Read(ctx context.Context, o store.JournalOwner, q store.JournalQuery, before int64, limit int) ([]store.JournalEntry, error) {
	var w where
	if o.TaskID != nil {
		w.add("task_id = " + w.arg(*o.TaskID))
	} else {
		id := w.arg(*o.TreeID)
		w.add("(tree_id = " + id + " OR (kind = 'note' AND task_id IN (SELECT id FROM tasks WHERE tree_id = " + id + ")))")
	}
	if q.Kind != "all" {
		kind := "note"
		if q.Kind == "events" {
			kind = "event"
		}
		w.add("kind = " + w.arg(kind))
	}
	if q.Category != "" {
		w.add("category = " + w.arg(q.Category))
	}
	if q.Q != "" {
		w.add(searchJournal(&w, q.Q))
	}
	if q.TaskID != nil {
		p := w.arg(*q.TaskID)
		w.add("(task_id = " + p + " OR source_task_id = " + p + ")")
	}
	if before > 0 {
		w.add("id < " + w.arg(before))
	}
	l := w.arg(limit)
	rows, err := r.Query(ctx, `SELECT `+journalColumns+` FROM journal_entries`+w.sql()+` ORDER BY id DESC LIMIT `+l, w.args...)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanJournal)
}

func (r journal) Duplicated(ctx context.Context, dst, src, createdBy int64, sourceMachine *int64) error {
	_, err := r.Exec(ctx, `INSERT INTO journal_entries (kind, task_id, source_task_id, event_type, text, created_by_user_id, source_machine_id) VALUES ('event', $1, $2, 'task_duplicated', 'Duplicated from task #' || $2::bigint::text, $3, $4)`, dst, src, createdBy, sourceMachine)
	return err
}

func (r journal) LaunchContext(ctx context.Context, taskID int64, attempt int) (string, error) {
	var result string
	err := r.QueryRow(ctx, `SELECT instructions FROM journal_launch_context WHERE task_id = $1 AND attempt = $2`, taskID, attempt).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return result, err
}

func (r journal) SetLaunchContext(ctx context.Context, taskID int64, attempt int, instructions string) error {
	_, err := r.Exec(ctx, `INSERT INTO journal_launch_context (task_id, attempt, instructions) VALUES ($1,$2,$3)`, taskID, attempt, instructions)
	return err
}
