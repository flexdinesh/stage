package db

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

func (s *Store) chainRow(ctx context.Context, q pgx.Row) (Chain, error) {
	var c Chain
	if err := q.Scan(&c.ID, &c.Title, &c.State, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Chain{}, notFound(err)
	}
	steps, err := s.ChainSteps(ctx, c.ID)
	if err != nil {
		return Chain{}, err
	}
	c.Steps = steps
	return c, nil
}

func (s *Store) GetChain(ctx context.Context, id int64) (Chain, error) {
	return s.chainRow(ctx, s.pool.QueryRow(ctx,
		`SELECT id, title, state, created_at, updated_at FROM chains WHERE id = $1`, id))
}

// ListChains returns chains (updatedAt desc) with steps.
func (s *Store) ListChains(ctx context.Context, states ...ChainState) ([]Chain, error) {
	q := `SELECT id, title, state, created_at, updated_at FROM chains`
	var args []any
	if len(states) > 0 {
		ss := make([]string, len(states))
		for i, st := range states {
			ss[i] = string(st)
		}
		q += ` WHERE state = ANY($1)`
		args = append(args, ss)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY updated_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	var chains []Chain
	for rows.Next() {
		var c Chain
		if err := rows.Scan(&c.ID, &c.Title, &c.State, &c.CreatedAt, &c.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		chains = append(chains, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(chains) == 0 {
		return []Chain{}, nil
	}
	ids := make([]int64, len(chains))
	for i, c := range chains {
		ids[i] = c.ID
	}
	all, err := collectTasks(s.pool.Query(ctx,
		taskSelect+` WHERE t.chain_id = ANY($1) ORDER BY t.chain_position, t.id`, ids))
	if err != nil {
		return nil, err
	}
	byChain := map[int64][]Task{}
	for _, t := range all {
		if t.ChainID != nil {
			byChain[*t.ChainID] = append(byChain[*t.ChainID], t)
		}
	}
	for i := range chains {
		chains[i].Steps = byChain[chains[i].ID]
		if chains[i].Steps == nil {
			chains[i].Steps = []Task{}
		}
	}
	return chains, nil
}

// CreateChain inserts an idle chain with the given backlog tasks as steps.
func (s *Store) CreateChain(ctx context.Context, title string, steps []int64) (Chain, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO chains (title) VALUES ($1) RETURNING id`, title).Scan(&id); err != nil {
			return err
		}
		return setChainSteps(ctx, tx, id, steps)
	})
	if err != nil {
		return Chain{}, err
	}
	return s.GetChain(ctx, id)
}

// UpdateChain sets title and/or the full ordered step list.
func (s *Store) UpdateChain(ctx context.Context, id int64, title *string, steps []int64, setSteps bool) (Chain, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var state ChainState
		if err := tx.QueryRow(ctx, `SELECT state FROM chains WHERE id = $1 FOR UPDATE`, id).Scan(&state); err != nil {
			return notFound(err)
		}
		if setSteps {
			if err := setChainSteps(ctx, tx, id, steps); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE chains SET title = COALESCE($2, title), updated_at = now() WHERE id = $1`, id, title)
		return err
	})
	if err != nil {
		return Chain{}, err
	}
	return s.GetChain(ctx, id)
}

// setChainSteps replaces chain id's ordered steps. Added tasks must be
// backlog and not in another chain; removed tasks must be backlog;
// non-backlog steps keep their relative order. A done chain that gains a
// not-yet-passed step goes back to idle.
func setChainSteps(ctx context.Context, tx pgx.Tx, chainID int64, steps []int64) error {
	seen := map[int64]bool{}
	for _, id := range steps {
		if seen[id] {
			return Invalid("duplicate step %d", id)
		}
		seen[id] = true
	}
	type cur struct {
		id    int64
		state TaskState
	}
	rows, err := tx.Query(ctx,
		`SELECT id, state FROM tasks WHERE chain_id = $1 ORDER BY chain_position, id FOR UPDATE`, chainID)
	if err != nil {
		return err
	}
	var existing []cur
	for rows.Next() {
		var c cur
		if err := rows.Scan(&c.id, &c.state); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	inChain := map[int64]TaskState{}
	var oldLocked []int64 // non-backlog existing steps, in order
	for _, c := range existing {
		inChain[c.id] = c.state
		if c.state != StateBacklog {
			oldLocked = append(oldLocked, c.id)
			if !seen[c.id] {
				return Conflict("step %d is %s and cannot be removed", c.id, c.state)
			}
		}
	}
	var newLocked []int64
	for _, id := range steps {
		if st, ok := inChain[id]; ok {
			if st != StateBacklog {
				newLocked = append(newLocked, id)
			}
			continue
		}
		var state TaskState
		var other *int64
		if err := tx.QueryRow(ctx, `SELECT state, chain_id FROM tasks WHERE id = $1 FOR UPDATE`, id).Scan(&state, &other); err != nil {
			return fmt.Errorf("task %d: %w", id, notFound(err))
		}
		if other != nil {
			return Conflict("task %d is in another chain", id)
		}
		if state != StateBacklog {
			return Conflict("task %d is %s; only backlog tasks can be added", id, state)
		}
	}
	if !slices.Equal(oldLocked, newLocked) {
		return Conflict("non-backlog steps must keep their relative order")
	}
	for id := range inChain {
		if !seen[id] {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET chain_id = NULL, chain_position = NULL, updated_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
		}
	}
	for i, id := range steps {
		if _, err := tx.Exec(ctx,
			`UPDATE tasks SET chain_id = $2, chain_position = $3,
			   updated_at = CASE WHEN chain_id IS DISTINCT FROM $2 OR chain_position IS DISTINCT FROM $3 THEN now() ELSE updated_at END
			 WHERE id = $1`, id, chainID, i+1); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
UPDATE chains SET state = 'idle', updated_at = now()
WHERE id = $1 AND state = 'done'
  AND EXISTS (SELECT 1 FROM tasks WHERE chain_id = $1 AND state NOT IN ('settled', 'cancelled'))`, chainID)
	return err
}

// SetChainState moves a chain to `to` iff it is in one of `from`. Returns
// changed=false (and the current state) when not; ErrNotFound if missing.
func (s *Store) SetChainState(ctx context.Context, id int64, from []ChainState, to ChainState) (bool, ChainState, error) {
	fs := make([]string, len(from))
	for i, f := range from {
		fs[i] = string(f)
	}
	var cur ChainState
	err := s.pool.QueryRow(ctx,
		`UPDATE chains SET state = $3, updated_at = now() WHERE id = $1 AND state = ANY($2) RETURNING state`,
		id, fs, string(to)).Scan(&cur)
	if err == nil {
		return true, cur, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	if err := s.pool.QueryRow(ctx, `SELECT state FROM chains WHERE id = $1`, id).Scan(&cur); err != nil {
		return false, "", notFound(err)
	}
	return false, cur, nil
}
