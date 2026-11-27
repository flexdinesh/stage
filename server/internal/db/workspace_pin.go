package db

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type WorkspacePin = store.WorkspacePin

func DestinationRequired() error {
	return &ConflictError{Code: "destination_required", Msg: "Choose a connection and workspace before launching. Agents can find both IDs in list_projects or GET /api/projects?view=destinations."}
}

func DestinationUnavailable() error {
	return &ConflictError{Code: "destination_unavailable", Msg: "The selected workspace is unavailable on its connection. Restore that workspace or choose a connection and workspace again."}
}

func pinOf(w Workspace) *WorkspacePin {
	return &WorkspacePin{ExternalID: w.ExternalID, Root: w.Workspace}
}

func selectorOf(connection int64, pin *WorkspacePin) string {
	if pin == nil {
		return ""
	}
	return WorkspaceID(connection, pin.ExternalID, pin.Root)
}

// ResolveDestination resolves only a selector in the named connection's
// current catalog. Workspace native IDs and paths stay inside Stage.
func (s *Store) ResolveDestination(ctx context.Context, connection int64, selector string) (Workspace, error) {
	return read(ctx, s, func(tx store.Tx) (Workspace, error) {
		return resolveDestination(ctx, tx, connection, selector)
	})
}

func resolveDestination(ctx context.Context, tx store.Tx, connection int64, selector string) (Workspace, error) {
	if connection <= 0 || selector == "" {
		return Workspace{}, DestinationRequired()
	}
	rows, err := tx.Projects().WorkspaceRows(ctx)
	if err != nil {
		return Workspace{}, err
	}
	for _, row := range rows {
		if row.ConnectionID != connection || !row.ConnectionActive || !row.Available || row.HarnessArchived ||
			WorkspaceID(connection, row.ExternalID, row.Workspace.Workspace) != selector {
			continue
		}
		status, err := tx.Connections().WorkspaceRefresh(ctx, connection)
		if err != nil {
			return Workspace{}, err
		}
		if status.RefreshedAt == nil || status.LastError != nil {
			break
		}
		row.WorkspaceID = selector
		return row.Workspace, nil
	}
	return Workspace{}, DestinationUnavailable()
}

// LegacyDestination snapshots the binding that legacy clients selected.
// Missing or ambiguous defaults leave planning work unresolved.
func (s *Store) LegacyDestination(ctx context.Context, project, connection int64, binding *int64) (*WorkspacePin, error) {
	return read(ctx, s, func(tx store.Tx) (*WorkspacePin, error) {
		return legacyDestination(ctx, tx, project, connection, binding)
	})
}

func legacyDestination(ctx context.Context, tx store.Tx, project, connection int64, binding *int64) (*WorkspacePin, error) {
	if binding != nil {
		if err := validateBinding(ctx, tx, project, connection, binding); err != nil {
			return nil, err
		}
		b, err := tx.Projects().Binding(ctx, *binding)
		if err != nil {
			return nil, err
		}
		return &WorkspacePin{ExternalID: b.ExternalProjectID, Root: b.Workspace}, nil
	}
	if project == 0 || connection == 0 {
		return nil, nil
	}
	p, err := tx.Projects().Get(ctx, project)
	if err != nil {
		return nil, err
	}
	var pin *WorkspacePin
	for _, b := range p.Bindings {
		if b.ConnectionID == connection && b.ConnectionActive && b.Primary && b.Status == "available" {
			if pin != nil {
				return nil, nil
			}
			pin = &WorkspacePin{ExternalID: b.ExternalProjectID, Root: b.Workspace}
		}
	}
	return pin, nil
}

// creationDestination runs under the identity lock. When the caller leaves
// project context open, an unbound workspace uses its historical association.
func creationDestination(ctx context.Context, tx store.Tx, connection, project int64, binding *int64, selector string) (int64, *WorkspacePin, error) {
	if project != 0 || selector == "" {
		var err error
		project, err = projectID(ctx, tx, project)
		if err != nil {
			return 0, nil, err
		}
	}
	if selector == "" {
		pin, err := legacyDestination(ctx, tx, project, connection, binding)
		return project, pin, err
	}
	if binding != nil {
		return 0, nil, Invalid("Choose workspaceId or the legacy bindingId.")
	}
	w, err := resolveDestination(ctx, tx, connection, selector)
	if err != nil {
		return 0, nil, err
	}
	if project == 0 {
		if w.ProjectID != nil {
			project = *w.ProjectID
		} else {
			b := ProjectBinding{ConnectionID: connection, ExternalProjectID: w.ExternalID, Workspace: w.Workspace}
			project, err = historicalWorkspaceProject(ctx, tx.Projects(), b, WorkspaceTitle(w.Title, w.Workspace))
			if err != nil {
				return 0, nil, err
			}
		}
	}
	return project, pinOf(w), nil
}

func destinationPatch(ctx context.Context, tx store.Tx, project, connection int64, p, c, b Optional[int64], selector Optional[string]) (Optional[int64], Optional[WorkspacePin], error) {
	var pin Optional[WorkspacePin]
	nextProject, nextConnection := project, connection
	if p.Set {
		nextProject = p.V
	}
	if c.Set {
		nextConnection = c.V
	}
	if selector.Set {
		if b.Set && !b.Null {
			return b, pin, Invalid("Choose workspaceId or the legacy bindingId.")
		}
		pin = Optional[WorkspacePin]{Set: true, Null: selector.Null}
		if !selector.Null {
			w, err := resolveDestination(ctx, tx, nextConnection, selector.V)
			if err != nil {
				return b, pin, err
			}
			pin.V = *pinOf(w)
		}
		return Optional[int64]{Set: true, Null: true}, pin, nil
	}
	if !b.Set && nextConnection == connection {
		// Project-only changes cannot alter a launch pin.
		return b, pin, nil
	}
	if !b.Set {
		b = Optional[int64]{Set: true, Null: true}
	}
	selected, err := legacyDestination(ctx, tx, nextProject, nextConnection, b.Ptr())
	pin = Optional[WorkspacePin]{Set: true, Null: selected == nil}
	if selected != nil {
		pin.V = *selected
	}
	return b, pin, err
}

func verifiedPin(ctx context.Context, tx store.Tx, connection int64, pin *WorkspacePin) (Workspace, error) {
	if pin == nil {
		return Workspace{}, DestinationRequired()
	}
	return resolveDestination(ctx, tx, connection, selectorOf(connection, pin))
}

func workDestination(ctx context.Context, tx store.Tx, task Task) (LaunchDestination, error) {
	if _, err := verifiedPin(ctx, tx, task.ConnectionID, task.WorkspacePin); err != nil {
		var conflict *ConflictError
		if task.BindingID != nil && errors.As(err, &conflict) && conflict.Code == "destination_unavailable" {
			return LaunchDestination{}, BindingUnavailable()
		}
		return LaunchDestination{}, err
	}
	d := LaunchDestination{ConnectionID: task.ConnectionID, ExternalProjectID: task.WorkspacePin.ExternalID, Workspace: task.WorkspacePin.Root}
	// Binding evidence is optional and never supplies launch identity.
	b, err := tx.Projects().ActiveWorkspaceBinding(ctx, d.ConnectionID, d.ExternalProjectID, d.Workspace)
	if err == nil && b.ProjectID == task.ProjectID {
		d.BindingID, d.BindingRevision = b.ID, b.Revision
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return LaunchDestination{}, err
	}
	return d, nil
}
