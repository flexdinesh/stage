package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// StartMode is when a tree member launches once its needs settle: auto
// (the release runner launches it) or manual (it reads ready).
type StartMode string

const (
	StartAuto   StartMode = "auto"
	StartManual StartMode = "manual"
)

// StartModes are the valid start modes.
var StartModes = []string{string(StartAuto), string(StartManual)}

func (m StartMode) Valid() bool { return slices.Contains(StartModes, string(m)) }

// TreeStatus is a tree's lifecycle (docs/api.md "Done, archive and delete"):
// open, done (marked done by hand) or archived. A tree that is not open
// releases nothing, rejects structural changes and its members leave the
// Dashboard; activity on a member reopens it.
type TreeStatus string

const (
	TreeOpen     TreeStatus = "open"
	TreeDone     TreeStatus = "done"
	TreeArchived TreeStatus = "archived"
)

// TreeStatuses are the valid tree statuses.
var TreeStatuses = []string{string(TreeOpen), string(TreeDone), string(TreeArchived)}

func (s TreeStatus) Valid() bool { return slices.Contains(TreeStatuses, string(s)) }

// Readiness of a backlog tree member: blocked (a need is not accepted) or
// ready (every need accepted, no start time, and manual or no needs).
type Readiness string

const (
	ReadinessBlocked Readiness = "blocked"
	ReadinessReady   Readiness = "ready"
)

type Task struct {
	ExecutionConfig *execution.Config `json:"executionConfig"`
	Launch          *Attempt          `json:"launch,omitempty"`
	ConnectionID    int64             `json:"connectionId"`
	BindingID       *int64            `json:"bindingId"`
	Workspace       *TaskWorkspace    `json:"workspace"`
	// Harness of the connection; ConnectionActive is false once its
	// instance was replaced (ConnectionLabel names that instance).
	Harness                 string      `json:"harness"`
	ConnectionActive        bool        `json:"connectionActive"`
	ConnectionLabel         string      `json:"connectionLabel"`
	SessionIdentityID       *int64      `json:"-"`
	OriginSessionIdentityID *int64      `json:"-"`
	Session                 *SessionRef `json:"session"`
	OriginSession           *SessionRef `json:"originSession"`
	AcceptanceID            *int64      `json:"acceptanceId"`
	AcceptanceRevision      int64       `json:"acceptanceRevision"`
	CompletedAt             *time.Time  `json:"completedAt"`
	CompletionProvenance    *string     `json:"completionProvenance"`
	ID                      int64       `json:"id"`
	ProjectID               int64       `json:"projectId"`
	ProjectTitle            string      `json:"projectTitle"`
	Title                   string      `json:"title"`
	Prompt                  string      `json:"prompt"`
	State                   TaskState   `json:"state"`
	// ExternalSessionID is the session's external id (nil without a session).
	ExternalSessionID *string `json:"externalSessionId"`
	TreeID            *int64  `json:"treeId"` // the task's tree (docs/api.md "Trees")
	TreeTitle         *string `json:"treeTitle"`
	// StartMode is the task's own start mode (nil = inherit the tree's);
	// EffectiveStartMode applies the tree's (nil outside a tree without one).
	StartMode          *StartMode `json:"startMode"`
	EffectiveStartMode *StartMode `json:"effectiveStartMode"`
	// Readiness: blocked | ready for a backlog tree member, else nil.
	Readiness  *Readiness `json:"readiness"`
	Needs      []int64    `json:"needs"`      // tasks this one needs (same tree), by id
	Dependents []int64    `json:"dependents"` // tasks that need this one, by id
	// Adopted: the task tracks an existing harness session (prompt ""); it is never
	// started, restarted or duplicated by stage.
	Adopted            bool       `json:"adopted"`
	ReadyAt            *time.Time `json:"readyAt"`    // a manual task's needs all accepted (ready notification episode)
	StartAt            *time.Time `json:"startAt"`    // one-shot start of a backlog task (scheduler launches it)
	StartTz            *string    `json:"startTz"`    // IANA zone startAt was entered in
	ScheduleID         *int64     `json:"scheduleId"` // recurring schedule that spawned the task
	ScheduleTitle      *string    `json:"scheduleTitle"`
	OriginSessionTitle *string    `json:"originSessionTitle"` // the origin session's mirrored title (null when not mirrored)
	Handoff            bool       `json:"handoff"`            // append the needs' handoff at launch (docs/api.md "Handoff")
	LaunchAttempt      int        `json:"launchAttempt"`
	LastError          *string    `json:"lastError"`
	LaunchedAt         *time.Time `json:"launchedAt"`
	FinishedAt         *time.Time `json:"finishedAt"`
	RunStartedAt       *time.Time `json:"runStartedAt"` // session's elapsed run span
	RunEndedAt         *time.Time `json:"runEndedAt"`
	LastWordPreview    *string    `json:"lastWordPreview"` // session's latest assistant reply, plain one-line preview
	LastWordAt         *time.Time `json:"lastWordAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	// CreatedBy: the user who created the task (a schedule's creator for a
	// task it spawned).
	CreatedBy int64 `json:"createdBy"`
	// SourceMachineID: the machine whose agent gateway forwarded the create
	// or adoption (nil: not through a gateway, or spawned by a schedule).
	SourceMachineID *int64    `json:"sourceMachineId"`
	UpdatedAt       time.Time `json:"updatedAt"`
	// StateSince: when the task entered its current state (latest task event
	// into it; nil without one).
	StateSince  *time.Time   `json:"stateSince"`
	Attachments []Attachment `json:"attachments"` // images, upload order
	// ArchivedAt: when the task was archived (nil = not archived; docs/api.md
	// "Done, archive and delete").
	ArchivedAt *time.Time `json:"archivedAt"`
	// TreeStatus: its tree's status (nil outside a tree).
	TreeStatus *TreeStatus `json:"treeStatus"`
	// Deletable: no notes or execution history; otherwise archive only.
	Deletable        bool       `json:"deletable"`
	SessionUpdatedAt *time.Time `json:"-"` // mirrored session's updatedAt (tree activity)
}

type TaskEvent struct {
	Attribution
	ID        int64      `json:"id"`
	TaskID    int64      `json:"taskId"`
	FromState *TaskState `json:"fromState"`
	ToState   TaskState  `json:"toState"`
	Detail    string     `json:"detail"`
	At        time.Time  `json:"at"`
}

type Attempt struct {
	Attribution
	TaskID     int64                `json:"taskId"`
	Number     int                  `json:"attempt"`
	Intent     *execution.Intent    `json:"-"`
	Checkpoint execution.Checkpoint `json:"-"`
	Phase      string               `json:"phase"`
	Revision   int64                `json:"revision"`
	Error      string               `json:"reason"`
	RetryAt    *time.Time           `json:"retryAt"`
	RetryCount int                  `json:"retryCount"`
	// Session is what the harness returned for this unconfirmed attempt when
	// Stage could not record it yet; nil when none was seen.
	Session *ObservedSession `json:"session"`
}

// ObservedSession is a session a harness reported for a launch attempt before
// Stage recorded it. Links are filled at the API edge; only the resume
// command (Links.Command) is stored as a fallback when recording the
// observation failed.
type ObservedSession struct {
	SessionRef
	Title string           `json:"title"`
	State string           `json:"state"`
	Links *execution.Links `json:"links,omitempty"`
}

// Handoff statuses: sent (the block went out with the launch prompt) or
// skipped (the need's read failed; the task launched without its block).
const (
	HandoffSent    = "sent"
	HandoffSkipped = "skipped"
)

// Handoff is what one launch attempt appended to its prompt for one need
// (docs/api.md "Handoff").
type Handoff struct {
	Session      *SessionRef `json:"session,omitempty"`
	Attempt      int         `json:"attempt"`
	Status       string      `json:"status"`
	SourceTaskID *int64      `json:"sourceTaskId"`
	SourceTitle  string      `json:"sourceTitle"`
	Text         string      `json:"text"`   // "" when skipped
	Reason       *string     `json:"reason"` // why skipped
	At           time.Time   `json:"at"`
}

// HandoffSource is a need a launch reads: a task (TaskID set; its current
// session). Records from before trees may name a session (TaskID nil).
type HandoffSource struct {
	TaskID *int64
	Title  string
}

// Label names the source in event details and the block: `#12` or
// `session "Title"`.
func (s HandoffSource) Label() string {
	if s.TaskID != nil {
		return fmt.Sprintf("#%d", *s.TaskID)
	}
	return fmt.Sprintf("session %q", s.Title)
}

// TaskWorkspace describes the workspace projected for a task or schedule.
type TaskWorkspace struct {
	BindingID  int64  `json:"bindingId"`
	Path       string `json:"path"`
	Source     string `json:"source"`               // pinned, chosen or primary
	ExternalID string `json:"externalId,omitempty"` // from the pinned launch destination
}

type LaunchDestination struct {
	ConnectionID      int64
	ExternalProjectID string
	Workspace         string
	BindingID         int64
	BindingRevision   int64
}

type TaskFilter struct {
	OriginSession *SessionRef
	ProjectID     *int64
	States        []string
	// Q is the Search capability (docs/storage.md "Search"): a
	// case-insensitive substring of title or prompt, every character
	// literal; it filters without changing the order.
	Q             string
	From, To      *time.Time
	CompletedFrom *time.Time // accepted_at >= CompletedFrom
	// Closed: "" or "false" (open only: not archived, tree open or none),
	// "true" (archived, or in a done or archived tree) or "all".
	Closed string
}

// Transition is a guarded task state change.
type Transition struct {
	RequireAuto bool // release runner: recheck policy while claiming the launch
	TaskID      int64
	From        []TaskState // allowed current states; empty = any
	To          TaskState
	Detail      string

	BeginAttempt  bool        // launch start: attempt+1 and a new launch_request_id, clear session/error/finished/start/ready_at
	Session       *SessionRef // set the session (+ launched_at)
	ObservedStart *time.Time  // first native execution of an adopted backlog session
	ObservedAt    *time.Time  // observation time for the event and first finish
	LastError     *string     // set last_error
}

// TransitionResult reports what happened. Changed=false: current state not
// in From (or already To); nothing written.
type TransitionResult struct {
	Attribution
	Changed bool
	From    TaskState
	Attempt int
	// RequestID is the attempt's clientRequestId (launch_request_id); ""
	// before the first launch.
	RequestID string
}

// TaskInsert is a new backlog task's columns (Handoff nil = on).
type TaskInsert struct {
	ProjectID       int64
	BindingID       *int64
	Title           string
	Prompt          string
	StartAt         *time.Time
	StartTz         *string
	ScheduleID      *int64
	Handoff         *bool
	StartMode       *StartMode
	ConnectionID    int64
	OriginSessionID *int64
	ExecutionConfig *execution.Config
	CreatedBy       int64
	SourceMachineID *int64
}

// AdoptedInsert is an adopted session's task columns (launch attempt 1).
type AdoptedInsert struct {
	ProjectID       int64
	Title           string
	State           TaskState
	ConnectionID    int64
	SessionID       int64
	ExecutionConfig execution.Config
	LaunchedAt      *time.Time
	FinishedAt      *time.Time
	CreatedBy       int64
	SourceMachineID *int64
}

// TaskEditState is what an edit checks before updating a task.
type TaskEditState struct {
	State           TaskState
	ExecutionConfig *execution.Config
	Adopted         bool
	ConnectionID    int64
	ProjectID       int64
	BindingID       *int64
	Needs           int
}

// TaskUpdate is the columns an edit sets (unset fields are kept;
// ExecutionConfig and StartMode Null clears).
type TaskUpdate struct {
	ConnectionID    Optional[int64]
	BindingID       Optional[int64]
	ExecutionConfig Optional[execution.Config]
	ProjectID       Optional[int64]
	Title           Optional[string]
	Prompt          Optional[string]
	Handoff         Optional[bool]
	StartMode       Optional[StartMode]
}

// TaskLaunchState is a task's state and launch identity.
type TaskLaunchState struct {
	State     TaskState
	Attempt   int
	RequestID string // "" before the first launch
}

// Tasks is the task repository (task rows and their events).
type Tasks interface {
	// Lock locks the task rows ids until the unit of work ends, on engines
	// with row locks (Postgres FOR NO KEY UPDATE, ascending id; a missing row
	// is skipped). SQLite's single writer already serializes writes: no-op.
	// Take locks in the order of docs/storage.md "Transactions and locking":
	// machine, connection, tree, task, schedule.
	Lock(ctx context.Context, ids ...int64) error
	Get(ctx context.Context, id int64) (Task, error)
	List(ctx context.Context, f TaskFilter) ([]Task, error)
	// Pollable: see db.Store.PollableTasks.
	Pollable(ctx context.Context, includeStale bool) ([]Task, error)
	BySession(ctx context.Context, ref SessionRef) (Task, error)
	// ByIDs reads the tasks with these ids (unknown ids are skipped).
	ByIDs(ctx context.Context, ids []int64) ([]Task, error)
	// BySessionID is the task owning a session identity (ErrNotFound).
	BySessionID(ctx context.Context, sessionID int64) (int64, error)
	// SessionOccupied: another task than taskID owns the session.
	SessionOccupied(ctx context.Context, sessionID, taskID int64) (bool, error)
	Events(ctx context.Context, taskID int64) ([]TaskEvent, error)
	// AddEvent appends a task event (from nil: none) and returns its time.
	AddEvent(ctx context.Context, taskID int64, from *TaskState, to TaskState, detail string, observedAt *time.Time, attribution Attribution) (time.Time, error)
	// JournalState is what the task's derived journal events read
	// (ErrNotFound).
	JournalState(ctx context.Context, id int64) (TaskJournalState, error)
	// MarkRecordedWork protects the task from deletion.
	MarkRecordedWork(ctx context.Context, id int64) error
	// ProjectBySession is the project of the task owning the session (nil:
	// no such task).
	ProjectBySession(ctx context.Context, sessionID int64) (*int64, error)
	Insert(ctx context.Context, in TaskInsert) (int64, error)
	InsertAdopted(ctx context.Context, in AdoptedInsert) (int64, error)
	EditState(ctx context.Context, id int64) (TaskEditState, error)
	SetStart(ctx context.Context, id int64, at *time.Time, tz *string) error
	// Update sets u's columns and updated_at.
	Update(ctx context.Context, id int64, u TaskUpdate) error
	// Deletability is the task's state and whether it has no notes or
	// execution history.
	Deletability(ctx context.Context, id int64) (state TaskState, unstarted bool, err error)
	Delete(ctx context.Context, id int64) error
	TreeID(ctx context.Context, id int64) (*int64, error)
	SetArchived(ctx context.Context, id int64, archived bool) error
	// Unarchive reports whether the task was archived.
	Unarchive(ctx context.Context, id int64) (bool, error)
	// Touch bumps updated_at (false: no such task).
	Touch(ctx context.Context, id int64) (bool, error)
	LaunchState(ctx context.Context, id int64) (TaskLaunchState, error)
	// ApplyTransition writes t's state change and returns the launch
	// attempt and request id after it.
	// sessionID: t.Session's identity (nil: none).
	ApplyTransition(ctx context.Context, t Transition, sessionID *int64) (attempt int, requestID string, err error)
	// Confirm attaches session to the launching attempt (false: the task
	// no longer has it).
	Confirm(ctx context.Context, a Attempt, state string, sessionID int64, endedAt *time.Time) (bool, error)
	// Reject returns the launching attempt's task to backlog, held.
	Reject(ctx context.Context, a Attempt, reason string) error
	// SetLaunchError records a launching attempt's error.
	SetLaunchError(ctx context.Context, a Attempt, msg string) error
	// NoteBlocked sets a backlog task's last error (false: not in backlog).
	NoteBlocked(ctx context.Context, id int64, reason string) (bool, error)
	// HoldScheduled holds a timed backlog task (false: not one).
	HoldScheduled(ctx context.Context, id int64, reason string) (bool, error)
}

// TaskJournalState is a task's tree, session, launch attempt, title and
// recorded-work markers.
type TaskJournalState struct {
	TreeID, SessionID *int64
	Title             string
	LaunchAttempt     int
	// Adopted: an adopted session; RecordedWork: deletion is blocked.
	Adopted, RecordedWork bool
}

// Attempts is the launch attempt repository: attempts, claims, pinned
// destinations and inputs, and handoffs.
type Attempts interface {
	Get(ctx context.Context, taskID int64, n int) (Attempt, error)
	// Prepare records the intent of a prepared attempt that has none.
	Prepare(ctx context.Context, taskID int64, n int, in execution.Intent) error
	// SaveCheckpoint stores c at a's revision (false: it changed).
	SaveCheckpoint(ctx context.Context, a Attempt, c execution.Checkpoint) (bool, error)
	// Unconfirmed keeps a open (false: it changed); seen nil keeps the
	// observed session.
	Unconfirmed(ctx context.Context, a Attempt, cause string, retryAt time.Time, seen *ObservedSession) (bool, error)
	// Recover opens an unconfirmed attempt for every launching task.
	Recover(ctx context.Context) error
	Confirm(ctx context.Context, a Attempt) (bool, error)
	Reject(ctx context.Context, a Attempt, reason string) (bool, error)
	// Begin records a prepared attempt (kept when present).
	Begin(ctx context.Context, taskID int64, n int, attribution Attribution) error
	// Claim claims the attempt's launch (false: already claimed).
	Claim(ctx context.Context, taskID int64, n int) (bool, error)
	// PinInputs records the accepted needs the attempt reads.
	PinInputs(ctx context.Context, taskID int64, n int) error
	Destination(ctx context.Context, taskID int64, n int) (LaunchDestination, error)
	HasDestination(ctx context.Context, taskID int64, n int) (bool, error)
	// BoundDestination is the task's active available binding (ErrNotFound).
	BoundDestination(ctx context.Context, taskID int64) (LaunchDestination, error)
	PinDestination(ctx context.Context, taskID int64, n int, d LaunchDestination) error
	// RecordHandoff replaces the attempt's handoff from h's source.
	RecordHandoff(ctx context.Context, taskID int64, h Handoff, sourceSessionID *int64) error
	// Handoffs are the attempt's handoffs by source task id.
	Handoffs(ctx context.Context, taskID int64, n int) ([]Handoff, error)
}

// ObservationSnapshot stores Stage's capture time alongside the harness payload.
type ObservationSnapshot struct {
	execution.Observation
	CapturedAt time.Time `json:"stageCapturedAt,omitzero"`
}

// Observations is the normalized execution observation repository.
type Observations interface {
	// Get is a session identity's observation (ErrNotFound).
	Get(ctx context.Context, sessionID int64) (execution.Observation, error)
	ByRef(ctx context.Context, ref SessionRef) (ObservationSnapshot, error)
	// Put stores o and clears its unavailable reason.
	Put(ctx context.Context, sessionID int64, o ObservationSnapshot) error
	Unavailable(ctx context.Context, ref SessionRef, reason string) error
	// Watched: connection's top-level sessions observed running, needing
	// input or done.
	Watched(ctx context.Context, connection int64) ([]SessionRef, error)
	// NeedsInputCount: tasks in needs_input plus top-level sessions no task
	// owns observed needing input.
	NeedsInputCount(ctx context.Context) (int, error)
}
