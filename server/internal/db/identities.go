package db

import (
	"context"
	"errors"
	"github.com/flexdinesh/stage/server/internal/db/store"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func checkDestinationChange(ctx context.Context, tx store.Tx, old, next int64, c Optional[execution.Config], w Optional[Workspace]) error {
	if err := requireConnection(ctx, tx, next); err != nil {
		return err
	}
	if old != next {
		if err := connectionUsable(ctx, tx, next); err != nil {
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

// ConnectionRetired: the connection's instance was replaced. Its history
// stays; new work goes to the harness's current connection.
func ConnectionRetired() error {
	return &ConflictError{Code: "connection_retired", Msg: "This harness connection was replaced. Its history stays here; start new work on the current connection."}
}
func (s *Store) EnsureSession(ctx context.Context, ref SessionRef) (int64, error) {
	return write(ctx, s, func(tx store.Tx) (int64, error) { return ensureSession(ctx, tx, ref) })
}

func (s *Store) TaskBySession(ctx context.Context, ref SessionRef) (Task, error) {
	return read(ctx, s, func(tx store.Tx) (Task, error) { return tx.Tasks().BySession(ctx, ref) })
}
func (s *Store) SessionIdentity(ctx context.Context, id int64) (SessionRef, error) {
	return read(ctx, s, func(tx store.Tx) (SessionRef, error) { return tx.Sessions().Ref(ctx, id) })
}

type LaunchDestination = store.LaunchDestination

func pinDestination(ctx context.Context, tx store.Tx, taskID int64, attempt int) error {
	exists, err := tx.Attempts().HasDestination(ctx, taskID, attempt)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	b, err := tx.Attempts().BoundDestination(ctx, taskID)
	if errors.Is(err, ErrNotFound) {
		return &ConflictError{Code: "binding_required", Msg: "Bind or confirm this project's workspace before launching."}
	}
	if err != nil {
		return err
	}
	return tx.Attempts().PinDestination(ctx, taskID, attempt, b)
}
func (s *Store) LaunchDestination(ctx context.Context, taskID int64, attempt int) (LaunchDestination, error) {
	return read(ctx, s, func(tx store.Tx) (LaunchDestination, error) { return tx.Attempts().Destination(ctx, taskID, attempt) })
}

func (s *Store) ReusesLaunch(ctx context.Context, id int64) (bool, error) {
	return read(ctx, s, func(tx store.Tx) (bool, error) { return tx.Tasks().ReusesLaunch(ctx, id) })
}

// NoteBlockedLaunch preserves a save-and-start task when preflight rejected
// its launch, with the reason visible on the saved task. No attempt is consumed.
func (s *Store) NoteBlockedLaunch(ctx context.Context, id int64, reason string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		ok, err := tx.Tasks().NoteBlocked(ctx, id, reason)
		if err != nil || !ok {
			return err
		}
		from := StateBacklog
		return addTaskEvent(ctx, tx, id, &from, StateBacklog, "Launch blocked: "+reason)
	})
}

// HoldScheduled clears its timer, not its work. A repair cannot start it as a
// delayed surprise, and the pending occurrence suppresses further schedule fires.
func (s *Store) HoldScheduled(ctx context.Context, id int64, reason string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		ok, err := tx.Tasks().HoldScheduled(ctx, id, reason)
		if err != nil || !ok {
			return err
		}
		from := StateBacklog
		return addTaskEvent(ctx, tx, id, &from, StateBacklog, reason+" Start manually after repairing the binding or connection.")
	})
}

// defaultConnection: no connection named means the legacy T3 connection.
func (s *Store) defaultConnection(id int64) int64 {
	if id == 0 {
		return s.LegacyT3ID()
	}
	return id
}
