package db

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// TaskState values (docs/api.md TaskState).
type TaskState string

const (
	StateBacklog     TaskState = "backlog"
	StateLaunching   TaskState = "launching"
	StateRunning     TaskState = "running"
	StateNeedsInput  TaskState = "needs_input"
	StateDone        TaskState = "done"
	StateSettled     TaskState = "settled"
	StateFailed      TaskState = "failed"
	StateInterrupted TaskState = "interrupted"
	StateCancelled   TaskState = "cancelled"
)

var TaskStates = []TaskState{
	StateBacklog, StateLaunching, StateRunning, StateNeedsInput, StateDone,
	StateSettled, StateFailed, StateInterrupted, StateCancelled,
}

func (s TaskState) Valid() bool { return slices.Contains(TaskStates, s) }

// Finished: entering one of these sets finishedAt (first entry per attempt).
func (s TaskState) Finished() bool {
	switch s {
	case StateDone, StateFailed, StateInterrupted, StateCancelled:
		return true
	}
	return false
}

// Active: a thread is (or is about to be) working; delete/edit restricted.
func (s TaskState) Active() bool {
	switch s {
	case StateLaunching, StateRunning, StateNeedsInput:
		return true
	}
	return false
}

const (
	SourceApp = "app"
	SourceT3  = "t3"
)

var (
	RuntimeModes     = []string{"approval-required", "auto-accept-edits", "auto", "full-access"}
	InteractionModes = []string{"default", "plan"}
)

type Project struct {
	ID            int64      `json:"id"`
	T3ProjectID   string     `json:"t3ProjectId"`
	Title         string     `json:"title"`
	WorkspaceRoot string     `json:"workspaceRoot"`
	Source        string     `json:"source"`
	ArchivedAt    *time.Time `json:"archivedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	// LastActivityAt: latest thread update/settle or task update; nil when none.
	LastActivityAt *time.Time `json:"lastActivityAt"`
}

// Workspace is the task workspace config (jsonb).
type Workspace struct {
	Type    string  `json:"type"` // root | worktree
	BaseRef string  `json:"baseRef,omitempty"`
	Branch  *string `json:"branch,omitempty"`
}

// MarshalJSON emits {type:"root"} or {type:"worktree", baseRef, branch|null}.
func (w Workspace) MarshalJSON() ([]byte, error) {
	if w.Type == "worktree" {
		return json.Marshal(struct {
			Type    string  `json:"type"`
			BaseRef string  `json:"baseRef"`
			Branch  *string `json:"branch"`
		}{w.Type, w.BaseRef, w.Branch})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{w.Type})
}

type Task struct {
	ID                 int64            `json:"id"`
	ProjectID          int64            `json:"projectId"`
	ProjectTitle       string           `json:"projectTitle"`
	Title              string           `json:"title"`
	Prompt             string           `json:"prompt"`
	ProviderInstanceID *string          `json:"providerInstanceId"`
	Model              *string          `json:"model"`
	ModelOptions       []t3.ModelOption `json:"modelOptions"`
	RuntimeMode        string           `json:"runtimeMode"`
	InteractionMode    string           `json:"interactionMode"`
	Workspace          Workspace        `json:"workspace"`
	Tags               []string         `json:"tags"`
	Notes              string           `json:"notes"`
	State              TaskState        `json:"state"`
	T3ThreadID         *string          `json:"t3ThreadId"`
	AfterTaskID        *int64           `json:"afterTaskId"` // runs after this task (launches when it settles)
	NextTaskID         *int64           `json:"nextTaskId"`  // the task that runs after this one
	LaunchAttempt      int              `json:"launchAttempt"`
	LastError          *string          `json:"lastError"`
	LaunchedAt         *time.Time       `json:"launchedAt"`
	FinishedAt         *time.Time       `json:"finishedAt"`
	SettledAt          *time.Time       `json:"settledAt"`
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
	Attachments        []Attachment     `json:"attachments"` // images, upload order
	T3ProjectID        string           `json:"-"`           // project's T3 id (for launch)
	ThreadLink         *string          `json:"-"`           // mirrored thread link
}

type TaskEvent struct {
	ID        int64      `json:"id"`
	TaskID    int64      `json:"taskId"`
	FromState *TaskState `json:"fromState"`
	ToState   TaskState  `json:"toState"`
	Detail    string     `json:"detail"`
	At        time.Time  `json:"at"`
}

// Thread is a T3 mirror row joined with its app project and task.
type Thread struct {
	T3ThreadID          string     `json:"t3ThreadId"`
	T3ProjectID         string     `json:"t3ProjectId"`
	ProjectID           *int64     `json:"projectId"`
	ProjectTitle        *string    `json:"projectTitle"`
	Title               string     `json:"title"`
	Status              string     `json:"status"`
	Settled             bool       `json:"settled"`
	SettledAt           *time.Time `json:"settledAt"`
	PendingRequestCount int        `json:"pendingRequestCount"`
	ProviderInstanceID  *string    `json:"providerInstanceId"`
	Model               *string    `json:"model"`
	RuntimeMode         *string    `json:"runtimeMode"`
	InteractionMode     *string    `json:"interactionMode"`
	Branch              *string    `json:"branch"`
	WorktreePath        *string    `json:"worktreePath"`
	ParentThreadID      *string    `json:"parentThreadId"`
	Relationship        *string    `json:"relationship"`
	CreationSource      *string    `json:"creationSource"`
	Source              string     `json:"source"`
	TaskID              *int64     `json:"taskId"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	SyncedAt            time.Time  `json:"syncedAt"`
	Link                *string    `json:"-"`
}

// Snippet is reusable prompt text; ProjectID nil = global.
type Snippet struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	ProjectID *int64    `json:"projectId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func nonNilOptions(o []t3.ModelOption) []t3.ModelOption {
	if o == nil {
		return []t3.ModelOption{}
	}
	return o
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
