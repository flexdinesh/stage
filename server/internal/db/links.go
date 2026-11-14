package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// linksLock serializes link mutations (pg_advisory_xact_lock key).
const linksLock = 0x7433_6c6e // "t3ln"

func lockLinks(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(linksLock))
	return err
}

// setAfter links task id to run after `after` (nil unlinks). Linking rules
// (409 Conflict): the task is backlog; the predecessor exists (404), is not
// the task, is not settled, has no other follower, and is not
// downstream of the task (no cycles). Links may span projects.
func setAfter(ctx context.Context, tx pgx.Tx, id int64, after *int64) error {
	if err := lockLinks(ctx, tx); err != nil {
		return err
	}
	var state TaskState
	var cur *int64
	if err := tx.QueryRow(ctx, `SELECT state, after_task_id FROM tasks WHERE id = $1 FOR UPDATE`, id).Scan(&state, &cur); err != nil {
		return notFound(err)
	}
	if after == nil {
		_, err := tx.Exec(ctx, `UPDATE tasks SET after_task_id = NULL WHERE id = $1`, id)
		return err
	}
	if cur != nil && *cur == *after {
		return nil
	}
	if state != StateBacklog {
		return Conflict("task is %s: only a backlog task can be linked to run after another", state)
	}
	if *after == id {
		return Conflict("a task cannot run after itself")
	}
	if err := continuationConflict(ctx, tx, []int64{id, *after}); err != nil {
		return err
	}
	var predState TaskState
	var follower *int64
	err := tx.QueryRow(ctx,
		`SELECT p.state, (SELECT f.id FROM tasks f WHERE f.after_task_id = p.id) FROM tasks p WHERE p.id = $1`, *after,
	).Scan(&predState, &follower)
	if err != nil {
		return fmt.Errorf("task to run after: %w", notFound(err))
	}
	if predState == StateSettled {
		return Conflict("task %d is %s: start this task instead of linking it", *after, predState)
	}
	if follower != nil && *follower != id {
		return Conflict("task %d already has task %d running after it", *after, *follower)
	}
	var cycle bool
	if err := tx.QueryRow(ctx, `
WITH RECURSIVE down AS (
  SELECT id FROM tasks WHERE after_task_id = $1
  UNION SELECT t.id FROM tasks t JOIN down ON t.after_task_id = down.id
)
SELECT EXISTS (SELECT 1 FROM down WHERE id = $2)`, id, *after).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return Conflict("task %d already runs after this task (links cannot loop)", *after)
	}
	_, err = tx.Exec(ctx, `UPDATE tasks SET after_task_id = $2 WHERE id = $1`, id, *after)
	return err
}

// setAfterThread links task id to run after T3 thread threadID (nil unlinks
// the thread link). A thread that is a task's current thread becomes a task
// link (setAfter rules). Otherwise (409 Conflict): the task is backlog; the
// thread is mirrored (404) and has not passed (threadPassedSQL). Many
// tasks may wait on one thread.
func setAfterThread(ctx context.Context, tx pgx.Tx, id int64, threadID *string) error {
	if threadID == nil {
		_, err := tx.Exec(ctx, `UPDATE tasks SET after_thread_id = NULL WHERE id = $1`, id)
		return err
	}
	var owner int64
	err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE t3_thread_id = $1 ORDER BY id LIMIT 1`, *threadID).Scan(&owner)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, `UPDATE tasks SET after_thread_id = NULL WHERE id = $1`, id); err != nil {
			return err
		}
		return setAfter(ctx, tx, id, &owner)
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	var state TaskState
	if err := tx.QueryRow(ctx, `SELECT state FROM tasks WHERE id = $1 FOR UPDATE`, id).Scan(&state); err != nil {
		return notFound(err)
	}
	if state != StateBacklog {
		return Conflict("task is %s: only a backlog task can be linked to run after a session", state)
	}
	var passed bool
	err = tx.QueryRow(ctx, `SELECT `+threadPassedSQL+` FROM threads th WHERE th.t3_thread_id = $1`, *threadID).Scan(&passed)
	if err != nil {
		return fmt.Errorf("session to run after: %w", notFound(err))
	}
	if passed {
		return Conflict("session %s is settled: start this task instead of linking it", *threadID)
	}
	_, err = tx.Exec(ctx, `UPDATE tasks SET after_thread_id = $2, after_task_id = NULL WHERE id = $1`, id, *threadID)
	return err
}

// Sequence returns the linked run order containing task id (head first;
// just the task when unlinked).
func (s *Store) Sequence(ctx context.Context, id int64) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
WITH RECURSIVE up AS (
  SELECT id, after_task_id, 0 AS d FROM tasks WHERE id = $1
  UNION ALL SELECT t.id, t.after_task_id, up.d - 1 FROM tasks t JOIN up ON t.id = up.after_task_id WHERE up.d > -1000
), down AS (
  SELECT id, 0 AS d FROM tasks WHERE id = $1
  UNION ALL SELECT t.id, down.d + 1 FROM tasks t JOIN down ON t.after_task_id = down.id WHERE down.d < 1000
), seq AS (SELECT id, d FROM up UNION SELECT id, d FROM down)
`+taskSelect+` JOIN seq ON seq.id = t.id ORDER BY seq.d`, id)
	return collectTasks(rows, err)
}

// ReadyFollowers: backlog tasks whose predecessor task, or session (mirrored
// thread, threadPassedSQL), is settled (the link runner launches them).
func (s *Store) ReadyFollowers(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
SELECT f.id FROM tasks f JOIN tasks p ON p.id = f.after_task_id
WHERE f.state = 'backlog' AND p.state = 'settled'
UNION
SELECT f.id FROM tasks f JOIN threads th ON th.t3_thread_id = f.after_thread_id
WHERE f.state = 'backlog' AND `+threadPassedSQL+`
ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// LinkedThreadIDs: sessions that backlog tasks wait on and that have not
// passed (the watcher refreshes them, subagents included).
func (s *Store) LinkedThreadIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT f.after_thread_id FROM tasks f
LEFT JOIN threads th ON th.t3_thread_id = f.after_thread_id
WHERE f.state = 'backlog' AND f.after_thread_id IS NOT NULL
  AND (th.t3_thread_id IS NULL OR NOT `+threadPassedSQL+`)
ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
