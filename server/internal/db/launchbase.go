package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Launch base sources: predecessor (the worktree started from the
// predecessor's branch) or fallback (from the task's own baseRef; Reason
// says why).
const (
	LaunchBasePredecessor = "predecessor"
	LaunchBaseFallback    = "fallback"
)

// LaunchBase is the worktree base one launch attempt of a task with
// workspace.base "predecessor" used (docs/api.md "Predecessor base").
type LaunchBase struct {
	Session         *SessionRef `json:"session,omitempty"`
	Attempt         int         `json:"attempt"`
	BaseRef         string      `json:"baseRef"`
	StartFromOrigin bool        `json:"startFromOrigin"`
	Source          string      `json:"source"`
	SourceTaskID    *int64      `json:"sourceTaskId"`
	SourceThreadID  *string     `json:"sourceThreadId"`
	SourceTitle     string      `json:"sourceTitle"`
	Reason          *string     `json:"reason"` // why it fell back
	At              time.Time   `json:"at"`
}

// Detail is the launching task event: `base from #12's branch feat-x` or
// `base fallback origin/main: <reason>`.
func (b LaunchBase) Detail() string {
	if b.Source == LaunchBasePredecessor {
		return fmt.Sprintf("base from %s's branch %s", HandoffSource{TaskID: b.SourceTaskID, Title: b.SourceTitle}.Label(), b.BaseRef)
	}
	reason := ""
	if b.Reason != nil {
		reason = *b.Reason
	}
	return fmt.Sprintf("base fallback %s: %s", b.BaseRef, reason)
}

// RecordLaunchBase stores attempt's launch base (replacing an earlier record
// of the same attempt) and a launching task event describing it, in one
// transaction.
func (s *Store) RecordLaunchBase(ctx context.Context, taskID int64, b LaunchBase) error {
	return s.inTx(ctx, func(tx *Tx) error {
		var sourceID *int64
		if b.Session != nil {
			id, err := sessionIdentity(ctx, tx, *b.Session)
			if err != nil {
				return err
			}
			sourceID = &id
		}
		_, err := tx.Exec(ctx, `
INSERT INTO task_launch_bases (task_id, attempt, base_ref, start_from_origin, source, source_task_id, source_thread_id, source_title, reason, source_session_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (task_id, attempt) DO UPDATE SET
  base_ref = EXCLUDED.base_ref, start_from_origin = EXCLUDED.start_from_origin, source = EXCLUDED.source,
  source_task_id = EXCLUDED.source_task_id, source_thread_id = EXCLUDED.source_thread_id,
  source_title = EXCLUDED.source_title, reason = EXCLUDED.reason, source_session_id=excluded.source_session_id, created_at = `+nowSQL+``,
			taskID, b.Attempt, b.BaseRef, b.StartFromOrigin, b.Source, b.SourceTaskID, b.SourceThreadID, b.SourceTitle, b.Reason, sourceID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, 'launching', 'launching', $2)`,
			taskID, b.Detail())
		return err
	})
}

// GetLaunchBase is attempt's launch base record; nil when none was recorded.
func (s *Store) GetLaunchBase(ctx context.Context, taskID int64, attempt int) (*LaunchBase, error) {
	var b LaunchBase
	err := s.pool.QueryRow(ctx, `
SELECT attempt, base_ref, start_from_origin, source, source_task_id, source_thread_id, source_title, reason, created_at, (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=task_launch_bases.source_session_id)
FROM task_launch_bases WHERE task_id = $1 AND attempt = $2`, taskID, attempt).
		Scan(&b.Attempt, &b.BaseRef, &b.StartFromOrigin, &b.Source, &b.SourceTaskID, &b.SourceThreadID, &b.SourceTitle, &b.Reason, &b.At, &b.Session)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}
