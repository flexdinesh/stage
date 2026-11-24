package sqlite

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type push struct{ *runner }

func (t *tx) Push() store.Push { return push{t.r} }

func (r push) VAPIDKeys(ctx context.Context) (store.VAPIDKeys, error) {
	var k store.VAPIDKeys
	err := r.QueryRow(ctx, `SELECT public_key, private_key FROM push_vapid WHERE id = 1`).Scan(&k.PublicKey, &k.PrivateKey)
	return k, notFound(err)
}

func (r push) InsertVAPIDKeys(ctx context.Context, k store.VAPIDKeys) error {
	_, err := r.Exec(ctx, `INSERT INTO push_vapid (id, public_key, private_key) VALUES (1, $1, $2)
ON CONFLICT (id) DO NOTHING`, k.PublicKey, k.PrivateKey)
	return err
}

const pushSubSelect = `SELECT id, endpoint, p256dh, auth, user_agent, created_at, updated_at,
  last_success_at, last_error FROM push_subscriptions`

func scanPushSub(row rowScanner) (store.PushSubscription, error) {
	var p store.PushSubscription
	err := row.Scan(&p.ID, &p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent, &p.CreatedAt, &p.UpdatedAt,
		&p.LastSuccessAt, &p.LastError)
	return p, notFound(err)
}

func (r push) UpsertSubscription(ctx context.Context, endpoint, p256dh, auth, userAgent string) error {
	_, err := r.Exec(ctx, `
INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent) VALUES ($1, $2, $3, $4)
ON CONFLICT (endpoint) DO UPDATE SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
  user_agent = EXCLUDED.user_agent, updated_at = `+nowSQL+``, endpoint, p256dh, auth, userAgent)
	return err
}

func (r push) DeleteSubscription(ctx context.Context, endpoint string) error {
	_, err := r.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

func (r push) GetSubscription(ctx context.Context, endpoint string) (store.PushSubscription, error) {
	return scanPushSub(r.QueryRow(ctx, pushSubSelect+` WHERE endpoint = $1`, endpoint))
}

func (r push) ListSubscriptions(ctx context.Context) ([]store.PushSubscription, error) {
	rows, err := r.Query(ctx, pushSubSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.PushSubscription{}
	for rows.Next() {
		p, err := scanPushSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r push) RecordResult(ctx context.Context, endpoint string, errMsg *string) error {
	q := `UPDATE push_subscriptions SET last_success_at = ` + nowSQL + `, last_error = NULL WHERE endpoint = $1`
	args := []any{endpoint}
	if errMsg != nil {
		q = `UPDATE push_subscriptions SET last_error = $2 WHERE endpoint = $1`
		args = append(args, *errMsg)
	}
	_, err := r.Exec(ctx, q, args...)
	return err
}
