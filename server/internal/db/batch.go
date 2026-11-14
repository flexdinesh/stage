package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreatedTasks is a session batch: the tasks one T3 session created
// (origin_thread_id), grouped by run order.
type CreatedTasks struct {
	// Chains: maximal runs of batch tasks linked by run order, head first;
	// ordered by head id.
	Chains [][]Task `json:"chains"`
	// Independent: batch tasks with no batch predecessor or follower (they
	// may still wait on a task outside the batch, a session or a time).
	Independent []Task `json:"independent"`
}

// CreatedBy returns the batch created by T3 session threadID.
func (s *Store) CreatedBy(ctx context.Context, threadID string) (CreatedTasks, error) {
	rows, err := s.pool.Query(ctx, taskSelect+` WHERE t.origin_thread_id = $1 ORDER BY t.id`, threadID)
	tasks, err := collectTasks(rows, err)
	if err != nil {
		return CreatedTasks{}, err
	}
	return GroupBatch(tasks), nil
}

// GroupBatch groups tasks (ordered by id) into chains and independent tasks.
// A link to a task outside the set does not join a chain.
func GroupBatch(tasks []Task) CreatedTasks {
	byID := make(map[int64]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	in := func(id *int64) bool {
		if id == nil {
			return false
		}
		_, ok := byID[*id]
		return ok
	}
	out := CreatedTasks{Chains: [][]Task{}, Independent: []Task{}}
	for _, t := range tasks {
		switch {
		case in(t.AfterTaskID):
			// Not a head: its chain starts upstream.
		case in(t.NextTaskID):
			chain := []Task{t}
			for cur := t; in(cur.NextTaskID); {
				cur = byID[*cur.NextTaskID]
				chain = append(chain, cur)
			}
			out.Chains = append(out.Chains, chain)
		default:
			out.Independent = append(out.Independent, t)
		}
	}
	return out
}

// orderRow is a task's run-order state for OrderTasks.
type orderRow struct {
	state       TaskState
	after       *int64
	afterThread *string
	startAt     *time.Time
	startTz     *string
}

// hasOutsideWait: the task waits on a time, a session or a task not in set.
func (r orderRow) hasOutsideWait(set map[int64]orderRow) bool {
	if r.startAt != nil || r.afterThread != nil {
		return true
	}
	if r.after == nil {
		return false
	}
	_, inside := set[*r.after]
	return !inside
}

// OrderTasks links ids to run in that order (ids[i] after ids[i-1]) in one
// transaction. Rules (docs/api.md "Session batch"): at least two distinct
// tasks (Invalid), all backlog (Conflict); links between listed tasks are
// replaced; followers lose their start time and session wait; ids[0] keeps
// its own wait. When the listed tasks already form exactly one chain
// (reorder), a new head without a wait takes the old head's wait, and the
// old tail's follower outside the list moves to the new tail. Each link
// follows setAfter rules. Returns the resulting run order (head first).
func (s *Store) OrderTasks(ctx context.Context, ids []int64) ([]Task, error) {
	if len(ids) < 2 {
		return nil, Invalid("taskIds: list at least two tasks")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, Invalid("taskIds: task %d is listed twice", id)
		}
		seen[id] = true
	}
	err := s.inTx(ctx, func(tx *Tx) error {
		set := make(map[int64]orderRow, len(ids))
		rows, err := tx.Query(ctx,
			`SELECT id, state, after_task_id, after_thread_id, start_at, start_tz FROM tasks WHERE id IN (SELECT value FROM json_each($1)) ORDER BY id`, jsonArray(ids))
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var r orderRow
			if err := rows.Scan(&id, &r.state, &r.after, &r.afterThread, &r.startAt, &r.startTz); err != nil {
				rows.Close()
				return err
			}
			set[id] = r
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			r, ok := set[id]
			if !ok {
				return fmt.Errorf("task %d: %w", id, ErrNotFound)
			}
			if r.state != StateBacklog {
				return Conflict("task %d is %s: only backlog tasks can be ordered", id, r.state)
			}
		}

		// Existing chain: n-1 links between listed tasks (each task has at
		// most one predecessor and one follower, and links never loop).
		internal := 0
		oldHead := ids[0]
		for _, id := range ids {
			if r := set[id]; r.after != nil {
				if _, ok := set[*r.after]; ok {
					internal++
					continue
				}
			}
			oldHead = id
		}
		isChain := internal == len(ids)-1
		var tailFollower *int64
		if isChain {
			var oldTail int64
			for _, id := range ids {
				var f *int64
				if err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE after_task_id = $1`, id).Scan(&f); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if f == nil {
					oldTail = id
					break
				}
				if _, ok := set[*f]; !ok {
					oldTail, tailFollower = id, f
					break
				}
			}
			if tailFollower != nil && oldTail == ids[len(ids)-1] {
				tailFollower = nil // already after the new tail
			}
		}

		headWait := set[ids[0]]
		if !headWait.hasOutsideWait(set) {
			headWait = orderRow{}
			if isChain {
				headWait = set[oldHead]
			}
		}
		if headWait.after != nil {
			if _, inside := set[*headWait.after]; inside {
				headWait.after = nil
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE tasks SET after_task_id = NULL, after_thread_id = NULL, start_at = NULL, start_tz = NULL,
  updated_at = `+nowSQL+` WHERE id IN (SELECT value FROM json_each($1))`, jsonArray(ids)); err != nil {
			return err
		}
		if tailFollower != nil {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET after_task_id = NULL WHERE id = $1`, *tailFollower); err != nil {
				return err
			}
		}
		head := ids[0]
		switch {
		case headWait.after != nil:
			if err := setAfter(ctx, tx, head, headWait.after); err != nil {
				return err
			}
		case headWait.afterThread != nil:
			if _, err := tx.Exec(ctx, `UPDATE tasks SET after_thread_id = $2 WHERE id = $1`, head, *headWait.afterThread); err != nil {
				return err
			}
		case headWait.startAt != nil:
			if _, err := tx.Exec(ctx, `UPDATE tasks SET start_at = $2, start_tz = $3 WHERE id = $1`, head, *headWait.startAt, headWait.startTz); err != nil {
				return err
			}
		}
		for i := 1; i < len(ids); i++ {
			if err := setAfter(ctx, tx, ids[i], &ids[i-1]); err != nil {
				return err
			}
		}
		if tailFollower != nil {
			tail := ids[len(ids)-1]
			if err := setAfter(ctx, tx, *tailFollower, &tail); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE tasks SET updated_at = `+nowSQL+` WHERE id = $1`, *tailFollower); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Sequence(ctx, ids[0])
}
