package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/t3"
)

// projectCols includes last_activity_at: latest thread update/settle or task
// update in the project (NULL when none). Selects/returns from "projects".
const projectCols = `id, t3_project_id, title, workspace_root, source, archived_at, created_at, updated_at,
	(SELECT max(a) FROM (
	   SELECT max(max(th.updated_at, COALESCE(th.settled_at, th.updated_at))) AS a FROM threads th WHERE th.t3_project_id = projects.t3_project_id
	   UNION ALL
	   SELECT max(tk.updated_at) AS a FROM tasks tk WHERE tk.project_id = projects.id
       UNION ALL
       SELECT max(j.created_at) FROM journal_entries j WHERE j.task_id IN (SELECT id FROM tasks WHERE project_id = projects.id)
         OR j.tree_id IN (SELECT tree_id FROM tasks WHERE project_id = projects.id)
	 )) AS last_activity_at`

func scanProject(row rowScanner) (Project, error) {
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
// Projects deleted in T3 do not claim their root.
func (s *Store) ProjectByWorkspaceRoot(ctx context.Context, root string) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects
		 WHERE workspace_root = $1 AND t3_deleted_at IS NULL ORDER BY id LIMIT 1`, root))
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

// UpsertT3Projects mirrors T3 projects (source t3 when new). T3 is the source
// of truth: title, workspace root and archive state follow T3 on every sync.
// A deleted T3 project is not inserted; an already mirrored one is marked
// deleted and archived (rows stay, tasks reference them). Returns the count
// of live T3 projects.
func (s *Store) UpsertT3Projects(ctx context.Context, projects []t3.Project) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx *Tx) error {
		for _, p := range projects {
			if p.ID == "" {
				continue
			}
			title := p.Title
			if title == "" {
				title = p.WorkspaceRoot
			}
			if p.DeletedAt != nil {
				if _, err := tx.Exec(ctx,
					`UPDATE projects SET
					   title = $2,
					   workspace_root = $3,
					   archived_at = COALESCE(archived_at, $4),
					   t3_deleted_at = $4,
					   updated_at = CASE WHEN title IS NOT $2 OR workspace_root IS NOT $3
					                          OR archived_at IS NOT COALESCE(archived_at, $4) OR t3_deleted_at IS NOT $4
					                     THEN `+nowSQL+` ELSE updated_at END
					 WHERE t3_project_id = $1`,
					p.ID, title, p.WorkspaceRoot, p.DeletedAt); err != nil {
					return err
				}
				continue
			}
			n++
			if _, err := tx.Exec(ctx,
				`INSERT INTO projects (t3_project_id, title, workspace_root, source, archived_at)
				 VALUES ($1, $2, $3, 't3', $4)
				 ON CONFLICT (t3_project_id) DO UPDATE SET
				   title = EXCLUDED.title,
				   workspace_root = EXCLUDED.workspace_root,
				   archived_at = EXCLUDED.archived_at,
				   t3_deleted_at = NULL,
				   updated_at = CASE WHEN projects.title IS NOT EXCLUDED.title OR projects.workspace_root IS NOT EXCLUDED.workspace_root
				                          OR projects.archived_at IS NOT EXCLUDED.archived_at OR projects.t3_deleted_at IS NOT NULL
				                     THEN `+nowSQL+` ELSE projects.updated_at END`,
				p.ID, title, p.WorkspaceRoot, p.ArchivedAt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
