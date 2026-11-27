package store

import (
	"context"
	"time"
)

// Project is a Stage project with its active bindings.
type Project struct {
	ID         int64            `json:"id"`
	Bindings   []ProjectBinding `json:"bindings"`
	Title      string           `json:"title"`
	ArchivedAt *time.Time       `json:"archivedAt"`
	CreatedAt  time.Time        `json:"createdAt"`
	CreatedBy  int64            `json:"createdBy"` // user who created it
	// SourceMachineID: the machine whose agent gateway forwarded the create
	// (nil: not through a gateway).
	SourceMachineID *int64    `json:"sourceMachineId"`
	UpdatedAt       time.Time `json:"updatedAt"`
	// LastActivityAt: latest task or journal activity (nil when none).
	LastActivityAt *time.Time `json:"lastActivityAt"`
	// Origin is "discovered" for a project discovery imported, else nil.
	Origin     *string    `json:"origin"`
	ImportedAt *time.Time `json:"importedAt"`
	// Repos: distinct normalized origin remotes of the active bindings'
	// workspaces. Search metadata only; titles and paths stay authoritative.
	Repos   []string       `json:"repos"`
	Aliases []ProjectAlias `json:"aliases"`
}

// ImportSummary counts the projects one connection's discovery created in
// the recent window (ImportNoticeWindow), with the latest import time.
type ImportSummary struct {
	ConnectionID int64     `json:"connectionId"`
	Harness      string    `json:"harness"`
	Count        int       `json:"count"`
	At           time.Time `json:"at"`
}

// Workspace is the persisted inventory and its current resolution.
type Workspace struct {
	ConnectionID    int64               `json:"connectionId"`
	WorkspaceID     string              `json:"workspaceId"`
	ExternalID      string              `json:"externalId"`
	Title           string              `json:"title"`
	Workspace       string              `json:"workspace"`
	Available       bool                `json:"available"`
	Importable      bool                `json:"importable"`
	HarnessArchived bool                `json:"harnessArchived"`
	ActiveAt        *time.Time          `json:"activeAt"`
	ProjectID       *int64              `json:"projectId"`
	BindingID       *int64              `json:"bindingId"`
	Primary         bool                `json:"primary"`
	FileHost        *int64              `json:"fileHost"`
	CanonicalPath   string              `json:"canonicalPath"`
	Remote          string              `json:"remote"`
	Subpath         string              `json:"subpath"`
	IdentityKey     string              `json:"identityKey"`
	EvidenceAt      *time.Time          `json:"evidenceAt"`
	EvidenceSource  string              `json:"evidenceSource"`
	Declined        bool                `json:"declined"`
	Resolution      WorkspaceResolution `json:"resolution"`
}

type WorkspaceResolution struct {
	Outcome    string  `json:"outcome"`
	Reason     string  `json:"reason"`
	Rule       string  `json:"rule"`
	Candidates []int64 `json:"candidates"`
}

type StoredWorkspace struct {
	Workspace
	MachineID        int64
	ConnectionActive bool
	CheckoutKey      string
	InodeHint        string
}

type IdentityBinding struct {
	ProjectBinding
	Available                  bool
	CanonicalPath, CheckoutKey string
	InodeHint                  string
}

type BindingState struct {
	ProjectBinding
	Active bool
}

type WorkspaceDecline struct {
	ConnectionID int64
	Key          string
}

// DiscoveredWorkspace is one discovery row to upsert.
type DiscoveredWorkspace struct {
	ExternalID     string
	Title          string
	Workspace      string
	Available      bool
	Importable     bool
	Archived       bool
	ActiveAt       *time.Time
	Repo           string
	RepoPath       string
	FileHost       *int64
	CanonicalPath  string
	CheckoutKey    string
	InodeHint      string
	EvidenceSource string
}

// ProjectArchive selects projects by archive state.
type ProjectArchive int

const (
	ProjectsActive ProjectArchive = iota
	ProjectsArchived
	ProjectsAll
)

// Projects is the projects repository: projects, bindings and discovery
// (external projects, import).
type Projects interface {
	ProjectLifecycle
	// LockIdentity serializes writes to workspace and binding identity across connections.
	LockIdentity(ctx context.Context) error
	Exists(ctx context.Context, id int64) (bool, error)
	// List orders by title (case-insensitive), then id.
	List(ctx context.Context, archive ProjectArchive) ([]Project, error)
	Get(ctx context.Context, id int64) (Project, error)
	// Bound is the project with the active, available binding of
	// (connection, external project, workspace).
	Bound(ctx context.Context, b ProjectBinding) (Project, error)
	Insert(ctx context.Context, title string, createdBy int64, sourceMachine *int64) (int64, error)
	// SoleBinding is the project of the only active binding of external
	// project on the connection (nil: none or several).
	SoleBinding(ctx context.Context, connectionID int64, external string) (*int64, error)
	// InsertDiscovered inserts a project discovery created on connection.
	InsertDiscovered(ctx context.Context, title string, connectionID, createdBy int64) (int64, error)
	// Update sets the Stage-owned title and archive state.
	Update(ctx context.Context, id int64, title string, archived bool) error

	// InsertBinding adds b as the next revision (unique violation when the
	// workspace is bound elsewhere).
	InsertBinding(ctx context.Context, b ProjectBinding) (int64, error)
	Binding(ctx context.Context, id int64) (BindingState, error)
	ActiveWorkspaceBinding(ctx context.Context, connection int64, externalID, path string) (BindingState, error)
	DeactivateBinding(ctx context.Context, id int64) error
	// PromoteBinding selects id, or the lowest active binding id when id is zero.
	PromoteBinding(ctx context.Context, project, connection, id int64) error
	RepointBindingChoices(ctx context.Context, from, to int64) error
	// AdoptionTarget is the sole project actively and availably bound to
	// (connection, external project[, workspace]) (false: none or several).
	AdoptionTarget(ctx context.Context, connectionID int64, externalID string, workspace *string) (int64, bool, error)
	// BoundOn: the project has an active binding on the connection.
	BoundOn(ctx context.Context, projectID, connectionID int64) (bool, error)
	WorkspaceRows(ctx context.Context) ([]StoredWorkspace, error)
	IdentityBindings(ctx context.Context) ([]IdentityBinding, error)
	WorkspaceDeclines(ctx context.Context) ([]WorkspaceDecline, error)
	SetWorkspaceDecline(ctx context.Context, connection int64, key string, decline bool) error
	SetWorkspaceResolution(ctx context.Context, connection int64, externalID, path string, v WorkspaceResolution) error
	MarkUnavailable(ctx context.Context, connectionID int64) error
	UpsertDiscovered(ctx context.Context, connectionID int64, w DiscoveredWorkspace) error
	// RefreshBindingStatus recomputes the connection's active bindings'
	// status from discovery.
	RefreshBindingStatus(ctx context.Context, connectionID int64) error
	ImportSummaries(ctx context.Context, since time.Time) ([]ImportSummary, error)
}
