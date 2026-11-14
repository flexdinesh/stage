package db

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
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
	StartAt            *time.Time       `json:"startAt"`     // one-shot start of a backlog task (scheduler launches it)
	StartTz            *string          `json:"startTz"`     // IANA zone startAt was entered in
	ScheduleID         *int64           `json:"scheduleId"`  // recurring schedule that spawned the task
	ScheduleTitle      *string          `json:"scheduleTitle"`
	OriginThreadID     *string          `json:"originThreadId"`    // T3 session that created the task (agent interface)
	OriginThreadTitle  *string          `json:"originThreadTitle"` // its mirrored title (null when not mirrored)
	AfterThreadID      *string          `json:"afterThreadId"`     // runs after this T3 session (launches when it settles)
	AfterThreadTitle   *string          `json:"afterThreadTitle"`
	Handoff            bool             `json:"handoff"` // append the predecessor handoff at launch (docs/api.md "Handoff")
	LaunchAttempt      int              `json:"launchAttempt"`
	LastError          *string          `json:"lastError"`
	LaunchedAt         *time.Time       `json:"launchedAt"`
	FinishedAt         *time.Time       `json:"finishedAt"`
	SettledAt          *time.Time       `json:"settledAt"`
	RunStartedAt       *time.Time       `json:"runStartedAt"` // thread's elapsed run span (RunSpan)
	RunEndedAt         *time.Time       `json:"runEndedAt"`
	LastWordPreview    *string          `json:"lastWordPreview"` // thread's latest assistant reply, plain one-line preview
	LastWordAt         *time.Time       `json:"lastWordAt"`
	PullRequests       []t3.PullRequest `json:"pullRequests"` // thread's PRs (mirror); [] without a thread
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
	T3ThreadID          string           `json:"t3ThreadId"`
	T3ProjectID         string           `json:"t3ProjectId"`
	ProjectID           *int64           `json:"projectId"`
	ProjectTitle        *string          `json:"projectTitle"`
	Title               string           `json:"title"`
	Status              string           `json:"status"`
	State               TaskState        `json:"state"`
	Archived            bool             `json:"archived"`
	ActiveRunID         *string          `json:"activeRunId"`
	LatestRunID         *string          `json:"latestRunId"`
	RunStartedAt        *time.Time       `json:"runStartedAt"` // elapsed run span (RunSpan)
	RunEndedAt          *time.Time       `json:"runEndedAt"`
	RunCount            int              `json:"runCount"` // runs T3 reported (detail reads); 0 until read
	PullRequests        []t3.PullRequest `json:"pullRequests"`
	Settled             bool             `json:"settled"`
	SettledAt           *time.Time       `json:"settledAt"`
	PendingRequestCount int              `json:"pendingRequestCount"`
	ProviderInstanceID  *string          `json:"providerInstanceId"`
	Model               *string          `json:"model"`
	RuntimeMode         *string          `json:"runtimeMode"`
	InteractionMode     *string          `json:"interactionMode"`
	Branch              *string          `json:"branch"`
	WorktreePath        *string          `json:"worktreePath"`
	ParentThreadID      *string          `json:"parentThreadId"`
	Relationship        *string          `json:"relationship"`
	CreationSource      *string          `json:"creationSource"`
	Source              string           `json:"source"`
	TaskID              *int64           `json:"taskId"`
	CreatedAt           time.Time        `json:"createdAt"`
	UpdatedAt           time.Time        `json:"updatedAt"`
	SyncedAt            time.Time        `json:"syncedAt"`
	LastWordPreview     *string          `json:"lastWordPreview"` // latest assistant reply, plain one-line preview
	LastWordAt          *time.Time       `json:"lastWordAt"`
	// LastWord is the reply excerpt; set on detail reads only
	// (GetThreadLastWord), null in lists.
	LastWord *LastWord `json:"lastWord"`
	// RecentRuns are the mirrored runs, newest first (at most 50); set on
	// detail reads only (ThreadRuns), null in lists.
	RecentRuns []ThreadRun `json:"recentRuns"`
	Link       *string     `json:"-"`
}

// LastWord is a thread's latest assistant reply as last read by sync: a
// markdown excerpt capped at sync's limit (Truncated = the reply is longer).
type LastWord struct {
	Text      string     `json:"text"`
	Truncated bool       `json:"truncated"`
	RunID     *string    `json:"runId"`
	At        *time.Time `json:"at"` // the reply item's updatedAt
}

// ThreadRun is one t3_thread_read recentRuns entry as mirrored in
// threads.recent_runs. Each follow-up on a thread is a new run.
type ThreadRun struct {
	RunID              string     `json:"runId"`
	Ordinal            int        `json:"ordinal"`
	Status             string     `json:"status"`
	ProviderInstanceID *string    `json:"providerInstanceId"`
	Model              *string    `json:"model"`         // requested
	ExecutedModel      *string    `json:"executedModel"` // what the provider ran
	RequestedAt        time.Time  `json:"requestedAt"`
	StartedAt          *time.Time `json:"startedAt"`
	CompletedAt        *time.Time `json:"completedAt"`
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
