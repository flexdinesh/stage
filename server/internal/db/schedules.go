package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/stage/server/internal/cronexpr"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Schedule is a recurring task template (docs/api.md "Schedules").
type Schedule struct {
	ID                 int64            `json:"id"`
	ProjectID          int64            `json:"projectId"`
	ProjectTitle       string           `json:"projectTitle"`
	Title              string           `json:"title"`
	Prompt             string           `json:"prompt"`
	ProviderInstanceID *string          `json:"providerInstanceId"`
	Model              *string          `json:"model"`
	ModelOptions       []t3.ModelOption `json:"modelOptions"`
	RuntimeMode        string           `json:"runtimeMode"`
	InteractionMode    string           `json:"interactionMode"`
	Workspace          Workspace        `json:"workspace"`
	Tags               []string         `json:"tags"`
	Notes              string           `json:"notes"`
	Attachments        []Attachment     `json:"attachments"`
	Cron               string           `json:"cron"`
	Tz                 string           `json:"tz"`
	Paused             bool             `json:"paused"`
	NextRunAt          *time.Time       `json:"nextRunAt"`
	LastRunAt          *time.Time       `json:"lastRunAt"`
	LastTaskID         *int64           `json:"lastTaskId"`
	LastTaskState      *TaskState       `json:"lastTaskState"`
	OriginThreadID     *string          `json:"originThreadId"` // T3 session that created the schedule; spawned tasks inherit it
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
}

// Schedule run outcomes.
const (
	RunStarted = "started"
	RunSkipped = "skipped"
	RunMissed  = "missed"
	RunManual  = "manual"
)

// ScheduleRun is one fire of a schedule.
type ScheduleRun struct {
	ID         int64     `json:"id"`
	ScheduleID int64     `json:"scheduleId"`
	FireAt     time.Time `json:"fireAt"`
	Outcome    string    `json:"outcome"`
	TaskID     *int64    `json:"taskId"`
	Detail     string    `json:"detail"`
	At         time.Time `json:"at"`
	Task       *Task     `json:"-"` // spawned task (detail reads)
}

const scheduleSelect = `
SELECT s.id, s.project_id, p.title, s.title, s.prompt, s.provider_instance_id, s.model, s.model_options,
  s.runtime_mode, s.interaction_mode, s.workspace, s.tags, s.notes, s.cron, s.tz, s.paused, s.next_run_at,
  (SELECT r.fire_at FROM schedule_runs r WHERE r.schedule_id = s.id ORDER BY r.id DESC LIMIT 1),
  lt.id, lt.state, s.origin_thread_id, s.created_at, s.updated_at,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('id', a.id, 'name', a.name, 'mimeType', a.mime_type,
    'sizeBytes', a.size_bytes) ORDER BY a.id) FROM task_attachments a WHERE a.schedule_id = s.id), '[]')
FROM schedules s
JOIN projects p ON p.id = s.project_id
LEFT JOIN LATERAL (SELECT t.id, t.state FROM tasks t WHERE t.schedule_id = s.id ORDER BY t.id DESC LIMIT 1) lt ON true`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.ProjectID, &s.ProjectTitle, &s.Title, &s.Prompt, &s.ProviderInstanceID, &s.Model,
		&s.ModelOptions, &s.RuntimeMode, &s.InteractionMode, &s.Workspace, &s.Tags, &s.Notes, &s.Cron, &s.Tz,
		&s.Paused, &s.NextRunAt, &s.LastRunAt, &s.LastTaskID, &s.LastTaskState, &s.OriginThreadID, &s.CreatedAt, &s.UpdatedAt,
		&s.Attachments)
	if err != nil {
		return Schedule{}, notFound(err)
	}
	s.ModelOptions = nonNilOptions(s.ModelOptions)
	s.Tags = nonNilStrings(s.Tags)
	if s.Workspace.Type == "" {
		s.Workspace.Type = "root"
	}
	return s, nil
}

func (s *Store) GetSchedule(ctx context.Context, id int64) (Schedule, error) {
	return scanSchedule(s.pool.QueryRow(ctx, scheduleSelect+` WHERE s.id = $1`, id))
}

// ScheduleFilter: ProjectID; DueBefore = unpaused with next_run_at <= it.
type ScheduleFilter struct {
	ProjectID *int64
	DueBefore *time.Time
}

// ListSchedules orders by next fire (paused last), then id.
func (s *Store) ListSchedules(ctx context.Context, f ScheduleFilter) ([]Schedule, error) {
	var w where
	if f.ProjectID != nil {
		w.add("s.project_id = " + w.arg(*f.ProjectID))
	}
	if f.DueBefore != nil {
		w.add("NOT s.paused AND s.next_run_at <= " + w.arg(*f.DueBefore))
	}
	rows, err := s.pool.Query(ctx, scheduleSelect+w.sql()+` ORDER BY s.paused, s.next_run_at ASC NULLS LAST, s.id`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// NewSchedule is a validated create input (cron and tz already parse).
type NewSchedule struct {
	ProjectID          int64
	Title              string
	Prompt             string
	ProviderInstanceID *string
	Model              *string
	ModelOptions       []t3.ModelOption
	RuntimeMode        string
	InteractionMode    string
	Workspace          Workspace
	Tags               []string
	Notes              string
	AttachmentIDs      []int64
	Cron               string
	Tz                 string
	Paused             bool
	OriginThreadID     *string
}

// nextRun is the first fire after now, or nil while paused.
func nextRun(cron, tz string, paused bool, now time.Time) (*time.Time, error) {
	spec, err := cronexpr.Parse(cron, tz)
	if err != nil {
		return nil, Invalid("%s", err.Error())
	}
	if paused {
		return nil, nil
	}
	n := spec.Next(now)
	if n.IsZero() {
		return nil, nil
	}
	return &n, nil
}

func (s *Store) CreateSchedule(ctx context.Context, in NewSchedule) (Schedule, error) {
	next, err := nextRun(in.Cron, in.Tz, in.Paused, time.Now())
	if err != nil {
		return Schedule{}, err
	}
	var id int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := projectExists(ctx, tx, in.ProjectID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
INSERT INTO schedules (project_id, title, prompt, provider_instance_id, model, model_options,
  runtime_mode, interaction_mode, workspace, tags, notes, cron, tz, paused, next_run_at, origin_thread_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`,
			in.ProjectID, in.Title, in.Prompt, in.ProviderInstanceID, in.Model, nonNilOptions(in.ModelOptions),
			in.RuntimeMode, in.InteractionMode, in.Workspace, nonNilStrings(in.Tags), in.Notes,
			strings.TrimSpace(in.Cron), in.Tz, in.Paused, next, in.OriginThreadID,
		).Scan(&id)
		if err != nil {
			return err
		}
		if len(in.AttachmentIDs) > 0 {
			return setOwnedAttachments(ctx, tx, "schedule_id", id, in.AttachmentIDs)
		}
		return nil
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.GetSchedule(ctx, id)
}

// SchedulePatch is a partial schedule update (JSON body of PATCH
// /api/schedules/:id).
type SchedulePatch struct {
	ProjectID          Optional[int64]            `json:"projectId"`
	Title              Optional[string]           `json:"title"`
	Prompt             Optional[string]           `json:"prompt"`
	ProviderInstanceID Optional[string]           `json:"providerInstanceId"`
	Model              Optional[string]           `json:"model"`
	ModelOptions       Optional[[]t3.ModelOption] `json:"modelOptions"`
	RuntimeMode        Optional[string]           `json:"runtimeMode"`
	InteractionMode    Optional[string]           `json:"interactionMode"`
	Workspace          Optional[Workspace]        `json:"workspace"`
	Tags               Optional[[]string]         `json:"tags"`
	Notes              Optional[string]           `json:"notes"`
	AttachmentIDs      Optional[[]int64]          `json:"attachmentIds"` // the exact image set; null = none
	Cron               Optional[string]           `json:"cron"`
	Tz                 Optional[string]           `json:"tz"`
	Paused             Optional[bool]             `json:"paused"`
}

// UpdateSchedule applies a validated patch. Changing cron, tz or paused
// recomputes next_run_at from now (a resume never fires a missed time).
func (s *Store) UpdateSchedule(ctx context.Context, id int64, p SchedulePatch) (Schedule, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var provider, model *string
		var cron, tz string
		var paused bool
		err := tx.QueryRow(ctx, `SELECT provider_instance_id, model, cron, tz, paused FROM schedules WHERE id = $1 FOR UPDATE`, id).
			Scan(&provider, &model, &cron, &tz, &paused)
		if err != nil {
			return notFound(err)
		}
		if p.ProviderInstanceID.Set {
			provider = p.ProviderInstanceID.Ptr()
		}
		if p.Model.Set {
			model = p.Model.Ptr()
		}
		if (provider == nil) != (model == nil) {
			return Invalid("providerInstanceId and model must be set together")
		}
		if p.AttachmentIDs.Set {
			if err := setOwnedAttachments(ctx, tx, "schedule_id", id, p.AttachmentIDs.V); err != nil {
				return err
			}
		}
		var w where
		w.arg(id)
		var sets []string
		set := func(col string, v any) { sets = append(sets, col+" = "+w.arg(v)) }
		if p.ProjectID.Set {
			if err := projectExists(ctx, tx, p.ProjectID.V); err != nil {
				return err
			}
			set("project_id", p.ProjectID.V)
		}
		if p.Title.Set {
			set("title", p.Title.V)
		}
		if p.Prompt.Set {
			set("prompt", p.Prompt.V)
		}
		if p.ProviderInstanceID.Set {
			set("provider_instance_id", p.ProviderInstanceID.Ptr())
		}
		if p.Model.Set {
			set("model", p.Model.Ptr())
		}
		if p.ModelOptions.Set {
			set("model_options", nonNilOptions(p.ModelOptions.V))
		}
		if p.RuntimeMode.Set {
			set("runtime_mode", p.RuntimeMode.V)
		}
		if p.InteractionMode.Set {
			set("interaction_mode", p.InteractionMode.V)
		}
		if p.Workspace.Set {
			set("workspace", p.Workspace.V)
		}
		if p.Tags.Set {
			set("tags", nonNilStrings(p.Tags.V))
		}
		if p.Notes.Set {
			set("notes", p.Notes.V)
		}
		if p.Cron.Set || p.Tz.Set || p.Paused.Set {
			if p.Cron.Set {
				cron = strings.TrimSpace(p.Cron.V)
				set("cron", cron)
			}
			if p.Tz.Set {
				tz = p.Tz.V
				set("tz", tz)
			}
			if p.Paused.Set {
				paused = p.Paused.V
				set("paused", paused)
			}
			next, err := nextRun(cron, tz, paused, time.Now())
			if err != nil {
				return err
			}
			set("next_run_at", next)
		}
		if len(sets) == 0 && !p.AttachmentIDs.Set {
			return nil
		}
		sets = append(sets, "updated_at = now()")
		_, err = tx.Exec(ctx, `UPDATE schedules SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...)
		return err
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.GetSchedule(ctx, id)
}

// DeleteSchedule removes a schedule, its runs and images; spawned tasks stay
// (schedule_id becomes NULL).
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ScheduleRuns returns the newest limit runs, each with its spawned task.
func (s *Store) ScheduleRuns(ctx context.Context, id int64, limit int) ([]ScheduleRun, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, schedule_id, fire_at, outcome, task_id, detail, at FROM schedule_runs
WHERE schedule_id = $1 ORDER BY id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ScheduleRun, error) {
		var r ScheduleRun
		err := row.Scan(&r.ID, &r.ScheduleID, &r.FireAt, &r.Outcome, &r.TaskID, &r.Detail, &r.At)
		return r, err
	})
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range runs {
		if r.TaskID != nil {
			ids = append(ids, *r.TaskID)
		}
	}
	if len(ids) == 0 {
		return runs, nil
	}
	tasks, err := collectTasks(s.pool.Query(ctx, taskSelect+` WHERE t.id = ANY($1)`, ids))
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for i, r := range runs {
		if r.TaskID == nil {
			continue
		}
		if t, ok := byID[*r.TaskID]; ok {
			runs[i].Task = &t
		}
	}
	return runs, nil
}

// spawnTask inserts a backlog task from schedule id's config and images.
func spawnTask(ctx context.Context, tx pgx.Tx, id int64, startAt *time.Time) (int64, error) {
	var in NewTask
	var tz string
	err := tx.QueryRow(ctx, `
SELECT project_id, title, prompt, provider_instance_id, model, model_options, runtime_mode,
  interaction_mode, workspace, tags, notes, tz, origin_thread_id FROM schedules WHERE id = $1`, id).
		Scan(&in.ProjectID, &in.Title, &in.Prompt, &in.ProviderInstanceID, &in.Model, &in.ModelOptions,
			&in.RuntimeMode, &in.InteractionMode, &in.Workspace, &in.Tags, &in.Notes, &tz, &in.OriginThreadID)
	if err != nil {
		return 0, notFound(err)
	}
	in.ScheduleID = &id
	if startAt != nil {
		in.StartAt, in.StartTz = startAt, &tz
	}
	return insertTask(ctx, tx, in, func(tx pgx.Tx, task int64) error {
		return copyScheduleAttachments(ctx, tx, id, task)
	})
}

func insertRun(ctx context.Context, tx pgx.Tx, id int64, fireAt time.Time, outcome string, taskID *int64, detail string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO schedule_runs (schedule_id, fire_at, outcome, task_id, detail) VALUES ($1, $2, $3, $4, $5)`,
		id, fireAt, outcome, taskID, detail)
	return err
}

// DueSchedules: unpaused schedules whose next fire is at or before now.
func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM schedules WHERE NOT paused AND next_run_at <= $1 ORDER BY next_run_at, id`, now)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// DueTask is a backlog task whose one-shot start has come.
type DueTask struct {
	ID      int64
	StartAt time.Time
}

// DueOneShots: backlog tasks with start_at at or before now, oldest first.
func (s *Store) DueOneShots(ctx context.Context, now time.Time) ([]DueTask, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, start_at FROM tasks WHERE state = 'backlog' AND start_at <= $1 ORDER BY start_at, id`, now)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (DueTask, error) {
		var d DueTask
		err := row.Scan(&d.ID, &d.StartAt)
		return d, err
	})
}

// FireResult is what FireSchedule did ("" outcome: not due, nothing written).
type FireResult struct {
	Outcome string
	TaskID  *int64
}

// FireSchedule handles one due fire of schedule id at now, in one
// transaction with the row locked (docs/api.md "Schedule rules"): a fire
// older than grace is recorded missed; a previous spawned task still pending
// or active skips it; else a task is spawned with start_at = the fire time.
// next_run_at always advances past now.
func (s *Store) FireSchedule(ctx context.Context, id int64, now time.Time, grace time.Duration) (FireResult, error) {
	var res FireResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var cron, tz string
		var paused bool
		var next *time.Time
		err := tx.QueryRow(ctx, `SELECT cron, tz, paused, next_run_at FROM schedules WHERE id = $1 FOR UPDATE`, id).
			Scan(&cron, &tz, &paused, &next)
		if err != nil {
			return notFound(err)
		}
		if paused || next == nil || next.After(now) {
			return nil
		}
		spec, err := cronexpr.Parse(cron, tz)
		if err != nil {
			return fmt.Errorf("schedule %d: %w", id, err)
		}
		fireAt := *next
		advance := spec.Next(now)
		switch {
		case now.Sub(fireAt) > grace:
			n := 0
			for t := fireAt; !t.IsZero() && !t.After(now) && n < 100000; t = spec.Next(t) {
				n++
			}
			detail := fmt.Sprintf("missed %d fires", n)
			if n == 1 {
				detail = "missed 1 fire"
			}
			res.Outcome = RunMissed
			err = insertRun(ctx, tx, id, fireAt, RunMissed, nil, detail)
		default:
			var prev int64
			var prevState TaskState
			err = tx.QueryRow(ctx, `
SELECT id, state FROM tasks WHERE schedule_id = $1
  AND (state IN ('launching', 'running', 'needs_input') OR (state = 'backlog' AND start_at IS NOT NULL))
ORDER BY id DESC LIMIT 1`, id).Scan(&prev, &prevState)
			switch {
			case err == nil:
				res.Outcome = RunSkipped
				err = insertRun(ctx, tx, id, fireAt, RunSkipped, nil, fmt.Sprintf("#%d still %s", prev, stateWords(prevState)))
			case errors.Is(err, pgx.ErrNoRows):
				var task int64
				if task, err = spawnTask(ctx, tx, id, &fireAt); err != nil {
					return err
				}
				res.Outcome, res.TaskID = RunStarted, &task
				err = insertRun(ctx, tx, id, fireAt, RunStarted, &task, "")
			}
		}
		if err != nil {
			return err
		}
		var nextAt *time.Time
		if !advance.IsZero() {
			nextAt = &advance
		}
		_, err = tx.Exec(ctx, `UPDATE schedules SET next_run_at = $2 WHERE id = $1`, id, nextAt)
		return err
	})
	if err != nil {
		return FireResult{}, err
	}
	return res, nil
}

// stateWords renders a state for run details ("needs input", "pending").
func stateWords(s TaskState) string {
	if s == StateBacklog {
		return "pending"
	}
	return strings.ReplaceAll(string(s), "_", " ")
}

// RunScheduleNow spawns a backlog task from the schedule (no start time) and
// records a manual run; the caller starts it.
func (s *Store) RunScheduleNow(ctx context.Context, id int64) (Task, error) {
	var task int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM schedules WHERE id = $1 FOR UPDATE`, id).Scan(&one); err != nil {
			return notFound(err)
		}
		var err error
		if task, err = spawnTask(ctx, tx, id, nil); err != nil {
			return err
		}
		return insertRun(ctx, tx, id, time.Now(), RunManual, &task, "run now")
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, task)
}
