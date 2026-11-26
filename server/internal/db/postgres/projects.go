//go:build postgres

package postgres

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
 SELECT max(updated_at) a FROM tasks WHERE project_id=projects.id
 UNION ALL SELECT max(created_at) FROM journal_entries WHERE task_id IN (SELECT id FROM tasks WHERE project_id=projects.id)
 OR tree_id IN (SELECT tree_id FROM tasks WHERE project_id=projects.id)) activity),
 COALESCE((SELECT jsonb_agg(` + projectBindingJSON + ` ORDER BY b.id)
 ` + bindingWorkspaceJoin + ` WHERE b.project_id=projects.id AND b.active),'[]'::jsonb),
 origin,imported_at,
 COALESCE((SELECT jsonb_agg(DISTINCT e.repo ORDER BY e.repo) FROM project_bindings b JOIN external_projects e
 ON e.connection_id=b.connection_id AND e.external_id=b.external_project_id AND e.workspace=b.workspace
 WHERE b.project_id=projects.id AND b.active AND e.repo<>''),'[]'::jsonb),
 created_by_user_id,source_machine_id,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.alias_id,'title',a.title,
 'mergedAt',to_char(a.merged_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')) ORDER BY a.alias_id)
 FROM project_aliases a WHERE a.project_id=projects.id),'[]'::jsonb)`

func scanProject(row rowScanner) (store.Project, error) {
	var p store.Project
	err := row.Scan(&p.ID, &p.Title, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt, &p.LastActivityAt, &p.Bindings, &p.Origin, &p.ImportedAt, &p.Repos, &p.CreatedBy, &p.SourceMachineID, &p.Aliases)
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
		AND workspace=$3 AND active AND status='available')`, b.ConnectionID, b.ExternalProjectID, b.Workspace))
}

func (r projects) Get(ctx context.Context, id int64) (store.Project, error) {
	return scanProject(r.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id=$1`, id))
}

func (r projects) Insert(ctx context.Context, title string, createdBy int64, sourceMachine *int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO projects(title,created_by_user_id,source_machine_id) VALUES($1,$2,$3) RETURNING id`, title, createdBy, sourceMachine).Scan(&id)
	return id, err
}

func (r projects) SoleBinding(ctx context.Context, connectionID int64, external string) (*int64, error) {
	var p *int64
	err := r.QueryRow(ctx, `SELECT min(project_id) FROM project_bindings WHERE connection_id = $1 AND external_project_id = $2 AND active HAVING count(*) = 1`, connectionID, external).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (r projects) InsertDiscovered(ctx context.Context, title string, connectionID, createdBy int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO projects(title,origin,imported_connection_id,imported_at,created_by_user_id)
 VALUES($1,'discovered',$2,`+nowSQL+`,$3) RETURNING id`, title, connectionID, createdBy).Scan(&id)
	return id, err
}

func (r projects) Update(ctx context.Context, id int64, title string, archived bool) error {
	_, err := r.Exec(ctx, `UPDATE projects SET title=$2,archived_at=CASE WHEN $3 THEN COALESCE(archived_at,`+nowSQL+`) ELSE NULL END,
 updated_at=`+nowSQL+` WHERE id=$1`, id, title, archived)
	return err
}

func (r projects) InsertBinding(ctx context.Context, b store.ProjectBinding) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace,revision,rule,is_primary)
 VALUES($1,$2,$3,$4,COALESCE((SELECT max(revision)+1 FROM project_bindings WHERE project_id=$1 AND connection_id=$2),1),$5,$6) RETURNING id`, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace, b.Rule, b.Primary).Scan(&id)
	return id, err
}

func (r projects) BoundOn(ctx context.Context, projectID, connectionID int64) (bool, error) {
	var here bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE project_id=$1 AND connection_id=$2 AND active)`, projectID, connectionID).Scan(&here)
	return here, err
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
