//go:build postgres

package postgres

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

const pushSubSelect = `SELECT p.id, COALESCE(s.user_id, 1), COALESCE(p.auth_session_id, 0),
  COALESCE(p.binding, ''), p.endpoint, p.p256dh, p.auth, p.user_agent, p.created_at, p.updated_at,
  p.last_success_at, p.last_error
FROM push_subscriptions p
JOIN installation_access i ON i.id = 1
LEFT JOIN auth_sessions s ON s.id = p.auth_session_id
LEFT JOIN users u ON u.id = s.user_id
WHERE ((i.mode = 'personal' AND p.auth_session_id IS NULL) OR (i.mode = 'claimed' AND ` + activePushSession + `))`

const activePushSession = `s.audience = 'browser' AND s.revoked_at IS NULL AND u.status = 'active'
  AND s.expires_at > ` + nowSQL + ` AND (s.idle_expires_at IS NULL OR s.idle_expires_at > ` + nowSQL + `)`

func scanPushSub(row rowScanner) (store.PushSubscription, error) {
	var p store.PushSubscription
	err := row.Scan(&p.ID, &p.UserID, &p.SessionID, &p.Binding, &p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent,
		&p.CreatedAt, &p.UpdatedAt, &p.LastSuccessAt, &p.LastError)
	return p, notFound(err)
}

func (r push) UpsertSubscription(ctx context.Context, sub store.PushSubscription) error {
	res, err := r.Exec(ctx, `
WITH eligible AS (
  SELECT s.id FROM auth_sessions s JOIN users u ON u.id = s.user_id
  WHERE s.id = $5 AND `+activePushSession+` FOR SHARE OF s
)
INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent, auth_session_id, binding)
SELECT $1, $2, $3, $4, NULLIF(CAST($5 AS BIGINT), 0), NULLIF($6, '')
FROM installation_access i WHERE i.id = 1 AND (
  (i.mode = 'personal' AND $5 = 0) OR (i.mode = 'claimed' AND EXISTS (SELECT 1 FROM eligible))
)
ON CONFLICT (endpoint) DO UPDATE SET
  p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent,
  auth_session_id = EXCLUDED.auth_session_id,
  binding = CASE WHEN push_subscriptions.auth_session_id = EXCLUDED.auth_session_id
    AND push_subscriptions.p256dh = EXCLUDED.p256dh AND push_subscriptions.auth = EXCLUDED.auth
    THEN push_subscriptions.binding ELSE EXCLUDED.binding END,
  last_success_at = CASE WHEN push_subscriptions.auth_session_id = EXCLUDED.auth_session_id
    OR EXCLUDED.auth_session_id IS NULL THEN push_subscriptions.last_success_at END,
  last_error = CASE WHEN push_subscriptions.auth_session_id = EXCLUDED.auth_session_id
    OR EXCLUDED.auth_session_id IS NULL THEN push_subscriptions.last_error END,
  updated_at = `+nowSQL+`
WHERE COALESCE(push_subscriptions.auth_session_id, 0) = $5
  OR (push_subscriptions.p256dh = EXCLUDED.p256dh AND push_subscriptions.auth = EXCLUDED.auth)`,
		sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent, sub.SessionID, sub.Binding)
	if err == nil && res.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}

func (r push) DeleteSubscription(ctx context.Context, sessionID int64, endpoint string) error {
	_, err := r.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1 AND COALESCE(auth_session_id, 0) = $2`, endpoint, sessionID)
	return err
}

func (r push) GetSubscription(ctx context.Context, sessionID int64, endpoint string) (store.PushSubscription, error) {
	return scanPushSub(r.QueryRow(ctx, pushSubSelect+` AND p.endpoint = $1 AND COALESCE(p.auth_session_id, 0) = $2`, endpoint, sessionID))
}

func (r push) GetBinding(ctx context.Context, sessionID int64, binding string) (store.PushSubscription, error) {
	return scanPushSub(r.QueryRow(ctx, pushSubSelect+` AND p.binding = $1 AND p.auth_session_id = $2`, binding, sessionID))
}

func (r push) ListSubscriptions(ctx context.Context) ([]store.PushSubscription, error) {
	rows, err := r.Query(ctx, pushSubSelect+` ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanPushSub)
}

func (r push) PruneInactiveBindings(ctx context.Context) error {
	_, err := r.Exec(ctx, `DELETE FROM push_subscriptions WHERE auth_session_id IS NOT NULL
AND auth_session_id NOT IN (
	SELECT s.id FROM auth_sessions s JOIN users u ON u.id = s.user_id WHERE `+activePushSession+`
)`)
	return err
}

const pushBindingWhere = ` WHERE id = $1 AND COALESCE(binding, '') = $2`

func (r push) DeleteExpired(ctx context.Context, sub store.PushSubscription) error {
	_, err := r.Exec(ctx, `DELETE FROM push_subscriptions`+pushBindingWhere, sub.ID, sub.Binding)
	return err
}

func (r push) RecordResult(ctx context.Context, sub store.PushSubscription, errMsg *string) error {
	if errMsg != nil {
		_, err := r.Exec(ctx, `UPDATE push_subscriptions SET last_error = $3`+pushBindingWhere, sub.ID, sub.Binding, *errMsg)
		return err
	}
	_, err := r.Exec(ctx, `UPDATE push_subscriptions SET last_success_at = `+nowSQL+`, last_error = NULL`+pushBindingWhere, sub.ID, sub.Binding)
	return err
}
