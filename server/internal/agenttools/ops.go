// Package agenttools is the agent interface to stage: operations over the
// REST client, exposed as MCP tools (stdio `stage mcp`, HTTP /mcp) and as
// CLI commands. Inputs and outputs are the tool schemas.
package agenttools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Ops runs agent operations against the REST API.
type Ops struct {
	Client *client.Client
	// Caller is the process's own caller context: the CLI's
	// STAGE_CALLER_SESSION and working directory. Empty for MCP servers.
	Caller agentctx.Caller
	// HarnessEnv reads this process's environment for the harness session
	// variable (harness.Attribution.Env, e.g. T3CODE_THREAD_ID): the
	// weakest caller source, for the CLI and stdio MCP (an agent's own
	// child process). Nil disables it (HTTP /mcp).
	HarnessEnv func(string) string

	harnessMu     sync.Mutex
	harnessCaller *harnessCaller
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
	c, _, err := o.detectCaller(ctx, a)
	return c, err
}

// detectCaller reports harness context even if it cannot resolve a session.
func (o *Ops) detectCaller(ctx context.Context, a CallerArgs) (agentctx.Caller, bool, error) {
	claim, _ := ctx.Value(claimKey{}).(*db.SessionRef)
	c, err := o.Caller.Resolve(a.CallerSession, a.Cwd, claim)
	if err != nil || c.Session != nil {
		return c, c.Session != nil, err
	}
	h, err := o.fromHarnessEnv(ctx)
	if err != nil {
		return c, false, err
	}
	if h.session != nil {
		c.Session, c.Source = h.session, h.source
	}
	return c, h.detected, nil
}

// harnessCaller is the caller derived from a harness's session variable,
// or why none was (warning).
type harnessCaller struct {
	session  *db.SessionRef
	source   string
	warning  string
	detected bool
}

// fromHarnessEnv derives the caller from the harness session variable set
// in this process's environment (harness.Attribution.Env), on that
// harness's active connection on this machine (resolved by the server).
// Several set: a candidate whose Attribution.Precedence.Owns matches wins
// (several owning: ambiguous); otherwise candidates whose Yields match drop
// out, and so does a wrapped candidate (Attribution.Origin) when a
// wrapper's variable is set or its origin variable names a wrapper
// (harness.Wraps); the one left wins. A derived session is cached for the
// process; several left or no connection leave the caller unknown with a
// warning; none set or all yielded leave it unknown without one.
func (o *Ops) fromHarnessEnv(ctx context.Context) (harnessCaller, error) {
	if o.HarnessEnv == nil {
		return harnessCaller{}, nil
	}
	o.harnessMu.Lock()
	defer o.harnessMu.Unlock()
	if o.harnessCaller != nil {
		return *o.harnessCaller, nil
	}
	hs, err := o.Client.Harnesses(ctx)
	if err != nil {
		return harnessCaller{}, err
	}
	var cands []envCandidate
	var wrapped []string
	for _, d := range hs.Harnesses {
		if d.Wraps != nil {
			wrapped = append(wrapped, d.Wraps.Origins...)
		}
		if d.Attribution.Env == "" {
			continue
		}
		if id := strings.TrimSpace(o.HarnessEnv(d.Attribution.Env)); id != "" {
			cands = append(cands, envCandidate{kind: d.Kind, env: d.Attribution.Env, id: id, precedence: d.Attribution.Precedence, origin: d.Attribution.Origin, wraps: d.Wraps != nil})
		}
	}
	detected := len(cands) > 0
	cands = rankCandidates(cands, wrapped, o.HarnessEnv)
	switch len(cands) {
	case 0:
		return harnessCaller{detected: detected}, nil
	case 1:
	default:
		names := make([]string, len(cands))
		for i, c := range cands {
			names[i] = c.env
		}
		return harnessCaller{detected: true, warning: fmt.Sprintf("several harness session variables are set (%s); pass the caller explicitly", strings.Join(names, ", "))}, nil
	}
	w := cands[0]
	c, err := o.Client.ResolveConnection(ctx, w.kind)
	var apiErr *client.Error
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return harnessCaller{detected: true, warning: fmt.Sprintf("%s is set but there is %s", w.env, apiErr.Msg)}, nil
	}
	if err != nil {
		return harnessCaller{}, err
	}
	h := harnessCaller{session: &db.SessionRef{ConnectionID: c.ID, ExternalSessionID: w.id}, source: w.env, detected: true}
	o.harnessCaller = &h
	return h, nil
}

// envCandidate is a harness whose session variable is set.
type envCandidate struct {
	kind, env, id string
	precedence    *harness.Precedence
	// origin: the harness may be wrapped (harness.Attribution.Origin).
	origin *harness.Origin
	// wraps: the harness runs others' sessions (harness.Wraps).
	wraps bool
}

// rankCandidates applies harness.Precedence and harness.Wraps to several
// set variables: the owning candidates if any, else those that neither
// yield nor are wrapped. A wrapped candidate drops out when a wrapper's
// variable is set or its origin variable holds an origin a wrapper leaves
// (wrapped).
func rankCandidates(cands []envCandidate, wrapped []string, getenv func(string) string) []envCandidate {
	if len(cands) < 2 {
		return cands
	}
	wrapperSet := slices.ContainsFunc(cands, func(c envCandidate) bool { return c.wraps })
	inWrapper := func(c envCandidate) bool {
		if c.origin == nil {
			return false
		}
		return wrapperSet || (c.origin.Env != "" && slices.Contains(wrapped, strings.TrimSpace(getenv(c.origin.Env))))
	}
	var owns, kept []envCandidate
	for _, c := range cands {
		switch {
		case c.precedence != nil && harness.AnyMatches(c.precedence.Owns, getenv):
			owns = append(owns, c)
		case c.precedence != nil && harness.AnyMatches(c.precedence.Yields, getenv):
		case inWrapper(c):
		default:
			kept = append(kept, c)
		}
	}
	if len(owns) > 0 {
		return owns
	}
	return kept
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
	BindingID    *int64  `json:"bindingId"`
	WorkspaceID  string  `json:"workspaceId,omitempty"`
	TreeID       *int64  `json:"treeId"`
	TreeTitle    *string `json:"treeTitle"`
	// SteeringTreeID: the tree the caller's task actively steers.
	SteeringTreeID *int64 `json:"steeringTreeId"`
	APIURL         string `json:"apiUrl"`
	// SessionSource: where session came from (request, argument,
	// STAGE_CALLER_SESSION or a harness variable such as T3CODE_THREAD_ID).
	SessionSource string `json:"sessionSource,omitempty"`
	// CallerWarning: why a harness session variable named no caller.
	CallerWarning string `json:"callerWarning,omitempty"`
}

// Context resolves the caller's session, directory and project.
func (o *Ops) Context(ctx context.Context, a CallerArgs) (ContextOut, error) {
	c, err := o.caller(ctx, a)
	if err != nil {
		return ContextOut{}, err
	}
	out := ContextOut{Session: c.Session, Cwd: c.Cwd, APIURL: o.Client.BaseURL, SessionSource: c.Source}
	if c.Session == nil {
		h, err := o.fromHarnessEnv(ctx)
		if err != nil {
			return out, err
		}
		out.CallerWarning = h.warning
		out.ProjectError = "No caller session. Pass callerSession or specify a project on creation."
		return out, nil
	}
	d, err := o.Client.SessionContext(ctx, *c.Session)
	if err != nil {
		if client.IsStatus(err, 404) || client.IsStatus(err, 409) || client.IsStatus(err, 502) || client.IsStatus(err, 503) {
			out.ProjectError = err.Error() + ". Pass a project explicitly for backlog or choose an exact launch destination."
			return out, nil
		}
		return out, err
	}
	out.Project = d.Project
	if d.Project != nil {
		out.ProjectSource = "session"
	} else {
		out.ProjectError = "No Stage project is recorded for this session. Choose a project explicitly for backlog."
	}
	if d.Task != nil {
		out.CallerTaskID = &d.Task.ID
		out.BindingID = d.Task.BindingID
		out.WorkspaceID = d.Task.WorkspaceID
		if w := d.Task.Workspace; w != nil && w.Source == "pinned" && w.BindingID > 0 {
			out.BindingID = &w.BindingID
		}
		out.TreeID = d.Task.TreeID
		out.TreeTitle = d.Task.TreeTitle
		if st := d.Task.Steering; st != nil && st.Active {
			out.SteeringTreeID = &st.TreeID
		}
	}
	return out, nil
}

// ProjectsOut is list_projects.
type ProjectsOut struct {
	Connections []db.ConnectionSummary `json:"connections"`
	client.ProjectSummaries
}

// ListProjectsArgs is list_projects.
type ListProjectsArgs struct {
	Archived string `json:"archived,omitempty" jsonschema:"false (default: active projects) | true (archived only) | all"`
}

func (o *Ops) ListProjects(ctx context.Context, a ListProjectsArgs) (ProjectsOut, error) {
	ps, err := o.Client.ProjectSummariesArchived(ctx, a.Archived)
	if err != nil {
		return ProjectsOut{}, err
	}
	cs, err := o.Client.Connections(ctx)
	if err != nil {
		return ProjectsOut{}, err
	}
	connections := make([]db.ConnectionSummary, 0, len(cs))
	for _, c := range cs {
		connections = append(connections, db.SummarizeConnection(c, ""))
	}
	return ProjectsOut{ProjectSummaries: ps, Connections: connections}, nil
}

type ModelsArgs struct {
	ConnectionID int64   `json:"connectionId" jsonschema:"harness connection from list_projects"`
	WorkspaceID  *string `json:"workspaceId,omitempty" jsonschema:"exact workspace on the selected connection from list_projects"`
}
type ModelsOut struct {
	Capabilities *execution.Capabilities `json:"capabilities,omitempty"`
}

func (o *Ops) ListModels(ctx context.Context, a ModelsArgs) (ModelsOut, error) {
	if a.ConnectionID <= 0 {
		return ModelsOut{}, errors.New("connectionId is required: choose a harness connection from list_projects")
	}
	if err := validateWorkspaceSelector(a.WorkspaceID); err != nil {
		return ModelsOut{}, err
	}
	c, err := o.destinationCapabilities(ctx, a.ConnectionID, a.WorkspaceID)
	c.Actions = slices.DeleteFunc(c.Actions, func(a execution.Action) bool { return !a.Allows(execution.CallerAgent) })
	return ModelsOut{Capabilities: &c}, err
}

func validateWorkspaceSelector(workspaceID *string) error {
	if workspaceID == nil {
		return nil
	}
	if *workspaceID == "" {
		return fmt.Errorf("workspaceId is empty, %s", destinationHelp)
	}
	return nil
}

func (o *Ops) destinationCapabilities(ctx context.Context, connectionID int64, workspaceID *string) (execution.Capabilities, error) {
	if workspaceID != nil {
		return o.Client.DestinationCapabilities(ctx, connectionID, *workspaceID)
	}
	return o.Client.Capabilities(ctx, connectionID)
}

// ---- task config shared by tasks and schedules ----

// ConfigArgs is the task configuration (create_task, create_schedule).
type ConfigArgs struct {
	ConnectionID    int64        `json:"connectionId,omitempty" jsonschema:"destination connection from list_projects; required with workspaceId"`
	WorkspaceID     *string      `json:"workspaceId,omitempty" jsonschema:"exact destination workspace on the selected connection from list_projects, including unbound workspaces"`
	ExecutionConfig *ConfigInput `json:"executionConfig,omitempty" jsonschema:"the selected connection's model, options, permission and mode from list_models; options include the harness's launch options (a worktree, its base ref or branch where the harness declares them); omitted = the connection's pinned model, else the harness defaults"`
	Project         string       `json:"project,omitempty" jsonschema:"Stage project id or unambiguous exact title; optional with workspaceId. Sets historical context and can use the caller's Stage project for backlog"`
	Title           string       `json:"title" jsonschema:"short task title"`
	Prompt          string       `json:"prompt" jsonschema:"the full prompt the agent session starts with; self-contained"`
	ImagePaths      []string     `json:"imagePaths,omitempty" jsonschema:"local image files (png/jpeg/gif/webp) sent with the prompt; stdio/CLI only. At most 8 images in all (attachmentIds, imagePaths, images)"`
	Images          []ImageInput `json:"images,omitempty" jsonschema:"inline images sent with the prompt (any transport); uploaded first, like imagePaths"`
	AttachmentIDs   []int64      `json:"attachmentIds,omitempty" jsonschema:"staged upload ids (POST /api/uploads) to attach, before imagePaths and images"`
}

// ImageInput is an inline image, staged through POST /api/uploads.
type ImageInput struct {
	Name     string `json:"name,omitempty" jsonschema:"file name (default image.<subtype>)"`
	MimeType string `json:"mimeType" jsonschema:"image/png | image/jpeg | image/gif | image/webp; must match the data"`
	Data     string `json:"data" jsonschema:"standard base64 of the image bytes (at most 10 MB decoded)"`
}

// decode validates the image as POST /api/uploads would.
func (in ImageInput) decode() (string, []byte, error) {
	if !slices.Contains(db.AttachmentMimeTypes, in.MimeType) {
		return "", nil, fmt.Errorf("unsupported image mimeType %q (want %s)", in.MimeType, strings.Join(db.AttachmentMimeTypes, ", "))
	}
	if len(in.Data) > base64.StdEncoding.EncodedLen(db.MaxAttachmentBytes) {
		return "", nil, errors.New("image is larger than 10 MB")
	}
	data, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil {
		return "", nil, fmt.Errorf("image data is not valid base64: %w", err)
	}
	if len(data) == 0 {
		return "", nil, errors.New("image is empty")
	}
	if len(data) > db.MaxAttachmentBytes {
		return "", nil, errors.New("image is larger than 10 MB")
	}
	if got := http.DetectContentType(data); got != in.MimeType {
		return "", nil, fmt.Errorf("image data is %s, not mimeType %s", got, in.MimeType)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "image." + strings.TrimPrefix(in.MimeType, "image/")
	}
	return name, data, nil
}

// resolvedConfig is ConfigArgs resolved against the API.
type resolvedConfig struct {
	connectionID  int64
	workspaceID   string
	config        *execution.Config
	projectID     int64
	attachmentIDs []int64
}

func strPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
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

// uploadImages stages local and inline images and returns their attachment
// ids (paths first). kept counts the other images toward the limit; every
// image is checked before any upload.
func (o *Ops) uploadImages(ctx context.Context, paths []string, images []ImageInput, kept int) ([]int64, error) {
	if len(paths) == 0 && len(images) == 0 {
		return nil, nil
	}
	if kept+len(paths)+len(images) > db.MaxTaskAttachments {
		return nil, fmt.Errorf("at most %d images", db.MaxTaskAttachments)
	}
	if len(paths) > 0 && !o.LocalFiles {
		return nil, errors.New("imagePaths needs local file access: use images (inline base64), the stdio MCP server (`stage mcp`) or the CLI")
	}
	type upload struct {
		name string
		data []byte
	}
	inline := make([]upload, 0, len(images))
	for i, img := range images {
		name, data, err := img.decode()
		if err != nil {
			return nil, fmt.Errorf("images[%d]: %w", i, err)
		}
		inline = append(inline, upload{name, data})
	}
	ids := make([]int64, 0, len(paths)+len(images))
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
	for i, img := range inline {
		att, err := o.Client.Upload(ctx, img.name, img.data)
		if err != nil {
			return nil, fmt.Errorf("upload images[%d]: %w", i, err)
		}
		ids = append(ids, att.ID)
	}
	return ids, nil
}

func (o *Ops) resolveConfig(ctx context.Context, a ConfigArgs, c agentctx.Caller, needsDestination bool) (resolvedConfig, error) {
	rc := resolvedConfig{connectionID: a.ConnectionID}
	if err := validateWorkspaceSelector(a.WorkspaceID); err != nil {
		return rc, err
	}
	if needsDestination && a.WorkspaceID == nil {
		return rc, fmt.Errorf("launch destination required: %s", destinationHelp)
	}
	if a.WorkspaceID != nil {
		rc.workspaceID = *a.WorkspaceID
	} else if rc.connectionID == 0 && c.Session != nil {
		// Unresolved backlog can retain the caller's connection as context.
		rc.connectionID = c.Session.ConnectionID
	}
	if rc.connectionID <= 0 {
		return rc, fmt.Errorf("connectionId is required: %s", destinationHelp)
	}
	if a.WorkspaceID == nil || strings.TrimSpace(a.Project) != "" {
		p, _, err := agentctx.ResolveProject(ctx, o.Client, c, a.Project)
		if err != nil {
			return rc, fmt.Errorf("%w, or %s", err, destinationHelp)
		}
		rc.projectID = p.ID
	}
	h, err := o.harnesses(ctx)
	if err != nil {
		return rc, err
	}
	conn := h.get(rc.connectionID)
	if conn == nil {
		return rc, fmt.Errorf("connection %d is unavailable, %s", rc.connectionID, destinationHelp)
	}
	if !conn.Active {
		return rc, fmt.Errorf("connection %d was replaced, %s", rc.connectionID, destinationHelp)
	}
	// Without a model choice, a new task starts with its connection's pinned model and options.
	pin := conn.NewTaskConfig
	config := execution.Config{}
	if a.ExecutionConfig != nil {
		config, err = a.ExecutionConfig.config()
		if err != nil {
			return rc, err
		}
	}
	if pin != nil && config.Model == "" {
		config.Model = pin.Model
		for _, o := range pin.Options {
			if _, set := config.Lookup(o.ID); !set {
				config.Options = append(config.Options, o)
			}
		}
	}
	rc.config = &config
	images := len(a.AttachmentIDs)+len(a.ImagePaths)+len(a.Images) > 0
	if config.Model != "" || config.Mode != "" || config.Permission != "" || len(config.Options) > 0 || images {
		f, err := o.destinationCapabilities(ctx, rc.connectionID, a.WorkspaceID)
		if err != nil {
			return rc, err
		}
		if err := f.ValidateConfig(config); err != nil {
			return rc, err
		}
		if images {
			if err := execution.RequireAvailable("images", f.Operations[execution.OpImages]); err != nil {
				return rc, err
			}
		}
	}
	uploaded, err := o.uploadImages(ctx, a.ImagePaths, a.Images, len(a.AttachmentIDs))
	if len(a.AttachmentIDs)+len(uploaded) > 0 {
		rc.attachmentIDs = append(slices.Clone(a.AttachmentIDs), uploaded...)
	}
	return rc, err
}

// ---- tasks ----

// TaskRow is a compact task for lists.
type TaskRow struct {
	ConnectionID      int64        `json:"connectionId"`
	WorkspaceID       string       `json:"workspaceId,omitempty"`
	ID                int64        `json:"id"`
	Title             string       `json:"title"`
	State             db.TaskState `json:"state"`
	ProjectID         int64        `json:"projectId"`
	Project           string       `json:"project"`
	ExternalSessionID *string      `json:"externalSessionId,omitempty"`
	TreeID            *int64       `json:"treeId,omitempty"`
	Needs             []int64      `json:"needs,omitempty"`
	// Readiness: blocked | ready for a backlog tree member.
	Readiness  *db.Readiness  `json:"readiness,omitempty"`
	TreeStatus *db.TreeStatus `json:"treeStatus,omitempty"` // its tree's status (open | done | archived)
	Archived   bool           `json:"archived,omitempty"`
	Adopted    bool           `json:"adopted,omitempty"` // tracks an existing session
	StartAt    *time.Time     `json:"startAt,omitempty"`
	ScheduleID *int64         `json:"scheduleId,omitempty"`
	// LastWordPreview is the session's latest assistant reply, one plain
	// line (get_task has the full finalReply).
	LastWordPreview *string   `json:"lastWordPreview,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func taskRow(t client.Task) TaskRow {
	return TaskRow{
		ConnectionID: t.ConnectionID,
		WorkspaceID:  t.WorkspaceID,
		ID:           t.ID, Title: t.Title, State: t.State, ProjectID: t.ProjectID, Project: t.ProjectTitle,
		ExternalSessionID: t.ExternalSessionID, TreeID: t.TreeID, Needs: t.Needs, Readiness: t.Readiness, TreeStatus: t.TreeStatus,
		Archived: t.ArchivedAt != nil, Adopted: t.Adopted,
		StartAt: t.StartAt, ScheduleID: t.ScheduleID, LastWordPreview: t.LastWordPreview,
		UpdatedAt: t.UpdatedAt,
	}
}

// ListTasksArgs is list_tasks.
type ListTasksArgs struct {
	OriginSession *db.SessionRef `json:"originSession,omitempty"`
	Project       string         `json:"project,omitempty" jsonschema:"project id or title; omitted = all projects"`
	States        []string       `json:"states,omitempty" jsonschema:"backlog|launching|running|needs_input|done|failed|interrupted|cancelled"`
	Query         string         `json:"query,omitempty" jsonschema:"substring of title or prompt"`
	ScheduleID    *int64         `json:"scheduleId,omitempty" jsonschema:"only tasks this schedule spawned"`
	From          string         `json:"from,omitempty" jsonschema:"createdAt >= from: ISO date or RFC 3339 datetime (no zone = UTC)"`
	To            string         `json:"to,omitempty" jsonschema:"createdAt < to: ISO date or RFC 3339 datetime (no zone = UTC)"`
	CompletedFrom string         `json:"completedFrom,omitempty" jsonschema:"completedAt (user acceptance) >= completedFrom: ISO date or RFC 3339 datetime (no zone = UTC)"`
	Closed        string         `json:"closed,omitempty" jsonschema:"false (default: open tasks only) | true (archived, or in a done or archived tree) | all"`
	Limit         int            `json:"limit,omitempty" jsonschema:"max rows (default 50)"`
}

// TasksOut is list_tasks.
type TasksOut struct {
	Tasks []TaskRow `json:"tasks"`
	Total int       `json:"total"`
}

func (o *Ops) ListTasks(ctx context.Context, a ListTasksArgs) (TasksOut, error) {
	if slices.Contains(a.States, "settled") {
		return TasksOut{}, db.Invalid("state settled was removed in API v9; use state done (completedAt marks acceptance)")
	}
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
	q.ScheduleID = a.ScheduleID
	var err error
	if q.From, err = parseTime("from", a.From); err != nil {
		return TasksOut{}, err
	}
	if q.To, err = parseTime("to", a.To); err != nil {
		return TasksOut{}, err
	}
	if q.CompletedFrom, err = parseTime("completedFrom", a.CompletedFrom); err != nil {
		return TasksOut{}, err
	}
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

// parseTime parses an ISO date (midnight UTC) or RFC 3339 datetime
// (no zone = UTC), as the REST list filters do; "" = nil.
func parseTime(name, v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return &t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("invalid %s %q (want ISO date or datetime)", name, v)
}

func (o *Ops) GetTask(ctx context.Context, a IDArgs) (client.TaskDetail, error) {
	return o.Client.Task(ctx, a.ID)
}

// WaitArgs are the tree and start options of create_task.
type WaitArgs struct {
	TreeID     *int64  `json:"treeId,omitempty" jsonschema:"join this tree as a parallel root unless needs are given (default: the needs' tree, else a new tree when there are needs)"`
	Needs      []int64 `json:"needs,omitempty" jsonschema:"task ids this task runs after (same tree): it starts once all of them are completed; tasks sharing a need run in parallel"`
	AfterMe    bool    `json:"afterMe,omitempty" jsonschema:"also need your own session: it is adopted as a task in the tree and this task waits for its user completion in Stage (needs your session id)"`
	StartMode  string  `json:"startMode,omitempty" jsonschema:"inherit (default; omit unless the user asks). In an auto tree the task launches once its needs are completed, at once if they already are: expected, not a reason for manual. auto or manual (reads ready) only for a user-requested per-task exception, preserved when the tree mode changes"`
	StartLocal string  `json:"startLocal,omitempty" jsonschema:"one-shot start, wall time YYYY-MM-DDTHH:mm in startTz"`
	StartTz    string  `json:"startTz,omitempty" jsonschema:"IANA zone for startLocal, e.g. America/New_York"`
	Handoff    *bool   `json:"handoff,omitempty" jsonschema:"append each need's final reply, branch and session link to the prompt at launch (default true)"`
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
	rc, err := o.resolveConfig(ctx, a.ConfigArgs, c, a.Start || strings.TrimSpace(a.StartLocal) != "" || strings.TrimSpace(a.StartTz) != "")
	if err != nil {
		return client.Task{}, err
	}
	return o.Client.CreateTask(ctx, client.CreateTask{
		ConnectionID: rc.connectionID, WorkspaceID: rc.workspaceID, ExecutionConfig: rc.config, ProjectID: rc.projectID, Title: a.Title, Prompt: a.Prompt,
		InitialNote: strPtr(a.InitialNote), AttachmentIDs: rc.attachmentIDs,
		TreeID: a.TreeID, Needs: a.Needs, NeedsSession: session, StartMode: mode.mode, OriginSession: c.Session,
		StartLocal: strPtr(a.StartLocal), StartTz: strPtr(a.StartTz), Start: a.Start, Handoff: a.Handoff,
	})
}

// UpdateArgs are editable config fields (nil = unchanged). Outside backlog
// only the title changes.
type UpdateArgs struct {
	ConnectionID    int64                    `json:"connectionId,omitempty" jsonschema:"destination connection from list_projects"`
	WorkspaceID     db.Optional[string]      `json:"workspaceId,omitzero" jsonschema:"exact destination workspace from list_projects; null clears the pin"`
	ExecutionConfig db.Optional[ConfigInput] `json:"executionConfig,omitzero" jsonschema:"replaces the stored model, options, permission and mode (list_models); null clears it (the harness defaults)"`
	ID              int64                    `json:"id"`
	Project         *string                  `json:"project,omitempty" jsonschema:"move to project (id or title)"`
	Title           *string                  `json:"title,omitempty"`
	Prompt          *string                  `json:"prompt,omitempty"`
	// Images: the result is attachmentIds (else the current images) then
	// imagePaths then images, sent as the exact set (PATCH attachmentIds).
	AttachmentIDs *[]int64     `json:"attachmentIds,omitempty" jsonschema:"the exact images to keep: ids from the current attachments (get_task or get_schedule) or staged uploads; current images not listed are deleted; [] removes all. Omitted keeps the current images"`
	ImagePaths    []string     `json:"imagePaths,omitempty" jsonschema:"local image files added after the kept images; stdio/CLI only"`
	Images        []ImageInput `json:"images,omitempty" jsonschema:"inline images added after the kept images (any transport). At most 8 images in all"`
}

// imagePatch is the attachmentIds a's image fields send (set=false: none);
// current reads the stored images when attachmentIds is omitted.
func (o *Ops) imagePatch(ctx context.Context, a UpdateArgs, current func() ([]db.Attachment, error)) ([]int64, bool, error) {
	if a.AttachmentIDs == nil && len(a.ImagePaths) == 0 && len(a.Images) == 0 {
		return nil, false, nil
	}
	ids := []int64{}
	if a.AttachmentIDs != nil {
		ids = append(ids, *a.AttachmentIDs...)
	} else {
		atts, err := current()
		if err != nil {
			return nil, false, err
		}
		for _, att := range atts {
			ids = append(ids, att.ID)
		}
	}
	added, err := o.uploadImages(ctx, a.ImagePaths, a.Images, len(ids))
	return append(ids, added...), true, err
}

func (o *Ops) configPatch(ctx context.Context, a UpdateArgs, current func() ([]db.Attachment, error)) (client.Patch, error) {
	p := client.Patch{}
	if a.ConnectionID != 0 {
		p["connectionId"] = a.ConnectionID
	}
	if a.WorkspaceID.Set {
		p["workspaceId"] = a.WorkspaceID.Ptr()
	}
	if a.Project != nil {
		pr, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, *a.Project)
		if err != nil {
			return nil, err
		}
		p["projectId"] = pr.ID
	}
	if a.ExecutionConfig.Null {
		p["executionConfig"] = nil
	} else if a.ExecutionConfig.Set {
		cfg, err := a.ExecutionConfig.V.config()
		if err != nil {
			return nil, err
		}
		// The API validates against the destination resolved for this edit.
		p["executionConfig"] = cfg
	}
	if a.Title != nil {
		p["title"] = *a.Title
	}
	if a.Prompt != nil {
		p["prompt"] = *a.Prompt
	}
	ids, set, err := o.imagePatch(ctx, a, current)
	if err != nil {
		return nil, err
	}
	if set {
		p["attachmentIds"] = ids
	}
	return p, nil
}

// UpdateTaskArgs is update_task.
type UpdateTaskArgs struct {
	UpdateArgs
	StartLocal *string `json:"startLocal,omitempty" jsonschema:"set a one-shot start (with startTz); empty string clears it"`
	StartTz    *string `json:"startTz,omitempty"`
	Handoff    *bool   `json:"handoff,omitempty" jsonschema:"append each need's final reply, branch and session link to the prompt at launch; backlog only"`
	StartMode  *string `json:"startMode,omitempty" jsonschema:"auto | manual | inherit (the tree's); backlog only"`
}

func (o *Ops) UpdateTask(ctx context.Context, a UpdateTaskArgs) (client.Task, error) {
	p, err := o.configPatch(ctx, a.UpdateArgs, func() ([]db.Attachment, error) {
		t, err := o.Client.Task(ctx, a.ID)
		return t.Attachments, err
	})
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

// WaitTaskArgs is wait_task.
type WaitTaskArgs struct {
	ID             int64    `json:"id"`
	Until          []string `json:"until,omitempty" jsonschema:"states to wait for (default: needs_input done failed interrupted cancelled)"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"give up after this many seconds (default 600, max 3600)"`
}

// WaitOut is wait_task.
type WaitOut struct {
	Task     client.Task `json:"task"`
	Reached  bool        `json:"reached"`
	TimedOut bool        `json:"timedOut"`
}

var defaultUntil = []string{"needs_input", "done", "failed", "interrupted", "cancelled"}

func (o *Ops) WaitTask(ctx context.Context, a WaitTaskArgs) (WaitOut, error) {
	until := a.Until
	if len(until) == 0 {
		until = defaultUntil
	}
	if slices.Contains(until, "settled") {
		return WaitOut{}, db.Invalid("until: settled was removed in API v9; wait for done")
	}
	for _, state := range until {
		if !db.TaskState(state).Valid() {
			want := make([]string, len(db.TaskStates))
			for i, s := range db.TaskStates {
				want[i] = string(s)
			}
			return WaitOut{}, db.Invalid("until: invalid state %q (want one of %s)", state, strings.Join(want, ", "))
		}
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

// ListSchedulesArgs is list_schedules.
type ListSchedulesArgs struct {
	ProjectArgs
	DueWithin string `json:"dueWithin,omitempty" jsonschema:"Go duration, e.g. 24h: only unpaused schedules whose next run is within it"`
}

func (o *Ops) ListSchedules(ctx context.Context, a ListSchedulesArgs) (SchedulesOut, error) {
	var pid *int64
	if a.Project != "" {
		p, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, a.Project)
		if err != nil {
			return SchedulesOut{}, err
		}
		pid = &p.ID
	}
	ss, err := o.Client.Schedules(ctx, pid, a.DueWithin)
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
	rc, err := o.resolveConfig(ctx, a.ConfigArgs, c, true)
	if err != nil {
		return db.Schedule{}, err
	}
	return o.Client.CreateSchedule(ctx, client.CreateSchedule{
		ConnectionID: rc.connectionID, WorkspaceID: rc.workspaceID, ExecutionConfig: rc.config, ProjectID: rc.projectID, Title: a.Title, Prompt: a.Prompt,
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
	p, err := o.configPatch(ctx, a.UpdateArgs, func() ([]db.Attachment, error) {
		sc, err := o.Client.Schedule(ctx, a.ID)
		return sc.Attachments, err
	})
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
	Ref     string         `json:"ref,omitempty" jsonschema:"what the user pasted: a stage task, tree, schedule, project, journal entry or session link (any host; markdown link ok), a harness session link, #<task id> or a session id with connectionId in session"`
}

// RefOut is read_ref: the task, tree, schedule, project, journal entry and/or
// session a reference names. A task reference includes its session (once
// launched); a session reference includes the task that owns it (if any); a
// tree, schedule, project or journal reference carries only what it names.
type RefOut struct {
	SessionRef   *db.SessionRef         `json:"sessionRef,omitempty"`
	Links        execution.Links        `json:"links"`
	SessionTitle string                 `json:"sessionTitle,omitempty"`
	Kind         string                 `json:"kind"` // task | tree | schedule | project | journal | session
	Task         *client.TaskDetail     `json:"task"`
	Attempt      *int                   `json:"attempt,omitempty"`
	Tree         *client.TreeDetail     `json:"tree,omitempty"`
	Schedule     *client.ScheduleDetail `json:"schedule,omitempty"`
	Project      *db.Project            `json:"project,omitempty"`
	Entry        *db.JournalEntry       `json:"entry,omitempty"`
	Session      *client.SessionContext `json:"session"`
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
	out := RefOut{Kind: r.Kind, SessionRef: r.Session, SessionTitle: r.SessionTitle, Links: r.Links, Attempt: r.Attempt}
	switch {
	case r.EntryID != nil:
		e, err := o.Client.GetJournalEntry(ctx, *r.EntryID)
		if err != nil {
			return out, err
		}
		out.Entry = &e
		return out, nil
	case r.ScheduleID != nil:
		sc, err := o.Client.Schedule(ctx, *r.ScheduleID)
		if err != nil {
			return out, err
		}
		out.Schedule = &sc
		return out, nil
	case r.ProjectID != nil:
		p, err := o.Client.Project(ctx, *r.ProjectID)
		if err != nil {
			return out, err
		}
		out.Project = &p
		return out, nil
	}
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
		if r.Attempt == nil {
			out.Links = t.Links
		}
	}
	if r.Session == nil {
		out.Next = "Not launched yet: no session or conversation."
		return out, nil
	}
	if out.Task == nil {
		d, err := o.Client.SessionContext(ctx, *r.Session)
		if err != nil {
			return out, err
		}
		out.Session = &d
	}
	out.Next = "Open the session in its native harness."
	return out, nil
}
