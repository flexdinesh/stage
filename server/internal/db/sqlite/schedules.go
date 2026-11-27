package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type schedules struct{ *runner }

func (t *tx) Schedules() store.Schedules { return schedules{t.r} }

var scheduleSelect = `
SELECT s.id, s.project_id, p.title, s.title, s.prompt, s.notes, s.cron, s.tz, s.paused, s.next_run_at,
  (SELECT r.fire_at FROM schedule_runs r WHERE r.schedule_id = s.id ORDER BY r.id DESC LIMIT 1),
  (SELECT max(t.id) FROM tasks t WHERE t.schedule_id = s.id),
  (SELECT t.state FROM tasks t WHERE t.schedule_id = s.id ORDER BY t.id DESC LIMIT 1),
  s.created_at, s.updated_at,
  COALESCE((SELECT json_group_array(json_object('id', a.id, 'name', a.name, 'mimeType', a.mime_type,
    'sizeBytes', a.size_bytes) ORDER BY a.id) FROM task_attachments a WHERE a.schedule_id = s.id), '[]'),s.connection_id,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=s.origin_session_id),s.execution_config,s.created_by_user_id,s.source_machine_id,s.binding_id,
 CASE WHEN b.id IS NOT NULL THEN json_object('bindingId',b.id,'path',b.workspace,'externalId',b.external_project_id,'source',CASE WHEN s.binding_id IS NULL THEN 'primary' ELSE 'chosen' END) END
FROM schedules s
JOIN projects p ON p.id = s.project_id
LEFT JOIN project_bindings b ON ` + effectiveBindingSQL("s")

func scanSchedule(row rowScanner) (store.Schedule, error) {
	var s store.Schedule
	err := row.Scan(&s.ID, &s.ProjectID, &s.ProjectTitle, &s.Title, &s.Prompt, &s.Notes, &s.Cron, &s.Tz,
		&s.Paused, &s.NextRunAt, &s.LastRunAt, &s.LastTaskID, &s.LastTaskState, &s.CreatedAt, &s.UpdatedAt,
		&s.Attachments, &s.ConnectionID, &s.OriginSession, &s.ExecutionConfig, &s.CreatedBy, &s.SourceMachineID, &s.BindingID, &s.Workspace)
	if err != nil {
		return store.Schedule{}, notFound(err)
	}
	return s, nil
}

func (r schedules) Get(ctx context.Context, id int64) (store.Schedule, error) {
	return scanSchedule(r.QueryRow(ctx, scheduleSelect+` WHERE s.id = $1`, id))
}

func (r schedules) List(ctx context.Context, f store.ScheduleFilter) ([]store.Schedule, error) {
	var w where
	if f.ProjectID != nil {
		w.add("s.project_id = " + w.arg(*f.ProjectID))
	}
	if f.DueBefore != nil {
		w.add("NOT s.paused AND s.next_run_at <= " + w.arg(*f.DueBefore))
	}
	rows, err := r.Query(ctx, scheduleSelect+w.sql()+` ORDER BY s.paused, s.next_run_at ASC NULLS LAST, s.id`, w.args...)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanSchedule)
}

func (r schedules) Exists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := r.QueryRow(ctx, `SELECT 1 FROM schedules WHERE id = $1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r schedules) Insert(ctx context.Context, in store.ScheduleInsert) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `
INSERT INTO schedules (project_id, title, prompt, notes, cron, tz, paused, next_run_at, origin_session_id,
  execution_config, connection_id, created_by_user_id, source_machine_id, binding_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		in.ProjectID, in.Title, in.Prompt, in.Notes,
		in.Cron, in.Tz, in.Paused, in.NextRunAt, in.OriginSessionID, in.ExecutionConfig, in.ConnectionID,
		in.CreatedBy, in.SourceMachineID, in.BindingID,
	).Scan(&id)
	return id, err
}

func (r schedules) EditState(ctx context.Context, id int64) (store.ScheduleEditState, error) {
	var e store.ScheduleEditState
	err := r.QueryRow(ctx, `SELECT execution_config, cron, tz, paused, connection_id, project_id, binding_id FROM schedules WHERE id = $1`, id).
		Scan(&e.ExecutionConfig, &e.Cron, &e.Tz, &e.Paused, &e.ConnectionID, &e.ProjectID, &e.BindingID)
	return e, notFound(err)
}

func (r schedules) Update(ctx context.Context, id int64, u store.ScheduleUpdate) error {
	var w where
	w.arg(id)
	var sets []string
	set := func(col string, v any) { sets = append(sets, col+" = "+w.arg(v)) }
	if u.ConnectionID.Set {
		set("connection_id", u.ConnectionID.V)
	}
	if u.ExecutionConfig.Set {
		set("execution_config", u.ExecutionConfig.Ptr())
	}
	if u.ProjectID.Set {
		set("project_id", u.ProjectID.V)
	}
	if u.BindingID.Set {
		set("binding_id", u.BindingID.Ptr())
	}
	if u.Title.Set {
		set("title", u.Title.V)
	}
	if u.Prompt.Set {
		set("prompt", u.Prompt.V)
	}
	if u.Notes.Set {
		set("notes", u.Notes.V)
	}
	if u.Cron.Set {
		set("cron", u.Cron.V)
	}
	if u.Tz.Set {
		set("tz", u.Tz.V)
	}
	if u.Paused.Set {
		set("paused", u.Paused.V)
	}
	if u.NextRunAt.Set {
		set("next_run_at", u.NextRunAt.Ptr())
	}
	sets = append(sets, "updated_at = "+nowSQL)
	_, err := r.Exec(ctx, `UPDATE schedules SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...)
	return err
}

func (r schedules) Delete(ctx context.Context, id int64) (bool, error) {
	tag, err := r.Exec(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	return err == nil && tag.RowsAffected() > 0, err
}

func (r schedules) Runs(ctx context.Context, id int64, limit int) ([]store.ScheduleRun, error) {
	rows, err := r.Query(ctx, `
SELECT id, schedule_id, fire_at, outcome, task_id, detail, at, `+attributionColumns("schedule_runs.attribution")+` FROM schedule_runs
WHERE schedule_id = $1 ORDER BY id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.ScheduleRun, error) {
		var r store.ScheduleRun
		err := row.Scan(&r.ID, &r.ScheduleID, &r.FireAt, &r.Outcome, &r.TaskID, &r.Detail, &r.At, &r.Attribution, &r.Actor.DisplayName)
		return r, err
	})
}

func (r schedules) Template(ctx context.Context, id int64) (store.ScheduleTemplate, error) {
	var in store.ScheduleTemplate
	err := r.QueryRow(ctx, `
SELECT project_id, title, prompt, notes, tz, connection_id, binding_id,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=schedules.origin_session_id),execution_config,created_by_user_id FROM schedules WHERE id = $1`, id).
		Scan(&in.ProjectID, &in.Title, &in.Prompt, &in.Notes, &in.Tz, &in.ConnectionID, &in.BindingID, &in.OriginSession, &in.ExecutionConfig, &in.CreatedBy)
	return in, notFound(err)
}

func (r schedules) InsertRun(ctx context.Context, id int64, fireAt time.Time, outcome string, taskID *int64, detail string, attribution store.Attribution) error {
	_, err := r.Exec(ctx, `
INSERT INTO schedule_runs (schedule_id, fire_at, outcome, task_id, detail, attribution) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, fireAt, outcome, taskID, detail, attribution)
	return err
}

func (r schedules) Due(ctx context.Context, now time.Time, connection int64) ([]int64, error) {
	rows, err := r.Query(ctx,
		`SELECT id FROM schedules WHERE NOT paused AND next_run_at <= $1 AND ($2=0 OR connection_id=$2) ORDER BY next_run_at, id`, now, connection)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r schedules) DueTasks(ctx context.Context, now time.Time, connection int64) ([]store.DueTask, error) {
	rows, err := r.Query(ctx, `
SELECT t.id, t.start_at FROM tasks t LEFT JOIN task_trees tr ON tr.id = t.tree_id
WHERE t.state = 'backlog' AND t.acceptance_id IS NULL AND t.start_at <= $1 AND ($2=0 OR t.connection_id=$2) AND NOT `+closedSQL+` ORDER BY t.start_at, t.id`, now, connection)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.DueTask, error) {
		var d store.DueTask
		err := row.Scan(&d.ID, &d.StartAt)
		return d, err
	})
}

func (r schedules) FireState(ctx context.Context, id int64) (store.ScheduleFireState, error) {
	var f store.ScheduleFireState
	err := r.QueryRow(ctx, `SELECT cron, tz, paused, next_run_at FROM schedules WHERE id = $1`, id).
		Scan(&f.Cron, &f.Tz, &f.Paused, &f.NextRunAt)
	return f, notFound(err)
}

func (r schedules) PendingTask(ctx context.Context, id int64) (int64, store.TaskState, error) {
	var prev int64
	var prevState store.TaskState
	err := r.QueryRow(ctx, `
SELECT id, state FROM tasks WHERE schedule_id = $1
  AND (state IN ('launching', 'running', 'needs_input') OR (state = 'backlog' AND acceptance_id IS NULL AND (start_at IS NOT NULL OR scheduled_hold=1)))
ORDER BY id DESC LIMIT 1`, id).Scan(&prev, &prevState)
	return prev, prevState, notFound(err)
}

func (r schedules) SetNextRun(ctx context.Context, id int64, next *time.Time) error {
	_, err := r.Exec(ctx, `UPDATE schedules SET next_run_at = $2 WHERE id = $1`, id, next)
	return err
}

func (r tasks) ByIDs(ctx context.Context, ids []int64) ([]store.Task, error) {
	return collectTasks(r.Query(ctx, taskSelect+` WHERE t.id IN (SELECT value FROM json_each($1))`, jsonArray(ids)))
}

// Lock is a no-op: BEGIN IMMEDIATE already serializes writes.
func (schedules) Lock(context.Context, int64) error { return nil }
