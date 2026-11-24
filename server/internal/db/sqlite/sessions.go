package sqlite

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type sessions struct{ *runner }

func (t *tx) Sessions() store.Sessions { return sessions{t.r} }

func (r sessions) Ensure(ctx context.Context, connectionID int64, externalID string) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO session_identities(connection_id,external_id) VALUES($1,$2)
 ON CONFLICT(connection_id,external_id) DO UPDATE SET external_id=excluded.external_id RETURNING id`, connectionID, externalID).Scan(&id)
	return id, err
}

type connections struct{ *runner }

func (t *tx) Connections() store.Connections { return connections{t.r} }

func (r connections) Exists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}
