package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type projects struct{ *runner }

func (t *tx) Projects() store.Projects { return projects{t.r} }

const projectCols = `id,title,archived_at,created_at,updated_at,
 (SELECT max(a) FROM (
 SELECT max(max(updated_at,COALESCE(settled_at,updated_at))) a FROM threads WHERE stage_project_id=projects.id
 UNION ALL SELECT max(updated_at) a FROM tasks WHERE project_id=projects.id
 UNION ALL SELECT max(created_at) FROM journal_entries WHERE task_id IN (SELECT id FROM tasks WHERE project_id=projects.id)
 OR tree_id IN (SELECT tree_id FROM tasks WHERE project_id=projects.id))),
 COALESCE((SELECT json_group_array(json_object('id',b.id,'projectId',b.project_id,'connectionId',b.connection_id,'harness',c.harness,
 'externalProjectId',b.external_project_id,'workspace',b.workspace,'revision',b.revision,'status',b.status,'observedWorkspace',b.observed_workspace,'connectionActive',json(CASE WHEN c.active=1 THEN 'true' ELSE 'false' END),'connectionLabel',c.label,'origin',b.origin))
 FROM project_bindings b JOIN harness_connections c ON c.id=b.connection_id WHERE b.project_id=projects.id AND b.active=1),'[]'),
 origin,imported_at,
 COALESCE((SELECT json_group_array(repo) FROM (SELECT DISTINCT e.repo FROM project_bindings b JOIN external_projects e
 ON e.connection_id=b.connection_id AND e.external_id=b.external_project_id AND e.workspace=b.workspace
 WHERE b.project_id=projects.id AND b.active=1 AND e.repo<>'' ORDER BY e.repo)),'[]'),
 created_by_user_id,source_machine_id`

func scanProject(row rowScanner) (store.Project, error) {
	var p store.Project
	err := row.Scan(&p.ID, &p.Title, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt, &p.LastActivityAt, &p.Bindings, &p.Origin, &p.ImportedAt, &p.Repos, &p.CreatedBy, &p.SourceMachineID)
	return p, notFound(err)
}

func (r projects) Exists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := r.QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1`, id).Scan(&one)
	if err != nil {
		if err = notFound(err); err == store.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r projects) List(ctx context.Context, archive store.ProjectArchive) ([]store.Project, error) {
	q := `SELECT ` + projectCols + ` FROM projects`
	switch archive {
	case store.ProjectsArchived:
		q += ` WHERE archived_at IS NOT NULL`
	case store.ProjectsAll:
	default:
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY lower(title),id`
	rows, err := r.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanProject)
}

func (r projects) Bound(ctx context.Context, b store.ProjectBinding) (store.Project, error) {
	return scanProject(r.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id=(
		SELECT project_id FROM project_bindings WHERE connection_id=$1 AND external_project_id=$2
		AND workspace=$3 AND active=1 AND status='available')`, b.ConnectionID, b.ExternalProjectID, b.Workspace))
}

func (r projects) Get(ctx context.Context, id int64) (store.Project, error) {
	return scanProject(r.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id=$1`, id))
}

// The legacy columns preserve upgrade evidence only. They are not identities,
// launch destinations or metadata sources after migration 8.
func (r projects) Insert(ctx context.Context, title string, createdBy int64, sourceMachine *int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO projects(t3_project_id,title,workspace_root,source,created_by_user_id,source_machine_id) VALUES($1,$2,'','app',$3,$4) RETURNING id`, "stage:"+newUUID(), title, createdBy, sourceMachine).Scan(&id)
	return id, err
}

func (r projects) SoleBinding(ctx context.Context, connectionID int64, external string) (*int64, error) {
	var p *int64
	err := r.QueryRow(ctx, `SELECT min(project_id) FROM project_bindings WHERE connection_id = $1 AND external_project_id = $2 AND active = 1 HAVING count(*) = 1`, connectionID, external).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (r projects) InsertDiscovered(ctx context.Context, title string, connectionID, createdBy int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO projects(t3_project_id,title,workspace_root,source,origin,imported_connection_id,imported_at,created_by_user_id)
 VALUES($1,$2,'','app','discovered',$3,stage_now(),$4) RETURNING id`, "stage:"+newUUID(), title, connectionID, createdBy).Scan(&id)
	return id, err
}

func (r projects) Update(ctx context.Context, id int64, title string, archived bool) error {
	_, err := r.Exec(ctx, `UPDATE projects SET title=$2,archived_at=CASE WHEN $3 THEN COALESCE(archived_at,stage_now()) ELSE NULL END,
 archive_owner=CASE WHEN (archived_at IS NOT NULL)<>$3 THEN 'user' ELSE archive_owner END,updated_at=stage_now() WHERE id=$1`, id, title, archived)
	return err
}

func (r projects) DeclineRebound(ctx context.Context, b store.ProjectBinding) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET declined_at=stage_now() WHERE EXISTS(SELECT 1 FROM project_bindings p
 WHERE p.project_id=$1 AND p.connection_id=$2 AND p.active=1 AND p.connection_id=external_projects.connection_id
 AND p.external_project_id=external_projects.external_id AND p.workspace=external_projects.workspace)
 AND NOT (external_id=$3 AND workspace=$4)`, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace)
	return err
}

func (r projects) DeclineBound(ctx context.Context, projectID, connectionID int64) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET declined_at=stage_now() WHERE EXISTS(SELECT 1 FROM project_bindings b
 WHERE b.project_id=$1 AND b.connection_id=$2 AND b.active=1 AND b.connection_id=external_projects.connection_id
 AND b.external_project_id=external_projects.external_id AND b.workspace=external_projects.workspace)`, projectID, connectionID)
	return err
}

func (r projects) DeactivateBindings(ctx context.Context, projectID, connectionID int64) error {
	_, err := r.Exec(ctx, `UPDATE project_bindings SET active=0 WHERE project_id=$1 AND connection_id=$2`, projectID, connectionID)
	return err
}

func (r projects) InsertBinding(ctx context.Context, b store.ProjectBinding) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace,revision,origin)
 VALUES($1,$2,$3,$4,COALESCE((SELECT max(revision)+1 FROM project_bindings WHERE project_id=$1 AND connection_id=$2),1),$5) RETURNING id`, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace, b.Origin).Scan(&id)
	return id, err
}

func (r projects) Undecline(ctx context.Context, connectionID int64, externalID, workspace string) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET declined_at=NULL WHERE connection_id=$1 AND external_id=$2 AND workspace=$3`, connectionID, externalID, workspace)
	return err
}

func (r projects) BoundOn(ctx context.Context, projectID, connectionID int64) (bool, error) {
	var here bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE project_id=$1 AND connection_id=$2 AND active=1)`, projectID, connectionID).Scan(&here)
	return here, err
}

func (r projects) AutoBindEligible(ctx context.Context, source int64, b store.ProjectBinding) (bool, error) {
	var eligible bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE id=$1 AND project_id=$2 AND active=1 AND status='available') AND NOT EXISTS(SELECT 1 FROM project_bindings WHERE project_id=$2 AND connection_id=$3 AND active=1)
 AND EXISTS(SELECT 1 FROM external_projects WHERE connection_id=$3 AND external_id=$4 AND workspace=$5 AND available=1)`, source, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace).Scan(&eligible)
	return eligible, err
}

func (r projects) ExternalProjects(ctx context.Context, connectionID int64) ([]store.ExternalProject, error) {
	rows, err := r.Query(ctx, `SELECT e.connection_id,e.external_id,e.title,e.workspace,b.project_id,e.repo FROM external_projects e
 LEFT JOIN project_bindings b ON b.connection_id=e.connection_id AND b.external_project_id=e.external_id AND b.workspace=e.workspace AND b.active=1
 WHERE e.connection_id=$1 AND e.available=1 ORDER BY lower(e.title),e.workspace`, connectionID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.ExternalProject, error) {
		var p store.ExternalProject
		err := r.Scan(&p.ConnectionID, &p.ExternalID, &p.Title, &p.Workspace, &p.ProjectID, &p.Repo)
		return p, err
	})
}

func (r projects) MarkUnavailable(ctx context.Context, connectionID int64) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET available=0 WHERE connection_id=$1`, connectionID)
	return err
}

func (r projects) UpsertDiscovered(ctx context.Context, connectionID int64, w store.DiscoveredWorkspace) error {
	_, err := r.Exec(ctx, `INSERT INTO external_projects(connection_id,external_id,title,workspace,available,importable,archived,active_at,repo,repo_path) VALUES($5,$1,$2,$3,$4,$6,$7,$8,$9,$10)
 ON CONFLICT(connection_id,external_id,workspace) DO UPDATE SET title=excluded.title,available=excluded.available,
 importable=excluded.importable,archived=excluded.archived,active_at=excluded.active_at,repo=excluded.repo,repo_path=excluded.repo_path`,
		w.ExternalID, w.Title, w.Workspace, w.Available, connectionID, w.Importable, w.Archived, w.ActiveAt, w.Repo, w.RepoPath)
	return err
}

func (r projects) RefreshBindingStatus(ctx context.Context, connectionID int64) error {
	_, err := r.Exec(ctx, `UPDATE project_bindings SET
 status=CASE WHEN EXISTS(SELECT 1 FROM external_projects e WHERE e.connection_id=$1 AND e.external_id=project_bindings.external_project_id AND e.workspace=project_bindings.workspace AND e.available=1) THEN 'available'
 WHEN EXISTS(SELECT 1 FROM external_projects e WHERE e.connection_id=$1 AND e.external_id=project_bindings.external_project_id AND e.available=1) THEN 'changed' ELSE 'missing' END,
 observed_workspace=(SELECT workspace FROM external_projects e WHERE e.connection_id=$1 AND e.external_id=project_bindings.external_project_id AND e.available=1 LIMIT 1)
 WHERE connection_id=$1 AND active=1`, connectionID)
	return err
}

func (r projects) ImportCandidates(ctx context.Context, connectionID int64) ([]store.ImportCandidate, error) {
	rows, err := r.Query(ctx, `SELECT e.external_id,e.title,e.workspace,e.repo,e.repo_path FROM external_projects e
 WHERE e.connection_id=$1 AND e.available=1 AND e.importable=1 AND e.archived=0 AND e.declined_at IS NULL AND e.workspace<>''
 AND NOT EXISTS(SELECT 1 FROM project_bindings b WHERE b.connection_id=e.connection_id AND b.workspace=e.workspace AND b.active=1)
 ORDER BY e.workspace,e.active_at IS NULL,e.active_at DESC,e.external_id`, connectionID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.ImportCandidate, error) {
		var c store.ImportCandidate
		err := row.Scan(&c.ExternalID, &c.Title, &c.Workspace, &c.Repo, &c.RepoPath)
		return c, err
	})
}

func (r projects) ids(ctx context.Context, query string, args ...any) ([]int64, error) {
	rows, err := r.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r projects) PathOwners(ctx context.Context, workspace string, machine int64) ([]int64, error) {
	return r.ids(ctx, `SELECT DISTINCT b.project_id FROM project_bindings b JOIN harness_connections c ON c.id=b.connection_id
 WHERE b.workspace=$1 AND b.active=1 AND c.machine_id=$2 ORDER BY b.project_id`, workspace, machine)
}

func (r projects) RemoteOwners(ctx context.Context, machine int64, repo, repoPath string) ([]int64, error) {
	return r.ids(ctx, `SELECT DISTINCT b.project_id FROM project_bindings b
 JOIN harness_connections c ON c.id=b.connection_id
 JOIN external_projects e ON e.connection_id=b.connection_id AND e.external_id=b.external_project_id AND e.workspace=b.workspace
 WHERE b.active=1 AND c.machine_id<>$1 AND e.repo=$2 AND e.repo_path=$3 ORDER BY b.project_id`, machine, repo, repoPath)
}

func (r projects) PlacedOn(ctx context.Context, projectID, machine int64) (bool, error) {
	var placed bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings b JOIN harness_connections c ON c.id=b.connection_id
 WHERE b.project_id=$1 AND b.active=1 AND c.machine_id=$2)`, projectID, machine).Scan(&placed)
	return placed, err
}

func (r projects) FollowArchive(ctx context.Context) (archived, unarchived int, err error) {
	const dead = `EXISTS(SELECT 1 FROM project_bindings b WHERE b.project_id=projects.id AND b.active=1)
 AND NOT EXISTS(SELECT 1 FROM project_bindings b JOIN external_projects e ON e.connection_id=b.connection_id
 AND e.external_id=b.external_project_id AND e.workspace=b.workspace
 WHERE b.project_id=projects.id AND b.active=1 AND e.available=1 AND e.archived=0)`
	tag, err := r.Exec(ctx, `UPDATE projects SET archived_at=stage_now(),archive_owner='harness',updated_at=stage_now()
 WHERE origin='discovered' AND archived_at IS NULL AND archive_owner IS NULL
 AND NOT EXISTS(SELECT 1 FROM tasks WHERE project_id=projects.id) AND `+dead)
	if err != nil {
		return 0, 0, err
	}
	archived = int(tag.RowsAffected())
	tag, err = r.Exec(ctx, `UPDATE projects SET archived_at=NULL,archive_owner=NULL,updated_at=stage_now()
 WHERE origin='discovered' AND archived_at IS NOT NULL AND archive_owner='harness' AND NOT (`+dead+`)`)
	if err != nil {
		return 0, 0, err
	}
	return archived, int(tag.RowsAffected()), nil
}

func (r projects) ImportSummaries(ctx context.Context, since time.Time) ([]store.ImportSummary, error) {
	rows, err := r.Query(ctx, `SELECT p.imported_connection_id,c.harness,count(*),max(p.imported_at) FROM projects p
 JOIN harness_connections c ON c.id=p.imported_connection_id
 WHERE p.imported_at>=$1 GROUP BY p.imported_connection_id,c.harness ORDER BY max(p.imported_at) DESC`, since)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.ImportSummary, error) {
		var m store.ImportSummary
		err := row.Scan(&m.ConnectionID, &m.Harness, &m.Count, &m.At)
		return m, err
	})
}
