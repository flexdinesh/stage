package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/SherClockHolmes/webpush-go"
)

// VAPIDKeys is the server's Web Push key pair (base64url).
type VAPIDKeys struct {
	PublicKey  string
	PrivateKey string
}

// VAPIDKeys returns the stored key pair, generating it on first use.
// Concurrent first calls agree (insert-if-absent, then read).
func (s *Store) VAPIDKeys(ctx context.Context) (VAPIDKeys, error) {
	var k VAPIDKeys
	err := s.pool.QueryRow(ctx, `SELECT public_key, private_key FROM push_vapid WHERE id = 1`).Scan(&k.PublicKey, &k.PrivateKey)
	if err == nil {
		return k, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return k, err
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO push_vapid (id, public_key, private_key) VALUES (1, $1, $2)
ON CONFLICT (id) DO NOTHING`, pub, priv); err != nil {
		return k, err
	}
	err = s.pool.QueryRow(ctx, `SELECT public_key, private_key FROM push_vapid WHERE id = 1`).Scan(&k.PublicKey, &k.PrivateKey)
	return k, err
}

// PushSubscription is a browser Web Push subscription.
type PushSubscription struct {
	ID            int64
	Endpoint      string
	P256dh        string
	Auth          string
	UserAgent     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastSuccessAt *time.Time
	LastError     *string
}

const pushSubSelect = `SELECT id, endpoint, p256dh, auth, user_agent, created_at, updated_at,
  last_success_at, last_error FROM push_subscriptions`

func scanPushSub(row rowScanner) (PushSubscription, error) {
	var p PushSubscription
	err := row.Scan(&p.ID, &p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent, &p.CreatedAt, &p.UpdatedAt,
		&p.LastSuccessAt, &p.LastError)
	return p, notFound(err)
}

// UpsertPushSubscription inserts or refreshes (keys, user agent) by endpoint.
func (s *Store) UpsertPushSubscription(ctx context.Context, endpoint, p256dh, auth, userAgent string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent) VALUES ($1, $2, $3, $4)
ON CONFLICT (endpoint) DO UPDATE SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
  user_agent = EXCLUDED.user_agent, updated_at = `+nowSQL+``, endpoint, p256dh, auth, userAgent)
	return err
}

// DeletePushSubscription removes a subscription (no error when unknown).
func (s *Store) DeletePushSubscription(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

// GetPushSubscription returns ErrNotFound for an unknown endpoint.
func (s *Store) GetPushSubscription(ctx context.Context, endpoint string) (PushSubscription, error) {
	return scanPushSub(s.pool.QueryRow(ctx, pushSubSelect+` WHERE endpoint = $1`, endpoint))
}

func (s *Store) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.pool.Query(ctx, pushSubSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSubscription{}
	for rows.Next() {
		p, err := scanPushSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecordPushResult stores a send outcome: errMsg nil = success.
func (s *Store) RecordPushResult(ctx context.Context, endpoint string, errMsg *string) error {
	q := `UPDATE push_subscriptions SET last_success_at = ` + nowSQL + `, last_error = NULL WHERE endpoint = $1`
	args := []any{endpoint}
	if errMsg != nil {
		q = `UPDATE push_subscriptions SET last_error = $2 WHERE endpoint = $1`
		args = append(args, *errMsg)
	}
	_, err := s.pool.Exec(ctx, q, args...)
	return err
}

// externalNeedsInput: external threads needing input (badge,
// threads?needsInput=1). Needs th + LEFT JOIN tasks tk. Subagents excluded,
// forks included.
const externalNeedsInput = `th.observed_state = 'needs_input'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// externalRunning: external threads observed running (dashboard,
// threads?running=1). Same scope as externalNeedsInput.
const externalRunning = `th.observed_state = 'running'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// externalSettled: external threads whose mirror state is settled
// (dashboard, threads?settledFrom=). Same scope as externalNeedsInput;
// archived threads included.
const externalSettled = `(` + threadStateSQL + `) = 'settled'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// NeedsInputCount: tasks in needs_input + external threads needing input.
func (s *Store) NeedsInputCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM tasks WHERE state = 'needs_input')
  + (SELECT count(*) FROM threads th LEFT JOIN tasks tk ON tk.session_id = th.session_id
     WHERE `+externalNeedsInput+` AND NOT EXISTS (SELECT 1 FROM execution_observations o WHERE o.session_id=th.session_id))
  + (SELECT count(*) FROM execution_observations o WHERE json_extract(o.observation,'$.state')='needs_input'
     AND json_extract(o.observation,'$.child')=0 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.session_id=o.session_id))`).Scan(&n)
	return n, err
}

// WatchedThreadIDs: external non-subagent threads the watcher last saw running,
// needing input or done (re-read until they leave those states; done until
// observed again, including when work resumes).
func (s *Store) WatchedThreadIDs(ctx context.Context, connection int64) ([]string, error) {
	// Scoped to the connection: a replaced T3's threads are never watched
	// against the T3 that replaced it.
	rows, err := s.pool.Query(ctx, `
SELECT th.t3_thread_id FROM threads th
WHERE (th.observed_state IN ('running', 'needs_input', 'done') OR (`+threadStateSQL+`) IN ('running','needs_input'))
  AND th.relationship IS NOT 'subagent'
  AND (th.session_id IN (SELECT id FROM session_identities WHERE connection_id=$1)
    OR (th.session_id IS NULL AND $1=`+activeT3SQL+`))
ORDER BY th.created_at`, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ThreadObservation is the watcher's stored view of a thread.
type ThreadObservation struct {
	ObservedState *TaskState // nil = never observed (or not mirrored)
	TaskOwned     bool       // a task has this thread
}

// ThreadObservations returns an entry for every id.
func (s *Store) ThreadObservations(ctx context.Context, ids []string) (map[string]ThreadObservation, error) {
	rows, err := s.pool.Query(ctx, `
SELECT i.id, th.observed_state, EXISTS (SELECT 1 FROM tasks tk WHERE tk.connection_id=`+activeT3SQL+` AND tk.t3_thread_id = i.id)
FROM (SELECT value AS id FROM json_each($1)) AS i LEFT JOIN threads th ON th.t3_thread_id = i.id`, jsonArray(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ThreadObservation, len(ids))
	for rows.Next() {
		var id string
		var o ThreadObservation
		if err := rows.Scan(&id, &o.ObservedState, &o.TaskOwned); err != nil {
			return nil, err
		}
		out[id] = o
	}
	return out, rows.Err()
}

// SetObservedState records the watcher's mapped state for a mirrored thread.
func (s *Store) SetObservedState(ctx context.Context, threadID string, st TaskState) error {
	_, err := s.pool.Exec(ctx, `UPDATE threads SET observed_state = $2 WHERE t3_thread_id = $1`, threadID, string(st))
	return err
}
