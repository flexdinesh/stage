package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Thread sources: a task's thread (app) or an external one (t3).
const (
	SourceApp = "app"
	SourceT3  = "t3"
)

// Thread is a T3 mirror row joined with its app project and task.
type Thread struct {
	ConnectionID        int64                `json:"connectionId"`
	Session             SessionRef           `json:"session"`
	T3ThreadID          string               `json:"externalSessionId"`
	T3ProjectID         string               `json:"externalProjectId"`
	ProjectID           *int64               `json:"projectId"`
	ProjectTitle        *string              `json:"projectTitle"`
	Title               string               `json:"title"`
	Status              string               `json:"status"`
	State               TaskState            `json:"state"`
	Archived            bool                 `json:"archived"`
	ActiveRunID         *string              `json:"activeRunId"`
	LatestRunID         *string              `json:"latestRunId"`
	RunStartedAt        *time.Time           `json:"runStartedAt"` // elapsed run span (RunSpan)
	RunEndedAt          *time.Time           `json:"runEndedAt"`
	RunCount            int                  `json:"runCount"` // runs T3 reported (detail reads); 0 until read
	PullRequests        []execution.Artifact `json:"-"`
	Settled             bool                 `json:"settled"`
	SettledAt           *time.Time           `json:"settledAt"`
	PendingRequestCount int                  `json:"pendingRequestCount"`
	// PendingQuestionCount: pending requests stage can answer (user-input
	// questions); the rest are approvals handled in T3.
	PendingQuestionCount int        `json:"-"`
	ProviderInstanceID   *string    `json:"providerInstanceId"`
	Model                *string    `json:"model"`
	RuntimeMode          *string    `json:"runtimeMode"`
	InteractionMode      *string    `json:"interactionMode"`
	Branch               *string    `json:"branch"`
	WorktreePath         *string    `json:"worktreePath"`
	ParentThreadID       *string    `json:"parentThreadId"`
	Relationship         *string    `json:"relationship"`
	CreationSource       *string    `json:"creationSource"`
	Source               string     `json:"source"`
	TaskID               *int64     `json:"taskId"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	SyncedAt             time.Time  `json:"syncedAt"`
	LastWordPreview      *string    `json:"lastWordPreview"` // latest assistant reply, plain one-line preview
	LastWordAt           *time.Time `json:"lastWordAt"`
	// LastWord is the reply excerpt; set on detail reads only
	// (GetThreadLastWord), null in lists.
	LastWord *LastWord `json:"lastWord"`
	// RecentRuns are the mirrored runs, newest first (at most 50); set on
	// detail reads only (ThreadRuns), null in lists.
	RecentRuns []ThreadRun `json:"-"`
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

// ThreadFilter selects mirrored threads (ListThreads).
type ThreadFilter struct {
	ProjectID    *int64
	States       []string
	Active       bool
	SortActivity bool
	Parent       *string
	Statuses     []string
	Settled      *bool
	Provider     string
	Source       string // app | t3 | ""
	Q            string
	// IDs keeps these T3 thread ids only (nil = no id filter).
	IDs      []string
	From, To *time.Time
	// ExcludeSubagents keeps root threads only (parent_thread_id IS NULL).
	ExcludeSubagents bool
	// NeedsInput keeps external threads needing input (see NeedsInputCount).
	NeedsInput bool
	// Running keeps external threads observed running.
	Running bool
	// SettledFrom keeps external threads settled at or after it, newest
	// settle first.
	SettledFrom *time.Time
	Limit       int
	Offset      int
}

// ThreadObservation is the watcher's stored view of a thread.
type ThreadObservation struct {
	ObservedState *TaskState // nil = never observed (or not mirrored)
	TaskOwned     bool       // a task has this thread
}

// LastWordRow is a reply to store as a thread's last word.
type LastWordRow struct {
	Text      string
	Preview   *string
	Truncated bool
	RunID     *string
	ItemID    *string
	At        *time.Time
}

// Threads is the T3 mirror repository. External threads are those no task
// owns; subagents are excluded from the external scopes, forks included.
type Threads interface {
	Get(ctx context.Context, id string) (Thread, error)
	// Runs is a thread's mirrored recent runs (nil when unread);
	// ErrNotFound for an unknown thread.
	Runs(ctx context.Context, id string) ([]ThreadRun, error)
	// List returns a page (by createdAt, settledAt or updatedAt desc) and
	// the total match count.
	List(ctx context.Context, f ThreadFilter) ([]Thread, int, error)
	SetPendingQuestionCount(ctx context.Context, id string, n int) error
	// NeedsInputCount: tasks in needs_input + external threads needing input.
	NeedsInputCount(ctx context.Context) (int, error)
	// WatchedIDs: connection's external non-subagent threads last seen
	// running, needing input or done, by creation.
	WatchedIDs(ctx context.Context, connection int64) ([]string, error)
	// Observations returns an entry for every id.
	Observations(ctx context.Context, ids []string) (map[string]ThreadObservation, error)
	// SetObservedState records the watcher's state for ref's mirrored thread.
	SetObservedState(ctx context.Context, ref SessionRef, st TaskState) error
	LastWordCheckedAt(ctx context.Context, id string) (*time.Time, error)
	// SetLastWord stores r (nil: only the check time) unless a later check
	// was stored.
	SetLastWord(ctx context.Context, id string, checked time.Time, r *LastWordRow) error
	// LastWord reads the stored reply (text nil: none).
	LastWord(ctx context.Context, id string) (text *string, lw LastWord, err error)
}

// Stats is the read-only statistics repository over [from, to).
type Stats interface {
	// ThreadTimes lists mirrored threads' creation times.
	ThreadTimes(ctx context.Context, from, to time.Time, excludeSubagents bool) ([]time.Time, error)
	ThreadsByProject(ctx context.Context, from, to time.Time, excludeSubagents bool) ([]ProjectCount, error)
	ThreadsByStatus(ctx context.Context, from, to time.Time, excludeSubagents bool) ([]StatusCount, error)
	// TasksByState counts every task by state.
	TasksByState(ctx context.Context) ([]StateCount, error)
}

type ProjectCount struct {
	ProjectID   *int64 `json:"projectId"`
	T3ProjectID string `json:"externalProjectId"`
	Title       string `json:"title"`
	Count       int    `json:"count"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type StateCount struct {
	State TaskState `json:"state"`
	Count int       `json:"count"`
}
