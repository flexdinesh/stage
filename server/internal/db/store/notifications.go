package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// SessionRef names a harness session: its connection and external id.
type SessionRef = execution.SessionRef

// Notification is an inbox entry (docs/api.md "Notifications inbox").
type Notification struct {
	ConnectionID int64      `json:"connectionId"`
	ID           int64      `json:"id"`
	Event        TaskState  `json:"event"` // needs_input | ready | done | failed | settled
	TaskID       *int64     `json:"taskId"`
	T3ThreadID   *string    `json:"externalSessionId"` // external thread, or the task's thread
	Title        string     `json:"title"`
	ProjectTitle *string    `json:"projectTitle"`
	URL          string     `json:"url"`
	CreatedAt    time.Time  `json:"createdAt"`
	ReadAt       *time.Time `json:"readAt"`
	// CurrentState: the task's state, or for an external thread `settled`
	// when settled, else its observed state. nil = unknown (deleted/unobserved).
	CurrentState *TaskState `json:"currentState"`
	// PendingQuestionCount: answerable questions the task's or external
	// thread's session is waiting on now.
	PendingQuestionCount int `json:"-"`
}

// NewNotification is what the notifier records.
type NewNotification struct {
	Session      *SessionRef
	Event        TaskState
	TaskID       *int64
	T3ThreadID   *string
	Title        string
	ProjectTitle *string
	URL          string
}

// NotificationGroup summarizes all retained notifications for one task/thread.
type NotificationGroup struct {
	Notification
	Key         string `json:"key"`
	EventCount  int    `json:"eventCount"`
	UnreadCount int    `json:"unreadCount"`
}

// NotificationCursor follows priority (currently waiting first), then newest ID.
type NotificationCursor struct {
	Waiting bool
	ID      int64
}

// WaitingSession is current external attention without a recorded inbox event.
// It has no event ID or read state: reading it cannot manufacture a notification.
type WaitingSession struct {
	ConnectionID int64   `json:"connectionId"`
	ThreadID     string  `json:"externalSessionId"`
	Title        string  `json:"title"`
	ProjectTitle *string `json:"projectTitle"`
}

// Notifications is the inbox repository. A group key is
// task:<id> | thread:<connection>:<external id> | notification:<id>.
type Notifications interface {
	// Insert stores n with its resolved session identity and returns its id.
	Insert(ctx context.Context, n NewNotification, sessionID *int64) (int64, error)
	// List returns up to limit entries, newest first, with id < beforeID
	// when set, only of group key when set.
	List(ctx context.Context, limit int, beforeID *int64, key *string) ([]Notification, error)
	// After returns up to limit entries with id > after, ascending; newest
	// takes the newest ones (still returned descending).
	After(ctx context.Context, after int64, limit int, newest bool) ([]Notification, error)
	ReadState(ctx context.Context) (latestID int64, unread int, err error)
	// Groups aggregates entries with id <= upToID before the cursor/limit.
	Groups(ctx context.Context, limit int, cursor *NotificationCursor, upToID int64) ([]NotificationGroup, error)
	UnreadCount(ctx context.Context) (int, error)
	MarkRead(ctx context.Context, upTo int64) error
	MarkGroupsRead(ctx context.Context, groupKeys []string) error
	// UnreadKeys returns the distinct group keys with unread entries, sorted.
	UnreadKeys(ctx context.Context) ([]string, error)
	// Prune deletes entries created before t.
	Prune(ctx context.Context, before time.Time) (int64, error)
	// Waiting lists external sessions needing input with no entry <= upToID.
	Waiting(ctx context.Context, upToID int64) ([]WaitingSession, error)
}

// Sessions is the session identity repository (the T3 mirror and session
// identities grow here).
type Sessions interface {
	// Ensure returns the identity of (connection, external id), creating it.
	Ensure(ctx context.Context, connectionID int64, externalID string) (int64, error)
}

// Connections is the harness connections repository.
type Connections interface {
	Exists(ctx context.Context, id int64) (bool, error)
}
