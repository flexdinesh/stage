package store

import (
	"context"
	"encoding/json"
	"time"
)

// AcceptedEvidence is copied once. No source-harness read is needed to use it.
type AcceptedEvidence struct {
	Session           *SessionRef `json:"session"`
	TaskID            int64       `json:"taskId"`
	Title             string      `json:"title"`
	TreeID            *int64      `json:"treeId"`
	TreeTitle         *string     `json:"treeTitle"`
	ProjectID         int64       `json:"projectId"`
	ExternalSessionID *string     `json:"externalSessionId"`
	SessionLink       *string     `json:"sessionLink"`
	Branch            *string     `json:"branch"`
	WorktreePath      *string     `json:"worktreePath"`
	ExecutionState    TaskState   `json:"executionState"`
	RunID             *string     `json:"runId"`
	ReplyRunID        *string     `json:"replyRunId"`
	ReplyItemID       *string     `json:"replyItemId"`
	Reply             *string     `json:"reply"`
	ReplyTruncated    bool        `json:"replyTruncated"`
	ReplyAt           *time.Time  `json:"replyAt"`
	ObservedAt        *time.Time  `json:"observedAt"`
	JournalThroughID  int64       `json:"journalThroughId"`
}

// UnmarshalJSON reads accepted evidence captured before API v9.
func (e *AcceptedEvidence) UnmarshalJSON(b []byte) error {
	type evidence AcceptedEvidence
	var v struct {
		evidence
		ExternalSessionID json.RawMessage `json:"externalSessionId"`
		SessionLink       json.RawMessage `json:"sessionLink"`
		ThreadID          json.RawMessage `json:"threadId"`
		ThreadLink        json.RawMessage `json:"threadLink"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if v.ExternalSessionID == nil {
		v.ExternalSessionID = v.ThreadID
	}
	if v.ExternalSessionID != nil {
		if err := json.Unmarshal(v.ExternalSessionID, &v.evidence.ExternalSessionID); err != nil {
			return err
		}
	}
	if v.SessionLink == nil {
		v.SessionLink = v.ThreadLink
	}
	if v.SessionLink != nil {
		if err := json.Unmarshal(v.SessionLink, &v.evidence.SessionLink); err != nil {
			return err
		}
	}
	if v.ExecutionState == "settled" {
		v.ExecutionState = StateDone
	}
	*e = AcceptedEvidence(v.evidence)
	return nil
}

type Acceptance struct {
	Attribution
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
	// Replayed: an exact requestId replay returned the original result
	// without applying it again (not part of the response).
	Replayed bool `json:"-"`
}

// ClaimState is what a launch claim checks.
type ClaimState struct {
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
	// ReleaseCause returns the latest current acceptance among the task's needs.
	ReleaseCause(ctx context.Context, taskID int64) (Acceptance, error)
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
	// Insert records an acceptance; sourceSessionID is the task's session.
	Insert(ctx context.Context, taskID int64, provenance string, evidence AcceptedEvidence, note string, sourceSessionID *int64, attribution Attribution) (int64, error)
	// SetCurrent sets the task's current acceptance (nil clears) at
	// revision and marks it and its tree as having recorded work.
	SetCurrent(ctx context.Context, taskID, revision int64, acceptanceID *int64) error
	// JournalEvent journals a task event in the task's tree too.
	JournalEvent(ctx context.Context, taskID int64, event, detail string, createdBy int64, sourceMachine *int64, attribution Attribution) error
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
