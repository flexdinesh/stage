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
	// LastActivityAt: latest thread update/settle or task update; nil when none.
	LastActivityAt *time.Time `json:"lastActivityAt"`
	// Origin is "discovered" for a project discovery imported, else nil.
	Origin     *string    `json:"origin"`
	ImportedAt *time.Time `json:"importedAt"`
	// Repos: distinct normalized origin remotes of the active bindings'
	// workspaces. Search metadata only; titles and paths stay authoritative.
	Repos []string `json:"repos"`
}

// ImportSummary counts the projects one connection's discovery created in
// the recent window (ImportNoticeWindow), with the latest import time.
type ImportSummary struct {
	ConnectionID int64     `json:"connectionId"`
	Harness      string    `json:"harness"`
	Count        int       `json:"count"`
	At           time.Time `json:"at"`
}

// ExternalProject is a discovered harness project and workspace.
type ExternalProject struct {
	ConnectionID int64  `json:"connectionId"`
	ExternalID   string `json:"externalId"`
	Title        string `json:"title"`
	Workspace    string `json:"workspace"`
	ProjectID    *int64 `json:"projectId"`
	Repo         string `json:"repo"`
}

// DiscoveredWorkspace is one discovery row to upsert.
type DiscoveredWorkspace struct {
	ExternalID string
	Title      string
	Workspace  string
	Available  bool
	Importable bool
	Archived   bool
	ActiveAt   *time.Time
	Repo       string
	RepoPath   string
}

// ImportCandidate is an importable discovered workspace.
type ImportCandidate struct {
	ExternalID, Title, Workspace, Repo, RepoPath string
}

// Claim is an active binding with what discovery stored about its
// workspace (Repo and RepoPath "" when unknown), the project identity
// ladder's evidence (projectmatch).
type Claim struct {
	BindingID, ProjectID, ConnectionID, MachineID int64
	Workspace, Repo, RepoPath                     string
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
	// Update sets title and archive state; a change of archive state makes
	// the user its owner.
	Update(ctx context.Context, id int64, title string, archived bool) error

	// DeclineRebound declines the workspaces the project is actively bound
	// to on b's connection, other than b's own.
	DeclineRebound(ctx context.Context, b ProjectBinding) error
	// DeclineBound declines the workspaces the project is actively bound to
	// on connectionID.
	DeclineBound(ctx context.Context, projectID, connectionID int64) error
	DeactivateBindings(ctx context.Context, projectID, connectionID int64) error
	// InsertBinding adds b as the next revision (unique violation when the
	// workspace is bound elsewhere).
	InsertBinding(ctx context.Context, b ProjectBinding) (int64, error)
	Undecline(ctx context.Context, connectionID int64, externalID, workspace string) error
	// AdoptionTarget is the sole project actively and availably bound to
	// (connection, external project[, workspace]) (false: none or several).
	AdoptionTarget(ctx context.Context, connectionID int64, externalID string, workspace *string) (int64, bool, error)
	// BoundOn: the project has an active binding on the connection.
	BoundOn(ctx context.Context, projectID, connectionID int64) (bool, error)
	// AutoBindEligible: source is an active available binding of the
	// project, the project has none on b's connection, and b's workspace is
	// available there.
	AutoBindEligible(ctx context.Context, source int64, b ProjectBinding) (bool, error)

	// ExternalProjects lists a connection's available discovery.
	ExternalProjects(ctx context.Context, connectionID int64) ([]ExternalProject, error)
	MarkUnavailable(ctx context.Context, connectionID int64) error
	UpsertDiscovered(ctx context.Context, connectionID int64, w DiscoveredWorkspace) error
	// RefreshBindingStatus recomputes the connection's active bindings'
	// status from discovery.
	RefreshBindingStatus(ctx context.Context, connectionID int64) error
	// ImportCandidates: available, importable, unarchived, undeclined,
	// unbound workspaces, by workspace then most recently active.
	ImportCandidates(ctx context.Context, connectionID int64) ([]ImportCandidate, error)
	// Claims are every active binding, retired connections' included, by
	// project then binding.
	Claims(ctx context.Context) ([]Claim, error)
	// FollowArchive mirrors harness archives onto discovered projects.
	FollowArchive(ctx context.Context) (archived, unarchived int, err error)
	ImportSummaries(ctx context.Context, since time.Time) ([]ImportSummary, error)
}
