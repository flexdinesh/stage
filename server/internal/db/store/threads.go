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
	ConnectionID        int64      `json:"connectionId"`
	Session             SessionRef `json:"session"`
	T3ThreadID          string     `json:"externalSessionId"`
	T3ProjectID         string     `json:"externalProjectId"`
	ProjectID           *int64     `json:"projectId"`
	ProjectTitle        *string    `json:"projectTitle"`
	Title               string     `json:"title"`
	Status              string     `json:"status"`
	State               TaskState  `json:"state"`
	Archived            bool       `json:"archived"`
	ActiveRunID         *string    `json:"activeRunId"`
	LatestRunID         *string    `json:"latestRunId"`
	RunStartedAt        *time.Time `json:"runStartedAt"` // elapsed run span (RunSpan)
	RunEndedAt          *time.Time `json:"runEndedAt"`
	Settled             bool       `json:"settled"`
	SettledAt           *time.Time `json:"settledAt"`
	PendingRequestCount int        `json:"pendingRequestCount"`
	Model               *string    `json:"model"`
	RuntimeMode         *string    `json:"runtimeMode"`
	InteractionMode     *string    `json:"interactionMode"`
	Branch              *string    `json:"branch"`
	WorktreePath        *string    `json:"worktreePath"`
	ParentThreadID      *string    `json:"parentThreadId"`
	Relationship        *string    `json:"relationship"`
	Source              string     `json:"source"`
	TaskID              *int64     `json:"taskId"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	SyncedAt            time.Time  `json:"syncedAt"`
	LastWordPreview     *string    `json:"lastWordPreview"` // latest assistant reply, plain one-line preview
	LastWordAt          *time.Time `json:"lastWordAt"`
	// LastWord is the reply excerpt; set on detail reads only
	// (GetThreadLastWord), null in lists.
	LastWord *LastWord `json:"lastWord"`
}

// LastWord is a thread's latest assistant reply as last read by sync: a
// markdown excerpt capped at sync's limit (Truncated = the reply is longer).
type LastWord struct {
	Text      string     `json:"text"`
	Truncated bool       `json:"truncated"`
	RunID     *string    `json:"runId"`
	At        *time.Time `json:"at"` // the reply item's updatedAt
}

// ThreadObservation is the watcher's stored view of a thread.
type ThreadObservation struct {
	ObservedState *TaskState // nil = never observed (or not mirrored)
	TaskOwned     bool       // a task has this thread's session
}

// LastWordRow is a reply to store as a thread's last word.
type LastWordRow struct {
	Text      string
	Preview   *string
	Truncated bool
	RunID     *string
	At        *time.Time
}

// Threads is the T3 mirror repository. External threads are those no task
// owns; subagents are excluded from the external scopes, forks included.
type Threads interface {
	Get(ctx context.Context, id string) (Thread, error)
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
	// Mirror upserts o's mirror row (a newer stored row wins) and sets its
	// model and modes; inserted: the row is new.
	Mirror(ctx context.Context, o execution.Observation, m ThreadMirror) (inserted bool, err error)
	// SetProject sets the mirrored thread's Stage project.
	SetProject(ctx context.Context, threadID string, project *int64) error
	// SetSessionLastWord is SetLastWord by session identity.
	SetSessionLastWord(ctx context.Context, sessionID int64, checked time.Time, lw LastWordRow) error
	// LastWord reads the stored reply (text nil: none).
	LastWord(ctx context.Context, id string) (text *string, lw LastWord, err error)
}
