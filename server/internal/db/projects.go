package db

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// projectCols includes last_activity_at: latest thread update/settle or task
// update in the project (NULL when none). Selects/returns from "projects".
const projectCols = `id, t3_project_id, title, workspace_root, source, archived_at, created_at, updated_at,
	(SELECT max(a) FROM (
	   SELECT max(GREATEST(th.updated_at, th.settled_at)) FROM threads th WHERE th.t3_project_id = projects.t3_project_id
	   UNION ALL
	   SELECT max(tk.updated_at) FROM tasks tk WHERE tk.project_id = projects.id
	 ) acts(a)) AS last_activity_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.T3ProjectID, &p.Title, &p.WorkspaceRoot, &p.Source, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt, &p.LastActivityAt)
	return p, notFound(err)
}

// ArchivedFilter: "false" (active only, default), "true" (archived only), "all".
func (s *Store) ListProjects(ctx context.Context, archived string) ([]Project, error) {
	q := `SELECT ` + projectCols + ` FROM projects`
	switch archived {
	case "true":
		q += ` WHERE archived_at IS NOT NULL`
	case "all":
	default:
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY lower(title), id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id = $1`, id))
}

// ProjectByWorkspaceRoot returns ErrNotFound when no project has the root.
func (s *Store) ProjectByWorkspaceRoot(ctx context.Context, root string) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE workspace_root = $1 ORDER BY id LIMIT 1`, root))
}

// InsertProject inserts an app-side project row; 409 on duplicate T3 id.
func (s *Store) InsertProject(ctx context.Context, p Project) (Project, error) {
	out, err := scanProject(s.pool.QueryRow(ctx,
		`INSERT INTO projects (t3_project_id, title, workspace_root, source)
		 VALUES ($1, $2, $3, $4) RETURNING `+projectCols,
		p.T3ProjectID, p.Title, p.WorkspaceRoot, p.Source))
	if isUniqueViolation(err) {
		return Project{}, Conflict("project already exists for T3 project %s", p.T3ProjectID)
	}
	return out, err
}

// LinkProject re-activates an imported project for a create request and
// takes the requested title.
func (s *Store) LinkProject(ctx context.Context, id int64, title string) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`UPDATE projects SET title = $2, archived_at = NULL, updated_at = now()
		 WHERE id = $1 RETURNING `+projectCols, id, title))
}

// UpdateProject applies a local-only patch.
func (s *Store) UpdateProject(ctx context.Context, id int64, title *string, archived *bool) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`UPDATE projects SET
		   title = COALESCE($2, title),
		   archived_at = CASE
		     WHEN $3::boolean IS NULL THEN archived_at
		     WHEN $3::boolean THEN COALESCE(archived_at, now())
		     ELSE NULL END,
		   updated_at = now()
		 WHERE id = $1 RETURNING `+projectCols, id, title, archived))
}

// UpsertT3Projects mirrors T3 projects (source t3 when new). Existing rows
// keep their local title/archive state; workspace root follows T3. Deleted
// T3 projects are skipped.
func (s *Store) UpsertT3Projects(ctx context.Context, projects []t3.Project) (int, error) {
	batch := &pgx.Batch{}
	n := 0
	for _, p := range projects {
		if p.DeletedAt != nil || p.ID == "" {
			continue
		}
		n++
		title := p.Title
		if title == "" {
			title = p.WorkspaceRoot
		}
		batch.Queue(
			`INSERT INTO projects (t3_project_id, title, workspace_root, source, archived_at)
			 VALUES ($1, $2, $3, 't3', $4)
			 ON CONFLICT (t3_project_id) DO UPDATE SET
			   workspace_root = EXCLUDED.workspace_root,
			   updated_at = CASE WHEN projects.workspace_root IS DISTINCT FROM EXCLUDED.workspace_root
			                     THEN now() ELSE projects.updated_at END`,
			p.ID, title, p.WorkspaceRoot, p.ArchivedAt)
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return n, nil
}
