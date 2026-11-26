//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/jackc/pgx/v5"
)

type tasks struct{ *runner }

func (t *tx) Tasks() store.Tasks { return tasks{t.r} }

// SQL over taskSelect's aliases (t = task, tr = its tree).
const (
	effectiveModeSQL  = `COALESCE(t.start_mode, tr.start_mode)`
	hasNeedsSQL       = `EXISTS (SELECT 1 FROM task_needs n WHERE n.task_id = t.id)`
	unacceptedNeedSQL = `EXISTS (SELECT 1 FROM task_needs n JOIN tasks d ON d.id = n.need_id WHERE n.task_id = t.id AND d.acceptance_id IS NULL)`
	readinessSQL      = `CASE WHEN t.tree_id IS NULL OR t.state <> 'backlog' OR t.adopted OR t.acceptance_id IS NOT NULL OR tr.status <> 'open' THEN NULL
 WHEN ` + unacceptedNeedSQL + ` THEN 'blocked'
 WHEN t.start_at IS NULL AND (` + effectiveModeSQL + ` = 'manual' OR NOT ` + hasNeedsSQL + `) THEN 'ready'
 END`
	// releasedSQL: a backlog task with needs, all accepted, and no start time
	// (auto launches it; manual reads ready and notifies).
	releasedSQL = `t.state = 'backlog' AND t.acceptance_id IS NULL AND t.start_at IS NULL AND NOT t.adopted AND tr.status = 'open' AND ` + hasNeedsSQL + ` AND NOT ` + unacceptedNeedSQL
	// unstartedSQL: task t has no notes or execution history (deletable).
	unstartedSQL = `(t.launch_attempt = 0 AND t.session_id IS NULL AND NOT t.adopted AND NOT t.recorded_work)`
)

// closedSQL: task t is archived or its tree tr is not open.
const closedSQL = `(t.archived_at IS NOT NULL OR COALESCE(tr.status, 'open') <> 'open')`

const taskSelect = `
SELECT t.id, t.project_id, p.title, t.title, t.prompt,
  t.state, t.tree_id, tr.title, tr.start_mode, t.start_mode, ` + effectiveModeSQL + `, ` + readinessSQL + `,
  COALESCE((SELECT jsonb_agg(n.need_id ORDER BY n.need_id) FROM task_needs n WHERE n.task_id = t.id), '[]'::jsonb),
  COALESCE((SELECT jsonb_agg(n.task_id ORDER BY n.task_id) FROM task_needs n WHERE n.need_id = t.id), '[]'::jsonb),
  t.adopted, t.ready_at, t.launch_attempt, t.last_error,
  t.launched_at, t.finished_at, (obs.observation->>'startedAt')::timestamptz, (obs.observation->>'endedAt')::timestamptz, substr(obs.observation #>> '{reply,text}',1,1000), (obs.observation #>> '{reply,updatedAt}')::timestamptz,
  t.created_at, t.updated_at,
  t.start_at, t.start_tz, t.schedule_id, sc.title, (SELECT observation->>'title' FROM execution_observations WHERE session_id=t.origin_session_id), t.handoff,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('id', a.id, 'name', a.name, 'mimeType', a.mime_type,
    'sizeBytes', a.size_bytes) ORDER BY a.id) FROM task_attachments a WHERE a.task_id = t.id), '[]'::jsonb),
  (SELECT max(e.at) FROM task_events e WHERE e.task_id = t.id AND e.to_state = t.state), (obs.observation->>'updatedAt')::timestamptz,
  t.archived_at, tr.status, ` + unstartedSQL + `,
  t.connection_id,t.session_id,t.origin_session_id,
 (SELECT jsonb_build_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=t.session_id),
 (SELECT jsonb_build_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=t.origin_session_id),t.acceptance_id,t.acceptance_revision,(SELECT accepted_at FROM task_acceptances a WHERE a.id=t.acceptance_id),(SELECT provenance FROM task_acceptances a WHERE a.id=t.acceptance_id),t.execution_config,
 hc.harness,hc.active,hc.label,t.created_by_user_id,t.source_machine_id
FROM tasks t
JOIN projects p ON p.id = t.project_id
JOIN harness_connections hc ON hc.id = t.connection_id
LEFT JOIN task_trees tr ON tr.id = t.tree_id
LEFT JOIN execution_observations obs ON obs.session_id=t.session_id
LEFT JOIN schedules sc ON sc.id = t.schedule_id`

func scanTask(row rowScanner) (store.Task, error) {
	var t store.Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.ProjectTitle, &t.Title, &t.Prompt,
		&t.State, &t.TreeID, &t.TreeTitle, &t.TreeStartMode,
		&t.StartMode, &t.EffectiveStartMode, &t.Readiness, &t.Needs, &t.Dependents, &t.Adopted, &t.ReadyAt,
		&t.LaunchAttempt, &t.LastError, &t.LaunchedAt, &t.FinishedAt, &t.RunStartedAt, &t.RunEndedAt,
		&t.LastWordPreview, &t.LastWordAt, &t.CreatedAt,
		&t.UpdatedAt, &t.StartAt, &t.StartTz, &t.ScheduleID, &t.ScheduleTitle,
		&t.OriginSessionTitle, &t.Handoff, &t.Attachments,
		&t.StateSince, &t.SessionUpdatedAt, &t.ArchivedAt, &t.TreeStatus, &t.Deletable,
		&t.ConnectionID, &t.SessionIdentityID, &t.OriginSessionIdentityID, &t.Session, &t.OriginSession, &t.AcceptanceID, &t.AcceptanceRevision, &t.CompletedAt, &t.CompletionProvenance, &t.ExecutionConfig, &t.Harness, &t.ConnectionActive, &t.ConnectionLabel, &t.CreatedBy, &t.SourceMachineID)
	if err != nil {
		return store.Task{}, notFound(err)
	}
	if t.Session != nil {
		t.ExternalSessionID = &t.Session.ExternalSessionID
	}
	t.LastWordPreview = store.Preview(t.LastWordPreview)
	t.Needs = nonNilIDs(t.Needs)
	t.Dependents = nonNilIDs(t.Dependents)
	return t, nil
}

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func collectTasks(rows *rowSet, err error) ([]store.Task, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r tasks) Get(ctx context.Context, id int64) (store.Task, error) {
	return scanTask(r.QueryRow(ctx, taskSelect+` WHERE t.id = $1`, id))
}

func (r tasks) List(ctx context.Context, f store.TaskFilter) ([]store.Task, error) {
	var w where
	if f.OriginSession != nil {
		w.add("t.origin_session_id = (SELECT id FROM session_identities WHERE connection_id=" + w.arg(f.OriginSession.ConnectionID) + " AND external_id=" + w.arg(f.OriginSession.ExternalSessionID) + ")")
	}
	if f.ProjectID != nil {
		w.add("t.project_id = " + w.arg(*f.ProjectID))
	}
	if len(f.States) > 0 {
		states := make([]string, len(f.States))
		for i, st := range f.States {
			states[i] = string(st)
		}
		w.add("t.state = ANY(" + w.arg(states) + "::text[])")
	}
	if f.Q != "" {
		w.add(searchTasks(&w, f.Q))
	}
	if f.From != nil {
		w.add("t.created_at >= " + w.arg(*f.From))
	}
	if f.To != nil {
		w.add("t.created_at < " + w.arg(*f.To))
	}
	if f.CompletedFrom != nil {
		w.add("(SELECT accepted_at FROM task_acceptances a WHERE a.id=t.acceptance_id) >= " + w.arg(*f.CompletedFrom))
	}
	switch f.Closed {
	case "all":
	case "true":
		w.add(closedSQL)
	default:
		w.add("NOT " + closedSQL)
	}
	rows, err := r.Query(ctx, taskSelect+w.sql()+` ORDER BY t.updated_at DESC, t.id DESC`, w.args...)
	return collectTasks(rows, err)
}

func (r tasks) Pollable(ctx context.Context, includeStale bool) ([]store.Task, error) {
	// failed|interrupted|cancelled sessions may receive follow-ups and
	// can run again. Poll them for 10 minutes after the last change, then
	// whenever the stored observation shows activity.
	rows, err := r.Query(ctx, taskSelect+`
WHERE (t.session_id IS NOT NULL OR t.state='launching') AND (
  t.state IN ('launching', 'running', 'needs_input')
  OR (t.state='backlog' AND t.adopted)
  OR (t.state = 'done' AND ((t.acceptance_id IS NULL AND $1::boolean) OR t.updated_at > `+nowPlusSQL("-1 hour")+`))
  OR (t.state IN ('failed', 'interrupted', 'cancelled') AND (
    t.updated_at > `+nowPlusSQL("-10 minutes")+`
    OR COALESCE((SELECT observation->'active' IN ('true'::jsonb, '1'::jsonb) FROM execution_observations WHERE session_id=t.session_id), false)))
ORDER BY t.id`, includeStale)
	return collectTasks(rows, err)
}

func (r tasks) BySession(ctx context.Context, ref store.SessionRef) (store.Task, error) {
	return scanTask(r.QueryRow(ctx, taskSelect+` WHERE t.session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID))
}

func (r tasks) CreatedBySession(ctx context.Context, ref store.SessionRef) ([]store.Task, error) {
	rows, err := r.Query(ctx, taskSelect+` WHERE t.origin_session_id = (SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2) ORDER BY t.id`, ref.ConnectionID, ref.ExternalSessionID)
	return collectTasks(rows, err)
}

func (r tasks) BySessionID(ctx context.Context, sessionID int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, "SELECT id FROM tasks WHERE session_id=$1", sessionID).Scan(&id)
	return id, notFound(err)
}

func (r tasks) SessionOccupied(ctx context.Context, sessionID, taskID int64) (bool, error) {
	var occupied bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE session_id=$1 AND id<>$2)`, sessionID, taskID).Scan(&occupied)
	return occupied, err
}

func (r tasks) Events(ctx context.Context, taskID int64) ([]store.TaskEvent, error) {
	rows, err := r.Query(ctx,
		`SELECT id, task_id, from_state, to_state, detail, at FROM task_events WHERE task_id = $1 ORDER BY at, id`, taskID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.TaskEvent, error) {
		var e store.TaskEvent
		err := r.Scan(&e.ID, &e.TaskID, &e.FromState, &e.ToState, &e.Detail, &e.At)
		return e, err
	})
}

func (r tasks) AddEvent(ctx context.Context, taskID int64, from *store.TaskState, to store.TaskState, detail string) (time.Time, error) {
	var f *string
	if from != nil {
		v := string(*from)
		f = &v
	}
	var at time.Time
	err := r.QueryRow(ctx, `INSERT INTO task_events (task_id, from_state, to_state, detail, at) VALUES ($1, $2, $3, $4, `+nowSQL+`) RETURNING at`, taskID, f, string(to), detail).Scan(&at)
	return at, err
}

func (r tasks) JournalState(ctx context.Context, id int64) (store.TaskJournalState, error) {
	var s store.TaskJournalState
	err := r.QueryRow(ctx, `SELECT tree_id, session_id, title, launch_attempt, adopted, recorded_work FROM tasks WHERE id = $1`, id).
		Scan(&s.TreeID, &s.SessionID, &s.Title, &s.LaunchAttempt, &s.Adopted, &s.RecordedWork)
	return s, notFound(err)
}

func (r tasks) MarkRecordedWork(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET recorded_work = true WHERE id = $1`, id)
	return err
}

func (r tasks) ProjectBySession(ctx context.Context, sessionID int64) (*int64, error) {
	var p *int64
	err := r.QueryRow(ctx, `SELECT project_id FROM tasks WHERE session_id = $1`, sessionID).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (r tasks) Insert(ctx context.Context, in store.TaskInsert) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `
INSERT INTO tasks (project_id, title, prompt, start_at, start_tz, schedule_id, handoff, start_mode,
  connection_id, origin_session_id, execution_config, created_by_user_id, source_machine_id)
VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::boolean, true),$8,$9,$10,$11,$12,$13) RETURNING id`,
		in.ProjectID, in.Title, in.Prompt, in.StartAt, in.StartTz, in.ScheduleID, in.Handoff, in.StartMode,
		in.ConnectionID, in.OriginSessionID, in.ExecutionConfig, in.CreatedBy, in.SourceMachineID,
	).Scan(&id)
	return id, err
}

func (r tasks) InsertAdopted(ctx context.Context, in store.AdoptedInsert) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO tasks(project_id,title,prompt,state,connection_id,session_id,
	 execution_config,adopted,launch_attempt,launched_at,finished_at,created_by_user_id,source_machine_id)
	 VALUES($1,$2,'',$3,$4,$5,$6,true,1,$7,$8,$9,$10) RETURNING id`, in.ProjectID, in.Title, in.State, in.ConnectionID, in.SessionID, in.ExecutionConfig, in.LaunchedAt, in.FinishedAt, in.CreatedBy, in.SourceMachineID).Scan(&id)
	return id, err
}

func (r tasks) EditState(ctx context.Context, id int64) (store.TaskEditState, error) {
	var e store.TaskEditState
	err := r.QueryRow(ctx, `SELECT state, execution_config, adopted, connection_id, project_id,
  (SELECT count(*) FROM task_needs WHERE task_id = tasks.id) FROM tasks WHERE id = $1`, id).
		Scan(&e.State, &e.ExecutionConfig, &e.Adopted, &e.ConnectionID, &e.ProjectID, &e.Needs)
	return e, notFound(err)
}

func (r tasks) SetStart(ctx context.Context, id int64, at *time.Time, tz *string) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET start_at = $2, start_tz = $3 WHERE id = $1`, id, at, tz)
	return err
}

func (r tasks) Update(ctx context.Context, id int64, u store.TaskUpdate) error {
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
	if u.Title.Set {
		set("title", u.Title.V)
	}
	if u.Prompt.Set {
		set("prompt", u.Prompt.V)
	}
	if u.Handoff.Set {
		set("handoff", u.Handoff.V)
	}
	if u.StartMode.Set {
		set("start_mode", u.StartMode.Ptr())
	}
	sets = append(sets, "updated_at = "+nowSQL)
	_, err := r.Exec(ctx, `UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...)
	return err
}

func (r tasks) Deletability(ctx context.Context, id int64) (store.TaskState, bool, error) {
	var state store.TaskState
	var unstarted bool
	err := r.QueryRow(ctx, `SELECT t.state, `+unstartedSQL+` FROM tasks t WHERE t.id = $1`, id).Scan(&state, &unstarted)
	return state, unstarted, notFound(err)
}

func (r tasks) Delete(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	return err
}

func (r tasks) TreeID(ctx context.Context, id int64) (*int64, error) {
	var tree *int64
	err := r.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, id).Scan(&tree)
	return tree, notFound(err)
}

func (r tasks) SetArchived(ctx context.Context, id int64, archived bool) error {
	if !archived {
		_, err := r.Exec(ctx, `UPDATE tasks SET archived_at = NULL, updated_at = `+nowSQL+` WHERE id = $1 AND archived_at IS NOT NULL`, id)
		return err
	}
	_, err := r.Exec(ctx, `UPDATE tasks SET archived_at = `+nowSQL+`, updated_at = `+nowSQL+` WHERE id = $1 AND archived_at IS NULL`, id)
	return err
}

func (r tasks) Unarchive(ctx context.Context, id int64) (bool, error) {
	tag, err := r.Exec(ctx, `UPDATE tasks SET archived_at = NULL WHERE id = $1 AND archived_at IS NOT NULL`, id)
	return err == nil && tag.RowsAffected() > 0, err
}

func (r tasks) Touch(ctx context.Context, id int64) (bool, error) {
	tag, err := r.Exec(ctx, `UPDATE tasks SET updated_at = `+nowSQL+` WHERE id = $1`, id)
	return err == nil && tag.RowsAffected() > 0, err
}

func (r tasks) LaunchState(ctx context.Context, id int64) (store.TaskLaunchState, error) {
	var s store.TaskLaunchState
	err := r.QueryRow(ctx, `SELECT state, launch_attempt, COALESCE(launch_request_id, '') FROM tasks WHERE id = $1`, id).
		Scan(&s.State, &s.Attempt, &s.RequestID)
	return s, notFound(err)
}

func (r tasks) ApplyTransition(ctx context.Context, t store.Transition, sessionID *int64) (int, string, error) {
	var attempt int
	var requestID string
	err := r.QueryRow(ctx, `
UPDATE tasks SET
  state = $2::text,
  launch_attempt = launch_attempt + CASE WHEN $3::boolean THEN 1 ELSE 0 END,
  launch_request_id = CASE WHEN $3::boolean THEN 'task-' || id || '-' || $7::text ELSE launch_request_id END,
  session_id = CASE WHEN $3::boolean THEN NULL WHEN $4::bigint IS NOT NULL THEN $4::bigint ELSE session_id END,
  scheduled_hold = CASE WHEN $3::boolean THEN false ELSE scheduled_hold END,
  launched_at = CASE WHEN $4::bigint IS NOT NULL THEN `+nowSQL+` ELSE COALESCE(launched_at,$8::timestamptz) END,
  last_error = CASE WHEN $3::boolean THEN NULL WHEN $5::text IS NOT NULL THEN $5::text ELSE last_error END,
  finished_at = CASE WHEN $3::boolean THEN NULL
                     WHEN $6::boolean AND finished_at IS NULL THEN `+nowSQL+` ELSE finished_at END,
  start_at = CASE WHEN $3::boolean THEN NULL ELSE start_at END,
  start_tz = CASE WHEN $3::boolean THEN NULL ELSE start_tz END,
  ready_at = CASE WHEN $3::boolean THEN NULL ELSE ready_at END,
  updated_at = `+nowSQL+`
WHERE id = $1 RETURNING launch_attempt, COALESCE(launch_request_id, '')`,
		t.TaskID, string(t.To), t.BeginAttempt, sessionID, t.LastError, t.To.Finished(), newUUID(), t.ObservedStart,
	).Scan(&attempt, &requestID)
	return attempt, requestID, err
}

func (r tasks) Confirm(ctx context.Context, a store.Attempt, state string, sessionID int64, endedAt *time.Time) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE tasks SET state=$3::text,session_id=$4,last_error=NULL,launched_at=COALESCE(launched_at,`+nowSQL+`),finished_at=CASE WHEN $3::text IN ('done','failed','interrupted','cancelled') THEN COALESCE($5::timestamptz,`+nowSQL+`) ELSE NULL END,updated_at=`+nowSQL+` WHERE id=$1 AND launch_attempt=$2 AND state='launching'`, a.TaskID, a.Number, state, sessionID, endedAt)
	return err == nil && res.RowsAffected() == 1, err
}

func (r tasks) Reject(ctx context.Context, a store.Attempt, reason string) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET state='backlog',last_error=$3,scheduled_hold=true,updated_at=`+nowSQL+` WHERE id=$1 AND launch_attempt=$2 AND state='launching'`, a.TaskID, a.Number, reason)
	return err
}

func (r tasks) SetLaunchError(ctx context.Context, a store.Attempt, msg string) error {
	_, err := r.Exec(ctx, "UPDATE tasks SET last_error=$3 WHERE id=$1 AND launch_attempt=$2 AND state='launching'", a.TaskID, a.Number, msg)
	return err
}

func (r tasks) NoteBlocked(ctx context.Context, id int64, reason string) (bool, error) {
	tag, err := r.Exec(ctx, `UPDATE tasks SET last_error=$2,updated_at=`+nowSQL+` WHERE id=$1 AND state='backlog'`, id, reason)
	return err == nil && tag.RowsAffected() > 0, err
}

func (r tasks) HoldScheduled(ctx context.Context, id int64, reason string) (bool, error) {
	tag, err := r.Exec(ctx, `UPDATE tasks SET scheduled_hold=true,start_at=NULL,start_tz=NULL,last_error=$2,updated_at=`+nowSQL+` WHERE id=$1 AND state='backlog' AND start_at IS NOT NULL`, id, reason)
	return err == nil && tag.RowsAffected() > 0, err
}
