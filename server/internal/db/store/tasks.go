package store

import (
	"context"
	"encoding/json"
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

// Workspace bases: ref (the worktree starts from BaseRef) or predecessor
// (from a need's branch; BaseRef is the fallback, docs/api.md "Predecessor
// base").
const (
	BaseRef         = "ref"
	BasePredecessor = "predecessor"
)

// Workspace is the task workspace config (jsonb).
type Workspace struct {
	Type    string  `json:"type"` // root | worktree
	BaseRef string  `json:"baseRef,omitempty"`
	Branch  *string `json:"branch,omitempty"`
	Base    string  `json:"base,omitempty"` // worktree: ref ("" = ref) | predecessor
	// BaseTaskID: with base predecessor, the need whose branch the worktree
	// starts from (nil = the sole need).
	BaseTaskID *int64 `json:"baseTaskId,omitempty"`
}

// FromPredecessor: a worktree that starts from a need's branch.
func (w Workspace) FromPredecessor() bool {
	return w.Type == "worktree" && w.Base == BasePredecessor
}

// MarshalJSON emits {type:"root"} or {type:"worktree", baseRef,
// branch|null, base, baseTaskId|null}.
func (w Workspace) MarshalJSON() ([]byte, error) {
	if w.Type == "worktree" {
		base := BaseRef
		baseTask := w.BaseTaskID
		if w.Base == BasePredecessor {
			base = BasePredecessor
		} else {
			baseTask = nil
		}
		return json.Marshal(struct {
			Type       string  `json:"type"`
			BaseRef    string  `json:"baseRef"`
			Branch     *string `json:"branch"`
			Base       string  `json:"base"`
			BaseTaskID *int64  `json:"baseTaskId"`
		}{w.Type, w.BaseRef, w.Branch, base, baseTask})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{w.Type})
}

type Task struct {
	ExecutionConfig *execution.Config `json:"executionConfig"`
	Launch          *Attempt          `json:"launch,omitempty"`
	ConnectionID    int64             `json:"connectionId"`
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
	Workspace               Workspace   `json:"workspace"`
	State                   TaskState   `json:"state"`
	// T3ThreadID is the session's external id (nil without a session).
	T3ThreadID *string `json:"externalSessionId"`
	TreeID     *int64  `json:"treeId"` // the task's tree (docs/api.md "Trees")
	TreeTitle  *string `json:"treeTitle"`
	// StartMode is the task's own start mode (nil = inherit the tree's);
	// EffectiveStartMode applies the tree's (nil outside a tree without one).
	StartMode          *StartMode `json:"startMode"`
	EffectiveStartMode *StartMode `json:"effectiveStartMode"`
	// Readiness: blocked | ready for a backlog tree member, else nil.
	Readiness  *Readiness `json:"readiness"`
	Needs      []int64    `json:"needs"`      // tasks this one needs (same tree), by id
	Dependents []int64    `json:"dependents"` // tasks that need this one, by id
	// Adopted: the task tracks an existing T3 session (prompt ""); it is never
	// started, restarted or duplicated by stage.
	Adopted           bool       `json:"adopted"`
	ReadyAt           *time.Time `json:"readyAt"`    // a manual task's needs all settled (ready notification episode)
	StartAt           *time.Time `json:"startAt"`    // one-shot start of a backlog task (scheduler launches it)
	StartTz           *string    `json:"startTz"`    // IANA zone startAt was entered in
	ScheduleID        *int64     `json:"scheduleId"` // recurring schedule that spawned the task
	ScheduleTitle     *string    `json:"scheduleTitle"`
	OriginThreadTitle *string    `json:"originSessionTitle"` // the origin session's mirrored title (null when not mirrored)
	Handoff           bool       `json:"handoff"`            // append the needs' handoff at launch (docs/api.md "Handoff")
	LaunchAttempt     int        `json:"launchAttempt"`
	LastError         *string    `json:"lastError"`
	LaunchedAt        *time.Time `json:"launchedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
	SettledAt         *time.Time `json:"settledAt"`
	RunStartedAt      *time.Time `json:"runStartedAt"` // thread's elapsed run span (RunSpan)
	RunEndedAt        *time.Time `json:"runEndedAt"`
	LastWordPreview   *string    `json:"lastWordPreview"` // thread's latest assistant reply, plain one-line preview
	LastWordAt        *time.Time `json:"lastWordAt"`
	CreatedAt         time.Time  `json:"createdAt"`
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
	Deletable       bool       `json:"deletable"`
	ThreadUpdatedAt *time.Time `json:"-"` // mirrored thread's updatedAt (tree activity)
	T3ProjectID     string     `json:"-"` // project's T3 id (for launch)
	TreeStartMode   *StartMode `json:"-"` // the tree's start mode (batch grouping)
}

type TaskEvent struct {
	ID        int64      `json:"id"`
	TaskID    int64      `json:"taskId"`
	FromState *TaskState `json:"fromState"`
	ToState   TaskState  `json:"toState"`
	Detail    string     `json:"detail"`
	At        time.Time  `json:"at"`
}

// LegacyT3ModelKey is the T3 model id of a provider and model, "" unless
// both are set (t3.ModelKey's encoding).
func LegacyT3ModelKey(provider, model *string) string {
	if provider == nil || model == nil || *provider == "" || *model == "" {
		return ""
	}
	b, _ := json.Marshal([2]string{*provider, *model})
	return string(b)
}

// LegacyT3Config is the execution config of the legacy T3 model inputs
// (providerInstanceId, model, modelOptions, runtimeMode, interactionMode;
// docs/api.md), as t3.LegacyConfig maps them. Writes and the legacy-column
// backfill store it.
func LegacyT3Config(provider, model *string, options []execution.ModelOption, runtime, interaction string) execution.Config {
	if options == nil {
		options = []execution.ModelOption{}
	}
	return execution.Config{Model: LegacyT3ModelKey(provider, model), Options: options, Permission: runtime, Mode: interaction}
}

type Attempt struct {
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
// command (Links.Command) is stored, as the fallback when no destination
// or stored observation resolves one.
type ObservedSession struct {
	SessionRef
	Title string           `json:"title"`
	State string           `json:"state"`
	Links *execution.Links `json:"links,omitempty"`
}

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
	// SourceThreadID is Session's external id on reads (nil without one).
	SourceThreadID *string   `json:"sourceThreadId"`
	SourceTitle    string    `json:"sourceTitle"`
	Reason         *string   `json:"reason"` // why it fell back
	At             time.Time `json:"at"`
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
	// SourceThreadID: on reads, Session's external id ("" without one); on
	// writes, legacy evidence's T3 thread id when Session is nil.
	SourceThreadID string    `json:"sourceThreadId"`
	SourceTitle    string    `json:"sourceTitle"`
	Text           string    `json:"text"`   // "" when skipped
	Reason         *string   `json:"reason"` // why skipped
	At             time.Time `json:"at"`
}

// HandoffSource is a need a launch reads: a task (TaskID set; its current
// thread). Records from before trees may name a session (TaskID nil).
type HandoffSource struct {
	TaskID    *int64
	ThreadID  string
	Title     string
	SettledAt *time.Time // nil: finished (done) but not settled yet
}

// Label names the source in event details and the block: `#12` or
// `session "Title"`.
func (s HandoffSource) Label() string {
	if s.TaskID != nil {
		return fmt.Sprintf("#%d", *s.TaskID)
	}
	return fmt.Sprintf("session %q", s.Title)
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
	Q           string
	From, To    *time.Time
	SettledFrom *time.Time // settled_at >= SettledFrom
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

	BeginAttempt  bool        // launch start: attempt+1 and a new launch_request_id, clear session/error/finished/settled/start/ready_at
	Session       *SessionRef // set the session (+ launched_at)
	ObservedStart *time.Time  // first native execution of an adopted backlog session
	LastError     *string     // set last_error
}

// TransitionResult reports what happened. Changed=false: current state not
// in From (or already To); nothing written.
type TransitionResult struct {
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
	Title           string
	Prompt          string
	Workspace       Workspace
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
	Needs           int
}

// TaskUpdate is the columns an edit sets (unset fields are kept;
// ExecutionConfig and StartMode Null clears).
type TaskUpdate struct {
	ConnectionID    Optional[int64]
	ExecutionConfig Optional[execution.Config]
	ProjectID       Optional[int64]
	Title           Optional[string]
	Prompt          Optional[string]
	Workspace       Optional[Workspace]
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
	// CreatedBySession: tasks ref created, by id.
	CreatedBySession(ctx context.Context, ref SessionRef) ([]Task, error)
	// BySessionID is the task owning a session identity (ErrNotFound).
	BySessionID(ctx context.Context, sessionID int64) (int64, error)
	// SessionOccupied: another task than taskID owns the session.
	SessionOccupied(ctx context.Context, sessionID, taskID int64) (bool, error)
	Events(ctx context.Context, taskID int64) ([]TaskEvent, error)
	// AddEvent appends a task event (from nil: none) and returns its time.
	AddEvent(ctx context.Context, taskID int64, from *TaskState, to TaskState, detail string) (time.Time, error)
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
	// BaseTask is workspace.baseTaskId and whether it names a need.
	BaseTask(ctx context.Context, id int64) (base *int64, isNeed bool, err error)
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
// destinations and inputs, launch bases and handoffs.
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
	Begin(ctx context.Context, taskID int64, n int) error
	// Claim claims the attempt's launch (false: already claimed).
	Claim(ctx context.Context, taskID int64, n int) (bool, error)
	// PinInputs records the accepted needs the attempt reads.
	PinInputs(ctx context.Context, taskID int64, n int) error
	Destination(ctx context.Context, taskID int64, n int) (LaunchDestination, error)
	HasDestination(ctx context.Context, taskID int64, n int) (bool, error)
	// BoundDestination is the task's active available binding (ErrNotFound).
	BoundDestination(ctx context.Context, taskID int64) (LaunchDestination, error)
	PinDestination(ctx context.Context, taskID int64, n int, d LaunchDestination) error
	// RecordBase replaces the attempt's launch base.
	RecordBase(ctx context.Context, taskID int64, b LaunchBase, sourceSessionID *int64) error
	// Base is the attempt's launch base (nil: none).
	Base(ctx context.Context, taskID int64, n int) (*LaunchBase, error)
	// RecordHandoff replaces the attempt's handoff from h's source.
	RecordHandoff(ctx context.Context, taskID int64, h Handoff, sourceSessionID *int64) error
	// Handoffs are the attempt's handoffs by source task id.
	Handoffs(ctx context.Context, taskID int64, n int) ([]Handoff, error)
}

// Observations is the normalized execution observation repository.
type Observations interface {
	// Get is a session identity's observation (ErrNotFound).
	Get(ctx context.Context, sessionID int64) (execution.Observation, error)
	ByRef(ctx context.Context, ref SessionRef) (execution.Observation, error)
	// Put stores o and clears its unavailable reason.
	Put(ctx context.Context, sessionID int64, o execution.Observation) error
	Unavailable(ctx context.Context, ref SessionRef, reason string) error
	// Watched: connection's top-level sessions observed running, needing
	// input or done.
	Watched(ctx context.Context, connection int64) ([]SessionRef, error)
}

// ThreadMirror is the T3 mirror row derived from an observation.
type ThreadMirror struct {
	SessionID       int64
	ParentThreadID  *string
	Relationship    *string
	Branch          *string
	WorktreePath    *string
	PendingRequests int
	ActiveRunID     *string
	LatestRunID     *string
	Settled         bool
	SettledAt       *time.Time
	Model           *string
	RuntimeMode     *string
	InteractionMode *string
}
