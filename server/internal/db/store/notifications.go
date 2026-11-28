package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// SessionRef names a harness session: its connection and external id.
type SessionRef = execution.SessionRef

// NotificationEvent is what an inbox entry reports (docs/api.md
// "Notification rules"). Ids are stable on the wire and in storage.
type NotificationEvent string

const (
	// EventNeedsInput: a task or untracked session waits on the user.
	EventNeedsInput NotificationEvent = "needs_input"
	// EventFailed: a task's or untracked session's run failed.
	EventFailed NotificationEvent = "failed"
	// EventReady: a manual tree member whose needs are all completed.
	EventReady NotificationEvent = "ready"
	// EventCompleted: a task was accepted by inference or by an agent
	// (never a user's own completion).
	EventCompleted NotificationEvent = "completed"
	// EventTreeCompleted: the last member of an open tree was accepted by
	// inference or by an agent.
	EventTreeCompleted NotificationEvent = "tree_completed"
	// EventScheduleStarted: a cron fire spawned a task.
	EventScheduleStarted NotificationEvent = "schedule_started"
	// EventScheduleSkipped: a cron fire was skipped or missed.
	EventScheduleSkipped NotificationEvent = "schedule_skipped"
	// EventDone: an untracked session's run finished ("Session finished").
	// Older task rows also carry it ("Run finished").
	EventDone NotificationEvent = "done"
)

// NotificationEvents: every event, in display order.
var NotificationEvents = []NotificationEvent{
	EventNeedsInput, EventFailed, EventReady, EventCompleted,
	EventTreeCompleted, EventScheduleStarted, EventScheduleSkipped, EventDone,
}

// DefaultNotifyEvents: the filter of an account that never saved one
// (every event but schedule_started).
var DefaultNotifyEvents = []NotificationEvent{
	EventNeedsInput, EventFailed, EventReady, EventCompleted,
	EventTreeCompleted, EventScheduleSkipped, EventDone,
}

// Notification is an inbox entry (docs/api.md "Notifications inbox").
type Notification struct {
	ConnectionID int64             `json:"connectionId"`
	ID           int64             `json:"id"`
	Event        NotificationEvent `json:"event"`
	TaskID       *int64            `json:"taskId"`
	// TreeID and ScheduleID name a tree_completed or schedule_* entry's
	// subject (nil otherwise, or once it is deleted).
	TreeID            *int64     `json:"treeId"`
	ScheduleID        *int64     `json:"scheduleId"`
	ExternalSessionID *string    `json:"externalSessionId"` // the session's external id: the task's, else the notification's
	Title             string     `json:"title"`
	ProjectTitle      *string    `json:"projectTitle"`
	URL               string     `json:"url"`
	CreatedAt         time.Time  `json:"createdAt"`
	ReadAt            *time.Time `json:"readAt"`
	// CurrentState: the task's state or an external session's observed state. nil = unknown (deleted/unobserved).
	CurrentState *TaskState `json:"currentState"`
	// Note: why a held notification was released ("Steerer did not answer").
	Note *string `json:"note"`
}

// NewNotification is what the notifier records.
type NewNotification struct {
	Session      *SessionRef
	Event        NotificationEvent
	TaskID       *int64
	TreeID       *int64
	ScheduleID   *int64
	Title        string
	ProjectTitle *string
	URL          string
	ObservedAt   *time.Time // observation time for created_at
	Note         *string
}

// NotificationGroup summarizes all retained notifications for one task or session.
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
	ConnectionID      int64   `json:"connectionId"`
	ExternalSessionID string  `json:"externalSessionId"`
	Title             string  `json:"title"`
	ProjectTitle      *string `json:"projectTitle"`
	// Directory is the session's working folder when the harness reports
	// one.
	Directory *string `json:"directory"`
}

// Notifications is the inbox repository. A group key is task:<id> |
// tree:<id> | schedule:<id> | session:<connection>:<external id> |
// notification:<id>.
type Notifications interface {
	// Insert stores n with its resolved session identity and returns its id.
	Insert(ctx context.Context, n NewNotification, sessionID *int64) (int64, error)
	// InsertRecipients records unread receipts for an event in the same write transaction.
	InsertRecipients(ctx context.Context, id, ownerID int64) error
	// ForDelivery returns ErrNotFound when the user has no unread receipt for id.
	ForDelivery(ctx context.Context, userID, id int64) (Notification, error)
	// List returns up to limit entries, newest first, with id < beforeID
	// when set, only of group key when set.
	List(ctx context.Context, userID int64, limit int, beforeID *int64, key *string) ([]Notification, error)
	// After returns up to limit entries with id > after, ascending; newest
	// takes the newest ones in the same order.
	After(ctx context.Context, userID, after int64, limit int, newest bool) ([]Notification, error)
	ReadState(ctx context.Context, userID int64) (latestID int64, unread int, err error)
	// Groups aggregates entries with id <= upToID before the cursor/limit.
	Groups(ctx context.Context, userID int64, limit int, cursor *NotificationCursor, upToID int64) ([]NotificationGroup, error)
	UnreadCount(ctx context.Context, userID int64) (int, error)
	MarkRead(ctx context.Context, userID, upTo int64) error
	MarkGroupsRead(ctx context.Context, userID int64, groupKeys []string) error
	// UnreadKeys returns the distinct group keys with unread entries, sorted.
	UnreadKeys(ctx context.Context, userID int64) ([]string, error)
	// Prune deletes entries created before t.
	Prune(ctx context.Context, before time.Time) (int64, error)
	// Waiting lists external sessions needing input with no entry <= upToID.
	Waiting(ctx context.Context, upToID int64) ([]WaitingSession, error)
}

// Sessions is the session identity repository (observations and session
// identities grow here).
type Sessions interface {
	// Ensure returns the identity of (connection, external id), creating it.
	Ensure(ctx context.Context, connectionID int64, externalID string) (int64, error)
	// Ref reads an identity (ErrNotFound when unknown).
	Ref(ctx context.Context, id int64) (SessionRef, error)
}
