package store

import (
	"context"
	"time"
)

// Notification hold (ADR 0013): a tree member's needs_input notification
// waits while the tree's steerer can answer it.

// HoldState is a notification hold's state.
type HoldState string

const (
	// HoldHeld: waiting (at most one per task).
	HoldHeld HoldState = "held"
	// HoldReleased: notified early because the steerer could no longer answer.
	HoldReleased HoldState = "released"
	// HoldCancelled: answered or left needs_input first; nothing notified.
	HoldCancelled HoldState = "cancelled"
	// HoldEscalated: notified because the steerer did not answer in time or
	// escalated it.
	HoldEscalated HoldState = "escalated"
)

// TaskHold is Task JSON's hold: the task's held notification.
type TaskHold struct {
	Until time.Time `json:"until"`
	// SteererTaskID: the steerer that can answer (nil: its task was deleted).
	SteererTaskID *int64 `json:"steererTaskId"`
}

// NewHold is a held row.
type NewHold struct {
	TaskID        int64
	StateRevision int64
	SteererID     int64
	HeldAt, Until time.Time
	Attribution   Attribution
}

// Hold is a notification_holds row.
type Hold struct {
	ID            int64
	TaskID        int64
	StateRevision int64
	SteererID     int64
	HeldAt, Until time.Time
	State         HoldState
}

// HeldHold is a held row with what the release loop decides on.
type HeldHold struct {
	Hold
	TaskState        TaskState
	TaskRevision     int64
	AnsweredRevision *int64
	// TaskTreeID: the member's tree now (nil: it left every tree).
	TaskTreeID   *int64
	ConnectionID int64
	// SteererTreeID: the tree of the hold's binding.
	SteererTreeID int64
	SteererEnded  bool
	// SteererTaskID and SteererState are nil when the steerer task was deleted.
	SteererTaskID *int64
	SteererState  *TaskState
}

// Holds is the notification holds.
type Holds interface {
	Insert(ctx context.Context, h NewHold) (int64, error)
	// Get is hold id (ErrNotFound: none).
	Get(ctx context.Context, id int64) (Hold, error)
	// HeldForTask is the task's held row (ErrNotFound: none).
	HeldForTask(ctx context.Context, taskID int64) (Hold, error)
	// Held lists every held row, oldest first.
	Held(ctx context.Context) ([]HeldHold, error)
	// Finish moves held row id to state (false: it was not held).
	Finish(ctx context.Context, id int64, state HoldState, reason *string, notificationID *int64) (bool, error)
	// CancelTask cancels the task's held row with reason; with revision,
	// only one held at that state revision.
	CancelTask(ctx context.Context, taskID int64, reason string, revision *int64) error
}
