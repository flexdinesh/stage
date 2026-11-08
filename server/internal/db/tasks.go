package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

const taskSelect = `
SELECT t.id, t.project_id, p.title, p.t3_project_id, t.title, t.prompt, t.provider_instance_id,
  t.model, t.model_options, t.runtime_mode, t.interaction_mode, t.workspace, t.tags, t.notes,
  t.state, t.t3_thread_id, t.after_task_id, nx.id, t.launch_attempt, t.last_error,
  t.launched_at, t.finished_at, t.settled_at, t.created_at, t.updated_at, th.link,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('id', a.id, 'name', a.name, 'mimeType', a.mime_type,
    'sizeBytes', a.size_bytes) ORDER BY a.id) FROM task_attachments a WHERE a.task_id = t.id), '[]')
FROM tasks t
JOIN projects p ON p.id = t.project_id
LEFT JOIN tasks nx ON nx.after_task_id = t.id
LEFT JOIN threads th ON th.t3_thread_id = t.t3_thread_id`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.ProjectTitle, &t.T3ProjectID, &t.Title, &t.Prompt,
		&t.ProviderInstanceID, &t.Model, &t.ModelOptions, &t.RuntimeMode, &t.InteractionMode,
		&t.Workspace, &t.Tags, &t.Notes, &t.State, &t.T3ThreadID, &t.AfterTaskID, &t.NextTaskID,
		&t.LaunchAttempt, &t.LastError, &t.LaunchedAt, &t.FinishedAt, &t.SettledAt, &t.CreatedAt,
		&t.UpdatedAt, &t.ThreadLink, &t.Attachments)
	if err != nil {
		return Task{}, notFound(err)
	}
	t.ModelOptions = nonNilOptions(t.ModelOptions)
	t.Tags = nonNilStrings(t.Tags)
	if t.Workspace.Type == "" {
		t.Workspace.Type = "root"
	}
	return t, nil
}

func collectTasks(rows pgx.Rows, err error) ([]Task, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, id int64) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, taskSelect+` WHERE t.id = $1`, id))
}

type TaskFilter struct {
	ProjectID   *int64
	States      []string
	Tag         string
	Q           string
	From, To    *time.Time
	SettledFrom *time.Time // settled_at >= SettledFrom
}

func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	var w where
	if f.ProjectID != nil {
		w.add("t.project_id = " + w.arg(*f.ProjectID))
	}
	if len(f.States) > 0 {
		w.add("t.state = ANY(" + w.arg(f.States) + ")")
	}
	if f.Tag != "" {
		w.add(w.arg(f.Tag) + " = ANY(t.tags)")
	}
	if f.Q != "" {
		p := w.arg(likePattern(f.Q))
		w.add("(t.title ILIKE " + p + " OR t.prompt ILIKE " + p + ")")
	}
	if f.From != nil {
		w.add("t.created_at >= " + w.arg(*f.From))
	}
	if f.To != nil {
		w.add("t.created_at < " + w.arg(*f.To))
	}
	if f.SettledFrom != nil {
		w.add("t.settled_at >= " + w.arg(*f.SettledFrom))
	}
	rows, err := s.pool.Query(ctx, taskSelect+w.sql()+` ORDER BY t.updated_at DESC, t.id DESC`, w.args...)
	return collectTasks(rows, err)
}

// PollableTasks: tasks whose thread may still change state. Active tasks
// always; done tasks updated in the last hour always, older ones only
// when includeStale (the poller passes it at most once a minute);
// failed/interrupted only when touched in the last 10 minutes (e.g. a
// follow-up restarted the thread). backlog, settled and cancelled (skip /
// rollback) are final for polling.
func (s *Store) PollableTasks(ctx context.Context, includeStale bool) ([]Task, error) {
	rows, err := s.pool.Query(ctx, taskSelect+`
WHERE t.t3_thread_id IS NOT NULL AND (
  t.state IN ('launching', 'running', 'needs_input')
  OR (t.state = 'done' AND ($1::boolean OR t.updated_at > now() - interval '1 hour'))
  OR (t.state IN ('failed', 'interrupted') AND t.updated_at > now() - interval '10 minutes'))
ORDER BY t.id`, includeStale)
	return collectTasks(rows, err)
}

func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]TaskEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, task_id, from_state, to_state, detail, at FROM task_events WHERE task_id = $1 ORDER BY at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.FromState, &e.ToState, &e.Detail, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// NewTask is a validated create input.
type NewTask struct {
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
	AfterTaskID        *int64  // runs after this task (link rules: setAfter)
	AttachmentIDs      []int64 // staged images to attach (setAttachments)
}

// CreateTask inserts a backlog task (+ creation event), linked to run after
// AfterTaskID when set (setAfter rules; the whole insert fails otherwise).
func (s *Store) CreateTask(ctx context.Context, in NewTask) (Task, error) {
	return s.createTask(ctx, in, nil)
}

// createTask is CreateTask with extra run inside the insert transaction.
func (s *Store) createTask(ctx context.Context, in NewTask, extra func(tx pgx.Tx, id int64) error) (Task, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := projectExists(ctx, tx, in.ProjectID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
INSERT INTO tasks (project_id, title, prompt, provider_instance_id, model, model_options,
  runtime_mode, interaction_mode, workspace, tags, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			in.ProjectID, in.Title, in.Prompt, in.ProviderInstanceID, in.Model, nonNilOptions(in.ModelOptions),
			in.RuntimeMode, in.InteractionMode, in.Workspace, nonNilStrings(in.Tags), in.Notes,
		).Scan(&id)
		if err != nil {
			return err
		}
		if in.AfterTaskID != nil {
			if err := setAfter(ctx, tx, id, in.AfterTaskID); err != nil {
				return err
			}
		}
		if len(in.AttachmentIDs) > 0 {
			if err := setAttachments(ctx, tx, id, in.AttachmentIDs); err != nil {
				return err
			}
		}
		if extra != nil {
			if err := extra(tx, id); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, NULL, 'backlog', 'created')`, id)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func projectExists(ctx context.Context, tx pgx.Tx, id int64) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1`, id).Scan(&one)
	if err != nil {
		return fmt.Errorf("project: %w", notFound(err))
	}
	return nil
}

// TaskPatch is a partial task update (JSON body of PATCH /api/tasks/:id).
// Nullable fields: providerInstanceId, model (Null clears).
type TaskPatch struct {
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
	AfterTaskID        Optional[int64]            `json:"afterTaskId"`   // null unlinks
	AttachmentIDs      Optional[[]int64]          `json:"attachmentIds"` // the exact image set; null = none
}

// restrictedSet reports whether a field editable only in backlog is present.
func (p TaskPatch) restrictedSet() bool {
	return p.ProjectID.Set || p.Prompt.Set || p.ProviderInstanceID.Set || p.Model.Set ||
		p.ModelOptions.Set || p.RuntimeMode.Set || p.InteractionMode.Set || p.Workspace.Set || p.AttachmentIDs.Set
}

// UpdateTask applies a validated patch. Outside backlog only title, tags and
// notes may change (Conflict otherwise).
func (s *Store) UpdateTask(ctx context.Context, id int64, p TaskPatch) (Task, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var state TaskState
		var provider, model *string
		err := tx.QueryRow(ctx, `SELECT state, provider_instance_id, model FROM tasks WHERE id = $1 FOR UPDATE`, id).
			Scan(&state, &provider, &model)
		if err != nil {
			return notFound(err)
		}
		if state != StateBacklog && p.restrictedSet() {
			return Conflict("task is %s: only title, tags and notes are editable outside backlog", state)
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
		if p.AfterTaskID.Set {
			if err := setAfter(ctx, tx, id, p.AfterTaskID.Ptr()); err != nil {
				return err
			}
		}
		if p.AttachmentIDs.Set {
			if err := setAttachments(ctx, tx, id, p.AttachmentIDs.V); err != nil {
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
		if len(sets) == 0 && !p.AfterTaskID.Set && !p.AttachmentIDs.Set {
			return nil
		}
		sets = append(sets, "updated_at = now()")
		_, err = tx.Exec(ctx, `UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// DeleteTask removes a task unless a thread is active (Conflict). Its follower
// is spliced onto its predecessor (A→X→B becomes A→B).
func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLinks(ctx, tx); err != nil {
			return err
		}
		var state TaskState
		var after *int64
		if err := tx.QueryRow(ctx, `SELECT state, after_task_id FROM tasks WHERE id = $1 FOR UPDATE`, id).Scan(&state, &after); err != nil {
			return notFound(err)
		}
		if state.Active() {
			return Conflict("task is %s: stop it first", state)
		}
		// Free the predecessor's follower slot before the follower takes it.
		if _, err := tx.Exec(ctx, `UPDATE tasks SET after_task_id = NULL WHERE id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET after_task_id = $2, updated_at = now() WHERE after_task_id = $1`, id, after); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
		return err
	})
}

// DuplicateTask copies a task's config and images into a new backlog task
// (not linked).
func (s *Store) DuplicateTask(ctx context.Context, id int64) (Task, error) {
	src, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	copyImages := func(tx pgx.Tx, dst int64) error { return copyAttachments(ctx, tx, src.ID, dst) }
	return s.createTask(ctx, NewTask{
		ProjectID: src.ProjectID, Title: src.Title, Prompt: src.Prompt,
		ProviderInstanceID: src.ProviderInstanceID, Model: src.Model, ModelOptions: src.ModelOptions,
		RuntimeMode: src.RuntimeMode, InteractionMode: src.InteractionMode, Workspace: src.Workspace,
		Tags: src.Tags, Notes: src.Notes,
	}, copyImages)
}

// TouchTask bumps updated_at (re-arms polling of failed/interrupted tasks).
func (s *Store) TouchTask(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tasks SET updated_at = now() WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Transition is a guarded task state change.
type Transition struct {
	TaskID int64
	From   []TaskState // allowed current states; empty = any
	To     TaskState
	Detail string

	BeginAttempt bool    // launch start: attempt+1 (unless launch_reuse), clear thread/error/finished/settled/launch_reuse
	ThreadID     *string // set t3_thread_id (+ launched_at)
	LastError    *string // set last_error
	// ReuseAttempt marks the attempt's launch outcome unknown (transport
	// failure, no thread): the next BeginAttempt keeps the attempt number
	// and so the clientRequestId.
	ReuseAttempt bool
	// UnlinkWaiting ("Start now"): a backlog task still waiting on an
	// unfinished predecessor (not settled|cancelled) is unlinked from it.
	UnlinkWaiting bool
}

// TransitionResult reports what happened. Changed=false: current state not
// in From (or already To); nothing written.
type TransitionResult struct {
	Changed bool
	From    TaskState
	Attempt int
}

// TransitionTask applies t atomically: state, timestamps (finishedAt on first
// entry into a finished state, settledAt on settled) and a task_events row.
func (s *Store) TransitionTask(ctx context.Context, t Transition) (TransitionResult, error) {
	var res TransitionResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT state, launch_attempt FROM tasks WHERE id = $1 FOR UPDATE`, t.TaskID).
			Scan(&res.From, &res.Attempt)
		if err != nil {
			return notFound(err)
		}
		if (len(t.From) > 0 && !slices.Contains(t.From, res.From)) || (res.From == t.To && !t.BeginAttempt) {
			return nil
		}
		err = tx.QueryRow(ctx, `
UPDATE tasks SET
  state = $2,
  launch_attempt = launch_attempt + CASE WHEN $3::boolean AND NOT launch_reuse THEN 1 ELSE 0 END,
  launch_reuse = CASE WHEN $3::boolean THEN false WHEN $7::boolean THEN true
                      WHEN $4::text IS NOT NULL THEN false ELSE launch_reuse END,
  t3_thread_id = CASE WHEN $3::boolean THEN NULL WHEN $4::text IS NOT NULL THEN $4::text ELSE t3_thread_id END,
  launched_at = CASE WHEN $4::text IS NOT NULL THEN now() ELSE launched_at END,
  last_error = CASE WHEN $3::boolean THEN NULL WHEN $5::text IS NOT NULL THEN $5::text ELSE last_error END,
  finished_at = CASE WHEN $3::boolean THEN NULL
                     WHEN $6::boolean AND finished_at IS NULL THEN now() ELSE finished_at END,
  settled_at = CASE WHEN $3::boolean THEN NULL WHEN $2 = 'settled' THEN now() ELSE settled_at END,
  after_task_id = CASE WHEN $8::boolean AND state = 'backlog' AND NOT EXISTS (
      SELECT 1 FROM tasks p WHERE p.id = tasks.after_task_id AND p.state IN ('settled', 'cancelled'))
    THEN NULL ELSE after_task_id END,
  updated_at = now()
WHERE id = $1 RETURNING launch_attempt`,
			t.TaskID, string(t.To), t.BeginAttempt, t.ThreadID, t.LastError, t.To.Finished(), t.ReuseAttempt, t.UnlinkWaiting,
		).Scan(&res.Attempt)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, $2, $3, $4)`,
			t.TaskID, string(res.From), string(t.To), t.Detail); err != nil {
			return err
		}
		res.Changed = true
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return TransitionResult{}, fmt.Errorf("transition task %d: %w", t.TaskID, err)
	}
	return res, err
}
