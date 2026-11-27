package db

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/projectmatch"
)

func checkDestinationChange(ctx context.Context, tx store.Tx, old, next int64, c Optional[execution.Config]) error {
	if err := requireConnection(ctx, tx, next); err != nil {
		return err
	}
	if old != next {
		if err := connectionUsable(ctx, tx, next); err != nil {
			return err
		}
	}
	if old != next && (!c.Set || c.Null) {
		return Invalid("Choose the new harness configuration when changing destination.")
	}
	return nil
}

type SessionRef = execution.SessionRef

type HarnessConnection = store.HarnessConnection

type ProjectBinding = store.ProjectBinding

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

func workspaceBound(ctx context.Context, pr store.Projects, id int64) error {
	p, err := pr.Get(ctx, id)
	if err != nil {
		return err
	}
	return &ConflictError{Code: "workspace_bound", Msg: fmt.Sprintf("This workspace is already bound to project #%d %q.", p.ID, p.Title)}
}

// bind adds a manual binding. The caller holds the identity lock.
func (s *Store) bind(ctx context.Context, tx store.Tx, b ProjectBinding) (int64, error) {
	if b.ConnectionID <= 0 || b.ExternalProjectID == "" {
		return 0, Invalid("connection and external project are required")
	}
	if err := requireProject(ctx, tx, b.ProjectID); err != nil {
		return 0, err
	}
	if err := connectionUsable(ctx, tx, b.ConnectionID); err != nil {
		return 0, err
	}
	pr := tx.Projects()
	current, err := pr.ActiveWorkspaceBinding(ctx, b.ConnectionID, b.ExternalProjectID, b.Workspace)
	switch {
	case err == nil && current.ProjectID == b.ProjectID:
		return current.ID, nil
	case err == nil:
		return 0, workspaceBound(ctx, pr, current.ProjectID)
	case !errors.Is(err, ErrNotFound):
		return 0, err
	}
	state, err := loadIdentity(ctx, pr)
	if err != nil {
		return 0, err
	}
	w := projectmatch.Workspace{Connection: b.ConnectionID, ExternalID: b.ExternalProjectID, Path: b.Workspace}
	if i, ok := state.byRef[refOf(w)]; ok {
		w = state.input.Workspaces[i].Workspace
		if !w.Available {
			return 0, Conflict("The workspace changed. Refresh and choose it again.")
		}
	} else {
		for _, r := range state.rows {
			if r.ConnectionID == b.ConnectionID && r.ExternalID == b.ExternalProjectID {
				return 0, Conflict("The workspace changed. Refresh and choose it again.")
			}
		}
	}
	var id int64
	if b.Replaces != 0 {
		old, err := pr.Binding(ctx, b.Replaces)
		if err != nil {
			return 0, err
		}
		if !old.Active || old.ProjectID != b.ProjectID || old.ConnectionID != b.ConnectionID {
			return 0, Conflict("The binding changed. Refresh and choose the workspace again.")
		}
		id, err = s.moveBinding(ctx, tx, b, old.ProjectBinding)
		if err != nil {
			return 0, err
		}
	} else {
		hasBinding, err := pr.BoundOn(ctx, b.ProjectID, b.ConnectionID)
		if err != nil {
			return 0, err
		}
		requestPrimary := b.Primary
		b.Rule, b.Primary = "manual", !hasBinding
		id, err = pr.InsertBinding(ctx, b)
		if err != nil {
			return 0, err
		}
		if hasBinding && requestPrimary {
			if err := pr.PromoteBinding(ctx, b.ProjectID, b.ConnectionID, id); err != nil {
				return 0, err
			}
		}
	}
	return id, setWorkspaceDecline(ctx, pr, w, false)
}

func (s *Store) moveBinding(ctx context.Context, tx store.Tx, next, old ProjectBinding) (int64, error) {
	pr := tx.Projects()
	conn, err := tx.Connections().Get(ctx, next.ConnectionID)
	if err != nil {
		return 0, err
	}
	if err := pr.DeactivateBinding(ctx, old.ID); err != nil {
		return 0, err
	}
	next.Rule, next.Primary = "moved", old.Primary
	id, err := pr.InsertBinding(ctx, next)
	if err != nil {
		return 0, err
	}
	label := conn.Label
	if label == "" {
		label = conn.Harness
	}
	text := fmt.Sprintf("Workspace moved from %s to %s on %s.", old.Workspace, next.Workspace, label)
	return id, pr.AddProjectEvent(ctx, next.ProjectID, "workspace_moved", text, ActingUser(ctx))
}

func (s *Store) BindProject(ctx context.Context, b ProjectBinding) (Project, error) {
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return Project{}, err
		}
		bound := b
		id, err := projectID(ctx, tx, b.ProjectID)
		if err != nil {
			return Project{}, err
		}
		bound.ProjectID = id
		if _, err := s.bind(ctx, tx, bound); err != nil {
			return Project{}, err
		}
		if err := resolveAll(ctx, tx); err != nil {
			return Project{}, err
		}
		return pr.Get(ctx, id)
	})
}

// UnbindProject removes one binding, preserving launch history.
func (s *Store) UnbindProject(ctx context.Context, project, binding int64) (Project, error) {
	return s.editBinding(ctx, project, binding, func(ctx context.Context, pr store.Projects, b store.BindingState) error {
		if !b.Active {
			return ErrNotFound
		}
		state, err := loadIdentity(ctx, pr)
		if err != nil {
			return err
		}
		w := projectmatch.Workspace{Connection: b.ConnectionID, ExternalID: b.ExternalProjectID, Path: b.Workspace}
		if i, ok := state.byRef[refOf(w)]; ok {
			w = state.input.Workspaces[i].Workspace
		}
		if err := pr.DeactivateBinding(ctx, b.ID); err != nil {
			return err
		}
		if err := setWorkspaceDecline(ctx, pr, w, true); err != nil {
			return err
		}
		if b.Primary {
			return pr.PromoteBinding(ctx, b.ProjectID, b.ConnectionID, 0)
		}
		return nil
	})
}

func (s *Store) SetPrimaryBinding(ctx context.Context, project, binding int64) (Project, error) {
	return s.editBinding(ctx, project, binding, func(ctx context.Context, pr store.Projects, b store.BindingState) error {
		if !b.Active {
			return Conflict("This workspace binding is inactive.")
		}
		if b.Primary {
			return nil
		}
		return pr.PromoteBinding(ctx, b.ProjectID, b.ConnectionID, b.ID)
	})
}

func (s *Store) editBinding(ctx context.Context, project, binding int64, edit func(context.Context, store.Projects, store.BindingState) error) (Project, error) {
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return Project{}, err
		}
		id, err := projectID(ctx, tx, project)
		if err != nil {
			return Project{}, err
		}
		b, err := pr.Binding(ctx, binding)
		if err != nil {
			return Project{}, err
		}
		if b.ProjectID != id {
			return Project{}, ErrNotFound
		}
		if err := edit(ctx, pr, b); err != nil {
			return Project{}, err
		}
		if err := resolveAll(ctx, tx); err != nil {
			return Project{}, err
		}
		return pr.Get(ctx, id)
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

type LaunchCheck = store.LaunchCheck

func pinDestination(ctx context.Context, tx store.Tx, taskID int64, attempt int, check *LaunchCheck) error {
	exists, err := tx.Attempts().HasDestination(ctx, taskID, attempt)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	task, err := tx.Tasks().Get(ctx, taskID)
	if err != nil {
		return err
	}
	if check != nil && (task.ConnectionID != check.ConnectionID || task.WorkspacePin == nil ||
		*task.WorkspacePin != check.WorkspacePin || !sameLaunchConfig(task.ExecutionConfig, check.Config)) {
		return Conflict("The destination or launch options changed. Review the task and start again.")
	}
	b, err := workDestination(ctx, tx, task)
	if err != nil {
		return err
	}
	return tx.Attempts().PinDestination(ctx, taskID, attempt, b)
}

func sameLaunchConfig(a, b *execution.Config) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Model == b.Model && a.Mode == b.Mode && a.Permission == b.Permission && slices.Equal(a.Options, b.Options)
}

func BindingUnavailable() error {
	return &ConflictError{Code: "binding_unavailable", Msg: "The saved workspace is unavailable on this connection. Restore it or choose a connection and workspace again."}
}
func (s *Store) LaunchDestination(ctx context.Context, taskID int64, attempt int) (LaunchDestination, error) {
	return read(ctx, s, func(tx store.Tx) (LaunchDestination, error) { return tx.Attempts().Destination(ctx, taskID, attempt) })
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
