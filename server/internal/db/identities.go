package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func checkDestinationChange(ctx context.Context, tx *Tx, old, next int64, c Optional[execution.Config], w Optional[Workspace]) error {
	if err := connectionExists(ctx, tx, next); err != nil {
		return err
	}
	if old != next {
		if err := connectionInUse(ctx, tx, next); err != nil {
			return err
		}
	}
	if old != next && (!c.Set || c.Null || !w.Set || w.Null) {
		return Invalid("Choose the new harness configuration and workspace when changing destination.")
	}
	return nil
}

type SessionRef = execution.SessionRef

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
}
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
	// for it) or path (discovery bound it to the project at that path).
	Origin string `json:"origin"`
}

// Binding origins (project_bindings.origin).
const (
	BindingManual   = "manual"
	BindingImported = "imported"
	BindingPath     = "path"
)
type ExternalProject struct {
	ConnectionID int64  `json:"connectionId"`
	ExternalID   string `json:"externalId"`
	Title        string `json:"title"`
	Workspace    string `json:"workspace"`
	ProjectID    *int64 `json:"projectId"`
	Repo         string `json:"repo"`
}

const connectionCols = `id,machine_id,harness,auto_complete,active,label,retired_at,forgotten_at,
 (SELECT count(*) FROM tasks t WHERE t.connection_id=c.id),
 (SELECT count(*) FROM project_bindings b WHERE b.connection_id=c.id AND b.active=1)`

func scanConnection(r rowScanner) (HarnessConnection, error) {
	var c HarnessConnection
	err := r.Scan(&c.ID, &c.MachineID, &c.Harness, &c.AutoComplete, &c.Active, &c.Label, &c.RetiredAt, &c.ForgottenAt, &c.Tasks, &c.Bindings)
	return c, notFound(err)
}

// Connections lists every connection: active ones first, then retired
// ones (forgotten included, so their tasks still name the harness).
func (s *Store) Connections(ctx context.Context) ([]HarnessConnection, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+connectionCols+` FROM harness_connections c ORDER BY active DESC,id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanConnection)
}

func (s *Store) GetHarnessConnection(ctx context.Context, id int64) (HarnessConnection, error) {
	return scanConnection(s.pool.QueryRow(ctx, `SELECT `+connectionCols+` FROM harness_connections c WHERE id=$1`, id))
}

// SetAutoComplete sets whether a harness closure may complete a done task.
func (s *Store) SetAutoComplete(ctx context.Context, id int64, on bool) (HarnessConnection, error) {
	if _, err := s.pool.Exec(ctx, `UPDATE harness_connections SET auto_complete=$2 WHERE id=$1`, id, on); err != nil {
		return HarnessConnection{}, err
	}
	return s.GetHarnessConnection(ctx, id)
}
func (s *Store) ExternalProjects(ctx context.Context, connectionID int64) ([]ExternalProject, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.connection_id,e.external_id,e.title,e.workspace,b.project_id,e.repo FROM external_projects e
 LEFT JOIN project_bindings b ON b.connection_id=e.connection_id AND b.external_project_id=e.external_id AND b.workspace=e.workspace AND b.active=1
 WHERE e.connection_id=$1 AND e.available=1 ORDER BY lower(e.title),e.workspace`, connectionID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (ExternalProject, error) {
		var p ExternalProject
		err := r.Scan(&p.ConnectionID, &p.ExternalID, &p.Title, &p.Workspace, &p.ProjectID, &p.Repo)
		return p, err
	})
}
func bindProject(ctx context.Context, tx *Tx, b ProjectBinding) (int64, error) {
	if b.ConnectionID <= 0 || b.ExternalProjectID == "" || b.Workspace == "" {
		return 0, Invalid("connection, external project and workspace are required")
	}
	if err := projectExists(ctx, tx, b.ProjectID); err != nil {
		return 0, err
	}
	if err := connectionInUse(ctx, tx, b.ConnectionID); err != nil {
		return 0, err
	}
	// A workspace the user rebinds away from is declined like an unbind.
	if _, err := tx.Exec(ctx, `UPDATE external_projects SET declined_at=stage_now() WHERE EXISTS(SELECT 1 FROM project_bindings p
 WHERE p.project_id=$1 AND p.connection_id=$2 AND p.active=1 AND p.connection_id=external_projects.connection_id
 AND p.external_project_id=external_projects.external_id AND p.workspace=external_projects.workspace)
 AND NOT (external_id=$3 AND workspace=$4)`, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_bindings SET active=0 WHERE project_id=$1 AND connection_id=$2`, b.ProjectID, b.ConnectionID); err != nil {
		return 0, err
	}
	origin := b.Origin
	if origin == "" {
		origin = BindingManual
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace,revision,origin)
 VALUES($1,$2,$3,$4,COALESCE((SELECT max(revision)+1 FROM project_bindings WHERE project_id=$1 AND connection_id=$2),1),$5) RETURNING id`, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace, origin).Scan(&id)
	if isUniqueViolation(err) {
		return 0, Conflict("workspace is already bound to another Stage project; unbind it there first")
	}
	if err != nil || origin != BindingManual {
		return id, err
	}
	// A manual bind withdraws an earlier decline of this workspace.
	_, err = tx.Exec(ctx, `UPDATE external_projects SET declined_at=NULL WHERE connection_id=$1 AND external_id=$2 AND workspace=$3`, b.ConnectionID, b.ExternalProjectID, b.Workspace)
	return id, err
}
func (s *Store) BindProject(ctx context.Context, b ProjectBinding) (Project, error) {
	err := s.inTx(ctx, func(tx *Tx) error { _, err := bindProject(ctx, tx, b); return err })
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, b.ProjectID)
}
// UnbindProject deactivates the binding and declines its workspace, so
// discovery never binds it again until a user binds it manually.
func (s *Store) UnbindProject(ctx context.Context, projectID, connectionID int64) (Project, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE external_projects SET declined_at=stage_now() WHERE EXISTS(SELECT 1 FROM project_bindings b
 WHERE b.project_id=$1 AND b.connection_id=$2 AND b.active=1 AND b.connection_id=external_projects.connection_id
 AND b.external_project_id=external_projects.external_id AND b.workspace=external_projects.workspace)`, projectID, connectionID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE project_bindings SET active=0 WHERE project_id=$1 AND connection_id=$2`, projectID, connectionID)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, projectID)
}
func sessionIdentity(ctx context.Context, tx *Tx, ref SessionRef) (int64, error) {
	if ref.ConnectionID <= 0 || ref.ExternalSessionID == "" {
		return 0, Invalid("connectionId and externalSessionId are required")
	}
	if err := connectionExists(ctx, tx, ref.ConnectionID); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO session_identities(connection_id,external_id) VALUES($1,$2)
 ON CONFLICT(connection_id,external_id) DO UPDATE SET external_id=excluded.external_id RETURNING id`, ref.ConnectionID, ref.ExternalSessionID).Scan(&id)
	return id, err
}

func connectionExists(ctx context.Context, tx *Tx, id int64) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return Invalid("unknown connection %d; select a configured harness connection", id)
	}
	return nil
}

// connectionInUse refuses new work on a replaced connection.
func connectionInUse(ctx context.Context, tx *Tx, id int64) error {
	if err := connectionExists(ctx, tx, id); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT active FROM harness_connections WHERE id=$1`, id).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ConnectionRetired()
	}
	return nil
}

// ConnectionRetired: the connection's instance was replaced. Its history
// stays; new work goes to the harness's current connection.
func ConnectionRetired() error {
	return &ConflictError{Code: "connection_retired", Msg: "This harness connection was replaced. Its history stays here; start new work on the current connection."}
}
func (s *Store) EnsureSession(ctx context.Context, ref SessionRef) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *Tx) error { var err error; id, err = sessionIdentity(ctx, tx, ref); return err })
	return id, err
}

func (s *Store) TaskBySession(ctx context.Context, ref SessionRef) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, taskSelect+` WHERE t.session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID))
}
func (s *Store) SessionIdentity(ctx context.Context, id int64) (SessionRef, error) {
	var r SessionRef
	err := s.pool.QueryRow(ctx, `SELECT connection_id,external_id FROM session_identities WHERE id=$1`, id).Scan(&r.ConnectionID, &r.ExternalSessionID)
	return r, notFound(err)
}

type LaunchDestination struct {
	ConnectionID      int64
	ExternalProjectID string
	Workspace         string
	BindingID         int64
	BindingRevision   int64
}

func pinDestination(ctx context.Context, tx *Tx, taskID int64, attempt int) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_launch_destinations WHERE task_id=$1 AND attempt=$2)`, taskID, attempt).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var b LaunchDestination
	err := tx.QueryRow(ctx, `SELECT b.connection_id,b.external_project_id,b.workspace,b.id,b.revision FROM tasks t JOIN project_bindings b ON b.project_id=t.project_id AND b.connection_id=t.connection_id AND b.active=1 AND b.status='available' WHERE t.id=$1`, taskID).Scan(&b.ConnectionID, &b.ExternalProjectID, &b.Workspace, &b.BindingID, &b.BindingRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return &ConflictError{Code: "binding_required", Msg: "Bind or confirm this project's workspace before launching."}
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_launch_destinations(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision) VALUES($1,$2,$3,$4,$5,$6,$7)`, taskID, attempt, b.ConnectionID, b.ExternalProjectID, b.Workspace, b.BindingID, b.BindingRevision)
	return err
}
func (s *Store) LaunchDestination(ctx context.Context, taskID int64, attempt int) (LaunchDestination, error) {
	var b LaunchDestination
	err := s.pool.QueryRow(ctx, `SELECT connection_id,external_project_id,workspace,binding_id,binding_revision FROM task_launch_destinations WHERE task_id=$1 AND attempt=$2`, taskID, attempt).Scan(&b.ConnectionID, &b.ExternalProjectID, &b.Workspace, &b.BindingID, &b.BindingRevision)
	return b, notFound(err)
}

func (s *Store) ReusesLaunch(ctx context.Context, id int64) (bool, error) {
	var reuse bool
	err := s.pool.QueryRow(ctx, `SELECT launch_reuse FROM tasks WHERE id=$1`, id).Scan(&reuse)
	return reuse, notFound(err)
}

// NoteBlockedLaunch preserves a save-and-start task when preflight rejected
// its launch, with the reason visible on the saved task. No attempt is consumed.
func (s *Store) NoteBlockedLaunch(ctx context.Context, id int64, reason string) error {
	return s.inTx(ctx, func(tx *Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tasks SET last_error=$2,updated_at=stage_now() WHERE id=$1 AND state='backlog'`, id, reason)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES($1,'backlog','backlog',$2)`, id, "Launch blocked: "+reason)
		return err
	})
}

// HoldScheduled clears its timer, not its work. A repair cannot start it as a
// delayed surprise, and the pending occurrence suppresses further schedule fires.
func (s *Store) HoldScheduled(ctx context.Context, id int64, reason string) error {
	return s.inTx(ctx, func(tx *Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tasks SET scheduled_hold=1,start_at=NULL,start_tz=NULL,last_error=$2,updated_at=stage_now() WHERE id=$1 AND state='backlog' AND start_at IS NOT NULL`, id, reason)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES($1,'backlog','backlog',$2)`, id, reason+" Start manually after repairing the binding or connection.")
		return err
	})
}

// defaultConnection: no connection named means the legacy T3 connection.
func (s *Store) defaultConnection(id int64) int64 {
	if id == 0 {
		return s.LegacyT3ID()
	}
	return id
}
