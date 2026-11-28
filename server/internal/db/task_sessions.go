package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type TaskSession = store.TaskSession

func (s *Store) TaskSessions(ctx context.Context, taskID int64) ([]TaskSession, error) {
	return read(ctx, s, func(tx store.Tx) ([]TaskSession, error) { return tx.Tasks().Sessions(ctx, taskID) })
}

func (s *Store) TaskSession(ctx context.Context, taskID int64, attempt int) (TaskSession, error) {
	return read(ctx, s, func(tx store.Tx) (TaskSession, error) { return tx.Tasks().SessionAttempt(ctx, taskID, attempt) })
}

// TaskSessionOwner includes past attempts. TaskBySession remains the
// current-session lookup used for live observation and coordination.
func (s *Store) TaskSessionOwner(ctx context.Context, ref SessionRef) (store.TaskSessionOwner, error) {
	return read(ctx, s, func(tx store.Tx) (store.TaskSessionOwner, error) { return tx.Tasks().SessionOwner(ctx, ref) })
}

func (s *Store) recordTaskSession(ctx context.Context, tx store.Tx, taskID int64, attempt int, sessionID int64) error {
	err := tx.Tasks().RecordSession(ctx, taskID, attempt, sessionID)
	if err != nil && s.e.Errors().IsUniqueViolation(err) {
		return Conflict("session already belongs to a task attempt")
	}
	return err
}
