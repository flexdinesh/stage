//go:build postgres

package postgres

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

func (r tasks) RecordSession(ctx context.Context, taskID int64, attempt int, sessionID int64) error {
	_, err := r.Exec(ctx, `INSERT INTO task_sessions(task_id, attempt, session_id) VALUES($1,$2,$3)
 ON CONFLICT(task_id, attempt) DO UPDATE SET session_id=excluded.session_id, recorded_at=excluded.recorded_at
 WHERE task_sessions.session_id<>excluded.session_id`, taskID, attempt, sessionID)
	return err
}

const taskSessionSelect = `
SELECT ts.attempt, si.connection_id, si.external_id, COALESCE(t.session_id=ts.session_id, false),
       ts.recorded_at, hc.harness, COALESCE(o.observation, '{}'), o.observed_at
FROM task_sessions ts
JOIN tasks t ON t.id=ts.task_id
JOIN session_identities si ON si.id=ts.session_id
JOIN harness_connections hc ON hc.id=si.connection_id
LEFT JOIN execution_observations o ON o.session_id=ts.session_id
WHERE ts.task_id=$1`

func (r tasks) Sessions(ctx context.Context, taskID int64) ([]store.TaskSession, error) {
	rows, err := r.Query(ctx, taskSessionSelect+` ORDER BY ts.attempt DESC`, taskID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanTaskSession)
}

func (r tasks) SessionAttempt(ctx context.Context, taskID int64, attempt int) (store.TaskSession, error) {
	return scanTaskSession(r.QueryRow(ctx, taskSessionSelect+` AND ts.attempt=$2::bigint`, taskID, attempt))
}

func scanTaskSession(row rowScanner) (store.TaskSession, error) {
	var s store.TaskSession
	var receivedAt *time.Time
	err := row.Scan(&s.Attempt, &s.Session.ConnectionID, &s.Session.ExternalSessionID, &s.Current,
		&s.RecordedAt, &s.Harness, &s.Observation, &receivedAt)
	if s.Observation.CapturedAt.IsZero() && receivedAt != nil {
		s.Observation.CapturedAt = *receivedAt
	}
	return s, notFound(err)
}

func (r tasks) SessionOwner(ctx context.Context, ref store.SessionRef) (store.TaskSessionOwner, error) {
	var owner store.TaskSessionOwner
	err := r.QueryRow(ctx, `SELECT ts.task_id, ts.attempt FROM task_sessions ts
 JOIN session_identities si ON si.id=ts.session_id WHERE si.connection_id=$1 AND si.external_id=$2`,
		ref.ConnectionID, ref.ExternalSessionID).Scan(&owner.TaskID, &owner.Attempt)
	return owner, notFound(err)
}
