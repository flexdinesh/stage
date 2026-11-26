package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *Store) ListProjects(ctx context.Context, archived string) ([]Project, error) {
	archive := store.ProjectsActive
	switch archived {
	case "true":
		archive = store.ProjectsArchived
	case "all":
		archive = store.ProjectsAll
	}
	return read(ctx, s, func(tx store.Tx) ([]Project, error) { return tx.Projects().List(ctx, archive) })
}

// BoundProject resolves the verified workspace in a single connection.
func (s *Store) BoundProject(ctx context.Context, b ProjectBinding) (Project, error) {
	return read(ctx, s, func(tx store.Tx) (Project, error) { return tx.Projects().Bound(ctx, b) })
}

func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	return read(ctx, s, func(tx store.Tx) (Project, error) { return tx.Projects().Get(ctx, id) })
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
		return tx.Projects().Get(ctx, id)
	})
}

func (s *Store) UpdateProject(ctx context.Context, id int64, title string, archived bool) (Project, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Project{}, Invalid("title is required")
	}
	// A user archive or unarchive owns the archive state from then on.
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		if err := tx.Projects().Update(ctx, id, title, archived); err != nil {
			return Project{}, err
		}
		return tx.Projects().Get(ctx, id)
	})
}

// RefreshWorkspaces refreshes a connection's discovery, never imports or
// edits Stage metadata. The caller supplies a complete successful list;
// failures never mark missing.
func (s *Store) RefreshWorkspaces(ctx context.Context, connectionID int64, ps []execution.WorkspaceInfo) (int, error) {
	return write(ctx, s, func(tx store.Tx) (int, error) {
		n := 0
		pr := tx.Projects()
		if err := pr.MarkUnavailable(ctx, connectionID); err != nil {
			return 0, err
		}
		for _, p := range ps {
			if p.ID == "" {
				continue
			}
			live := p.DeletedAt == nil
			if live {
				n++
			}
			repoPath := p.RepoPath
			if p.Repo == "" {
				repoPath = ""
			}
			if err := pr.UpsertDiscovered(ctx, connectionID, store.DiscoveredWorkspace{
				ExternalID: p.ID, Title: p.Title, Workspace: p.WorkspaceRoot, Available: live,
				Importable: p.Importable, Archived: p.ArchivedAt != nil, ActiveAt: p.ActiveAt,
				Repo: p.Repo, RepoPath: repoPath,
			}); err != nil {
				return 0, err
			}
		}
		return n, pr.RefreshBindingStatus(ctx, connectionID)
	})
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
