package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// HarnessConnection is one harness instance. At most one per harness is
// active; a replaced instance is retired and keeps its tasks and bindings.
type HarnessConnection struct {
	Configured   bool   `json:"configured"`
	ID           int64  `json:"id"`
	MachineID    int64  `json:"machineId"`
	Harness      string `json:"harness"`
	AutoComplete bool   `json:"autoComplete"`
	Active       bool   `json:"active"`
	// Label is the display name of the instance.
	Label       string  `json:"label"`
	RetiredAt   *string `json:"retiredAt"`
	ForgottenAt *string `json:"forgottenAt"`
	// Tasks and Bindings count what stays on the connection.
	Tasks    int `json:"tasks"`
	Bindings int `json:"bindings"`
	// NewTaskConfig is the model and options pinned for new tasks here
	// (permission and mode empty; nil = the harness default).
	NewTaskConfig *execution.Config `json:"newTaskConfig"`
}

// ConnectionRow is a connection's identity row (the db.Store cache).
type ConnectionRow struct {
	ID, Machine int64
	Harness     string
	Active      bool
}

// ActiveConnectionRow is the active connection of a harness on a machine.
type ActiveConnectionRow struct {
	ID           int64
	Identity     string
	AutoComplete bool
}

// Move is a connection whose instance now runs on another machine: Old is
// active there but holds the same stable identity as New, active elsewhere.
type Move struct {
	Old, New int64
}

// Connections is the harness connections repository.
type Connections interface {
	// Lock locks the connection rows id until the unit of work ends, on engines
	// with row locks (Postgres FOR NO KEY UPDATE, ascending id; a missing row
	// is skipped). SQLite's single writer already serializes writes: no-op.
	// Take locks in the order of docs/storage.md "Transactions and locking":
	// machine, connection, tree, task, schedule.
	Lock(ctx context.Context, id int64) error
	Exists(ctx context.Context, id int64) (bool, error)
	// IsActive reads a connection's active flag (ErrNotFound when unknown).
	IsActive(ctx context.Context, id int64) (bool, error)
	// MachineOf is a connection's machine (ErrNotFound when unknown).
	MachineOf(ctx context.Context, id int64) (int64, error)
	// Rows lists every connection's identity row, by id.
	Rows(ctx context.Context) ([]ConnectionRow, error)
	// List: active first, then retired (forgotten included), by id.
	List(ctx context.Context) ([]HarnessConnection, error)
	Get(ctx context.Context, id int64) (HarnessConnection, error)
	ConnectionFacts(ctx context.Context, id int64) (string, error)
	SaveConnectionFacts(ctx context.Context, id int64, facts string) error
	SetAutoComplete(ctx context.Context, id int64, on bool) error
	// SetNewTaskConfig: false when the connection is unknown.
	SetNewTaskConfig(ctx context.Context, id int64, c *execution.Config) (bool, error)

	// Active is machine's active connection of harness (ErrNotFound when none).
	Active(ctx context.Context, machine int64, harness string) (ActiveConnectionRow, error)
	// ActiveOnMachine lists machine's active connection ids.
	ActiveOnMachine(ctx context.Context, machine int64) ([]int64, error)
	Insert(ctx context.Context, machine int64, harness, identity, label string, stableIdentity bool) (int64, error)
	// InsertReplacement inserts the replacement of connection from, copying
	// its new-task config.
	InsertReplacement(ctx context.Context, machine int64, harness, identity, label string, autoComplete, stableIdentity bool, from int64) (int64, error)
	// Restorable: id is a retired, not forgotten connection of harness on machine.
	Restorable(ctx context.Context, id, machine int64, harness string) (bool, error)
	// RetiredWithIdentity finds a retired connection by identity
	// (ErrNotFound when none).
	RetiredWithIdentity(ctx context.Context, machine int64, harness, identity string) (int64, error)
	SetLabel(ctx context.Context, id int64, label string) error
	SetIdentity(ctx context.Context, id int64, identity, label string) error
	Retire(ctx context.Context, id int64) error
	// Revive makes a retired connection active again; empty label or
	// identity keeps the stored one.
	Revive(ctx context.Context, id int64, label, identity string) error
	// HasRelations: bindings, sessions or launched tasks tie it to its instance.
	HasRelations(ctx context.Context, id int64) (bool, error)
	// HasWork: any task or schedule names it.
	HasWork(ctx context.Context, id int64) (bool, error)
	// Referenced: tasks, schedules, sessions or launch destinations name it,
	// or workspace choices still reference one of its bindings.
	Referenced(ctx context.Context, id int64) (bool, error)
	// FollowReplacement moves the default connection, unlaunched backlog
	// tasks and schedules to a replacement, preserving workspace choices.
	FollowReplacement(ctx context.Context, from, to int64) error
	ClearDefault(ctx context.Context, id int64) error
	ClearDiscovery(ctx context.Context, id int64) error
	// Delete deletes a connection with its discovery and shared paths.
	Delete(ctx context.Context, id int64) error
	// RetireState reads a not forgotten connection (ErrNotFound otherwise).
	RetireState(ctx context.Context, id int64) (harness string, active bool, err error)
	// IsActiveReplacement: replacement is another active connection of harness.
	IsActiveReplacement(ctx context.Context, replacement int64, harness string, id int64) (bool, error)
	// ForgetState (ErrNotFound when unknown).
	ForgetState(ctx context.Context, id int64) (active, forgotten bool, err error)
	DeleteBindings(ctx context.Context, id int64) error
	// Forget clears identity, marks it forgotten, deactivates its bindings
	// and drops its discovery and workspace declines.
	Forget(ctx context.Context, id int64) error
	// Moved lists active stable identities also active on another machine.
	Moved(ctx context.Context) ([]Move, error)
	// Retired lists machine's retired, not forgotten connections of
	// harness, newest first.
	Retired(ctx context.Context, machine int64, harness string) ([]int64, error)
	// Evidence: the connection's bindings (external project and workspace,
	// retired included), its sessions' observations (nil: unobserved), and
	// whether a task on it is launching.
	Evidence(ctx context.Context, id int64) ([]ProjectBinding, []*execution.Observation, bool, error)
}

// ProjectBinding binds a Stage project to a harness project and workspace.
type ProjectBinding struct {
	ID                int64   `json:"id"`
	ProjectID         int64   `json:"projectId"`
	ConnectionID      int64   `json:"connectionId"`
	Harness           string  `json:"harness"`
	ExternalProjectID string  `json:"externalProjectId"`
	Workspace         string  `json:"workspace"`
	Revision          int64   `json:"revision"`
	Status            string  `json:"status"`
	ObservedWorkspace *string `json:"observedWorkspace"`
	// ConnectionActive is false for a binding on a replaced instance.
	ConnectionActive bool             `json:"connectionActive"`
	ConnectionLabel  string           `json:"connectionLabel"`
	Primary          bool             `json:"primary"`
	Rule             string           `json:"rule"`
	MachineID        int64            `json:"machineId"`
	Title            string           `json:"title"`
	Remote           string           `json:"remote"`
	Subpath          string           `json:"subpath"`
	FileHost         *int64           `json:"fileHost"`
	EvidenceAt       *time.Time       `json:"evidenceAt"`
	Facts            []execution.Fact `json:"facts"`
	Replaces         int64            `json:"-"`
}
