package db

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

func (s *Store) ListProjects(ctx context.Context, archived string) ([]Project, error) {
	return read(ctx, s, func(tx store.Tx) ([]Project, error) {
		return tx.Projects().List(ctx, projectArchive(archived), false)
	})
}

func projectArchive(archived string) store.ProjectArchive {
	switch archived {
	case "true":
		return store.ProjectsArchived
	case "all":
		return store.ProjectsAll
	default:
		return store.ProjectsActive
	}
}

// BoundProject resolves the verified workspace in a single connection.
func (s *Store) BoundProject(ctx context.Context, b ProjectBinding) (Project, error) {
	return read(ctx, s, func(tx store.Tx) (Project, error) { return tx.Projects().Bound(ctx, b) })
}

func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	return read(ctx, s, func(tx store.Tx) (Project, error) {
		canonical, err := projectID(ctx, tx, id)
		if err != nil {
			return Project{}, err
		}
		return tx.Projects().Get(ctx, canonical)
	})
}

// requireProject is ErrNotFound (wrapped "project: ...") for an unknown project.
func requireProject(ctx context.Context, tx store.Tx, id int64) error {
	ok, err := tx.Projects().Exists(ctx, id)
	if err != nil {
		return fmt.Errorf("project: %w", err)
	}
	if !ok {
		return fmt.Errorf("project: %w", ErrNotFound)
	}
	return nil
}

func (s *Store) InsertProject(ctx context.Context, p Project) (Project, error) {
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		if len(p.Bindings) > 0 {
			if err := tx.Projects().LockIdentity(ctx); err != nil {
				return Project{}, err
			}
		}
		id, err := tx.Projects().Insert(ctx, p.Title, ActingUser(ctx), ActingMachine(ctx))
		if err != nil {
			return Project{}, err
		}
		for _, b := range p.Bindings {
			b.ProjectID = id
			if _, err = s.bind(ctx, tx, b); err != nil {
				return Project{}, err
			}
		}
		if len(p.Bindings) > 0 {
			if err := resolveAll(ctx, tx); err != nil {
				return Project{}, err
			}
		}
		return tx.Projects().Get(ctx, id)
	})
}

func (s *Store) UpdateProject(ctx context.Context, id int64, title string, archived bool) (Project, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Project{}, Invalid("title is required")
	}
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return Project{}, err
		}
		canonical, err := projectID(ctx, tx, id)
		if err != nil {
			return Project{}, err
		}
		if err := tx.Projects().Update(ctx, canonical, title, archived); err != nil {
			return Project{}, err
		}
		return tx.Projects().Get(ctx, canonical)
	})
}

// RefreshWorkspaces records a complete inventory and organizes new rows.
// It never imports legacy bindings. Failures never mark rows missing.
func (s *Store) RefreshWorkspaces(ctx context.Context, connectionID int64, ps []execution.WorkspaceInfo) (int, error) {
	return s.refreshWorkspaces(ctx, connectionID, ps, true)
}

func (s *Store) refreshWorkspaces(ctx context.Context, connectionID int64, ps []execution.WorkspaceInfo, organize bool) (int, error) {
	return write(ctx, s, func(tx store.Tx) (int, error) {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return 0, err
		}
		n, err := refreshWorkspaceRows(ctx, tx, connectionID, ps)
		if err != nil {
			return 0, err
		}
		if organize {
			return n, resolveCatalog(ctx, tx, connectionID)
		}
		return n, resolveAll(ctx, tx)
	})
}

func refreshWorkspaceRows(ctx context.Context, tx store.Tx, connectionID int64, ps []execution.WorkspaceInfo) (int, error) {
	machine, err := tx.Connections().MachineOf(ctx, connectionID)
	if err != nil {
		return 0, err
	}
	pr := tx.Projects()
	if err := pr.MarkUnavailable(ctx, connectionID); err != nil {
		return 0, err
	}
	n := 0
	for _, p := range ps {
		if p.ID == "" {
			continue
		}
		row := store.DiscoveredWorkspace{
			ExternalID: p.ID, Title: p.Title, Workspace: p.WorkspaceRoot, Available: p.DeletedAt == nil,
			Importable: p.Importable, Archived: p.ArchivedAt != nil, ActiveAt: p.ActiveAt,
		}
		if e := p.Evidence; e != nil {
			if e.Gone {
				row.Available = false
			} else {
				row.FileHost, row.EvidenceSource = &machine, "machine"
				row.CanonicalPath, row.CheckoutKey, row.InodeHint = e.CanonicalPath, e.CheckoutKey, e.InodeHint
				row.Repo, row.RepoPath = e.Remote, e.Subpath
			}
		} else if remote := cmp.Or(p.Repo, gitmeta.RemoteIdentity(p.OriginRemote)); remote != "" {
			row.Repo, row.RepoPath, row.EvidenceSource = remote, p.RepoPath, "harness"
		}
		if row.Available {
			n++
		}
		if err := pr.UpsertDiscovered(ctx, connectionID, row); err != nil {
			return 0, err
		}
	}
	if err := pr.RefreshBindingStatus(ctx, connectionID); err != nil {
		return 0, err
	}
	return n, tx.Connections().RecordWorkspaceRefresh(ctx, connectionID, nil)
}

// BoundProjectTitle is the title of the Stage project bound to a
// connection's harness project: the project of its only active binding
// (false when none or several).
func (s *Store) BoundProjectTitle(ctx context.Context, connectionID int64, external string) (string, bool, error) {
	var title string
	var found bool
	err := s.e.Read(ctx, func(tx store.Tx) error {
		id, err := tx.Projects().SoleBinding(ctx, connectionID, external)
		if err != nil || id == nil {
			return err
		}
		p, err := tx.Projects().Get(ctx, *id)
		if err != nil {
			return err
		}
		title, found = p.Title, true
		return nil
	})
	return title, found, err
}
