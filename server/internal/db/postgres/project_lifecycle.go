//go:build postgres

package postgres

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Lock both project keys against new references before locking the source's
// existing content. Task and schedule writers lock their own rows, so the
// preview sees a launch or fire that won the race before the merge.
func (r projects) LockMergeRows(ctx context.Context, into, from int64) error {
	rows, err := r.Query(ctx, `SELECT id FROM projects WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, from, into)
	if err != nil {
		return err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, table := range []string{"tasks", "schedules", "snippets"} {
		rows, err := r.Query(ctx, `SELECT id FROM `+table+` WHERE project_id=$1 ORDER BY id FOR NO KEY UPDATE`, from)
		if err != nil {
			return err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (r projects) CanonicalID(ctx context.Context, id int64) (int64, error) {
	var canonical int64
	err := r.QueryRow(ctx, `SELECT id FROM projects WHERE id=COALESCE(
 (SELECT project_id FROM project_aliases WHERE alias_id=$1),$1)`, id).Scan(&canonical)
	return canonical, notFound(err)
}

func (r projects) MergeRows(ctx context.Context, into, from int64) (store.ProjectMergeRows, error) {
	var result store.ProjectMergeRows
	rows, err := r.Query(ctx, `SELECT 'task' AS kind,id,updated_at,binding_id,state,launch_attempt FROM tasks WHERE project_id=$1
 UNION ALL SELECT 'schedule',id,updated_at,binding_id,'',0 FROM schedules WHERE project_id=$1
 UNION ALL SELECT 'snippet',id,updated_at,NULL,'',0 FROM snippets WHERE project_id=$1
 ORDER BY kind,id`, from)
	if err != nil {
		return result, err
	}
	result.Content, err = collectRows(rows, func(row rowScanner) (store.ProjectMergeContent, error) {
		var c store.ProjectMergeContent
		err := row.Scan(&c.Kind, &c.ID, &c.UpdatedAt, &c.BindingID, &c.State, &c.LaunchAttempt)
		return c, err
	})
	if err != nil {
		return result, err
	}

	rows, err = r.Query(ctx, `SELECT id,project_id,connection_id,revision,active,is_primary,
 external_project_id,workspace,rule,status,observed_workspace
 FROM project_bindings WHERE project_id IN ($1,$2) ORDER BY id`, from, into)
	if err != nil {
		return result, err
	}
	result.Workspaces, err = collectRows(rows, func(row rowScanner) (store.ProjectMergeWorkspace, error) {
		var w store.ProjectMergeWorkspace
		err := row.Scan(&w.ID, &w.ProjectID, &w.ConnectionID, &w.Revision, &w.Active, &w.Primary,
			&w.ExternalID, &w.Workspace, &w.Rule, &w.Status, &w.ObservedWorkspace)
		return w, err
	})
	if err != nil {
		return result, err
	}

	rows, err = r.Query(ctx, `SELECT 'alias' AS kind,alias_id AS id,project_id FROM project_aliases WHERE project_id=$1
 UNION ALL SELECT 'event',id,project_id FROM project_events WHERE project_id=$1
 UNION ALL SELECT 'dismissal',other_id,project_id FROM project_merge_dismissals
 WHERE project_id IN ($1,$2) OR other_id IN ($1,$2)
 ORDER BY kind,id,project_id`, from, into)
	if err != nil {
		return result, err
	}
	result.History, err = collectRows(rows, func(row rowScanner) (store.ProjectMergeHistory, error) {
		var h store.ProjectMergeHistory
		err := row.Scan(&h.Kind, &h.ID, &h.ProjectID)
		return h, err
	})
	return result, err
}

func (r projects) ApplyMerge(ctx context.Context, into, from, by int64) error {
	for _, q := range []string{
		`UPDATE project_bindings AS source SET is_primary=false
 WHERE source.project_id=$1 AND source.active AND source.is_primary
 AND EXISTS (SELECT 1 FROM project_bindings target WHERE target.project_id=$2
 AND target.connection_id=source.connection_id AND target.active AND target.is_primary)`,
		`UPDATE project_bindings SET project_id=$2 WHERE project_id=$1`,
		`UPDATE tasks SET project_id=$2 WHERE project_id=$1`,
		`UPDATE schedules SET project_id=$2 WHERE project_id=$1`,
		`UPDATE snippets SET project_id=$2 WHERE project_id=$1`,
		`UPDATE project_events SET project_id=$2 WHERE project_id=$1`,
		`UPDATE project_aliases SET project_id=$2 WHERE project_id=$1`,
		`INSERT INTO project_merge_dismissals(project_id,other_id)
 SELECT CASE WHEN $2<partner THEN $2 ELSE partner END, CASE WHEN $2<partner THEN partner ELSE $2 END
 FROM (SELECT CASE WHEN project_id=$1 THEN other_id ELSE project_id END AS partner
 FROM project_merge_dismissals WHERE project_id=$1 OR other_id=$1) AS source
 WHERE partner<>$2 ON CONFLICT DO NOTHING`,
	} {
		if _, err := r.Exec(ctx, q, from, into); err != nil {
			return err
		}
	}
	if _, err := r.Exec(ctx, `INSERT INTO project_aliases(alias_id,project_id,title,merged_at,merged_by_user_id)
 SELECT id,$2,title,`+nowSQL+`,$3 FROM projects WHERE id=$1`, from, into, by); err != nil {
		return err
	}
	if _, err := r.Exec(ctx, `DELETE FROM project_merge_dismissals WHERE project_id=$1 OR other_id=$1`, from); err != nil {
		return err
	}
	if _, err := r.Exec(ctx, `DELETE FROM projects WHERE id=$1`, from); err != nil {
		return err
	}
	return r.touchProject(ctx, into)
}

func (r projects) SplitWorkspace(ctx context.Context, project, binding int64) (store.ProjectSplitWorkspace, error) {
	var w store.ProjectSplitWorkspace
	err := r.QueryRow(ctx, `SELECT b.connection_id,b.workspace,COALESCE(e.title,''),b.external_project_id,b.is_primary,
 (SELECT count(*) FROM project_bindings WHERE project_id=$1 AND active)
 FROM project_bindings b LEFT JOIN external_projects e ON e.connection_id=b.connection_id
 AND e.external_id=b.external_project_id AND e.workspace=b.workspace
 WHERE b.project_id=$1 AND b.id=$2 AND b.active`, project, binding).
		Scan(&w.ConnectionID, &w.Workspace, &w.Title, &w.ExternalID, &w.Primary, &w.WorkspaceCount)
	return w, notFound(err)
}

func (r projects) ApplySplit(ctx context.Context, from, into, binding int64) error {
	if _, err := r.Exec(ctx, `UPDATE project_bindings SET project_id=$2,is_primary=true,rule='manual' WHERE id=$1`, binding, into); err != nil {
		return err
	}
	return r.touchProject(ctx, from)
}

func (r projects) touchProject(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE projects SET updated_at=`+nowSQL+` WHERE id=$1`, id)
	return err
}

func (r projects) DismissMerge(ctx context.Context, project, other int64) error {
	_, err := r.Exec(ctx, `INSERT INTO project_merge_dismissals(project_id,other_id)
 VALUES($1,$2) ON CONFLICT DO NOTHING`, min(project, other), max(project, other))
	return err
}

func (r projects) MergeDismissals(ctx context.Context) ([][2]int64, error) {
	rows, err := r.Query(ctx, `SELECT project_id,other_id FROM project_merge_dismissals ORDER BY project_id,other_id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) ([2]int64, error) {
		var pair [2]int64
		err := row.Scan(&pair[0], &pair[1])
		return pair, err
	})
}

func (r projects) AddProjectEvent(ctx context.Context, project int64, kind, text string, by int64) error {
	_, err := r.Exec(ctx, `INSERT INTO project_events(project_id,type,text,created_at,created_by_user_id)
 VALUES($1,$2,$3,`+nowSQL+`,$4)`, project, kind, text, by)
	return err
}

func (r projects) ProjectEvents(ctx context.Context, project int64) ([]store.ProjectEvent, error) {
	rows, err := r.Query(ctx, `SELECT id,project_id,type,text,created_at,created_by_user_id
 FROM project_events WHERE project_id=$1 ORDER BY id DESC LIMIT 100`, project)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.ProjectEvent, error) {
		var e store.ProjectEvent
		err := row.Scan(&e.ID, &e.ProjectID, &e.Type, &e.Text, &e.CreatedAt, &e.CreatedBy)
		return e, err
	})
}
