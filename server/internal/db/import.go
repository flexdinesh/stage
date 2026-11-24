package db

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// ImportNoticeWindow is how long the Projects page reports an import.
const ImportNoticeWindow = 7 * 24 * time.Hour

// ImportResult counts one connection's import pass.
type ImportResult struct{ Created, Bound, Archived, Unarchived int }

// ImportWorkspaces turns a connection's discovered deliberate projects into
// Stage projects (docs/architecture.md "Project import"). It runs after a
// successful RefreshWorkspaces, never on a failed inventory.
//
// A candidate is available, importable, not archived by the harness, not
// declined, and its workspace is unbound on this connection. When several
// external IDs share a workspace, the most recently active one wins. Within
// one machine a workspace path names one Stage project across harnesses:
// exactly one project with an active binding at that path on this machine
// gets this connection's binding (unless it already has one here); more is
// ambiguous and left for manual binding. With no owner at the path, the
// origin remote plus subproject path is the match key across machines:
// exactly one project bound to it on another machine, and not yet bound on
// this machine, gets the binding; several, or one already bound here, are
// left for manual binding; none (or no known remote) creates a project.
func (s *Store) ImportWorkspaces(ctx context.Context, connectionID int64) (ImportResult, error) {
	var r ImportResult
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		r = ImportResult{}
		// One project per workspace path per machine: imports on a
		// connection serialize on its row.
		if err := tx.Connections().Lock(ctx, connectionID); err != nil {
			return err
		}
		active, err := tx.Connections().IsActive(ctx, connectionID)
		if err != nil {
			return err
		}
		machine, err := tx.Connections().MachineOf(ctx, connectionID)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		all, err := tx.Projects().ImportCandidates(ctx, connectionID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, c := range all {
			if seen[c.Workspace] {
				continue
			}
			seen[c.Workspace] = true
			bound, err := s.importOne(ctx, tx, connectionID, machine, c)
			if err != nil {
				return err
			}
			switch bound {
			case BindingImported:
				r.Created++
			case BindingPath, BindingRemote:
				r.Bound++
			}
		}
		r.Archived, r.Unarchived, err = tx.Projects().FollowArchive(ctx)
		return err
	})
	return r, err
}

// importOne binds or creates; it returns the binding origin it used, or ""
// when the candidate was left for manual binding.
func (s *Store) importOne(ctx context.Context, tx store.Tx, connectionID, machine int64, c store.ImportCandidate) (string, error) {
	// Bindings on replaced connections still name the project at a path.
	// Paths name a project only within one machine.
	owners, err := tx.Projects().PathOwners(ctx, c.Workspace, machine)
	if err != nil {
		return "", err
	}
	b := ProjectBinding{ConnectionID: connectionID, ExternalProjectID: c.ExternalID, Workspace: c.Workspace}
	switch len(owners) {
	case 0:
		project, ambiguous, err := remoteOwner(ctx, tx, machine, c)
		if err != nil || ambiguous {
			return "", err
		}
		if project != 0 {
			b.ProjectID, b.Origin = project, BindingRemote
			break
		}
		id, err := tx.Projects().InsertDiscovered(ctx, c.Title, connectionID, ActingUser(ctx))
		if err != nil {
			return "", err
		}
		b.ProjectID, b.Origin = id, BindingImported
	case 1:
		here, err := tx.Projects().BoundOn(ctx, owners[0], connectionID)
		if err != nil {
			return "", err
		}
		if here {
			return "", nil
		}
		b.ProjectID, b.Origin = owners[0], BindingPath
	default:
		return "", nil
	}
	if _, err := s.bind(ctx, tx, b); err != nil {
		return "", err
	}
	return b.Origin, nil
}

// remoteOwner resolves a candidate no project claims by path on its machine.
// The origin remote plus subproject path is a match key only across
// machines, never proof within one: the one project with an active binding
// on another machine whose workspace has the same key, and no active binding
// on this machine, is the owner. Owner 0 without ambiguity means create a
// project (no known key, or no project has it); ambiguous means several
// owners, or one already bound here, so the workspace is left for manual
// binding.
func remoteOwner(ctx context.Context, tx store.Tx, machine int64, c store.ImportCandidate) (owner int64, ambiguous bool, err error) {
	if c.Repo == "" || c.RepoPath == "" {
		return 0, false, nil
	}
	owners, err := tx.Projects().RemoteOwners(ctx, machine, c.Repo, c.RepoPath)
	if err != nil {
		return 0, false, err
	}
	switch len(owners) {
	case 0:
		return 0, false, nil
	case 1:
	default:
		return 0, true, nil
	}
	// A project already placed on this machine lives at another path here:
	// joining it would merge two local checkouts by remote alone.
	placed, err := tx.Projects().PlacedOn(ctx, owners[0], machine)
	if err != nil {
		return 0, false, err
	}
	if placed {
		return 0, true, nil
	}
	return owners[0], false, nil
}

// ImportSummaries reports, per connection, the projects its discovery
// created within ImportNoticeWindow, newest first.
func (s *Store) ImportSummaries(ctx context.Context) ([]ImportSummary, error) {
	since := time.Now().Add(-ImportNoticeWindow)
	return read(ctx, s, func(tx store.Tx) ([]ImportSummary, error) { return tx.Projects().ImportSummaries(ctx, since) })
}
