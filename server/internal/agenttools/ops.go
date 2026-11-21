// Package agenttools is the agent interface to stage: operations over the
// REST client, exposed as MCP tools (stdio `stage mcp`, HTTP /mcp) and as
// CLI commands. Inputs and outputs are the tool schemas.
package agenttools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// Ops runs agent operations against the REST API.
type Ops struct {
	Client *client.Client
	// Caller is process-local only for the CLI or dedicated T3 stdio.
	Caller agentctx.Caller
	// LocalFiles allows imagePaths (reads files on this host). False for
	// HTTP /mcp: the server cannot read the agent's files.
	LocalFiles bool
	// PollEvery is wait_task's poll interval (default 3s).
	PollEvery time.Duration
	// CLI is the installed CLI's name the guidance names (default "stage").
	CLI string
}

func (o *Ops) cli() string {
	if o.CLI == "" {
		return "stage"
	}
	return o.CLI
}

// CallerArgs override the environment's caller.
type CallerArgs struct {
	CallerSession *db.SessionRef `json:"callerSession,omitempty" jsonschema:"your session: the Stage connectionId of your harness and your externalSessionId there (your launch instructions say where to find them)"`
	Cwd           string         `json:"cwd,omitempty" jsonschema:"your working directory for context only; never used to infer a project"`
}

// claimKey holds the per-call transport claim (harness request metadata or
// the Stage-Caller-Session header).
type claimKey struct{}

func (o *Ops) caller(ctx context.Context, a CallerArgs) (agentctx.Caller, error) {
	claim, _ := ctx.Value(claimKey{}).(*db.SessionRef)
	return o.Caller.Resolve(a.CallerSession, a.Cwd, claim)
}

// IDArgs names one task or schedule.
type IDArgs struct {
	ID int64 `json:"id" jsonschema:"task or schedule id"`
}

// ---- context, projects, models ----

// ContextOut is get_context.
type ContextOut struct {
	Session       *db.SessionRef `json:"session"`
	Cwd           string         `json:"cwd"`
	Project       *db.Project    `json:"project"`
	ProjectSource string         `json:"projectSource"` // session | "" (unresolved)
	ProjectError  string         `json:"projectError,omitempty"`
	// CallerTaskID: the stage task that owns the caller's session.
	CallerTaskID *int64  `json:"callerTaskId"`
	TreeID       *int64  `json:"treeId"`
	TreeTitle    *string `json:"treeTitle"`
	APIURL       string  `json:"apiUrl"`
}

// Context resolves the caller's session, directory and project.
func (o *Ops) Context(ctx context.Context, a CallerArgs) (ContextOut, error) {
	c, err := o.caller(ctx, a)
	if err != nil {
		return ContextOut{}, err
	}
	out := ContextOut{Session: c.Session, Cwd: c.Cwd, APIURL: o.Client.BaseURL}
	if c.Session == nil {
		out.ProjectError = "No caller session. Pass callerSession or specify a project on creation."
		return out, nil
	}
	d, err := o.Client.SessionContext(ctx, *c.Session)
	if err != nil {
		if client.IsStatus(err, 404) || client.IsStatus(err, 409) || client.IsStatus(err, 502) || client.IsStatus(err, 503) {
			out.ProjectError = err.Error() + "; pass project explicitly or check the session binding"
			return out, nil
		}
		return out, err
	}
	out.Project = d.Project
	if d.Project != nil {
		out.ProjectSource = "session"
	} else {
		out.ProjectError = "No bound project for this session. Choose a project explicitly."
	}
	if d.Task != nil {
		out.CallerTaskID = &d.Task.ID
		out.TreeID = d.Task.TreeID
		out.TreeTitle = d.Task.TreeTitle
	}
	return out, nil
}

// ProjectsOut is list_projects.
type ProjectsOut struct {
	Connections []db.HarnessConnection `json:"connections"`
	Projects    []db.Project           `json:"projects"`
}

func (o *Ops) ListProjects(ctx context.Context, _ struct{}) (ProjectsOut, error) {
	ps, err := o.Client.Projects(ctx)
	if err != nil {
		return ProjectsOut{}, err
	}
	cs, err := o.Client.Connections(ctx)
	return ProjectsOut{Projects: ps, Connections: cs}, err
}

type ModelsArgs struct {
	ProjectID    int64 `json:"projectId,omitempty" jsonschema:"Stage project for workspace-specific models and modes"`
	ConnectionID int64 `json:"connectionId,omitempty"`
}
type ModelsOut struct {
	client.Catalog
	Capabilities *execution.Capabilities `json:"capabilities,omitempty"`
}

func (o *Ops) ListModels(ctx context.Context, a ModelsArgs) (ModelsOut, error) {
	if a.ConnectionID != 0 {
		c, err := o.Client.ProjectCapabilities(ctx, a.ConnectionID, a.ProjectID)
		return ModelsOut{Capabilities: &c}, err
	}
	c, err := o.Client.Catalog(ctx)
	return ModelsOut{Catalog: c}, err
}

// ---- task config shared by tasks and schedules ----

// ConfigArgs is the task configuration (create_task, create_schedule).
type ConfigArgs struct {
	WorkspaceType   string            `json:"workspaceType,omitempty" jsonschema:"root or worktree; OpenCode worktrees use the service default base"`
	ConnectionID    int64             `json:"connectionId,omitempty" jsonschema:"execution destination from list_projects"`
	ExecutionConfig *ConfigInput      `json:"executionConfig,omitempty" jsonschema:"use the selected connection model, options, permission and mode from list_models"`
	Project         string            `json:"project,omitempty" jsonschema:"project id or unambiguous exact title; default: your session's explicitly bound project"`
	Title           string            `json:"title" jsonschema:"short task title"`
	Prompt          string            `json:"prompt" jsonschema:"the full prompt the agent thread starts with; self-contained"`
	Provider        string            `json:"provider,omitempty" jsonschema:"providerInstanceId (list_models); inferred from model when omitted"`
	Model           string            `json:"model,omitempty" jsonschema:"model id (list_models); default: the pinned or last-used model"`
	Effort          string            `json:"effort,omitempty" jsonschema:"shortcut for options.effort, e.g. low|medium|high"`
	Options         map[string]string `json:"options,omitempty" jsonschema:"model options by id (list_models); 'true'/'false' become booleans"`
	RuntimeMode     string            `json:"runtimeMode,omitempty" jsonschema:"approval-required|auto-accept-edits|auto|full-access"`
	InteractionMode string            `json:"interactionMode,omitempty" jsonschema:"default|plan"`
	BaseRef         string            `json:"baseRef,omitempty" jsonschema:"run in a new worktree from this ref (e.g. origin/main); omitted = project root"`
	Branch          string            `json:"branch,omitempty" jsonschema:"worktree branch name with baseRef; omit to let the selected harness choose"`
	Base            string            `json:"base,omitempty" jsonschema:"ref (default: from baseRef) | predecessor: the worktree starts from a need's branch (baseTaskId, else the sole need); unavailable predecessors block launch; choose base=ref explicitly to use baseRef (tasks only)"`
	ImagePaths      []string          `json:"imagePaths,omitempty" jsonschema:"local image files (png/jpeg/gif/webp, max 8) sent with the prompt; stdio/CLI only"`
}

// resolvedConfig is ConfigArgs resolved against the API.
type resolvedConfig struct {
	connectionID    int64
	config          *execution.Config
	project         db.Project
	provider, model *string
	options         []execution.ModelOption
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

func modelOptions(effort string, opts map[string]string) []execution.ModelOption {
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
	out := []execution.ModelOption{}
	for _, k := range keys {
		v := strings.TrimSpace(m[k])
		switch v {
		case "true":
			out = append(out, execution.ModelOption{ID: k, Value: execution.BoolValue(true)})
		case "false":
			out = append(out, execution.ModelOption{ID: k, Value: execution.BoolValue(false)})
		default:
			out = append(out, execution.ModelOption{ID: k, Value: execution.StringValue(v)})
		}
	}
	return out
}

// resolveModel fills provider/model: explicit, provider inferred from the
// catalog, else (applyDefaults) the pinned or last-used model and options like
// the web app's new-task form. Runtime and interaction mode never come from
// defaults: unset stays the harness default.
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
	return nil
}

func workspace(baseRef, branch, base string) *db.Workspace {
	if strings.TrimSpace(baseRef) == "" {
		return nil
	}
	return &db.Workspace{Type: "worktree", BaseRef: strings.TrimSpace(baseRef), Branch: strPtr(branch), Base: strings.TrimSpace(base)}
}

// errBaseNeedsWorktree: base without a worktree to apply it to.
var errBaseNeedsWorktree = errors.New("base needs a worktree: pass baseRef (with base predecessor it is the fallback)")

// baseOnly patches task id's current workspace with base and/or baseTask
// (given without baseRef; nil = unchanged).
func (o *Ops) baseOnly(ctx context.Context, id int64, base *string, baseTaskID *int64, p client.Patch) error {
	cur, err := o.Client.Task(ctx, id)
	if err != nil {
		return err
	}
	w := cur.Workspace
	if w.Type != "worktree" {
		return errBaseNeedsWorktree
	}
	if base != nil {
		w.Base = strings.TrimSpace(*base)
	}
	if baseTaskID != nil {
		w.BaseTaskID = baseTask(*baseTaskID)
	}
	if w.Base != db.BasePredecessor {
		w.BaseTaskID = nil
	}
	p["workspace"] = w
	return nil
}

// baseTask is a baseTaskId argument: 0 = none (the sole need).
func baseTask(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

// parsedStartMode is a startMode argument: mode nil = omitted (or inherit).
type parsedStartMode struct {
	mode    *db.StartMode
	inherit bool
}

// startMode parses auto | manual | inherit ("" = omitted).
func startMode(s string) (parsedStartMode, error) {
	switch v := strings.TrimSpace(s); v {
	case "":
		return parsedStartMode{}, nil
	case "inherit":
		return parsedStartMode{inherit: true}, nil
	default:
		m := db.StartMode(v)
		if !m.Valid() {
			return parsedStartMode{}, fmt.Errorf("invalid startMode %q (want auto|manual|inherit)", v)
		}
		return parsedStartMode{mode: &m}, nil
	}
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
	rc.connectionID, err = o.destination(ctx, a, p, c)
	if err != nil {
		return rc, err
	}
	h, err := o.harnesses(ctx)
	if err != nil {
		return rc, err
	}
	toT3 := h.isT3(rc.connectionID)
	if a.ExecutionConfig != nil || !toT3 {
		if a.legacyConfig() {
			return rc, errors.New("use executionConfig for the selected harness; legacy T3 configuration cannot be combined with it")
		}
		config := execution.Config{}
		if a.ExecutionConfig != nil {
			config, err = a.ExecutionConfig.config()
			if err != nil {
				return rc, err
			}
		}
		rc.config = &config
		if config.Model != "" || config.Mode != "" || config.Permission != "" || len(config.Options) > 0 || a.BaseRef != "" || a.WorkspaceType == "worktree" || len(a.ImagePaths) > 0 {
			f, err := o.Client.ProjectCapabilities(ctx, rc.connectionID, p.ID)
			if err != nil {
				return rc, err
			}
			if err := f.ValidateConfig(config); err != nil {
				return rc, err
			}
			if a.BaseRef != "" || a.WorkspaceType == "worktree" {
				if err := execution.RequireChoice("workspace", "worktree", f.Workspaces); err != nil {
					return rc, err
				}
			}
			if a.Base == db.BasePredecessor {
				if err := execution.RequireAvailable("predecessor base", f.Operations["predecessorBase"]); err != nil {
					return rc, err
				}
			}
			if len(a.ImagePaths) > 0 {
				if err := execution.RequireAvailable("images", f.Operations["images"]); err != nil {
					return rc, err
				}
			}
		}
	} else if err := o.resolveModel(ctx, a, true, &rc); err != nil {
		return rc, err
	}
	if rc.workspace = workspace(a.BaseRef, a.Branch, a.Base); rc.workspace == nil && strings.TrimSpace(a.Base) != "" {
		return rc, errBaseNeedsWorktree
	}
	switch a.WorkspaceType {
	case "root":
		if rc.workspace != nil || a.Branch != "" {
			return rc, errors.New("a root workspace cannot select a branch or base ref")
		}
	case "worktree":
		if rc.workspace == nil {
			rc.workspace = &db.Workspace{Type: "worktree", Branch: strPtr(a.Branch)}
		}
	case "":
	default:
		return rc, errors.New("choose workspaceType root or worktree")
	}
	if !toT3 && (a.Branch != "" || a.Base != "" || a.BaseRef != "") {
		return rc, errors.New("OpenCode selects the worktree base and branch; choose workspaceType worktree without a branch or base")
	}
	rc.attachmentIDs, err = o.uploadImages(ctx, a.ImagePaths)
	return rc, err
}

// ---- tasks ----

// TaskRow is a compact task for lists.
type TaskRow struct {
	ConnectionID int64        `json:"connectionId"`
	ID           int64        `json:"id"`
	Title        string       `json:"title"`
	State        db.TaskState `json:"state"`
	ProjectID    int64        `json:"projectId"`
	Project      string       `json:"project"`
	T3ThreadID   *string      `json:"externalSessionId,omitempty"`
	TreeID       *int64       `json:"treeId,omitempty"`
	Needs        []int64      `json:"needs,omitempty"`
	// Readiness: blocked | ready for a backlog tree member.
	Readiness  *db.Readiness  `json:"readiness,omitempty"`
	TreeStatus *db.TreeStatus `json:"treeStatus,omitempty"` // its tree's status (open | done | archived)
	Archived   bool           `json:"archived,omitempty"`
	Adopted    bool           `json:"adopted,omitempty"` // tracks an existing session
	StartAt    *time.Time     `json:"startAt,omitempty"`
	ScheduleID *int64         `json:"scheduleId,omitempty"`
	// LastWordPreview is the thread's latest assistant reply, one plain line
	// (get_task has the excerpt in thread.lastWord).
	LastWordPreview *string   `json:"lastWordPreview,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func taskRow(t client.Task) TaskRow {
	return TaskRow{
		ConnectionID: t.ConnectionID,
		ID:           t.ID, Title: t.Title, State: t.State, ProjectID: t.ProjectID, Project: t.ProjectTitle,
		T3ThreadID: t.T3ThreadID, TreeID: t.TreeID, Needs: t.Needs, Readiness: t.Readiness, TreeStatus: t.TreeStatus,
		Archived: t.ArchivedAt != nil, Adopted: t.Adopted,
		StartAt: t.StartAt, ScheduleID: t.ScheduleID, LastWordPreview: t.LastWordPreview,
		UpdatedAt: t.UpdatedAt,
	}
}

// ListTasksArgs is list_tasks.
type ListTasksArgs struct {
	OriginSession *db.SessionRef `json:"originSession,omitempty"`
	Project       string         `json:"project,omitempty" jsonschema:"project id or title; omitted = all projects"`
	States        []string       `json:"states,omitempty" jsonschema:"backlog|launching|running|needs_input|done|settled|failed|interrupted|cancelled"`
	Query         string         `json:"query,omitempty" jsonschema:"substring of title or prompt"`
	Closed        string         `json:"closed,omitempty" jsonschema:"false (default: open tasks only) | true (archived, or in a done or archived tree) | all"`
	Limit         int            `json:"limit,omitempty" jsonschema:"max rows (default 50)"`
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
	q.States, q.Q = a.States, a.Query
	q.OriginSession = a.OriginSession
	switch a.Closed {
	case "", "false":
	case "true", "all":
		q.Closed = a.Closed
	default:
		return TasksOut{}, fmt.Errorf("invalid closed %q (want false|true|all)", a.Closed)
	}
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

// WaitArgs are the tree and start options of create_task.
type WaitArgs struct {
	TreeID     *int64  `json:"treeId,omitempty" jsonschema:"join this tree as a parallel root unless needs are given (default: the needs' tree, else a new tree when there are needs)"`
	Needs      []int64 `json:"needs,omitempty" jsonschema:"task ids this task runs after (same tree): it starts once all of them settle; tasks sharing a need run in parallel"`
	AfterMe    bool    `json:"afterMe,omitempty" jsonschema:"also need your own session: it is adopted as a task in the tree and this task waits for its user completion in Stage (needs your session id)"`
	StartMode  string  `json:"startMode,omitempty" jsonschema:"inherit (default; omit unless the user asks). In an auto tree the task launches once its needs are completed, at once if they already are: expected, not a reason for manual. auto or manual (reads ready) only for a user-requested per-task exception, preserved when the tree mode changes"`
	BaseTaskID *int64  `json:"baseTaskId,omitempty" jsonschema:"with base=predecessor, the need whose branch the worktree starts from (default: the sole need)"`
	StartLocal string  `json:"startLocal,omitempty" jsonschema:"one-shot start, wall time YYYY-MM-DDTHH:mm in startTz"`
	StartTz    string  `json:"startTz,omitempty" jsonschema:"IANA zone for startLocal, e.g. America/New_York"`
	Handoff    *bool   `json:"handoff,omitempty" jsonschema:"append each need's final reply, PRs, branch and thread link to the prompt at launch (default true)"`
}

// needsSession resolves afterMe to the caller's session id.
func needsSession(w WaitArgs, c agentctx.Caller) (*db.SessionRef, error) {
	if !w.AfterMe {
		return nil, nil
	}
	if c.Session == nil {
		return nil, errors.New("afterMe needs your session id: pass callerSession or use per-call harness context")
	}
	return c.Session, nil
}

// CreateTaskArgs is create_task.
type CreateTaskArgs struct {
	InitialNote string `json:"initialNote,omitempty"`
	ConfigArgs
	WaitArgs
	CallerArgs
	Start bool `json:"start,omitempty" jsonschema:"start now (not with needs or afterMe); default: save to backlog"`
}

func (o *Ops) CreateTask(ctx context.Context, a CreateTaskArgs) (client.Task, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return client.Task{}, err
	}
	session, err := needsSession(a.WaitArgs, c)
	if err != nil {
		return client.Task{}, err
	}
	mode, err := startMode(a.StartMode)
	if err != nil {
		return client.Task{}, err
	}
	rc, err := o.resolveConfig(ctx, a.ConfigArgs, c)
	if err != nil {
		return client.Task{}, err
	}
	if a.BaseTaskID != nil {
		if rc.workspace == nil {
			return client.Task{}, errBaseNeedsWorktree
		}
		rc.workspace.BaseTaskID = a.BaseTaskID
	}
	return o.Client.CreateTask(ctx, client.CreateTask{
		ConnectionID: rc.connectionID, ExecutionConfig: rc.config, ProjectID: rc.project.ID, Title: a.Title, Prompt: a.Prompt,
		ProviderInstanceID: rc.provider, Model: rc.model, ModelOptions: rc.options,
		RuntimeMode: rc.runtimeMode, InteractionMode: rc.interactionMode, Workspace: rc.workspace,
		InitialNote: strPtr(a.InitialNote), AttachmentIDs: rc.attachmentIDs,
		TreeID: a.TreeID, Needs: a.Needs, NeedsSession: session, StartMode: mode.mode, OriginSession: c.Session,
		StartLocal: strPtr(a.StartLocal), StartTz: strPtr(a.StartTz), Start: a.Start, Handoff: a.Handoff,
	})
}

// UpdateArgs are editable config fields (nil = unchanged). Outside backlog
// only the title changes.
type UpdateArgs struct {
	WorkspaceType   *string           `json:"workspaceType,omitempty"`
	ConnectionID    int64             `json:"connectionId,omitempty"`
	ExecutionConfig *ConfigInput      `json:"executionConfig,omitempty"`
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
	Base            *string           `json:"base,omitempty" jsonschema:"ref | predecessor (worktree from a need's branch: baseTaskId, else the sole need; unavailable predecessors block launch; choose base=ref explicitly to use baseRef); without baseRef keeps the current worktree"`
}

// workspacePatch is the workspace baseRef/branch/base set (ok false without
// baseRef).
func (a UpdateArgs) workspacePatch() (db.Workspace, bool) {
	if a.BaseRef == nil {
		if a.WorkspaceType != nil {
			return db.Workspace{Type: *a.WorkspaceType, Branch: a.Branch}, true
		}
		return db.Workspace{}, false
	}
	branch, base := "", ""
	if a.Branch != nil {
		branch = *a.Branch
	}
	if a.Base != nil {
		base = *a.Base
	}
	if w := workspace(*a.BaseRef, branch, base); w != nil {
		return *w, true
	}
	return db.Workspace{Type: "root", Base: base}, true
}

func (o *Ops) configPatch(ctx context.Context, a UpdateArgs, schedule bool) (client.Patch, error) {
	p := client.Patch{}
	legacy := a.Model != nil || a.Provider != nil || a.Effort != nil || a.Options != nil || a.RuntimeMode != nil || a.InteractionMode != nil
	if a.WorkspaceType != nil && (*a.WorkspaceType != "root" && *a.WorkspaceType != "worktree" || *a.WorkspaceType == "root" && a.BaseRef != nil && *a.BaseRef != "") {
		return nil, errors.New("choose root without a base or choose worktree")
	}
	if a.ConnectionID != 0 {
		p["connectionId"] = a.ConnectionID
	}
	if legacy || a.ExecutionConfig != nil {
		var id, projectID int64
		if schedule {
			v, err := o.Client.Schedule(ctx, a.ID)
			if err != nil {
				return nil, err
			}
			id, projectID = v.ConnectionID, v.ProjectID
		} else {
			v, err := o.Client.Task(ctx, a.ID)
			if err != nil {
				return nil, err
			}
			id, projectID = v.ConnectionID, v.ProjectID
		}
		if a.ConnectionID != 0 {
			id = a.ConnectionID
		}
		if a.Project != nil {
			pr, _, e := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, *a.Project)
			if e != nil {
				return nil, e
			}
			projectID = pr.ID
		}
		h, err := o.harnesses(ctx)
		if err != nil {
			return nil, err
		}
		if legacy && (a.ExecutionConfig != nil || !h.isT3(id)) {
			return nil, errors.New("use executionConfig for this connection without legacy T3 options")
		}
		if a.ExecutionConfig != nil {
			cfg, err := a.ExecutionConfig.config()
			if err != nil {
				return nil, err
			}
			if cfg.Model != "" || cfg.Permission != "" || cfg.Mode != "" || len(cfg.Options) > 0 {
				f, err := o.Client.ProjectCapabilities(ctx, id, projectID)
				if err != nil {
					return nil, err
				}
				if err = f.ValidateConfig(cfg); err != nil {
					return nil, err
				}
			}
			p["executionConfig"] = cfg
		}
	}
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
	if w, ok := a.workspacePatch(); ok {
		p["workspace"] = w
	}
	return p, nil
}

// UpdateTaskArgs is update_task.
type UpdateTaskArgs struct {
	UpdateArgs
	StartLocal *string `json:"startLocal,omitempty" jsonschema:"set a one-shot start (with startTz); empty string clears it"`
	StartTz    *string `json:"startTz,omitempty"`
	Handoff    *bool   `json:"handoff,omitempty" jsonschema:"append each need's final reply, PRs, branch and thread link to the prompt at launch; backlog only"`
	StartMode  *string `json:"startMode,omitempty" jsonschema:"auto | manual | inherit (the tree's); backlog only"`
	BaseTaskID *int64  `json:"baseTaskId,omitempty" jsonschema:"with base=predecessor, the need whose branch the worktree starts from; 0 = the sole need"`
}

func (o *Ops) UpdateTask(ctx context.Context, a UpdateTaskArgs) (client.Task, error) {
	p, err := o.configPatch(ctx, a.UpdateArgs, false)
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
	if a.Handoff != nil {
		p["handoff"] = *a.Handoff
	}
	if a.StartMode != nil {
		mode, err := startMode(*a.StartMode)
		if err != nil {
			return client.Task{}, err
		}
		if mode.inherit || mode.mode == nil {
			p["startMode"] = nil
		} else {
			p["startMode"] = *mode.mode
		}
	}
	if (a.Base != nil || a.BaseTaskID != nil) && a.BaseRef == nil {
		if err := o.baseOnly(ctx, a.ID, a.Base, a.BaseTaskID, p); err != nil {
			return client.Task{}, err
		}
	} else if w, ok := a.workspacePatch(); ok && a.BaseTaskID != nil {
		if w.Type != "worktree" {
			return client.Task{}, errBaseNeedsWorktree
		}
		w.BaseTaskID = baseTask(*a.BaseTaskID)
		p["workspace"] = w
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

// ArchiveTaskArgs is archive_task.
type ArchiveTaskArgs struct {
	ID       int64 `json:"id" jsonschema:"task id (standalone; a tree member is closed with its tree)"`
	Archived *bool `json:"archived,omitempty" jsonschema:"true (default) archives; false unarchives"`
}

func (o *Ops) ArchiveTask(ctx context.Context, a ArchiveTaskArgs) (client.Task, error) {
	return o.Client.ArchiveTask(ctx, a.ID, a.Archived == nil || *a.Archived)
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
	Notes string `json:"notes,omitempty"`
	ConfigArgs
	CallerArgs
	Cron   string `json:"cron" jsonschema:"5-field cron (minute hour day-of-month month day-of-week) or @hourly|@daily|@weekly|@monthly"`
	Tz     string `json:"tz" jsonschema:"IANA zone the cron is evaluated in, e.g. America/New_York"`
	Paused bool   `json:"paused,omitempty"`
}

func (o *Ops) CreateSchedule(ctx context.Context, a CreateScheduleArgs) (db.Schedule, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return db.Schedule{}, err
	}
	rc, err := o.resolveConfig(ctx, a.ConfigArgs, c)
	if err != nil {
		return db.Schedule{}, err
	}
	return o.Client.CreateSchedule(ctx, client.CreateSchedule{
		ConnectionID: rc.connectionID, ExecutionConfig: rc.config, ProjectID: rc.project.ID, Title: a.Title, Prompt: a.Prompt,
		ProviderInstanceID: rc.provider, Model: rc.model, ModelOptions: rc.options,
		RuntimeMode: rc.runtimeMode, InteractionMode: rc.interactionMode, Workspace: rc.workspace,
		Notes: strPtr(a.Notes), AttachmentIDs: rc.attachmentIDs, OriginSession: c.Session,
		Cron: a.Cron, Tz: a.Tz, Paused: a.Paused,
	})
}

// UpdateScheduleArgs is update_schedule.
type UpdateScheduleArgs struct {
	Notes *string `json:"notes,omitempty"`
	UpdateArgs
	Cron   *string `json:"cron,omitempty"`
	Tz     *string `json:"tz,omitempty"`
	Paused *bool   `json:"paused,omitempty" jsonschema:"true pauses, false resumes"`
}

func (o *Ops) UpdateSchedule(ctx context.Context, a UpdateScheduleArgs) (db.Schedule, error) {
	p, err := o.configPatch(ctx, a.UpdateArgs, true)
	if err != nil {
		return db.Schedule{}, err
	}
	if a.Notes != nil {
		p["notes"] = *a.Notes
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

// RefArgs accepts one pasted reference or a connection-scoped session.
type RefArgs struct {
	Session *db.SessionRef `json:"session,omitempty" jsonschema:"explicit scoped session instead of ref"`
	Ref     string         `json:"ref,omitempty" jsonschema:"what the user pasted: a stage task, tree or session link (any host; markdown link ok), a T3 thread link, #<task id> or a T3 thread id"`
}

// RefOut is read_ref: the task, tree and/or session a reference names. A
// task reference includes its session (once launched); a session reference
// includes the task that owns it (if any); a tree reference carries only the
// tree.
type RefOut struct {
	SessionRef   *db.SessionRef       `json:"sessionRef,omitempty"`
	Links        execution.Links      `json:"links"`
	SessionTitle string               `json:"sessionTitle,omitempty"`
	Kind         string               `json:"kind"` // task | tree | session
	Task         *client.TaskDetail   `json:"task"`
	Tree         *client.TreeDetail   `json:"tree,omitempty"`
	Session      *client.ThreadDetail `json:"session"`
	// Next: how to read the full conversation.
	Next string `json:"next,omitempty"`
}

// ReadRef resolves a pasted reference and reads what it names.
func (o *Ops) ReadRef(ctx context.Context, a RefArgs) (RefOut, error) {
	if a.Session != nil {
		if a.Ref != "" || a.Session.ConnectionID <= 0 || strings.TrimSpace(a.Session.ExternalSessionID) == "" {
			return RefOut{}, errors.New("pass a reference or a session with connectionId and externalSessionId")
		}
		a.Ref = fmt.Sprintf("/connections/%d/sessions/%s", a.Session.ConnectionID, url.PathEscape(a.Session.ExternalSessionID))
	}
	r, err := o.Client.ResolveRef(ctx, a.Ref)
	if err != nil {
		return RefOut{}, err
	}
	out := RefOut{Kind: r.Kind, SessionRef: r.Session, SessionTitle: r.SessionTitle, Links: r.Links}
	if r.TreeID != nil {
		t, err := o.Client.Tree(ctx, *r.TreeID)
		if err != nil {
			return out, err
		}
		out.Tree = &t
		out.Next = fmt.Sprintf("Edit tree %d with create_task or add_to_tree (treeId %d), update_tree or start_tree (id %d), set_needs or remove_from_tree.", t.ID, t.ID, t.ID)
		return out, nil
	}
	if r.TaskID != nil {
		t, err := o.Client.Task(ctx, *r.TaskID)
		if err != nil {
			return out, err
		}
		out.Task = &t
		out.Links = t.Links
		if t.Thread != nil {
			out.Session = &client.ThreadDetail{Thread: *t.Thread, Task: &t.Task}
		}
	}
	if r.Session == nil {
		out.Next = "Not launched yet: no session or conversation."
		return out, nil
	}
	h, err := o.harnesses(ctx)
	if err != nil {
		return out, err
	}
	if h.isT3(r.Session.ConnectionID) {
		if out.Task == nil {
			d, err := o.Client.Thread(ctx, r.Session.ExternalSessionID)
			if err != nil {
				return out, err
			}
			out.Session = &d
		}
		out.Next = "Open in T3 or use t3_thread_read " + r.Session.ExternalSessionID + " in T3."
	} else {
		out.Next = "Open the session in its native harness."
	}
	return out, nil
}
