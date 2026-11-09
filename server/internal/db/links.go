package db

import (
	"context"
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
// the task, is not settled or cancelled, has no other follower, and is not
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
	if predState == StateSettled || predState == StateCancelled {
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

// ReadyFollowers: backlog tasks whose predecessor is settled or cancelled
// (the link runner launches them).
func (s *Store) ReadyFollowers(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
SELECT f.id FROM tasks f JOIN tasks p ON p.id = f.after_task_id
WHERE f.state = 'backlog' AND p.state IN ('settled', 'cancelled') ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
