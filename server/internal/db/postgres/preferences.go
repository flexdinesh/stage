//go:build postgres

package postgres

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type preferences struct{ *runner }

func (t *tx) Preferences() store.Preferences { return preferences{t.r} }

func (r preferences) Get(ctx context.Context) (store.PreferencesRow, error) {
	var p store.PreferencesRow
	// A retired default (its machine removed, or replaced without a successor) reads as none.
	err := r.QueryRow(ctx, `SELECT (SELECT c.id FROM harness_connections c WHERE c.id=p.default_connection_id AND c.active), notify_events FROM preferences p WHERE id = 1`).
		Scan(&p.DefaultConnectionID, &p.NotifyEvents)
	return p, err
}

func (r preferences) SetNotifyEvents(ctx context.Context, events []string) error {
	_, err := r.Exec(ctx, `UPDATE preferences SET notify_events = $1, updated_at = `+nowSQL+` WHERE id = 1`, events)
	return err
}

func (r preferences) SetDefaultConnection(ctx context.Context, id *int64) error {
	_, err := r.Exec(ctx, `UPDATE preferences SET default_connection_id = $1, updated_at = `+nowSQL+` WHERE id = 1`, id)
	return err
}

type users struct{ *runner }

func (t *tx) Users() store.Users { return users{t.r} }

func (r users) Get(ctx context.Context, id int64) (store.User, error) {
	var u store.User
	err := r.QueryRow(ctx, `SELECT id,display_name,created_at FROM users WHERE id=$1`, id).Scan(&u.ID, &u.DisplayName, &u.CreatedAt)
	return u, notFound(err)
}
