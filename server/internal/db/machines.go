package db

import "context"

// Machine runs harnesses: their connections, workspaces and processes.
// One local machine exists until workers do (migration 0015).
type Machine struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	CreatedBy int64  `json:"createdBy"` // user who added it (the local machine: the default user)
}

// GetMachine reads a machine.
func (s *Store) GetMachine(ctx context.Context, id int64) (Machine, error) {
	var m Machine
	err := s.pool.QueryRow(ctx, `SELECT id,label,created_by_user_id FROM machines WHERE id=$1`, id).Scan(&m.ID, &m.Label, &m.CreatedBy)
	return m, notFound(err)
}
