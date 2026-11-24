package store

import (
	"context"
	"time"
)

// Tree is a DAG of tasks (docs/api.md "Trees").
type Tree struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	StartMode StartMode  `json:"startMode"`
	Status    TreeStatus `json:"status"`
	// ClosedAt: when it was marked done or archived (nil while open).
	ClosedAt  *time.Time `json:"closedAt"`
	CreatedAt time.Time  `json:"createdAt"`
	CreatedBy int64      `json:"createdBy"` // user who created it
	// SourceMachineID: the machine whose agent gateway forwarded the create
	// (nil: not through a gateway).
	SourceMachineID *int64    `json:"sourceMachineId"`
	UpdatedAt       time.Time `json:"updatedAt"`
	// Deletable: no recorded work, including from members that left.
	Deletable         bool      `json:"deletable"`
	JournalActivityAt time.Time `json:"-"`
}

// TreeEdge: TaskID needs NeedID.
type TreeEdge struct {
	TaskID int64 `json:"taskId"`
	NeedID int64 `json:"needId"`
}

// TreeView picks trees by lifecycle: active (open with a member not
// settled), done (marked done, or open with every member settled),
// archived, or all.
type TreeView string

const (
	TreeViewActive   TreeView = "active"
	TreeViewDone     TreeView = "done"
	TreeViewArchived TreeView = "archived"
	TreeViewAll      TreeView = "all"
)

// TreeViews are the valid tree views.
var TreeViews = []string{string(TreeViewActive), string(TreeViewDone), string(TreeViewArchived), string(TreeViewAll)}

// TreeFilter selects trees: View ("" = active); with active, open trees
// completed at or after SettledFrom are listed too.
type TreeFilter struct {
	View        TreeView
	SettledFrom *time.Time
	ProjectID   *int64 // a member is in this project
}

// NeedInfo is a need's tree and title.
type NeedInfo struct {
	ID     int64
	TreeID *int64
	Title  string
}

// NeedsState is what a needs change checks.
type NeedsState struct {
	State   TaskState
	TreeID  *int64
	StartAt *time.Time
}

// Trees is the task tree repository (trees, membership and needs).
type Trees interface {
	Insert(ctx context.Context, title string, mode StartMode, createdBy int64, sourceMachine *int64) (int64, error)
	Get(ctx context.Context, id int64) (Tree, error)
	// List orders by last activity, newest first.
	List(ctx context.Context, f TreeFilter) ([]Tree, error)
	Exists(ctx context.Context, id int64) (bool, error)
	// Status is the tree's status (ErrNotFound).
	Status(ctx context.Context, id int64) (TreeStatus, error)
	// Members are the trees' tasks, by id.
	Members(ctx context.Context, trees []int64) ([]Task, error)
	// Edges are the needs of the trees' members.
	Edges(ctx context.Context, trees []int64) ([]TreeEdge, error)
	// Events are the members' newest task events, newest first.
	Events(ctx context.Context, id int64, limit int) ([]TaskEvent, error)
	// Update sets the title and start mode when set, and updated_at.
	Update(ctx context.Context, id int64, title Optional[string], mode Optional[StartMode]) error
	SetStatus(ctx context.Context, id int64, status TreeStatus) error
	// ActiveMember is the tree's first member launching, running or
	// needing input (ErrNotFound: none).
	ActiveMember(ctx context.Context, id int64) (int64, TaskState, error)
	// ActiveTask is the task when launching, running or needing input
	// (ErrNotFound otherwise).
	ActiveTask(ctx context.Context, taskID int64) (int64, TaskState, error)
	RecordedWork(ctx context.Context, id int64) (bool, error)
	// FirstStarted is the lowest member id with notes or execution history.
	FirstStarted(ctx context.Context, id int64) (*int64, error)
	DeleteMembers(ctx context.Context, id int64) error
	Delete(ctx context.Context, id int64) error
	// DetachMembers drops the members' needs and takes them out of the tree.
	DetachMembers(ctx context.Context, id int64) error
	// ArchiveDissolved archives a dissolved tree.
	ArchiveDissolved(ctx context.Context, id int64) error
	// Startable: the tree's ready members, by id.
	Startable(ctx context.Context, id int64) ([]int64, error)
	// Launchable: released auto members (of connection; 0 = any), by id.
	Launchable(ctx context.Context, connection int64) ([]int64, error)
	// ClearReady clears ready_at where a manual member is no longer released.
	ClearReady(ctx context.Context) error
	// SetReady sets ready_at on newly released manual members and returns
	// their ids.
	SetReady(ctx context.Context) ([]int64, error)
	// NeedsInfo reads the tasks with these ids (unknown ids are skipped).
	NeedsInfo(ctx context.Context, ids []int64) ([]NeedInfo, error)
	IsArchived(ctx context.Context, taskID int64) (bool, error)
	// SetMembership puts the task in tree (nil: none) and bumps updated_at.
	SetMembership(ctx context.Context, taskID int64, tree *int64) error
	// InheritNeeds gives the task's dependents its needs.
	InheritNeeds(ctx context.Context, taskID int64) error
	// TouchDependents bumps the task's dependents' updated_at.
	TouchDependents(ctx context.Context, taskID int64) error
	// DropNeeds removes every need edge of the task, both ways.
	DropNeeds(ctx context.Context, taskID int64) error
	// ClearStaleBases drops workspace.baseTaskId where it no longer names a
	// need.
	ClearStaleBases(ctx context.Context) error
	NeedsState(ctx context.Context, taskID int64) (NeedsState, error)
	// Needs are the task's needs, by id.
	Needs(ctx context.Context, taskID int64) ([]int64, error)
	// WouldCycle: need already (transitively) needs taskID.
	WouldCycle(ctx context.Context, taskID, need int64) (bool, error)
	// KeepNeeds drops the task's needs not in keep.
	KeepNeeds(ctx context.Context, taskID int64, keep []int64) error
	AddNeed(ctx context.Context, taskID, need int64) error
}
