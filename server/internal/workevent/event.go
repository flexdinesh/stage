// Package workevent defines events consumed by Stage notification delivery.
package workevent

import "github.com/flexdinesh/stage/server/internal/db"

type TaskChange struct {
	TaskID int64
	From   db.TaskState
	To     db.TaskState
	Detail string
}

// SessionChange is emitted by a harness-specific observer. IDs are scoped
// to the configured harness; connection identities arrive before a second harness.
type SessionChange struct {
	Session   *db.SessionRef
	ThreadID  string
	ProjectID string
	Title     string
	From      *db.TaskState
	To        db.TaskState
}

func Notifiable(s db.TaskState) bool {
	return s == db.StateNeedsInput || s == db.StateDone || s == db.StateFailed || s == db.StateSettled
}
