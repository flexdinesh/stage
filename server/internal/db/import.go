package db

import (
	"context"
	"time"
)

// ImportNoticeWindow is how long the Projects page reports an import.
const ImportNoticeWindow = 7 * 24 * time.Hour

// ImportResult counts one connection's import pass.
type ImportResult struct{ Created, Bound, Archived, Unarchived int }

type importCandidate struct{ externalID, title, workspace string }

// ImportWorkspaces turns a connection's discovered deliberate projects into
// Stage projects (docs/architecture.md "Project import"). It runs after a
// successful RefreshWorkspaces, never on a failed inventory.
//
// A candidate is available, importable, not archived by the harness, not
// declined, and its workspace is unbound on this connection. When several
// external IDs share a workspace, the most recently active one wins. A
// workspace path names one Stage project across harnesses: exactly one
// project with an active binding at that path gets this connection's binding
// (unless it already has one here); none creates a project; more is ambiguous
// and left for manual binding.
func (s *Store) ImportWorkspaces(ctx context.Context, connectionID int64) (ImportResult, error) {
	var r ImportResult
	err := s.inTx(ctx, func(tx *Tx) error {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT active FROM harness_connections WHERE id=$1`, connectionID).Scan(&active); err != nil {
			return notFound(err)
		}
		if !active {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT e.external_id,e.title,e.workspace FROM external_projects e
 WHERE e.connection_id=$1 AND e.available=1 AND e.importable=1 AND e.archived=0 AND e.declined_at IS NULL AND e.workspace<>''
 AND NOT EXISTS(SELECT 1 FROM project_bindings b WHERE b.connection_id=e.connection_id AND b.workspace=e.workspace AND b.active=1)
 ORDER BY e.workspace,e.active_at IS NULL,e.active_at DESC,e.external_id`, connectionID)
		if err != nil {
			return err
		}
		all, err := collectRows(rows, func(row rowScanner) (importCandidate, error) {
			var c importCandidate
			err := row.Scan(&c.externalID, &c.title, &c.workspace)
			return c, err
		})
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, c := range all {
			if seen[c.workspace] {
				continue
			}
			seen[c.workspace] = true
			bound, err := s.importOne(ctx, tx, connectionID, c)
			if err != nil {
				return err
			}
			switch bound {
			case BindingImported:
				r.Created++
			case BindingPath:
				r.Bound++
			}
		}
		r.Archived, r.Unarchived, err = followArchive(ctx, tx)
		return err
	})
	return r, err
}

// importOne binds or creates; it returns the binding origin it used, or ""
// when the candidate was left for manual binding.
func (s *Store) importOne(ctx context.Context, tx *Tx, connectionID int64, c importCandidate) (string, error) {
	// Bindings on replaced connections still name the project at a path.
	rows, err := tx.Query(ctx, `SELECT DISTINCT project_id FROM project_bindings WHERE workspace=$1 AND active=1 ORDER BY project_id`, c.workspace)
	if err != nil {
		return "", err
	}
	owners, err := collectRows(rows, func(row rowScanner) (int64, error) {
		var id int64
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		return "", err
	}
	b := ProjectBinding{ConnectionID: connectionID, ExternalProjectID: c.externalID, Workspace: c.workspace}
	switch len(owners) {
	case 0:
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO projects(t3_project_id,title,workspace_root,source,origin,imported_connection_id,imported_at,created_by_user_id)
 VALUES($1,$2,'','app','discovered',$3,stage_now(),$4) RETURNING id`, "stage:"+newUUID(), c.title, connectionID, ActingUser(ctx)).Scan(&id); err != nil {
			return "", err
		}
		b.ProjectID, b.Origin = id, BindingImported
	case 1:
		var here bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE project_id=$1 AND connection_id=$2 AND active=1)`, owners[0], connectionID).Scan(&here); err != nil {
			return "", err
		}
		if here {
			return "", nil
		}
		b.ProjectID, b.Origin = owners[0], BindingPath
	default:
		return "", nil
	}
	if _, err := bindProject(ctx, tx, b); err != nil {
		return "", err
	}
	return b.Origin, nil
}

// followArchive mirrors harness archives onto discovered projects the user
// has not archived or unarchived: one with no tasks whose every active
// binding points at an archived or unavailable workspace is archived; a
// harness-archived one with a live workspace again is unarchived.
func followArchive(ctx context.Context, tx *Tx) (archived, unarchived int, err error) {
	const dead = `EXISTS(SELECT 1 FROM project_bindings b WHERE b.project_id=projects.id AND b.active=1)
 AND NOT EXISTS(SELECT 1 FROM project_bindings b JOIN external_projects e ON e.connection_id=b.connection_id
 AND e.external_id=b.external_project_id AND e.workspace=b.workspace
 WHERE b.project_id=projects.id AND b.active=1 AND e.available=1 AND e.archived=0)`
	tag, err := tx.Exec(ctx, `UPDATE projects SET archived_at=stage_now(),archive_owner='harness',updated_at=stage_now()
 WHERE origin='discovered' AND archived_at IS NULL AND archive_owner IS NULL
 AND NOT EXISTS(SELECT 1 FROM tasks WHERE project_id=projects.id) AND `+dead)
	if err != nil {
		return 0, 0, err
	}
	archived = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `UPDATE projects SET archived_at=NULL,archive_owner=NULL,updated_at=stage_now()
 WHERE origin='discovered' AND archived_at IS NOT NULL AND archive_owner='harness' AND NOT (`+dead+`)`)
	if err != nil {
		return 0, 0, err
	}
	return archived, int(tag.RowsAffected()), nil
}

// ImportSummaries reports, per connection, the projects its discovery
// created within ImportNoticeWindow, newest first.
func (s *Store) ImportSummaries(ctx context.Context) ([]ImportSummary, error) {
	since := time.Now().Add(-ImportNoticeWindow)
	rows, err := s.pool.Query(ctx, `SELECT p.imported_connection_id,c.harness,count(*),max(p.imported_at) FROM projects p
 JOIN harness_connections c ON c.id=p.imported_connection_id
 WHERE p.imported_at>=$1 GROUP BY p.imported_connection_id,c.harness ORDER BY max(p.imported_at) DESC`, since)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (ImportSummary, error) {
		var m ImportSummary
		err := row.Scan(&m.ConnectionID, &m.Harness, &m.Count, &m.At)
		return m, err
	})
}
