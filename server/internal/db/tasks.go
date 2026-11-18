package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const taskSelect = `
SELECT t.id, t.project_id, p.title, COALESCE((SELECT external_project_id FROM task_launch_destinations d WHERE d.task_id=t.id AND d.attempt=t.launch_attempt),(SELECT external_project_id FROM project_bindings b WHERE b.project_id=t.project_id AND b.connection_id=t.connection_id AND b.active=1),''), t.title, t.prompt, t.provider_instance_id,
  t.model, t.model_options, t.runtime_mode, t.interaction_mode, t.workspace, t.tags,
  t.state, t.t3_thread_id, t.tree_id, tr.title, tr.start_mode, t.start_mode, ` + effectiveModeSQL + `, ` + readinessSQL + `,
  COALESCE((SELECT json_group_array(n.need_id ORDER BY n.need_id) FROM task_needs n WHERE n.task_id = t.id), '[]'),
  COALESCE((SELECT json_group_array(n.task_id ORDER BY n.task_id) FROM task_needs n WHERE n.need_id = t.id), '[]'),
  t.adopted, t.ready_at, t.launch_attempt, t.last_error,
  t.launched_at, t.finished_at, t.settled_at, th.run_started_at, th.run_ended_at, th.last_word_preview, th.last_word_at,
  COALESCE(th.pull_requests, '[]'), t.created_at, t.updated_at, th.link,
  t.start_at, t.start_tz, t.schedule_id, sc.title,
  COALESCE((SELECT external_id FROM session_identities WHERE id=t.origin_session_id AND connection_id=1),t.origin_thread_id), oth.title, t.handoff,
  COALESCE(min(th.pending_question_count, th.pending_request_count), 0),
  COALESCE((SELECT json_group_array(json_object('id', a.id, 'name', a.name, 'mimeType', a.mime_type,
    'sizeBytes', a.size_bytes) ORDER BY a.id) FROM task_attachments a WHERE a.task_id = t.id), '[]'),
  (SELECT max(e.at) FROM task_events e WHERE e.task_id = t.id AND e.to_state = t.state), th.updated_at,
  t.archived_at, tr.status, ` + unstartedSQL + `,
  t.connection_id,t.session_id,t.origin_session_id,
 (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=t.session_id),
 (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=t.origin_session_id),t.acceptance_id,t.acceptance_revision,(SELECT accepted_at FROM task_acceptances a WHERE a.id=t.acceptance_id),t.execution_config
FROM tasks t
JOIN projects p ON p.id = t.project_id
LEFT JOIN task_trees tr ON tr.id = t.tree_id
LEFT JOIN threads th ON th.session_id = t.session_id
LEFT JOIN schedules sc ON sc.id = t.schedule_id
LEFT JOIN threads oth ON oth.session_id = t.origin_session_id`

func scanTask(row rowScanner) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.ProjectTitle, &t.T3ProjectID, &t.Title, &t.Prompt,
		&t.ProviderInstanceID, &t.Model, &t.ModelOptions, &t.RuntimeMode, &t.InteractionMode,
		&t.Workspace, &t.Tags, &t.State, &t.T3ThreadID, &t.TreeID, &t.TreeTitle, &t.TreeStartMode,
		&t.StartMode, &t.EffectiveStartMode, &t.Readiness, &t.Needs, &t.Dependents, &t.Adopted, &t.ReadyAt,
		&t.LaunchAttempt, &t.LastError, &t.LaunchedAt, &t.FinishedAt, &t.SettledAt, &t.RunStartedAt, &t.RunEndedAt,
		&t.LastWordPreview, &t.LastWordAt, &t.PullRequests, &t.CreatedAt,
		&t.UpdatedAt, &t.ThreadLink, &t.StartAt, &t.StartTz, &t.ScheduleID, &t.ScheduleTitle,
		&t.OriginThreadID, &t.OriginThreadTitle, &t.Handoff, &t.PendingQuestionCount, &t.Attachments,
		&t.StateSince, &t.ThreadUpdatedAt, &t.ArchivedAt, &t.TreeStatus, &t.Deletable,
		&t.ConnectionID, &t.SessionIdentityID, &t.OriginSessionIdentityID, &t.Session, &t.OriginSession, &t.AcceptanceID, &t.AcceptanceRevision, &t.CompletedAt, &t.ExecutionConfig)
	if err != nil {
		return Task{}, notFound(err)
	}
	if t.Session != nil {
		t.T3ThreadID = &t.Session.ExternalSessionID
	}
	t.ModelOptions = nonNilOptions(t.ModelOptions)
	t.Tags = nonNilStrings(t.Tags)
	t.PullRequests = nonNilPRs(t.PullRequests)
	t.Needs = nonNilIDs(t.Needs)
	t.Dependents = nonNilIDs(t.Dependents)
	if t.Workspace.Type == "" {
		t.Workspace.Type = "root"
	}
	return t, nil
}

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func collectTasks(rows *rowSet, err error) ([]Task, error) {
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
	OriginThreadID string
	ProjectID      *int64
	States         []string
	Tag            string
	Q              string
	From, To       *time.Time
	SettledFrom    *time.Time // settled_at >= SettledFrom
	// Closed: "" or "false" (open only: not archived, tree open or none),
	// "true" (archived, or in a done or archived tree) or "all".
	Closed string
}

// closedSQL: task t is archived or its tree tr is not open.
const closedSQL = `(t.archived_at IS NOT NULL OR COALESCE(tr.status, 'open') <> 'open')`

func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	var w where
	if f.OriginThreadID != "" {
		w.add("t.origin_session_id = (SELECT id FROM session_identities WHERE connection_id=1 AND external_id=" + w.arg(f.OriginThreadID) + ")")
	}
	if f.ProjectID != nil {
		w.add("t.project_id = " + w.arg(*f.ProjectID))
	}
	if len(f.States) > 0 {
		w.add("t.state IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.States)) + "))")
	}
	if f.Tag != "" {
		w.add("EXISTS (SELECT 1 FROM json_each(t.tags) WHERE value = " + w.arg(f.Tag) + ")")
	}
	if f.Q != "" {
		p := w.arg(likePattern(f.Q))
		w.add("(" + likeSQL("t.title", p) + " OR " + likeSQL("t.prompt", p) + ")")
	}
	if f.From != nil {
		w.add("t.created_at >= " + w.arg(*f.From))
	}
	if f.To != nil {
		w.add("t.created_at < " + w.arg(*f.To))
	}
	if f.SettledFrom != nil {
		w.add("(SELECT accepted_at FROM task_acceptances a WHERE a.id=t.acceptance_id) >= " + w.arg(*f.SettledFrom))
	}
	switch f.Closed {
	case "all":
	case "true":
		w.add(closedSQL)
	default:
		w.add("NOT " + closedSQL)
	}
	rows, err := s.pool.Query(ctx, taskSelect+w.sql()+` ORDER BY t.updated_at DESC, t.id DESC`, w.args...)
	return collectTasks(rows, err)
}

// PollableTasks: tasks whose thread may still change state. Active tasks
// always; done tasks updated in the last hour always, older ones only
// when includeStale (the poller passes it at most once a minute);
// failed/interrupted/cancelled when touched in the last 10 minutes (e.g. a
// follow-up restarted the thread) or the mirror shows the thread active.
// backlog and settled are final for polling.
func (s *Store) PollableTasks(ctx context.Context, includeStale bool) ([]Task, error) {
	// failed|interrupted|cancelled are not final: T3 keeps the thread and it
	// can run again (a cancelled turn, a follow-up sent in T3). Polled for 10
	// minutes after the last change, then whenever the thread mirror (full
	// sync) shows it active again.
	rows, err := s.pool.Query(ctx, taskSelect+`
WHERE (t.session_id IS NOT NULL OR t.state='launching') AND (
  t.state IN ('launching', 'running', 'needs_input')
  OR (t.state IN ('done','settled') AND ($1 OR t.updated_at > `+nowPlusSQL("-1 hour")+`))
  OR (t.state IN ('failed', 'interrupted', 'cancelled') AND (
    t.updated_at > `+nowPlusSQL("-10 minutes")+`
    OR COALESCE((SELECT json_extract(observation,'$.active') FROM execution_observations WHERE session_id=t.session_id),0)=1)))
ORDER BY t.id`, includeStale)
	return collectTasks(rows, err)
}

func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]TaskEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, task_id, from_state, to_state, detail, at FROM task_events WHERE task_id = $1 ORDER BY at, id`, taskID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanTaskEvent)
}

func scanTaskEvent(r rowScanner) (TaskEvent, error) {
	var e TaskEvent
	err := r.Scan(&e.ID, &e.TaskID, &e.FromState, &e.ToState, &e.Detail, &e.At)
	return e, err
}

// NewTask is a validated create input.
type NewTask struct {
	ExecutionConfig    *execution.Config
	ConnectionID       int64
	OriginSession      *SessionRef
	ProjectID          int64
	Title              string
	Prompt             string
	ProviderInstanceID *string
	Model              *string
	ModelOptions       []execution.ModelOption
	RuntimeMode        string
	InteractionMode    string
	Workspace          Workspace
	Tags               []string
	InitialNote        string
	AttachmentIDs      []int64 // staged images to attach (setAttachments)
	StartAt            *time.Time
	StartTz            *string // set with StartAt (IANA zone)
	ScheduleID         *int64  // spawning schedule
	OriginThreadID     *string // T3 thread that created the task (agent interface)
	Handoff            *bool   // append the needs' handoff at launch; nil = default (on)
	// TreeID: join this tree (docs/api.md "Tree rules"); nil with Needs joins
	// the needs' tree (or a new one).
	TreeID *int64
	// Needs: tasks this one needs (setNeeds rules).
	Needs []int64
	// NeedsThreadID ("afterMe"): adopt this T3 session (or take its owning
	// task) and need it, before Needs.
	NeedsThreadID *string
	StartMode     *StartMode // nil = inherit the tree's
}

// errStartAndNeeds: a task waits on a start time or on needs, not both.
var errStartAndNeeds = Invalid("a task can have a start time or needs, not both")

// CreateTask inserts a backlog task (+ creation event) and places it in its
// tree with its needs (the whole insert fails on a rule violation).
func (s *Store) CreateTask(ctx context.Context, in NewTask) (Task, error) {
	return s.createTask(ctx, in, nil)
}

// createTask is CreateTask with extra run inside the insert transaction.
func (s *Store) createTask(ctx context.Context, in NewTask, extra func(tx *Tx, id int64) error) (Task, error) {
	var id int64
	err := s.inTx(ctx, func(tx *Tx) error {
		var err error
		id, err = insertTask(ctx, tx, in, extra)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// insertTask is createTask's insert, inside tx: insert, adopt
// NeedsThreadID, resolve and join the tree, set needs.
func insertTask(ctx context.Context, tx *Tx, in NewTask, extra func(tx *Tx, id int64) error) (int64, error) {
	if in.StartAt != nil && (len(in.Needs) > 0 || in.NeedsThreadID != nil) {
		return 0, errStartAndNeeds
	}
	if err := projectExists(ctx, tx, in.ProjectID); err != nil {
		return 0, err
	}
	var originID *int64
	if in.ConnectionID == 0 {
		in.ConnectionID = T3ConnectionID
	}
	if in.OriginSession != nil {
		if in.OriginThreadID != nil {
			return 0, Invalid("use originSession or the legacy T3 originThreadId, not both")
		}
		value, err := sessionIdentity(ctx, tx, *in.OriginSession)
		if err != nil {
			return 0, err
		}
		originID = &value
	}
	var id int64
	err := tx.QueryRow(ctx, `
INSERT INTO tasks (project_id, title, prompt, provider_instance_id, model, model_options,
  runtime_mode, interaction_mode, workspace, tags, start_at, start_tz, schedule_id, origin_thread_id, handoff, start_mode,connection_id,origin_session_id,execution_config)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,COALESCE($15, true),$16,$17,$18,$19) RETURNING id`,
		in.ProjectID, in.Title, in.Prompt, in.ProviderInstanceID, in.Model, nonNilOptions(in.ModelOptions),
		in.RuntimeMode, in.InteractionMode, in.Workspace, nonNilStrings(in.Tags),
		in.StartAt, in.StartTz, in.ScheduleID, in.OriginThreadID, in.Handoff, in.StartMode, in.ConnectionID, originID, in.ExecutionConfig,
	).Scan(&id)
	if err != nil {
		return 0, err
	}

	needs := slices.Clone(in.Needs)
	if in.NeedsThreadID != nil {
		owner, _, err := adoptThread(ctx, tx, AdoptInput{ThreadID: *in.NeedsThreadID})
		if err != nil {
			return 0, err
		}
		needs = append([]int64{owner}, needs...)
	}
	if in.TreeID != nil || len(needs) > 0 {
		tree, err := resolveTree(ctx, tx, in.TreeID, needs)
		if err != nil {
			return 0, err
		}
		if err := joinTree(ctx, tx, id, tree); err != nil {
			return 0, err
		}
		if err := setNeeds(ctx, tx, id, needs); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, NULL, 'backlog', 'created')`, id); err != nil {
		return 0, err
	}
	if err := checkBaseTask(ctx, tx, id); err != nil {
		return 0, err
	}
	if len(in.AttachmentIDs) > 0 {
		if err := setAttachments(ctx, tx, id, in.AttachmentIDs); err != nil {
			return 0, err
		}
	}
	if in.InitialNote != "" {
		if _, err := appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: in.InitialNote, RequestID: "initial-note", SourceThreadID: in.OriginThreadID, SourceSession: in.OriginSession}); err != nil {
			return 0, err
		}
	}
	if extra != nil {
		if err := extra(tx, id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func projectExists(ctx context.Context, tx *Tx, id int64) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1`, id).Scan(&one)
	if err != nil {
		return fmt.Errorf("project: %w", notFound(err))
	}
	return nil
}

// TaskPatch is a partial task update (JSON body of PATCH /api/tasks/:id).
// Nullable fields: providerInstanceId, model, startMode (Null clears).
type TaskPatch struct {
	ExecutionConfig    Optional[execution.Config]        `json:"executionConfig"`
	ProjectID          Optional[int64]                   `json:"projectId"`
	Title              Optional[string]                  `json:"title"`
	Prompt             Optional[string]                  `json:"prompt"`
	ProviderInstanceID Optional[string]                  `json:"providerInstanceId"`
	Model              Optional[string]                  `json:"model"`
	ModelOptions       Optional[[]execution.ModelOption] `json:"modelOptions"`
	RuntimeMode        Optional[string]                  `json:"runtimeMode"`
	InteractionMode    Optional[string]                  `json:"interactionMode"`
	Workspace          Optional[Workspace]               `json:"workspace"`
	Tags               Optional[[]string]                `json:"tags"`
	AttachmentIDs      Optional[[]int64]                 `json:"attachmentIds"` // the exact image set; null = none
	Handoff            Optional[bool]                    `json:"handoff"`       // append the needs' handoff at launch
	StartMode          Optional[StartMode]               `json:"startMode"`     // null = inherit the tree's
	// One-shot start: the API converts startLocal (wall time in startTz) into
	// StartAt; StartAt and StartTz are set (or null) together.
	StartLocal Optional[string]    `json:"startLocal"`
	StartTz    Optional[string]    `json:"startTz"`
	StartAt    Optional[time.Time] `json:"-"`
}

// restrictedSet reports whether a field editable only in backlog is present.
func (p TaskPatch) restrictedSet() bool {
	return p.ExecutionConfig.Set || p.ProjectID.Set || p.Prompt.Set || p.ProviderInstanceID.Set || p.Model.Set ||
		p.ModelOptions.Set || p.RuntimeMode.Set || p.InteractionMode.Set || p.Workspace.Set || p.AttachmentIDs.Set ||
		p.StartAt.Set || p.Handoff.Set || p.StartMode.Set
}

// UpdateTask applies a validated patch. Outside backlog only title and tags
// may change (Conflict otherwise).
func (s *Store) UpdateTask(ctx context.Context, id int64, p TaskPatch) (Task, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		var state TaskState
		var provider, model *string
		var adopted bool
		var nNeeds int
		err := tx.QueryRow(ctx, `SELECT state, provider_instance_id, model, adopted,
  (SELECT count(*) FROM task_needs WHERE task_id = tasks.id) FROM tasks WHERE id = $1`, id).
			Scan(&state, &provider, &model, &adopted, &nNeeds)
		if err != nil {
			return notFound(err)
		}
		if state != StateBacklog && p.restrictedSet() {
			return Conflict("task is %s: only title and tags are editable outside backlog", state)
		}
		if adopted && p.Prompt.Set {
			return Conflict("an adopted session's prompt is its T3 conversation")
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
		if p.StartAt.Set && !p.StartAt.Null && nNeeds > 0 {
			return errStartAndNeeds
		}
		if p.StartAt.Set {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET start_at = $2, start_tz = $3 WHERE id = $1`, id, p.StartAt.Ptr(), p.StartTz.Ptr()); err != nil {
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
		if p.ExecutionConfig.Set {
			set("execution_config", p.ExecutionConfig.Ptr())
		} else if p.ProviderInstanceID.Set || p.Model.Set || p.ModelOptions.Set || p.RuntimeMode.Set || p.InteractionMode.Set {
			set("execution_config", nil)
		}
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
		if p.Handoff.Set {
			set("handoff", p.Handoff.V)
		}
		if p.StartMode.Set {
			set("start_mode", p.StartMode.Ptr())
		}
		if len(sets) == 0 && !p.AttachmentIDs.Set && !p.StartAt.Set {
			return nil
		}
		sets = append(sets, "updated_at = "+nowSQL)
		if _, err := tx.Exec(ctx, `UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...); err != nil {
			return err
		}
		if p.Workspace.Set {
			return checkBaseTask(ctx, tx, id)
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// checkBaseTask: workspace.baseTaskId, when set, names one of the task's
// needs (Invalid otherwise).
func checkBaseTask(ctx context.Context, tx *Tx, id int64) error {
	var base *int64
	var isNeed bool
	err := tx.QueryRow(ctx, `
SELECT json_extract(workspace, '$.baseTaskId'),
  EXISTS (SELECT 1 FROM task_needs n WHERE n.task_id = tasks.id AND n.need_id = json_extract(tasks.workspace, '$.baseTaskId'))
FROM tasks WHERE id = $1`, id).Scan(&base, &isNeed)
	if err != nil {
		return notFound(err)
	}
	if base != nil && !isNeed {
		return Invalid("workspace.baseTaskId: task %d is not a need of this task", *base)
	}
	return nil
}

// DeleteTask removes a task without notes or execution history;
// recorded work can only be archived (Conflict). It is spliced out of its
// tree first (its dependents take over its needs).
func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *Tx) error {
		var state TaskState
		var unstarted bool
		if err := tx.QueryRow(ctx, `SELECT t.state, `+unstartedSQL+` FROM tasks t WHERE t.id = $1`, id).Scan(&state, &unstarted); err != nil {
			return notFound(err)
		}
		if state.Active() {
			return Conflict("task is %s: stop it first", state)
		}
		if !unstarted {
			return Conflict("task %d has recorded work: archive it instead", id)
		}
		if err := spliceOut(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
		return err
	})
}

// ArchiveTask archives (archived) or unarchives task id. Archiving needs a
// standalone task (a tree member is closed with its tree) that is not
// launching, running or needing input (Conflict).
func (s *Store) ArchiveTask(ctx context.Context, id int64, archived bool) (Task, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		var tree *int64
		if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, id).Scan(&tree); err != nil {
			return notFound(err)
		}
		if !archived {
			_, err := tx.Exec(ctx, `UPDATE tasks SET archived_at = NULL, updated_at = `+nowSQL+` WHERE id = $1 AND archived_at IS NOT NULL`, id)
			return err
		}
		if tree != nil {
			return Conflict("task %d is in tree %d: archive the tree, or remove it from the tree first", id, *tree)
		}
		if err := noActiveTask(ctx, tx, `t.id = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE tasks SET archived_at = `+nowSQL+`, updated_at = `+nowSQL+` WHERE id = $1 AND archived_at IS NULL`, id)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// DuplicateTask copies a task's config and images into a new backlog task
// (no tree, no needs). An adopted session cannot be duplicated (Conflict).
func (s *Store) DuplicateTask(ctx context.Context, id int64) (Task, error) {
	src, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if src.Adopted {
		return Task{}, Conflict("task %d tracks an adopted session: it has no prompt to duplicate", id)
	}
	ws := src.Workspace
	ws.BaseTaskID = nil
	copyImages := func(tx *Tx, dst int64) error {
		if err := copyAttachments(ctx, tx, src.ID, dst); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO journal_entries (kind, task_id, source_task_id, event_type, text) VALUES ('event', $1, $2, 'task_duplicated', 'Duplicated from task #' || $2)`, dst, src.ID)
		return err
	}
	return s.createTask(ctx, NewTask{
		ProjectID: src.ProjectID, ConnectionID: src.ConnectionID, ExecutionConfig: src.ExecutionConfig, Title: src.Title, Prompt: src.Prompt,
		ProviderInstanceID: src.ProviderInstanceID, Model: src.Model, ModelOptions: src.ModelOptions,
		RuntimeMode: src.RuntimeMode, InteractionMode: src.InteractionMode, Workspace: ws,
		Tags: src.Tags,
	}, copyImages)
}

// TouchTask bumps updated_at (re-arms polling of failed/interrupted tasks).
func (s *Store) TouchTask(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tasks SET updated_at = `+nowSQL+` WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Transition is a guarded task state change.
type Transition struct {
	RequireAuto bool // release runner: recheck policy while claiming the launch
	TaskID      int64
	From        []TaskState // allowed current states; empty = any
	To          TaskState
	Detail      string

	BeginAttempt bool    // launch start: attempt+1 and a new launch_request_id (unless launch_reuse), clear thread/error/finished/settled/launch_reuse/start/ready_at
	ThreadID     *string // set t3_thread_id (+ launched_at)
	LastError    *string // set last_error
	// ReuseAttempt marks the attempt's launch outcome unknown (transport
	// failure or restart mid-launch, no thread): the next BeginAttempt keeps
	// the attempt number and the clientRequestId.
	ReuseAttempt bool
}

// TransitionResult reports what happened. Changed=false: current state not
// in From (or already To); nothing written.
type TransitionResult struct {
	Changed bool
	From    TaskState
	Attempt int
	// RequestID is the attempt's clientRequestId (launch_request_id); ""
	// before the first launch.
	RequestID string
}

// TransitionTask applies t atomically: state, timestamps (finishedAt on first
// entry into a finished state, settledAt on settled) and a task_events row.
func (s *Store) TransitionTask(ctx context.Context, t Transition) (TransitionResult, error) {
	var res TransitionResult
	err := s.inTx(ctx, func(tx *Tx) error {
		err := tx.QueryRow(ctx, `SELECT state, launch_attempt, COALESCE(launch_request_id, '') FROM tasks WHERE id = $1`, t.TaskID).
			Scan(&res.From, &res.Attempt, &res.RequestID)
		if err != nil {
			return notFound(err)
		}
		if (len(t.From) > 0 && !slices.Contains(t.From, res.From)) || (res.From == t.To && !t.BeginAttempt) {
			return nil
		}
		if t.BeginAttempt {
			if err := checkLaunchClaim(ctx, tx, t, res.Attempt); err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `
UPDATE tasks SET
  state = $2,
  launch_attempt = launch_attempt + CASE WHEN $3 AND NOT launch_reuse THEN 1 ELSE 0 END,
  launch_request_id = CASE WHEN $3 AND NOT (launch_reuse AND launch_request_id IS NOT NULL)
                           THEN 'task-' || id || '-' || $8 ELSE launch_request_id END,
  launch_reuse = CASE WHEN $3 THEN false WHEN $7 THEN true
                      WHEN $4 IS NOT NULL THEN false ELSE launch_reuse END,
  t3_thread_id = CASE WHEN $3 THEN NULL WHEN $4 IS NOT NULL THEN $4 ELSE t3_thread_id END,
  session_id = CASE WHEN $3 THEN NULL ELSE session_id END,
  scheduled_hold = CASE WHEN $3 THEN 0 ELSE scheduled_hold END,
  launched_at = CASE WHEN $4 IS NOT NULL THEN `+nowSQL+` ELSE launched_at END,
  last_error = CASE WHEN $3 THEN NULL WHEN $5 IS NOT NULL THEN $5 ELSE last_error END,
  finished_at = CASE WHEN $3 THEN NULL
                     WHEN $6 AND finished_at IS NULL THEN `+nowSQL+` ELSE finished_at END,
  settled_at = CASE WHEN $3 THEN NULL WHEN $2 = 'settled' THEN `+nowSQL+` ELSE settled_at END,
  start_at = CASE WHEN $3 THEN NULL ELSE start_at END,
  start_tz = CASE WHEN $3 THEN NULL ELSE start_tz END,
  ready_at = CASE WHEN $3 THEN NULL ELSE ready_at END,
  updated_at = `+nowSQL+`
WHERE id = $1 RETURNING launch_attempt, COALESCE(launch_request_id, '')`,
			t.TaskID, string(t.To), t.BeginAttempt, t.ThreadID, t.LastError, t.To.Finished(), t.ReuseAttempt, newUUID(),
		).Scan(&res.Attempt, &res.RequestID)
		if err != nil {
			return err
		}
		if t.BeginAttempt {
			if _, err := tx.Exec(ctx, `INSERT INTO execution_attempts(task_id,attempt,phase) VALUES($1,$2,'prepared') ON CONFLICT DO NOTHING`, t.TaskID, res.Attempt); err != nil {
				return err
			}
			if err := pinDestination(ctx, tx, t.TaskID, res.Attempt); err != nil {
				return err
			}
			claim, err := tx.Exec(ctx, `INSERT INTO task_launch_claims(task_id,attempt) VALUES($1,$2) ON CONFLICT DO NOTHING`, t.TaskID, res.Attempt)
			if err != nil {
				return err
			}
			if claim.RowsAffected() > 0 {
				if _, err := tx.Exec(ctx, `INSERT INTO task_launch_inputs(task_id,attempt,source_task_id,acceptance_id)
    SELECT $1,$2,d.id,d.acceptance_id FROM task_needs n JOIN tasks d ON d.id=n.need_id
    WHERE n.task_id=$1 AND d.acceptance_id IS NOT NULL
    ON CONFLICT DO NOTHING`, t.TaskID, res.Attempt); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, $2, $3, $4)`,
			t.TaskID, string(res.From), string(t.To), t.Detail); err != nil {
			return err
		}
		if t.To.Active() {
			if err := reopenOnActivity(ctx, tx, t.TaskID, t.To); err != nil {
				return err
			}
		}
		res.Changed = true
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return TransitionResult{}, fmt.Errorf("transition task %d: %w", t.TaskID, err)
	}
	return res, err
}

// reopenOnActivity: task id became active (to), so an archived task is
// unarchived and a done or archived tree reopened, each with a task event
// (docs/api.md "Done, archive and delete").
func reopenOnActivity(ctx context.Context, tx *Tx, id int64, to TaskState) error {
	label := strings.ReplaceAll(string(to), "_", " ")
	tag, err := tx.Exec(ctx, `UPDATE tasks SET archived_at = NULL WHERE id = $1 AND archived_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, $2, $2, $3)`,
			id, string(to), "unarchived: became "+label); err != nil {
			return err
		}
	}
	return nil
}
