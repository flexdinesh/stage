package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// AcceptedEvidence is copied once. No source-harness read is needed to use it.
type AcceptedEvidence struct {
	Session             *SessionRef          `json:"session"`
	TaskID              int64                `json:"taskId"`
	Title               string               `json:"title"`
	TreeID              *int64               `json:"treeId"`
	TreeTitle           *string              `json:"treeTitle"`
	ProjectID           int64                `json:"projectId"`
	ThreadID            *string              `json:"threadId"`
	ThreadLink          *string              `json:"threadLink"`
	Branch              *string              `json:"branch"`
	WorktreePath        *string              `json:"worktreePath"`
	ExecutionState      TaskState            `json:"executionState"`
	RunID               *string              `json:"runId"`
	ReplyRunID          *string              `json:"replyRunId"`
	ReplyItemID         *string              `json:"replyItemId"`
	Reply               *string              `json:"reply"`
	ReplyTruncated      bool                 `json:"replyTruncated"`
	ReplyAt             *time.Time           `json:"replyAt"`
	ObservedAt          *time.Time           `json:"observedAt"`
	HistoricalSettledAt *time.Time           `json:"historicalSettledAt"`
	Artifacts           []execution.Artifact `json:"artifacts"`
	JournalThroughID    int64                `json:"journalThroughId"`
}

type Acceptance struct {
	ID         int64            `json:"id"`
	TaskID     int64            `json:"taskId"`
	AcceptedAt time.Time        `json:"acceptedAt"`
	CapturedAt time.Time        `json:"capturedAt"`
	Provenance string           `json:"provenance"`
	Evidence   AcceptedEvidence `json:"evidence"`
	Note       string           `json:"note"`
}

type AcceptanceImpact struct {
	TaskID    int64      `json:"taskId"`
	Title     string     `json:"title"`
	Claimed   bool       `json:"claimed"`
	StartMode *StartMode `json:"startMode"`
}
type AcceptanceResult struct {
	Revision     int64  `json:"revision"`
	AcceptanceID *int64 `json:"acceptanceId"`
}

// ThreadEvidence is a task's T3 mirror row as completion evidence.
type ThreadEvidence struct {
	Branch         *string
	WorktreePath   *string
	RunID          *string
	ReplyRunID     *string
	ReplyItemID    *string
	Reply          *string
	ReplyTruncated bool
	ReplyAt        *time.Time
	ObservedAt     *time.Time
	Artifacts      []execution.Artifact
	Status         string
	Pending        int
	ActiveRunID    *string
}

// ClaimState is what a launch claim checks.
type ClaimState struct {
	Reuse     bool
	Claimed   bool // the attempt's launch is claimed
	Adopted   bool
	Accepted  *int64
	TreeID    *int64
	StartMode *StartMode // effective
}

// Acceptances is the task completion repository.
type Acceptances interface {
	// List is a task's acceptances, newest first.
	List(ctx context.Context, taskID int64) ([]Acceptance, error)
	// LaunchInputs are the acceptances the attempt read, by source task.
	LaunchInputs(ctx context.Context, taskID int64, attempt int) ([]Acceptance, error)
	// ThreadEvidence reads the mirror row of a session identity (false:
	// none).
	ThreadEvidence(ctx context.Context, sessionID *int64) (ThreadEvidence, bool, error)
	// JournalThrough is the newest journal entry id of the task or tree.
	JournalThrough(ctx context.Context, taskID int64, treeID *int64) (int64, error)
	// Dependents are the tasks needing taskID, by id.
	Dependents(ctx context.Context, taskID int64) ([]AcceptanceImpact, error)
	// Request is a recorded request's input and result (ErrNotFound).
	Request(ctx context.Context, taskID int64, requestID string) (string, AcceptanceResult, error)
	SaveRequest(ctx context.Context, taskID int64, requestID, input string, result AcceptanceResult) error
	Current(ctx context.Context, taskID int64) (*int64, error)
	// MarkReopened suppresses inference for the task's observed execution.
	MarkReopened(ctx context.Context, taskID int64) error
	Insert(ctx context.Context, taskID int64, provenance string, evidence AcceptedEvidence, note string) (int64, error)
	// SetCurrent sets the task's current acceptance (nil clears) at
	// revision and marks it and its tree as having recorded work.
	SetCurrent(ctx context.Context, taskID, revision int64, acceptanceID *int64) error
	// JournalEvent journals a task event in the task's tree too.
	JournalEvent(ctx context.Context, taskID int64, event, detail string, createdBy int64, sourceMachine *int64) error
	// ResetDependentsReady clears ready_at on the task's backlog dependents.
	ResetDependentsReady(ctx context.Context, taskID int64) error
	// InferenceEligible: a done, uncompleted task of ref on an auto-complete
	// connection with no inference for executionID (ErrNotFound: no such
	// task session).
	InferenceEligible(ctx context.Context, taskID int64, ref SessionRef, executionID string) (bool, error)
	RecordInference(ctx context.Context, taskID int64, executionID, signal string, acceptanceID int64) error
	ClaimState(ctx context.Context, taskID int64, attempt int) (ClaimState, error)
	// UnacceptedNeeds are the task's needs without an acceptance, by id.
	UnacceptedNeeds(ctx context.Context, taskID int64) ([]int64, error)
	// Released: a backlog task whose needs are all accepted (releasedSQL).
	Released(ctx context.Context, taskID int64) (bool, error)
}
