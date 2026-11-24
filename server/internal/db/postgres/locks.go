//go:build postgres

package postgres

import (
	"context"
	"slices"
)

// Row locks for read-check-write use cases (docs/storage.md "Transactions
// and locking"). FOR NO KEY UPDATE serializes writers of the row without
// blocking foreign-key checks (KEY SHARE) on it, so inserting a child row
// (an event, a need) never waits on the lock. Rows lock in ascending id;
// a missing row is skipped.

func lockRows(ctx context.Context, r *runner, table string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	rows, err := r.Query(ctx, `SELECT id FROM `+table+` WHERE id = ANY($1) ORDER BY id FOR NO KEY UPDATE`, ids)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

func (r tasks) Lock(ctx context.Context, ids ...int64) error {
	return lockRows(ctx, r.runner, "tasks", ids)
}

func (r trees) Lock(ctx context.Context, ids ...int64) error {
	return lockRows(ctx, r.runner, "task_trees", ids)
}

func (r schedules) Lock(ctx context.Context, id int64) error {
	return lockRows(ctx, r.runner, "schedules", []int64{id})
}

func (r machines) Lock(ctx context.Context, id int64) error {
	return lockRows(ctx, r.runner, "machines", []int64{id})
}

func (r connections) Lock(ctx context.Context, id int64) error {
	return lockRows(ctx, r.runner, "harness_connections", []int64{id})
}
