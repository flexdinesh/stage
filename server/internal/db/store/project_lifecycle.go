package store

import (
	"context"
	"time"
)

type ProjectAlias struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	MergedAt time.Time `json:"mergedAt"`
}

type ProjectEvent struct {
	// Attribution is the admitted actor and cause; historical when NULL.
	Attribution
	ID        int64     `json:"id"`
	ProjectID int64     `json:"-"`
	Type      string    `json:"type"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy int64     `json:"createdBy"`
}

// ProjectMergeRows contains the stable, ordered inputs to a merge preview.
type ProjectMergeRows struct {
	Content    []ProjectMergeContent
	Workspaces []ProjectMergeWorkspace
	History    []ProjectMergeHistory
}

type ProjectMergeContent struct {
	Kind          string
	ID            int64
	UpdatedAt     time.Time
	BindingID     *int64
	State         TaskState
	LaunchAttempt int
}

type ProjectMergeWorkspace struct {
	ID, ProjectID, ConnectionID, Revision int64
	Active, Primary                       bool
	ExternalID, Workspace, Rule, Status   string
	ObservedWorkspace                     *string
}

// ProjectMergeHistory identifies aliases and events by ID, and dismissals
// by the pair (ProjectID, ID).
type ProjectMergeHistory struct {
	Kind          string
	ID, ProjectID int64
}

type ProjectSplitWorkspace struct {
	ConnectionID                 int64
	Workspace, Title, ExternalID string
	Primary                      bool
	WorkspaceCount               int
}

type ProjectLifecycle interface {
	CanonicalID(ctx context.Context, id int64) (int64, error)
	LockMergeRows(ctx context.Context, into, from int64) error
	MergeRows(ctx context.Context, into, from int64) (ProjectMergeRows, error)
	ApplyMerge(ctx context.Context, into, from, by int64) error
	SplitWorkspace(ctx context.Context, project, binding int64) (ProjectSplitWorkspace, error)
	ApplySplit(ctx context.Context, from, into, binding int64) error
	DismissMerge(ctx context.Context, project, other int64) error
	MergeDismissals(ctx context.Context) ([][2]int64, error)
	AddProjectEvent(ctx context.Context, project int64, kind, text string, by int64, attribution Attribution) error
	ProjectEvents(ctx context.Context, project int64) ([]ProjectEvent, error)
}
