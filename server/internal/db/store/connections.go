package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
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
	// Label names the instance (T3 environment, OpenCode address).
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

// SharedPath maps a harness directory to the directory Stage sees.
type SharedPath struct {
	ConnectionID int64  `json:"connectionId"`
	HarnessRoot  string `json:"harnessRoot"`
	StageRoot    string `json:"stageRoot"`
}

// Connections is the harness connections repository, with each machine's
// T3, OpenCode and Claude Code settings and shared paths.
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
	HarnessIs(ctx context.Context, id int64, harness string) (bool, error)
	// LegacyT3 is the local machine's active T3 connection (0 when none).
	LegacyT3(ctx context.Context) (int64, error)
	// MachineOf is a connection's machine (ErrNotFound when unknown).
	MachineOf(ctx context.Context, id int64) (int64, error)
	// Rows lists every connection's identity row, by id.
	Rows(ctx context.Context) ([]ConnectionRow, error)
	// List: active first, then retired (forgotten included), by id.
	List(ctx context.Context) ([]HarnessConnection, error)
	Get(ctx context.Context, id int64) (HarnessConnection, error)
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
	// Referenced: tasks, schedules, sessions or launch destinations name it.
	Referenced(ctx context.Context, id int64) (bool, error)
	// FollowReplacement moves the default connection, unlaunched backlog
	// tasks and schedules from one connection to another.
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
	// and drops its discovery and shared paths.
	Forget(ctx context.Context, id int64) error
	// Moved lists active stable identities also active on another machine.
	Moved(ctx context.Context) ([]Move, error)
	// Retired lists machine's retired, not forgotten connections of
	// harness, newest first.
	Retired(ctx context.Context, machine int64, harness string) ([]int64, error)
	// EnsureOpenCode is machine's active OpenCode connection, inserted when none.
	EnsureOpenCode(ctx context.Context, machine int64) (int64, error)
	// Evidence: the connection's bindings (external project and workspace,
	// retired included), its sessions' observations (nil: unobserved), and
	// whether a task on it is launching.
	Evidence(ctx context.Context, id int64) ([]ProjectBinding, []*execution.Observation, bool, error)

	// SharedPaths lists machine's shared paths (0: every machine).
	SharedPaths(ctx context.Context, machine int64) ([]SharedPath, error)
	DeleteSharedPath(ctx context.Context, connectionID int64, harnessRoot string) error
	PutSharedPath(ctx context.Context, p SharedPath) error

	// T3Credential is machine's T3 token (ErrNotFound when no row).
	T3Credential(ctx context.Context, machine int64) (execution.Credential, error)
	SaveT3Credential(ctx context.Context, machine int64, tok execution.Credential) error
	SetT3LastError(ctx context.Context, machine int64, msg string) error
	T3Settings(ctx context.Context, machine int64) (T3Connection, error)
	SetT3URL(ctx context.Context, machine int64, u string) error
	SetT3PublicURL(ctx context.Context, machine int64, u string) error
	// T3AdminToken: "" when none.
	T3AdminToken(ctx context.Context, machine int64) (string, error)
	SaveT3AdminToken(ctx context.Context, machine int64, tok string) error
	SetT3LastFullSync(ctx context.Context, machine int64, at time.Time) error

	// OpenCodeSettings reads machine's row (found false: none).
	OpenCodeSettings(ctx context.Context, machine int64) (c config.OpenCodeSettings, found bool, err error)
	// OpenCodeIdentity is the identity of machine's active OpenCode connection ("" when none).
	OpenCodeIdentity(ctx context.Context, machine int64) (string, error)
	SaveOpenCodeSettings(ctx context.Context, machine int64, c config.OpenCodeSettings) error

	// ClaudeSettings reads machine's claude_settings row (found false: none).
	ClaudeSettings(ctx context.Context, machine int64) (c config.ClaudeSettings, found bool, err error)
	// SaveClaudeSettings upserts machine's row and sets its updated_at.
	SaveClaudeSettings(ctx context.Context, machine int64, c config.ClaudeSettings) error
}

// T3Connection is the non-secret part of a machine's t3_connection row.
type T3Connection struct {
	LastError      *string
	LastFullSyncAt *time.Time
	// T3URL is the T3 URL saved in Settings (nil = default).
	T3URL *string
	// T3PublicURL is the browser URL saved in Settings (nil = the T3 URL).
	T3PublicURL *string
	// EnvironmentID is the identity of the machine's active T3 connection
	// (nil until the first successful pairing).
	EnvironmentID *string
	// ConnectionID is the machine's active T3 connection.
	ConnectionID int64
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
	ConnectionActive bool   `json:"connectionActive"`
	ConnectionLabel  string `json:"connectionLabel"`
	// Origin: manual (user bound), imported (discovery created the project
	// for it), path (discovery bound it to the project at that path on this
	// machine) or remote (discovery bound it to the one project with the same
	// origin remote and subproject on another machine).
	Origin string `json:"origin"`
}
