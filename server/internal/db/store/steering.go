package store

import (
	"context"
	"encoding/json"
	"time"
)

// Tree steering (ADR 0013): one steerer binding per tree and its durable
// event outbox.

// SteeringEndReason is why a binding ended.
type SteeringEndReason string

const (
	SteeringReplaced     SteeringEndReason = "replaced"
	SteeringStopped      SteeringEndReason = "stopped"
	SteeringTreeFinished SteeringEndReason = "tree_finished"
	SteeringTaskRemoved  SteeringEndReason = "task_removed"
)

// Steerer is an active binding row (tree_steerers).
type Steerer struct {
	ID     int64
	TreeID int64
	TaskID int64
}

// SteererDelivery is a binding's delivery progress (written by the dispatcher).
type SteererDelivery struct {
	DeliveredThrough int64      `json:"deliveredThrough"`
	LastDeliveredAt  *time.Time `json:"lastDeliveredAt"`
	LastError        *string    `json:"lastError"`
}

// TreeSteerer is Tree JSON's steerer: the active binding with its task.
type TreeSteerer struct {
	TaskID int64     `json:"taskId"`
	Title  string    `json:"title"`
	State  TaskState `json:"state"`
	Since  time.Time `json:"since"`
	SteererDelivery
	// Pending counts the binding's events after DeliveredThrough not caused
	// by the steerer itself (delivery skips those).
	Pending int64 `json:"pending"`
}

// TaskSteering is Task JSON's steering: the task's latest binding.
type TaskSteering struct {
	TreeID    int64              `json:"treeId"`
	Active    bool               `json:"active"`
	EndedAt   *time.Time         `json:"endedAt,omitempty"`
	EndReason *SteeringEndReason `json:"endReason,omitempty"`
}

// SteeringBrief is Task detail's currentSteering: the steering brief the
// current launch attempt appended.
type SteeringBrief struct {
	Attempt int    `json:"attempt"`
	Brief   string `json:"brief"`
}

// NewSteeringEvent is one outbox row.
type NewSteeringEvent struct {
	TreeID    int64
	SteererID int64
	Kind      string
	TaskID    *int64
	Payload   map[string]any
	// CausedBy: the caller session and its task (nil: none).
	CausedBy     *SessionRef
	CausedByTask *int64
}

// SteeringEvent is a feed row.
type SteeringEvent struct {
	ID        int64           `json:"id"`
	TreeID    int64           `json:"treeId"`
	SteererID int64           `json:"steererId"`
	Kind      string          `json:"kind"`
	TaskID    *int64          `json:"taskId"`
	Payload   json.RawMessage `json:"payload"`
	CausedBy  *SteeringCause  `json:"causedBy"`
	// Self: caused by the binding's steerer task.
	Self      bool      `json:"self"`
	CreatedAt time.Time `json:"createdAt"`
}

// SteeringCause is who caused an event.
type SteeringCause struct {
	Session *SessionRef `json:"session"`
	TaskID  *int64      `json:"taskId"`
}

// SteererState is an active binding as the delivery dispatcher sees it.
type SteererState struct {
	ID, TreeID, TaskID int64
	DeliveredThrough   int64
	LastError          *string
	// FinishEventID is the binding's tree_finished event (nil: the tree has
	// not finished).
	FinishEventID *int64
}

// SteeringPending summarizes a binding's undelivered events (after
// DeliveredThrough) not caused by its steerer task.
type SteeringPending struct {
	Count           int64
	FirstID, LastID int64
}

// SteererCompletion is a binding that ended with its tree after the final
// digest was sent, awaiting the steerer task's auto-complete decision.
type SteererCompletion struct {
	ID, TreeID, TaskID int64
	// FinalStateRevision is the steerer task's state revision before the
	// final digest was sent.
	FinalStateRevision int64
}

// Steerer completion decisions (tree_steerers.completion).
const (
	SteererCompleted = "completed"
	SteererSkipped   = "skipped"
)

// SteeringRequest is a replayable set-steerer request.
type SteeringRequest struct {
	SteererID int64
	TaskID    *int64
	Digest    string
}

// Steering is the steerer bindings and the outbox.
type Steering interface {
	// Active is the tree's active binding (ErrNotFound: none).
	Active(ctx context.Context, treeID int64) (Steerer, error)
	// ActiveByTask is the binding the task actively steers (ErrNotFound: none).
	ActiveByTask(ctx context.Context, taskID int64) (Steerer, error)
	// ActiveForTrees maps each tree with an active binding to its steerer.
	ActiveForTrees(ctx context.Context, treeIDs []int64) (map[int64]TreeSteerer, error)
	// Request is the binding requestID created on tree (ErrNotFound: none).
	Request(ctx context.Context, treeID int64, requestID string) (SteeringRequest, error)
	// Bind inserts an active binding (a unique violation: one is active).
	Bind(ctx context.Context, treeID, taskID int64, requestID, digest string, a Attribution) (int64, error)
	// End ends binding id with reason (false: not active).
	End(ctx context.Context, id int64, reason SteeringEndReason) (bool, error)
	// LockEvents serializes event id allocation for tree until commit, so a
	// per-tree cursor never skips a lower id committed later (no-op on
	// SQLite's single writer).
	LockEvents(ctx context.Context, treeID int64) error
	InsertEvent(ctx context.Context, e NewSteeringEvent) (int64, error)
	// Events are tree's events after id after, ascending, at most limit.
	Events(ctx context.Context, treeID, after int64, limit int) ([]SteeringEvent, error)
	// LaunchBrief is the steering brief frozen for the task's launch
	// attempt (ErrNotFound: none).
	LaunchBrief(ctx context.Context, taskID int64, attempt int) (string, error)
	// SetLaunchBrief freezes brief for the attempt (a unique violation: one is frozen).
	SetLaunchBrief(ctx context.Context, taskID int64, attempt int, brief string) error

	// Delivery (the steering dispatcher).

	// ActiveStates are every active binding's delivery state, by id.
	ActiveStates(ctx context.Context) ([]SteererState, error)
	// ActiveState is active binding id's delivery state (ErrNotFound: none
	// or ended).
	ActiveState(ctx context.Context, id int64) (SteererState, error)
	// Pending summarizes binding id's events after after not caused by
	// its steerer task.
	Pending(ctx context.Context, id, after int64) (SteeringPending, error)
	// SetDelivered moves active binding id's delivered_through up to
	// through; delivered also stamps last_delivered_at and clears
	// last_error (false: not active).
	SetDelivered(ctx context.Context, id, through int64, delivered bool) (bool, error)
	// SetError sets (nil: clears) active binding id's last_error.
	SetError(ctx context.Context, id int64, msg *string) error
	// MarkFinish records binding id's tree_finished event (false: already
	// recorded).
	MarkFinish(ctx context.Context, id, eventID int64) (bool, error)
	// SetFinalRevision records the steerer's state revision before the
	// final digest.
	SetFinalRevision(ctx context.Context, id, revision int64) error
	// AwaitingCompletion are bindings ended tree_finished with a final
	// revision and no completion decision, by id.
	AwaitingCompletion(ctx context.Context) ([]SteererCompletion, error)
	// SetCompletion records binding id's decision (false: already decided).
	SetCompletion(ctx context.Context, id int64, completion string) (bool, error)
	// MembersAccepted reports whether tree has members and every one has
	// current acceptance.
	MembersAccepted(ctx context.Context, treeID int64) (bool, error)
}
