package db

import (
	"encoding/json"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// TaskState values (docs/api.md TaskState).
type TaskState = store.TaskState

const (
	StateBacklog     = store.StateBacklog
	StateLaunching   = store.StateLaunching
	StateRunning     = store.StateRunning
	StateNeedsInput  = store.StateNeedsInput
	StateDone        = store.StateDone
	StateSettled     = store.StateSettled
	StateFailed      = store.StateFailed
	StateInterrupted = store.StateInterrupted
	StateCancelled   = store.StateCancelled
)

var TaskStates = store.TaskStates

const (
	SourceApp = store.SourceApp
	SourceT3  = store.SourceT3
)

var (
	RuntimeModes     = []string{"approval-required", "auto-accept-edits", "auto", "full-access"}
	InteractionModes = []string{"default", "plan"}
)

type (
	Project       = store.Project
	ImportSummary = store.ImportSummary
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
	// Legacy model columns of rows saved before executionConfig (T3); the
	// API shows executionConfig only (harness.Descriptor.LegacyConfig).
	ProviderInstanceID *string                 `json:"-"`
	Model              *string                 `json:"-"`
	ModelOptions       []execution.ModelOption `json:"-"`
	RuntimeMode        string                  `json:"-"`
	InteractionMode    string                  `json:"-"`
	Workspace          Workspace               `json:"workspace"`
	State              TaskState               `json:"state"`
	T3ThreadID         *string                 `json:"externalSessionId"`
	TreeID             *int64                  `json:"treeId"` // the task's tree (docs/api.md "Trees")
	TreeTitle          *string                 `json:"treeTitle"`
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
	Adopted           bool                 `json:"adopted"`
	ReadyAt           *time.Time           `json:"readyAt"`    // a manual task's needs all settled (ready notification episode)
	StartAt           *time.Time           `json:"startAt"`    // one-shot start of a backlog task (scheduler launches it)
	StartTz           *string              `json:"startTz"`    // IANA zone startAt was entered in
	ScheduleID        *int64               `json:"scheduleId"` // recurring schedule that spawned the task
	ScheduleTitle     *string              `json:"scheduleTitle"`
	OriginThreadID    *string              `json:"-"`                  // legacy column: T3 session that created the task
	OriginThreadTitle *string              `json:"originSessionTitle"` // the origin session's mirrored title (null when not mirrored)
	Handoff           bool                 `json:"handoff"`            // append the needs' handoff at launch (docs/api.md "Handoff")
	LaunchAttempt     int                  `json:"launchAttempt"`
	LastError         *string              `json:"lastError"`
	LaunchedAt        *time.Time           `json:"launchedAt"`
	FinishedAt        *time.Time           `json:"finishedAt"`
	SettledAt         *time.Time           `json:"settledAt"`
	RunStartedAt      *time.Time           `json:"runStartedAt"` // thread's elapsed run span (RunSpan)
	RunEndedAt        *time.Time           `json:"runEndedAt"`
	LastWordPreview   *string              `json:"lastWordPreview"` // thread's latest assistant reply, plain one-line preview
	LastWordAt        *time.Time           `json:"lastWordAt"`
	PullRequests      []execution.Artifact `json:"-"` // thread's PRs (mirror); [] without a thread
	// PendingQuestionCount: the thread's answerable pending questions.
	PendingQuestionCount int       `json:"-"`
	CreatedAt            time.Time `json:"createdAt"`
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
	ThreadLink      *string    `json:"-"` // mirrored thread link
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

type (
	Thread    = store.Thread
	LastWord  = store.LastWord
	ThreadRun = store.ThreadRun
)

func nonNilOptions(o []execution.ModelOption) []execution.ModelOption {
	if o == nil {
		return []execution.ModelOption{}
	}
	return o
}

// LegacyModel is the row's legacy model columns as JSON, the input of
// harness.Descriptor.LegacyConfig.
func (t Task) LegacyModel() json.RawMessage {
	return legacyModel(t.ProviderInstanceID, t.Model, t.ModelOptions, t.RuntimeMode, t.InteractionMode)
}

func legacyModel(provider, model *string, options []execution.ModelOption, runtime, interaction string) json.RawMessage {
	raw, _ := json.Marshal(struct {
		ProviderInstanceID *string                 `json:"providerInstanceId"`
		Model              *string                 `json:"model"`
		ModelOptions       []execution.ModelOption `json:"modelOptions"`
		RuntimeMode        string                  `json:"runtimeMode"`
		InteractionMode    string                  `json:"interactionMode"`
	}{provider, model, options, runtime, interaction})
	return raw
}
