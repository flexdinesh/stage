package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/flexdinesh/stage/server/internal/db/store"

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

type HarnessConnection = store.HarnessConnection

type ProjectBinding = store.ProjectBinding

// Binding origins (project_bindings.origin).
const (
	BindingManual   = "manual"
	BindingImported = "imported"
	BindingPath     = "path"
	BindingRemote   = "remote"
)

type ExternalProject = store.ExternalProject

func (s *Store) Connections(ctx context.Context) ([]HarnessConnection, error) {
	return read(ctx, s, func(tx store.Tx) ([]HarnessConnection, error) { return tx.Connections().List(ctx) })
}

func (s *Store) GetHarnessConnection(ctx context.Context, id int64) (HarnessConnection, error) {
	return read(ctx, s, func(tx store.Tx) (HarnessConnection, error) { return tx.Connections().Get(ctx, id) })
}

// SetAutoComplete sets whether a harness closure may complete a done task.
func (s *Store) SetAutoComplete(ctx context.Context, id int64, on bool) (HarnessConnection, error) {
	return write(ctx, s, func(tx store.Tx) (HarnessConnection, error) {
		if err := tx.Connections().SetAutoComplete(ctx, id, on); err != nil {
			return HarnessConnection{}, err
		}
		return tx.Connections().Get(ctx, id)
	})
}

// SetNewTaskConfig pins (or clears, nil) the model and options new tasks on a
// connection start with. Permission and mode are never pinned: they start at
// the harness default.
func (s *Store) SetNewTaskConfig(ctx context.Context, id int64, c *execution.Config) (HarnessConnection, error) {
	if c != nil {
		c = &execution.Config{Model: c.Model, Options: append([]execution.ModelOption{}, c.Options...)}
	}
	return write(ctx, s, func(tx store.Tx) (HarnessConnection, error) {
		found, err := tx.Connections().SetNewTaskConfig(ctx, id, c)
		if err != nil {
			return HarnessConnection{}, err
		}
		if !found {
			return HarnessConnection{}, ErrNotFound
		}
		return tx.Connections().Get(ctx, id)
	})
}

func (s *Store) ExternalProjects(ctx context.Context, connectionID int64) ([]ExternalProject, error) {
	return read(ctx, s, func(tx store.Tx) ([]ExternalProject, error) { return tx.Projects().ExternalProjects(ctx, connectionID) })
}

// bind makes b the project's active binding on its connection.
func (s *Store) bind(ctx context.Context, tx store.Tx, b ProjectBinding) (int64, error) {
	if b.ConnectionID <= 0 || b.ExternalProjectID == "" || b.Workspace == "" {
		return 0, Invalid("connection, external project and workspace are required")
	}
	if err := requireProject(ctx, tx, b.ProjectID); err != nil {
		return 0, err
	}
	if err := connectionUsable(ctx, tx, b.ConnectionID); err != nil {
		return 0, err
	}
	pr := tx.Projects()
	// A workspace the user rebinds away from is declined like an unbind.
	if err := pr.DeclineRebound(ctx, b); err != nil {
		return 0, err
	}
	if err := pr.DeactivateBindings(ctx, b.ProjectID, b.ConnectionID); err != nil {
		return 0, err
	}
	if b.Origin == "" {
		b.Origin = BindingManual
	}
	id, err := pr.InsertBinding(ctx, b)
	if s.e.Errors().IsUniqueViolation(err) {
		return 0, Conflict("workspace is already bound to another Stage project; unbind it there first")
	}
	if err != nil || b.Origin != BindingManual {
		return id, err
	}
	// A manual bind withdraws an earlier decline of this workspace.
	return id, pr.Undecline(ctx, b.ConnectionID, b.ExternalProjectID, b.Workspace)
}

// bindProject (transitional raw SQL) is bind for a raw transaction.
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
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		if _, err := s.bind(ctx, tx, b); err != nil {
			return Project{}, err
		}
		return tx.Projects().Get(ctx, b.ProjectID)
	})
}

// UnbindProject deactivates the binding and declines its workspace, so
// discovery never binds it again until a user binds it manually.
func (s *Store) UnbindProject(ctx context.Context, projectID, connectionID int64) (Project, error) {
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		if err := tx.Projects().DeclineBound(ctx, projectID, connectionID); err != nil {
			return Project{}, err
		}
		if err := tx.Projects().DeactivateBindings(ctx, projectID, connectionID); err != nil {
			return Project{}, err
		}
		return tx.Projects().Get(ctx, projectID)
	})
}

// ensureSession returns ref's session identity, creating it; Invalid for an
// incomplete ref or an unknown connection.
func ensureSession(ctx context.Context, tx store.Tx, ref SessionRef) (int64, error) {
	if ref.ConnectionID <= 0 || ref.ExternalSessionID == "" {
		return 0, Invalid("connectionId and externalSessionId are required")
	}
	if err := requireConnection(ctx, tx, ref.ConnectionID); err != nil {
		return 0, err
	}
	return tx.Sessions().Ensure(ctx, ref.ConnectionID, ref.ExternalSessionID)
}

// requireConnection is Invalid for an unknown connection.
func requireConnection(ctx context.Context, tx store.Tx, id int64) error {
	exists, err := tx.Connections().Exists(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return Invalid("unknown connection %d; select a configured harness connection", id)
	}
	return nil
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

// connectionInUse (transitional raw SQL) refuses new work on a replaced connection.
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
	return write(ctx, s, func(tx store.Tx) (int64, error) { return ensureSession(ctx, tx, ref) })
}

func (s *Store) TaskBySession(ctx context.Context, ref SessionRef) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, taskSelect+` WHERE t.session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID))
}
func (s *Store) SessionIdentity(ctx context.Context, id int64) (SessionRef, error) {
	return read(ctx, s, func(tx store.Tx) (SessionRef, error) { return tx.Sessions().Ref(ctx, id) })
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
