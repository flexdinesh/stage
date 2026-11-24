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
