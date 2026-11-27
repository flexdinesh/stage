// Package workevent defines events consumed by Stage notification delivery.
package workevent

import (
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// ObservationChange describes a notification derived from a committed sample.
type ObservationChange struct {
	Task    *TaskChange
	Session *SessionChange
	At      time.Time
}

type TaskChange struct {
	TaskID int64
	From   db.TaskState
	To     db.TaskState
	Detail string
}

// SessionChange is emitted by a harness-specific observer for a session on its connection.
type SessionChange struct {
	Session   *db.SessionRef
	ProjectID string
	Title     string
	From      *db.TaskState
	To        db.TaskState
}

func Notifiable(s db.TaskState) bool {
	return s == db.StateNeedsInput || s == db.StateDone || s == db.StateFailed
}
