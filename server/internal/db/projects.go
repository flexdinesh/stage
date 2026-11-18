package db

import (
	"context"
	"github.com/flexdinesh/stage/server/internal/t3"
	"strings"
)

const projectCols = `id,title,archived_at,created_at,updated_at,
 (SELECT max(a) FROM (
 SELECT max(max(updated_at,COALESCE(settled_at,updated_at))) a FROM threads WHERE stage_project_id=projects.id
 UNION ALL SELECT max(updated_at) a FROM tasks WHERE project_id=projects.id
 UNION ALL SELECT max(created_at) FROM journal_entries WHERE task_id IN (SELECT id FROM tasks WHERE project_id=projects.id)
 OR tree_id IN (SELECT tree_id FROM tasks WHERE project_id=projects.id))),
 COALESCE((SELECT json_group_array(json_object('id',b.id,'projectId',b.project_id,'connectionId',b.connection_id,'harness',c.harness,
 'externalProjectId',b.external_project_id,'workspace',b.workspace,'revision',b.revision,'status',b.status,'observedWorkspace',b.observed_workspace))
 FROM project_bindings b JOIN harness_connections c ON c.id=b.connection_id WHERE b.project_id=projects.id AND b.active=1),'[]')`

func scanProject(row rowScanner) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Title, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt, &p.LastActivityAt, &p.Bindings)
	return p, notFound(err)
}
func (s *Store) ListProjects(ctx context.Context, archived string) ([]Project, error) {
	q := `SELECT ` + projectCols + ` FROM projects`
	switch archived {
	case "true":
		q += ` WHERE archived_at IS NOT NULL`
	case "all":
	default:
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY lower(title),id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanProject)
}
func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id=$1`, id))
}

// The legacy columns preserve upgrade evidence only. They are not identities,
// launch destinations or metadata sources after migration 8.
func insertProject(ctx context.Context, tx *Tx, p Project) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO projects(t3_project_id,title,workspace_root,source) VALUES($1,$2,'','app') RETURNING id`, "stage:"+newUUID(), p.Title).Scan(&id)
	return id, err
}
func (s *Store) InsertProject(ctx context.Context, p Project) (Project, error) {
	var id int64
	err := s.inTx(ctx, func(tx *Tx) error {
		var err error
		id, err = insertProject(ctx, tx, p)
		if err != nil {
			return err
		}
		for _, b := range p.Bindings {
			b.ProjectID = id
			if _, err = bindProject(ctx, tx, b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, id)
}
func (s *Store) UpdateProject(ctx context.Context, id int64, title string, archived bool) (Project, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Project{}, Invalid("title is required")
	}
	_, err := s.pool.Exec(ctx, `UPDATE projects SET title=$2,archived_at=CASE WHEN $3 THEN COALESCE(archived_at,stage_now()) ELSE NULL END,updated_at=stage_now() WHERE id=$1`, id, title, archived)
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, id)
}

// UpsertT3Projects refreshes discovery, never imports or edits Stage metadata.
// The caller supplies a complete successful list; failures never mark missing.
func (s *Store) UpsertT3Projects(ctx context.Context, ps []t3.Project) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE external_projects SET available=0 WHERE connection_id=1`); err != nil {
			return err
		}
		for _, p := range ps {
			if p.ID == "" {
				continue
			}
			live := p.DeletedAt == nil
			if live {
				n++
			}
			if _, err := tx.Exec(ctx, `INSERT INTO external_projects(connection_id,external_id,title,workspace,available) VALUES(1,$1,$2,$3,$4)
 ON CONFLICT(connection_id,external_id,workspace) DO UPDATE SET title=excluded.title,available=excluded.available`, p.ID, p.Title, p.WorkspaceRoot, live); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE project_bindings SET
 status=CASE WHEN EXISTS(SELECT 1 FROM external_projects e WHERE e.connection_id=1 AND e.external_id=project_bindings.external_project_id AND e.workspace=project_bindings.workspace AND e.available=1) THEN 'available'
 WHEN EXISTS(SELECT 1 FROM external_projects e WHERE e.connection_id=1 AND e.external_id=project_bindings.external_project_id AND e.available=1) THEN 'changed' ELSE 'missing' END,
 observed_workspace=(SELECT workspace FROM external_projects e WHERE e.connection_id=1 AND e.external_id=project_bindings.external_project_id AND e.available=1 LIMIT 1)
 WHERE connection_id=1 AND active=1`)
		return err
	})
	return n, err
}
