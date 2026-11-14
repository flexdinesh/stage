// Package agenttools is the agent interface to stage: operations over the
// REST client, exposed as MCP tools (stdio `stage mcp`, HTTP /mcp) and as
// CLI commands. Inputs and outputs are the tool schemas.
package agenttools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Ops runs agent operations against the REST API.
type Ops struct {
	Client *client.Client
	// Caller is the environment's caller (CLI, stdio MCP); zero for HTTP
	// /mcp, where tool arguments carry it.
	Caller agentctx.Caller
	// LocalFiles allows imagePaths (reads files on this host). False for
	// HTTP /mcp: the server cannot read the agent's files.
	LocalFiles bool
	// PollEvery is wait_task's poll interval (default 3s).
	PollEvery time.Duration
}

// CallerArgs override the environment's caller.
type CallerArgs struct {
	CallerThreadID string `json:"callerThreadId,omitempty" jsonschema:"your T3 session id (env T3CODE_THREAD_ID); stdio/CLI read it from the environment"`
	Cwd            string `json:"cwd,omitempty" jsonschema:"your working directory, used to pick the project; stdio/CLI use the process cwd"`
}

func (o *Ops) caller(a CallerArgs) agentctx.Caller { return o.Caller.Merge(a.CallerThreadID, a.Cwd) }

// IDArgs names one task or schedule.
type IDArgs struct {
	ID int64 `json:"id" jsonschema:"task or schedule id"`
}

// ---- context, projects, models ----

// ContextOut is get_context.
type ContextOut struct {
	ThreadID      string      `json:"threadId"`
	Cwd           string      `json:"cwd"`
	Project       *db.Project `json:"project"`
	ProjectSource string      `json:"projectSource"` // session | cwd | "" (unresolved)
	ProjectError  string      `json:"projectError,omitempty"`
	// CallerTaskID: the stage task that owns the caller's session.
	CallerTaskID *int64 `json:"callerTaskId"`
	APIURL       string `json:"apiUrl"`
}

// Context resolves the caller's session, directory and project.
func (o *Ops) Context(ctx context.Context, a CallerArgs) (ContextOut, error) {
	c := o.caller(a)
	out := ContextOut{ThreadID: c.ThreadID, Cwd: c.Cwd, APIURL: o.Client.BaseURL}
	p, src, err := agentctx.ResolveProject(ctx, o.Client, c, "")
	switch {
	case err == nil:
		out.Project, out.ProjectSource = &p, src
	case errors.Is(err, agentctx.ErrNoProject):
		out.ProjectError = err.Error()
	default:
		return out, err
	}
	if c.ThreadID != "" {
		d, err := o.Client.Thread(ctx, c.ThreadID)
		if err == nil && d.Task != nil {
			id := d.Task.ID
			out.CallerTaskID = &id
		}
	}
	return out, nil
}

// ProjectsOut is list_projects.
type ProjectsOut struct {
	Projects []db.Project `json:"projects"`
}

func (o *Ops) ListProjects(ctx context.Context, _ struct{}) (ProjectsOut, error) {
	ps, err := o.Client.Projects(ctx)
	return ProjectsOut{Projects: ps}, err
}

func (o *Ops) ListModels(ctx context.Context, _ struct{}) (client.Catalog, error) {
	return o.Client.Catalog(ctx)
}

// ---- task config shared by tasks and schedules ----

// ConfigArgs is the task configuration (create_task, create_schedule).
type ConfigArgs struct {
	Project         string            `json:"project,omitempty" jsonschema:"project id or exact title; default: your session's project, else the project containing cwd"`
	Title           string            `json:"title" jsonschema:"short task title"`
	Prompt          string            `json:"prompt" jsonschema:"the full prompt the agent thread starts with; self-contained"`
	Provider        string            `json:"provider,omitempty" jsonschema:"providerInstanceId (list_models); inferred from model when omitted"`
	Model           string            `json:"model,omitempty" jsonschema:"model id (list_models); default: the pinned or last-used model"`
	Effort          string            `json:"effort,omitempty" jsonschema:"shortcut for options.effort, e.g. low|medium|high"`
	Options         map[string]string `json:"options,omitempty" jsonschema:"model options by id (list_models); 'true'/'false' become booleans"`
	RuntimeMode     string            `json:"runtimeMode,omitempty" jsonschema:"approval-required|auto-accept-edits|auto|full-access"`
	InteractionMode string            `json:"interactionMode,omitempty" jsonschema:"default|plan"`
	BaseRef         string            `json:"baseRef,omitempty" jsonschema:"run in a new worktree from this ref (e.g. origin/main); omitted = project root"`
	Branch          string            `json:"branch,omitempty" jsonschema:"worktree branch name (with baseRef); omitted = T3 picks"`
	Tags            []string          `json:"tags,omitempty"`
	Notes           string            `json:"notes,omitempty"`
	ImagePaths      []string          `json:"imagePaths,omitempty" jsonschema:"local image files (png/jpeg/gif/webp, max 8) sent with the prompt; stdio/CLI only"`
}

// resolvedConfig is ConfigArgs resolved against the API.
type resolvedConfig struct {
	project         db.Project
	provider, model *string
	options         []t3.ModelOption
	runtimeMode     *string
	interactionMode *string
	workspace       *db.Workspace
	attachmentIDs   []int64
}

func strPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func modelOptions(effort string, opts map[string]string) []t3.ModelOption {
	m := map[string]string{}
	for k, v := range opts {
		m[k] = v
	}
	if e := strings.TrimSpace(effort); e != "" {
		m["effort"] = e
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := []t3.ModelOption{}
	for _, k := range keys {
		v := strings.TrimSpace(m[k])
		switch v {
		case "true":
			out = append(out, t3.ModelOption{ID: k, Value: t3.BoolValue(true)})
		case "false":
			out = append(out, t3.ModelOption{ID: k, Value: t3.BoolValue(false)})
		default:
			out = append(out, t3.ModelOption{ID: k, Value: t3.StringValue(v)})
		}
	}
	return out
}

// resolveModel fills provider/model: explicit, provider inferred from the
// catalog, else (applyDefaults) the pinned or last-used defaults like the
// web app's new-task form.
func (o *Ops) resolveModel(ctx context.Context, a ConfigArgs, applyDefaults bool, rc *resolvedConfig) error {
	rc.provider, rc.model = strPtr(a.Provider), strPtr(a.Model)
	rc.runtimeMode, rc.interactionMode = strPtr(a.RuntimeMode), strPtr(a.InteractionMode)
	if a.Effort != "" || len(a.Options) > 0 {
		rc.options = modelOptions(a.Effort, a.Options)
	}
	if rc.model != nil && rc.provider == nil {
		cat, err := o.Client.Catalog(ctx)
		if err != nil {
			return err
		}
		for _, p := range cat.Providers {
			for _, m := range p.Models {
				if m.ID == *rc.model {
					id := p.ProviderInstanceID
					rc.provider = &id
				}
			}
		}
		if rc.provider == nil {
			return fmt.Errorf("model %q not in the catalog; see list_models", *rc.model)
		}
	}
	if rc.provider != nil && rc.model == nil {
		return errors.New("provider needs a model (list_models)")
	}
	if rc.model != nil || !applyDefaults {
		return nil
	}
	prefs, err := o.Client.Preferences(ctx)
	if err != nil {
		return err
	}
	d := prefs.Pinned
	if d == nil {
		d = prefs.LastUsed
	}
	if d == nil {
		return nil
	}
	rc.provider, rc.model = d.ProviderInstanceID, d.Model
	if rc.options == nil {
		rc.options = d.ModelOptions
	}
	if rc.runtimeMode == nil && d.RuntimeMode != "" {
		m := d.RuntimeMode
		rc.runtimeMode = &m
	}
	if rc.interactionMode == nil && d.InteractionMode != "" {
		m := d.InteractionMode
		rc.interactionMode = &m
	}
	return nil
}

func workspace(baseRef, branch string) *db.Workspace {
	if strings.TrimSpace(baseRef) == "" {
		return nil
	}
	return &db.Workspace{Type: "worktree", BaseRef: strings.TrimSpace(baseRef), Branch: strPtr(branch)}
}

// uploadImages stages local images and returns their attachment ids.
func (o *Ops) uploadImages(ctx context.Context, paths []string) ([]int64, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if !o.LocalFiles {
		return nil, errors.New("imagePaths needs local file access: use the stdio MCP server (`stage mcp`) or the CLI")
	}
	ids := make([]int64, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		att, err := o.Client.Upload(ctx, filepath.Base(p), data)
		if err != nil {
			return nil, fmt.Errorf("upload %s: %w", p, err)
		}
		ids = append(ids, att.ID)
	}
	return ids, nil
}

func (o *Ops) resolveConfig(ctx context.Context, a ConfigArgs, c agentctx.Caller) (resolvedConfig, error) {
	var rc resolvedConfig
	p, _, err := agentctx.ResolveProject(ctx, o.Client, c, a.Project)
	if err != nil {
		return rc, err
	}
	rc.project = p
	if err := o.resolveModel(ctx, a, true, &rc); err != nil {
		return rc, err
	}
	rc.workspace = workspace(a.BaseRef, a.Branch)
	rc.attachmentIDs, err = o.uploadImages(ctx, a.ImagePaths)
	return rc, err
}

// ---- tasks ----

// TaskRow is a compact task for lists.
type TaskRow struct {
	ID            int64        `json:"id"`
	Title         string       `json:"title"`
	State         db.TaskState `json:"state"`
	ProjectID     int64        `json:"projectId"`
	Project       string       `json:"project"`
	T3ThreadID    *string      `json:"t3ThreadId,omitempty"`
	AfterTaskID   *int64       `json:"afterTaskId,omitempty"`
	AfterThreadID *string      `json:"afterThreadId,omitempty"`
	StartAt       *time.Time   `json:"startAt,omitempty"`
	ScheduleID    *int64       `json:"scheduleId,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

func taskRow(t client.Task) TaskRow {
	return TaskRow{
		ID: t.ID, Title: t.Title, State: t.State, ProjectID: t.ProjectID, Project: t.ProjectTitle,
		T3ThreadID: t.T3ThreadID, AfterTaskID: t.AfterTaskID, AfterThreadID: t.AfterThreadID,
		StartAt: t.StartAt, ScheduleID: t.ScheduleID, Tags: t.Tags, UpdatedAt: t.UpdatedAt,
	}
}

// ListTasksArgs is list_tasks.
type ListTasksArgs struct {
	Project string   `json:"project,omitempty" jsonschema:"project id or title; omitted = all projects"`
	States  []string `json:"states,omitempty" jsonschema:"backlog|launching|running|needs_input|done|settled|failed|interrupted|cancelled"`
	Tag     string   `json:"tag,omitempty"`
	Query   string   `json:"query,omitempty" jsonschema:"substring of title or prompt"`
	Limit   int      `json:"limit,omitempty" jsonschema:"max rows (default 50)"`
}

// TasksOut is list_tasks.
type TasksOut struct {
	Tasks []TaskRow `json:"tasks"`
	Total int       `json:"total"`
}

func (o *Ops) ListTasks(ctx context.Context, a ListTasksArgs) (TasksOut, error) {
	var q client.TaskQuery
	if a.Project != "" {
		p, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, a.Project)
		if err != nil {
			return TasksOut{}, err
		}
		q.ProjectID = &p.ID
	}
	q.States, q.Tag, q.Q = a.States, a.Tag, a.Query
	ts, err := o.Client.Tasks(ctx, q)
	if err != nil {
		return TasksOut{}, err
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	out := TasksOut{Tasks: []TaskRow{}, Total: len(ts)}
	for i, t := range ts {
		if i >= limit {
			break
		}
		out.Tasks = append(out.Tasks, taskRow(t))
	}
	return out, nil
}

func (o *Ops) GetTask(ctx context.Context, a IDArgs) (client.TaskDetail, error) {
	return o.Client.Task(ctx, a.ID)
}

// WaitArgs are the run-order and start options of create_task.
type WaitArgs struct {
	AfterTaskID   int64  `json:"afterTaskId,omitempty" jsonschema:"run after this task settles"`
	AfterThreadID string `json:"afterThreadId,omitempty" jsonschema:"run after this T3 session settles"`
	AfterMe       bool   `json:"afterMe,omitempty" jsonschema:"run after your own session settles (needs your session id)"`
	StartLocal    string `json:"startLocal,omitempty" jsonschema:"one-shot start, wall time YYYY-MM-DDTHH:mm in startTz"`
	StartTz       string `json:"startTz,omitempty" jsonschema:"IANA zone for startLocal, e.g. Asia/Kolkata"`
}

// afterThread resolves afterThreadId / afterMe.
func afterThread(w WaitArgs, c agentctx.Caller) (*string, error) {
	if w.AfterMe {
		if w.AfterThreadID != "" || w.AfterTaskID != 0 {
			return nil, errors.New("afterMe excludes afterTaskId and afterThreadId")
		}
		if c.ThreadID == "" {
			return nil, errors.New("afterMe needs your session id: run inside a T3 session (T3CODE_THREAD_ID) or pass callerThreadId")
		}
		return c.ThreadPtr(), nil
	}
	return strPtr(w.AfterThreadID), nil
}

func idPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// CreateTaskArgs is create_task.
type CreateTaskArgs struct {
	ConfigArgs
	WaitArgs
	CallerArgs
	Start bool `json:"start,omitempty" jsonschema:"start now (ignored when linked to run after something); default: save to backlog"`
}

func (o *Ops) CreateTask(ctx context.Context, a CreateTaskArgs) (client.Task, error) {
	c := o.caller(a.CallerArgs)
	after, err := afterThread(a.WaitArgs, c)
	if err != nil {
		return client.Task{}, err
	}
	rc, err := o.resolveConfig(ctx, a.ConfigArgs, c)
	if err != nil {
		return client.Task{}, err
	}
	return o.Client.CreateTask(ctx, client.CreateTask{
		ProjectID: rc.project.ID, Title: a.Title, Prompt: a.Prompt,
		ProviderInstanceID: rc.provider, Model: rc.model, ModelOptions: rc.options,
		RuntimeMode: rc.runtimeMode, InteractionMode: rc.interactionMode, Workspace: rc.workspace,
		Tags: a.Tags, Notes: strPtr(a.Notes), AttachmentIDs: rc.attachmentIDs,
		AfterTaskID: idPtr(a.AfterTaskID), AfterThreadID: after, OriginThreadID: c.ThreadPtr(),
		StartLocal: strPtr(a.StartLocal), StartTz: strPtr(a.StartTz), Start: a.Start,
	})
}

// UpdateArgs are editable config fields (nil = unchanged). Outside backlog
// only title, tags and notes change.
type UpdateArgs struct {
	ID              int64             `json:"id"`
	Project         *string           `json:"project,omitempty" jsonschema:"move to project (id or title)"`
	Title           *string           `json:"title,omitempty"`
	Prompt          *string           `json:"prompt,omitempty"`
	Provider        *string           `json:"provider,omitempty"`
	Model           *string           `json:"model,omitempty" jsonschema:"model id; empty string = T3 default"`
	Effort          *string           `json:"effort,omitempty"`
	Options         map[string]string `json:"options,omitempty" jsonschema:"replaces model options"`
	RuntimeMode     *string           `json:"runtimeMode,omitempty"`
	InteractionMode *string           `json:"interactionMode,omitempty"`
	BaseRef         *string           `json:"baseRef,omitempty" jsonschema:"worktree base ref; empty string = project root"`
	Branch          *string           `json:"branch,omitempty"`
	Tags            []string          `json:"tags,omitempty" jsonschema:"replaces tags"`
	Notes           *string           `json:"notes,omitempty"`
}

func (o *Ops) configPatch(ctx context.Context, a UpdateArgs) (client.Patch, error) {
	p := client.Patch{}
	if a.Project != nil {
		pr, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, *a.Project)
		if err != nil {
			return nil, err
		}
		p["projectId"] = pr.ID
	}
	if a.Title != nil {
		p["title"] = *a.Title
	}
	if a.Prompt != nil {
		p["prompt"] = *a.Prompt
	}
	if a.Model != nil {
		if strings.TrimSpace(*a.Model) == "" {
			p["providerInstanceId"], p["model"] = nil, nil
		} else {
			var rc resolvedConfig
			provider := ""
			if a.Provider != nil {
				provider = *a.Provider
			}
			if err := o.resolveModel(ctx, ConfigArgs{Provider: provider, Model: *a.Model}, false, &rc); err != nil {
				return nil, err
			}
			p["providerInstanceId"], p["model"] = rc.provider, rc.model
		}
	}
	if a.Effort != nil || a.Options != nil {
		effort := ""
		if a.Effort != nil {
			effort = *a.Effort
		}
		p["modelOptions"] = modelOptions(effort, a.Options)
	}
	if a.RuntimeMode != nil {
		p["runtimeMode"] = *a.RuntimeMode
	}
	if a.InteractionMode != nil {
		p["interactionMode"] = *a.InteractionMode
	}
	if a.BaseRef != nil {
		branch := ""
		if a.Branch != nil {
			branch = *a.Branch
		}
		if w := workspace(*a.BaseRef, branch); w != nil {
			p["workspace"] = w
		} else {
			p["workspace"] = db.Workspace{Type: "root"}
		}
	}
	if a.Tags != nil {
		p["tags"] = a.Tags
	}
	if a.Notes != nil {
		p["notes"] = *a.Notes
	}
	return p, nil
}

// UpdateTaskArgs is update_task.
type UpdateTaskArgs struct {
	UpdateArgs
	StartLocal *string `json:"startLocal,omitempty" jsonschema:"set a one-shot start (with startTz); empty string clears it"`
	StartTz    *string `json:"startTz,omitempty"`
}

func (o *Ops) UpdateTask(ctx context.Context, a UpdateTaskArgs) (client.Task, error) {
	p, err := o.configPatch(ctx, a.UpdateArgs)
	if err != nil {
		return client.Task{}, err
	}
	if a.StartLocal != nil {
		if strings.TrimSpace(*a.StartLocal) == "" {
			p["startLocal"], p["startTz"] = nil, nil
		} else {
			p["startLocal"], p["startTz"] = *a.StartLocal, a.StartTz
		}
	}
	return o.Client.UpdateTask(ctx, a.ID, p)
}

// DeletedOut confirms a delete.
type DeletedOut struct {
	Deleted bool  `json:"deleted"`
	ID      int64 `json:"id"`
}

func (o *Ops) DeleteTask(ctx context.Context, a IDArgs) (DeletedOut, error) {
	err := o.Client.DeleteTask(ctx, a.ID)
	return DeletedOut{Deleted: err == nil, ID: a.ID}, err
}

func (o *Ops) StartTask(ctx context.Context, a IDArgs) (client.Task, error) {
	return o.Client.StartTask(ctx, a.ID)
}

func (o *Ops) StopTask(ctx context.Context, a IDArgs) (client.Task, error) {
	return o.Client.StopTask(ctx, a.ID)
}

func (o *Ops) DuplicateTask(ctx context.Context, a IDArgs) (client.Task, error) {
	return o.Client.DuplicateTask(ctx, a.ID)
}

// FollowupArgs is followup_task.
type FollowupArgs struct {
	ID         int64    `json:"id"`
	Message    string   `json:"message" jsonschema:"message to send to the task's thread"`
	Mode       string   `json:"mode,omitempty" jsonschema:"auto|queue|steer|restart (not with images)"`
	ImagePaths []string `json:"imagePaths,omitempty" jsonschema:"local images to send; stdio/CLI only"`
}

func (o *Ops) FollowupTask(ctx context.Context, a FollowupArgs) (client.Task, error) {
	ids, err := o.uploadImages(ctx, a.ImagePaths)
	if err != nil {
		return client.Task{}, err
	}
	return o.Client.FollowupTask(ctx, a.ID, client.Followup{Message: a.Message, Mode: strPtr(a.Mode), AttachmentIDs: ids})
}

// LinkTaskArgs is link_task: exactly one of afterTaskId, afterThreadId,
// afterMe.
type LinkTaskArgs struct {
	ID            int64  `json:"id" jsonschema:"backlog task to link"`
	AfterTaskID   int64  `json:"afterTaskId,omitempty" jsonschema:"run after this task settles"`
	AfterThreadID string `json:"afterThreadId,omitempty" jsonschema:"run after this T3 session settles"`
	AfterMe       bool   `json:"afterMe,omitempty" jsonschema:"run after your own session settles"`
	CallerArgs
}

func (o *Ops) LinkTask(ctx context.Context, a LinkTaskArgs) (client.Task, error) {
	c := o.caller(a.CallerArgs)
	after, err := afterThread(WaitArgs{AfterTaskID: a.AfterTaskID, AfterThreadID: a.AfterThreadID, AfterMe: a.AfterMe}, c)
	if err != nil {
		return client.Task{}, err
	}
	p := client.Patch{}
	switch {
	case a.AfterTaskID != 0 && after != nil:
		return client.Task{}, errors.New("pass one of afterTaskId, afterThreadId, afterMe")
	case a.AfterTaskID != 0:
		p["afterTaskId"], p["afterThreadId"] = a.AfterTaskID, nil
	case after != nil:
		p["afterThreadId"], p["afterTaskId"] = *after, nil
	default:
		return client.Task{}, errors.New("pass one of afterTaskId, afterThreadId, afterMe")
	}
	cur, err := o.Client.Task(ctx, a.ID)
	if err != nil {
		return client.Task{}, err
	}
	if cur.StartAt != nil {
		p["startLocal"], p["startTz"] = nil, nil // a link replaces a start time
	}
	return o.Client.UpdateTask(ctx, a.ID, p)
}

func (o *Ops) UnlinkTask(ctx context.Context, a IDArgs) (client.Task, error) {
	return o.Client.UpdateTask(ctx, a.ID, client.Patch{"afterTaskId": nil, "afterThreadId": nil})
}

// WaitTaskArgs is wait_task.
type WaitTaskArgs struct {
	ID             int64    `json:"id"`
	Until          []string `json:"until,omitempty" jsonschema:"states to wait for (default: needs_input done settled failed interrupted cancelled)"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"give up after this many seconds (default 600, max 3600)"`
}

// WaitOut is wait_task.
type WaitOut struct {
	Task     client.Task `json:"task"`
	Reached  bool        `json:"reached"`
	TimedOut bool        `json:"timedOut"`
}

var defaultUntil = []string{"needs_input", "done", "settled", "failed", "interrupted", "cancelled"}

func (o *Ops) WaitTask(ctx context.Context, a WaitTaskArgs) (WaitOut, error) {
	until := a.Until
	if len(until) == 0 {
		until = defaultUntil
	}
	timeout := time.Duration(a.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	timeout = min(timeout, time.Hour)
	every := o.PollEvery
	if every <= 0 {
		every = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		d, err := o.Client.Task(ctx, a.ID)
		if err != nil {
			return WaitOut{}, err
		}
		if slices.Contains(until, string(d.State)) {
			return WaitOut{Task: d.Task, Reached: true}, nil
		}
		if !time.Now().Before(deadline) {
			return WaitOut{Task: d.Task, TimedOut: true}, nil
		}
		select {
		case <-ctx.Done():
			return WaitOut{Task: d.Task, TimedOut: true}, ctx.Err()
		case <-time.After(min(every, time.Until(deadline))):
		}
	}
}

// ---- schedules ----

// ProjectArgs optionally names a project.
type ProjectArgs struct {
	Project string `json:"project,omitempty" jsonschema:"project id or title; omitted = all projects"`
}

// SchedulesOut is list_schedules.
type SchedulesOut struct {
	Schedules []db.Schedule `json:"schedules"`
}

func (o *Ops) ListSchedules(ctx context.Context, a ProjectArgs) (SchedulesOut, error) {
	var pid *int64
	if a.Project != "" {
		p, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, a.Project)
		if err != nil {
			return SchedulesOut{}, err
		}
		pid = &p.ID
	}
	ss, err := o.Client.Schedules(ctx, pid)
	return SchedulesOut{Schedules: ss}, err
}

func (o *Ops) GetSchedule(ctx context.Context, a IDArgs) (client.ScheduleDetail, error) {
	return o.Client.Schedule(ctx, a.ID)
}

// CreateScheduleArgs is create_schedule.
type CreateScheduleArgs struct {
	ConfigArgs
	CallerArgs
	Cron   string `json:"cron" jsonschema:"5-field cron (minute hour day-of-month month day-of-week) or @hourly|@daily|@weekly|@monthly"`
	Tz     string `json:"tz" jsonschema:"IANA zone the cron is evaluated in, e.g. Asia/Kolkata"`
	Paused bool   `json:"paused,omitempty"`
}

func (o *Ops) CreateSchedule(ctx context.Context, a CreateScheduleArgs) (db.Schedule, error) {
	c := o.caller(a.CallerArgs)
	rc, err := o.resolveConfig(ctx, a.ConfigArgs, c)
	if err != nil {
		return db.Schedule{}, err
	}
	return o.Client.CreateSchedule(ctx, client.CreateSchedule{
		ProjectID: rc.project.ID, Title: a.Title, Prompt: a.Prompt,
		ProviderInstanceID: rc.provider, Model: rc.model, ModelOptions: rc.options,
		RuntimeMode: rc.runtimeMode, InteractionMode: rc.interactionMode, Workspace: rc.workspace,
		Tags: a.Tags, Notes: strPtr(a.Notes), AttachmentIDs: rc.attachmentIDs, OriginThreadID: c.ThreadPtr(),
		Cron: a.Cron, Tz: a.Tz, Paused: a.Paused,
	})
}

// UpdateScheduleArgs is update_schedule.
type UpdateScheduleArgs struct {
	UpdateArgs
	Cron   *string `json:"cron,omitempty"`
	Tz     *string `json:"tz,omitempty"`
	Paused *bool   `json:"paused,omitempty" jsonschema:"true pauses, false resumes"`
}

func (o *Ops) UpdateSchedule(ctx context.Context, a UpdateScheduleArgs) (db.Schedule, error) {
	p, err := o.configPatch(ctx, a.UpdateArgs)
	if err != nil {
		return db.Schedule{}, err
	}
	if a.Cron != nil {
		p["cron"] = *a.Cron
	}
	if a.Tz != nil {
		p["tz"] = *a.Tz
	}
	if a.Paused != nil {
		p["paused"] = *a.Paused
	}
	return o.Client.UpdateSchedule(ctx, a.ID, p)
}

func (o *Ops) DeleteSchedule(ctx context.Context, a IDArgs) (DeletedOut, error) {
	err := o.Client.DeleteSchedule(ctx, a.ID)
	return DeletedOut{Deleted: err == nil, ID: a.ID}, err
}

func (o *Ops) RunSchedule(ctx context.Context, a IDArgs) (client.Task, error) {
	return o.Client.RunSchedule(ctx, a.ID)
}

// PreviewArgs is preview_schedule.
type PreviewArgs struct {
	Cron string `json:"cron"`
	Tz   string `json:"tz"`
}

// PreviewOut is preview_schedule.
type PreviewOut struct {
	Next []time.Time `json:"next"`
}

func (o *Ops) PreviewSchedule(ctx context.Context, a PreviewArgs) (PreviewOut, error) {
	next, err := o.Client.PreviewSchedule(ctx, a.Cron, a.Tz)
	return PreviewOut{Next: next}, err
}

// ---- sessions ----

// SessionRow is a compact session for lists.
type SessionRow struct {
	T3ThreadID     string       `json:"t3ThreadId"`
	Title          string       `json:"title"`
	State          db.TaskState `json:"state"`
	Project        *string      `json:"project,omitempty"`
	TaskID         *int64       `json:"taskId,omitempty"`
	ParentThreadID *string      `json:"parentThreadId,omitempty"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	T3WebURL       *string      `json:"t3WebUrl,omitempty"`
}

// ListSessionsArgs is list_sessions.
type ListSessionsArgs struct {
	Project string `json:"project,omitempty" jsonschema:"project id or title; omitted = all"`
	Active  bool   `json:"active,omitempty" jsonschema:"only running or needs-input sessions"`
	Query   string `json:"query,omitempty" jsonschema:"title substring"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max rows (default 20, max 200)"`
}

// SessionsOut is list_sessions.
type SessionsOut struct {
	Sessions []SessionRow `json:"sessions"`
	Total    int          `json:"total"`
}

func (o *Ops) ListSessions(ctx context.Context, a ListSessionsArgs) (SessionsOut, error) {
	q := client.ThreadQuery{Active: a.Active, Q: a.Query, Limit: a.Limit, ExcludeSubagents: true}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	q.Limit = min(q.Limit, 200)
	if a.Project != "" {
		p, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, a.Project)
		if err != nil {
			return SessionsOut{}, err
		}
		q.ProjectID = &p.ID
	}
	page, err := o.Client.Threads(ctx, q)
	if err != nil {
		return SessionsOut{}, err
	}
	out := SessionsOut{Sessions: []SessionRow{}, Total: page.Total}
	for _, t := range page.Threads {
		out.Sessions = append(out.Sessions, SessionRow{
			T3ThreadID: t.T3ThreadID, Title: t.Title, State: t.State, Project: t.ProjectTitle, TaskID: t.TaskID,
			ParentThreadID: t.ParentThreadID, UpdatedAt: t.UpdatedAt, T3WebURL: t.T3WebURL,
		})
	}
	return out, nil
}

// SessionArgs names one session.
type SessionArgs struct {
	ID string `json:"id" jsonschema:"T3 thread id"`
}

func (o *Ops) GetSession(ctx context.Context, a SessionArgs) (client.ThreadDetail, error) {
	return o.Client.Thread(ctx, a.ID)
}
