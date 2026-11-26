package db

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/projectmatch"
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
// external IDs share a workspace, the most recently active one wins. The
// project identity ladder (projectmatch.Resolve) places each candidate
// against every active binding: the same path on this machine binds to
// its one owner (origin path), the same origin remote and subproject on
// another machine joins its one owner (origin remote), ambiguous evidence
// leaves the workspace for manual binding, and no evidence creates a
// project (origin imported).
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
		claims, err := tx.Projects().Claims(ctx)
		if err != nil {
			return err
		}
		// Bindings on replaced connections still name the project at a path.
		bindings := make([]projectmatch.Binding, 0, len(claims))
		for _, c := range claims {
			bindings = append(bindings, projectmatch.Binding{ID: c.BindingID, Project: c.ProjectID, Evidence: projectmatch.Evidence{Connection: c.ConnectionID, Machine: c.MachineID, Path: c.Workspace, Remote: c.Repo, RepoPath: c.RepoPath}})
		}
		seen := map[string]bool{}
		for _, c := range all {
			if seen[c.Workspace] {
				continue
			}
			seen[c.Workspace] = true
			evidence := projectmatch.Evidence{Connection: connectionID, Machine: machine, Path: c.Workspace, Remote: c.Repo, RepoPath: c.RepoPath}
			v := projectmatch.Resolve(projectmatch.Candidate{Evidence: evidence}, bindings)
			if v.Leave {
				continue
			}
			b := ProjectBinding{ConnectionID: connectionID, ExternalProjectID: c.ExternalID, Workspace: c.Workspace, ProjectID: v.Project, Origin: BindingPath}
			switch {
			case v.New():
				id, err := tx.Projects().InsertDiscovered(ctx, c.Title, connectionID, ActingUser(ctx))
				if err != nil {
					return err
				}
				b.ProjectID, b.Origin = id, BindingImported
				r.Created++
			case v.Rung == projectmatch.RungRemote:
				b.Origin = BindingRemote
				r.Bound++
			default:
				r.Bound++
			}
			id, err := s.bind(ctx, tx, b)
			if err != nil {
				return err
			}
			bindings = append(bindings, projectmatch.Binding{ID: id, Project: b.ProjectID, Evidence: evidence})
		}
		r.Archived, r.Unarchived, err = tx.Projects().FollowArchive(ctx)
		return err
	})
	return r, err
}

// ImportSummaries reports, per connection, the projects its discovery
// created within ImportNoticeWindow, newest first.
func (s *Store) ImportSummaries(ctx context.Context) ([]ImportSummary, error) {
	since := time.Now().Add(-ImportNoticeWindow)
	return read(ctx, s, func(tx store.Tx) ([]ImportSummary, error) { return tx.Projects().ImportSummaries(ctx, since) })
}
