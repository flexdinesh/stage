package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/cronexpr"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *server) listTasks(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	var f db.TaskFilter
	var err error
	if f.ProjectID, err = queryInt64(q, "project"); err != nil {
		return err
	}
	f.States = multi(q, "state")
	for _, st := range f.States {
		if err := validEnum("state", st, taskStateStrings()); err != nil {
			return err
		}
	}
	if q.Has("tag") {
		return db.Invalid("tag was removed: tasks have no tags")
	}
	f.Q = strings.TrimSpace(q.Get("q"))
	if q.Has("originThreadId") {
		return db.Invalid("originThreadId was removed in API v6; supply originConnectionId and originSessionId")
	}
	if q.Has("originConnectionId") || q.Has("originSessionId") {
		id, e := queryInt64(q, "originConnectionId")
		if e != nil || id == nil || *id <= 0 || strings.TrimSpace(q.Get("originSessionId")) == "" {
			return db.Invalid("supply originConnectionId and originSessionId together")
		}
		f.OriginSession = &db.SessionRef{ConnectionID: *id, ExternalSessionID: q.Get("originSessionId")}
	}
	if f.From, err = queryTime(q, "from", time.UTC); err != nil {
		return err
	}
	if f.To, err = queryTime(q, "to", time.UTC); err != nil {
		return err
	}
	if f.SettledFrom, err = queryTime(q, "settledFrom", time.UTC); err != nil {
		return err
	}
	switch v := strings.TrimSpace(q.Get("closed")); v {
	case "", "false":
	case "true", "all":
		f.Closed = v
	default:
		return db.Invalid("invalid closed %q (want false|true|all)", v)
	}
	ts, err := s.Store.ListTasks(r.Context(), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.tasks(s.envID(r.Context()), ts)})
	return nil
}

type createTaskReq struct {
	taskConfigReq
	InitialNote string `json:"initialNote"`
}

type taskConfigReq struct {
	ConnectionID       *int64                  `json:"connectionId"`
	NeedsSession       *db.SessionRef          `json:"needsSession"`
	ExecutionConfig    *execution.Config       `json:"executionConfig"`
	OriginSession      *db.SessionRef          `json:"originSession"`
	ProjectID          *int64                  `json:"projectId"`
	Title              string                  `json:"title"`
	Prompt             string                  `json:"prompt"`
	ProviderInstanceID *string                 `json:"providerInstanceId"`
	Model              *string                 `json:"model"`
	ModelOptions       []execution.ModelOption `json:"modelOptions"`
	RuntimeMode        *string                 `json:"runtimeMode"`
	InteractionMode    *string                 `json:"interactionMode"`
	Workspace          *db.Workspace           `json:"workspace"`
	TreeID             *int64                  `json:"treeId"`
	Needs              []int64                 `json:"needs"`
	StartMode          *db.StartMode           `json:"startMode"`
	AttachmentIDs      []int64                 `json:"attachmentIds"`
	StartLocal         *string                 `json:"startLocal"`
	StartTz            *string                 `json:"startTz"`
	Handoff            *bool                   `json:"handoff"`
	Start              bool                    `json:"start"`
}

// A request without a connection starts on the default connection when the
// project is available there, else on the project's only binding, else (with
// a default set) its first available binding (db.NewTaskDestination).
func (s *server) chooseConnection(r *http.Request, req *taskConfigReq) error {
	if req.ConnectionID != nil {
		return s.plannedConnection(r.Context(), *req.ConnectionID)
	}
	if req.ProjectID == nil {
		return nil
	}
	project, err := s.Store.GetProject(r.Context(), *req.ProjectID)
	if err != nil {
		return err
	}
	prefs, err := s.Store.GetPreferences(r.Context())
	if err != nil {
		return err
	}
	// Bindings on replaced connections take no new work.
	active := slices.ContainsFunc(project.Bindings, func(b db.ProjectBinding) bool { return b.ConnectionActive })
	id := db.NewTaskDestination(project.Bindings, prefs.DefaultConnectionID, s.Store.ConnectionMachine)
	switch {
	case id != 0:
		req.ConnectionID = &id
		return s.plannedConnection(r.Context(), id)
	case !active:
		// Preserve legacy unbound T3 planning. New clients send an explicit destination.
		return nil
	default:
		return db.Invalid("This project has multiple harness bindings. Choose a connectionId.")
	}
}

func (req createTaskReq) validate(isT3 func(int64) bool) (db.NewTask, error) {
	in, err := req.taskConfigReq.validate(isT3)
	in.InitialNote = req.InitialNote
	return in, err
}

// isT3 reports whether a connection is a T3 instance (db.Store.IsT3).
func (req taskConfigReq) validate(isT3 func(int64) bool) (db.NewTask, error) {
	if req.ExecutionConfig != nil && (req.ProviderInstanceID != nil || req.Model != nil || req.ModelOptions != nil || req.RuntimeMode != nil || req.InteractionMode != nil) {
		return db.NewTask{}, db.Invalid("use executionConfig or legacy model/permission fields, not both")
	}
	in := db.NewTask{
		NeedsSession:       req.NeedsSession,
		ExecutionConfig:    req.ExecutionConfig,
		OriginSession:      req.OriginSession,
		Title:              strings.TrimSpace(req.Title),
		Prompt:             req.Prompt,
		ProviderInstanceID: trimmedPtr(req.ProviderInstanceID),
		Model:              trimmedPtr(req.Model),
		RuntimeMode:        "full-access",
		InteractionMode:    "default",
		Workspace:          db.Workspace{Type: "root"},
		TreeID:             req.TreeID,
		Needs:              req.Needs,
		StartMode:          req.StartMode,
		AttachmentIDs:      req.AttachmentIDs,
		Handoff:            req.Handoff,
	}
	if req.ConnectionID != nil {
		if *req.ConnectionID <= 0 {
			return db.NewTask{}, db.Invalid("choose a positive connectionId")
		}
		in.ConnectionID = *req.ConnectionID
	}
	if in.ConnectionID > 0 && !isT3(in.ConnectionID) && (req.Model != nil || req.ProviderInstanceID != nil || req.RuntimeMode != nil || req.InteractionMode != nil || req.ModelOptions != nil) {
		return db.NewTask{}, db.Invalid("use executionConfig with this destination connection")
	}
	if in.ConnectionID > 0 && !isT3(in.ConnectionID) && len(req.AttachmentIDs) > 0 {
		return in, db.Invalid("OpenCode does not support image attachments. Remove them or select T3.")
	}
	if in.ConnectionID > 0 && !isT3(in.ConnectionID) && in.ExecutionConfig == nil {
		in.ExecutionConfig = &execution.Config{}
	}
	if in.StartMode != nil && !in.StartMode.Valid() {
		return in, db.Invalid("invalid startMode %q (want auto|manual)", *in.StartMode)
	}
	hasNeeds := len(in.Needs) > 0 || in.NeedsThreadID != nil || in.NeedsSession != nil
	if req.Start && hasNeeds {
		return in, db.Invalid("a task with needs cannot also start now (complete its needs in Stage before starting)")
	}
	if err := attachmentIDs(req.AttachmentIDs); err != nil {
		return in, err
	}
	if req.ProjectID == nil {
		return in, db.Invalid("projectId is required")
	}
	in.ProjectID = *req.ProjectID
	if in.Title == "" {
		return in, db.Invalid("title is required")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return in, db.Invalid("prompt is required")
	}
	if (in.ProviderInstanceID == nil) != (in.Model == nil) {
		return in, db.Invalid("providerInstanceId and model must be set together")
	}
	var err error
	if in.ModelOptions, err = validateOptions(req.ModelOptions); err != nil {
		return in, err
	}
	if req.RuntimeMode != nil {
		if err := validEnum("runtimeMode", *req.RuntimeMode, db.RuntimeModes); err != nil {
			return in, err
		}
		in.RuntimeMode = *req.RuntimeMode
	}
	if req.InteractionMode != nil {
		if err := validEnum("interactionMode", *req.InteractionMode, db.InteractionModes); err != nil {
			return in, err
		}
		in.InteractionMode = *req.InteractionMode
	}
	if req.Workspace != nil {
		if in.Workspace, err = validateWorkspaceFor(*req.Workspace, in.ConnectionID, isT3); err != nil {
			return in, err
		}
	}
	if in.StartAt, err = startAt(req.StartLocal, req.StartTz, time.Now()); err != nil {
		return in, err
	}
	if in.StartAt != nil {
		in.StartTz = req.StartTz
		if hasNeeds {
			return in, db.Invalid("a task can have a start time or needs, not both")
		}
		if req.Start {
			return in, db.Invalid("a task with a start time cannot also start now")
		}
	}
	return in, nil
}

// startLocalLayout is the startLocal wall-time format.
const startLocalLayout = "2006-01-02T15:04"

// startAt converts a one-shot start (wall time local in zone tz) into an
// instant, which must be after now. Both nil: no start.
func startAt(local, tz *string, now time.Time) (*time.Time, error) {
	if local == nil && tz == nil {
		return nil, nil
	}
	if local == nil || tz == nil {
		return nil, db.Invalid("startLocal and startTz must be set together")
	}
	loc, err := cronexpr.Zone(*tz)
	if err != nil {
		return nil, db.Invalid("%s", err.Error())
	}
	at, err := time.ParseInLocation(startLocalLayout, strings.TrimSpace(*local), loc)
	if err != nil {
		return nil, db.Invalid("invalid startLocal %q (want YYYY-MM-DDTHH:mm)", *local)
	}
	if !at.After(now) {
		return nil, db.Invalid("start time %s %s is not in the future", *local, *tz)
	}
	at = at.UTC()
	return &at, nil
}

func (s *server) createTask(w http.ResponseWriter, r *http.Request) error {
	var req createTaskReq
	if err := decodeStrict(r, &req); err != nil {
		return err
	}
	if err := s.chooseConnection(r, &req.taskConfigReq); err != nil {
		return err
	}
	in, err := req.validate(s.Store.IsT3)
	if err != nil {
		return err
	}
	if err := s.validateSelection(r.Context(), in.ConnectionID, in.ProjectID, in.ExecutionConfig); err != nil {
		return err
	}
	if in.NeedsSession != nil {
		in.NeedsBinding, err = s.prepareAdoption(r, *in.NeedsSession)
		if err != nil {
			return err
		}
	}
	task, err := s.Store.CreateTask(r.Context(), in)
	if err != nil {
		return err
	}
	s.treeChanged()
	if req.Start {
		// Start errors after save: the task stays created (failed + lastError).
		if _, err := s.Sync.StartTask(r.Context(), task.ID); err != nil {
			s.log.Warn("start after create", "task", task.ID, "err", err)
			if noteErr := s.Store.NoteBlockedLaunch(r.Context(), task.ID, err.Error()); noteErr != nil {
				return noteErr
			}
		}
		if task, err = s.Store.GetTask(r.Context(), task.ID); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusCreated, s.task(s.envID(r.Context()), task))
	return nil
}

func (s *server) getTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	task, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	events, err := s.Store.TaskEvents(ctx, id)
	if err != nil {
		return err
	}
	env := s.envID(ctx)
	out := taskDetailJSON{taskJSON: s.task(env, task), Events: events, CurrentHandoffs: []db.Handoff{}}
	if task.Session != nil {
		obs, e := s.Store.Observation(ctx, *task.Session)
		if e == nil {
			out.FinalReply = obs.Reply
			for _, link := range obs.Artifacts {
				if link.URL != "" && !slices.ContainsFunc(out.ArtifactLinks, func(a artifactLink) bool { return a.URL == link.URL }) {
					label := link.Repository
					if link.Number > 0 {
						label += "#" + strconv.Itoa(link.Number)
					}
					out.ArtifactLinks = append(out.ArtifactLinks, artifactLink{Label: label, URL: link.URL})
				}
			}
		} else if !errors.Is(e, db.ErrNotFound) {
			return e
		}
	}
	if out.Acceptances, err = s.Store.Acceptances(ctx, id); err != nil {
		return err
	}
	if task.TreeID != nil {
		tree, err := s.Store.GetTree(ctx, *task.TreeID)
		if err != nil {
			return err
		}
		shaped := s.treeDetail(env, tree)
		out.Tree = &shaped
	}
	if task.LaunchAttempt > 0 {
		if out.CurrentHandoffs, err = s.Store.GetHandoffs(ctx, id, task.LaunchAttempt); err != nil {
			return err
		}
		if out.CurrentLaunchBase, err = s.Store.GetLaunchBase(ctx, id, task.LaunchAttempt); err != nil {
			return err
		}
	}
	if task.OriginSession != nil {
		batch, err := s.Store.CreatedBySession(ctx, *task.OriginSession)
		if err != nil {
			return err
		}
		shaped := s.createdTasks(env, batch)
		out.Batch = &shaped
	}
	if s.Store.IsT3(task.ConnectionID) && s.Store.ConnectionActive(task.ConnectionID) && task.T3ThreadID != nil {
		th, err := s.Store.GetThread(ctx, *task.T3ThreadID)
		if err == nil {
			th.LastWord, err = s.Store.GetThreadLastWord(ctx, th.T3ThreadID)
		}
		switch {
		case err == nil:
			tj := s.thread(env, th)
			out.Thread = &tj
		case !errors.Is(err, db.ErrNotFound):
			return err
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) updateTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var p db.TaskPatch
	if err := decodeStrict(r, &p); err != nil {
		return err
	}
	current, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return err
	}
	p.ValidatedConnection, p.ValidatedProject = current.ConnectionID, current.ProjectID
	connection := current.ConnectionID
	if p.ConnectionID.Set {
		connection = p.ConnectionID.V
		if err := s.plannedConnection(r.Context(), connection); err != nil {
			return err
		}
	}
	if !s.Store.IsT3(connection) && (p.ProviderInstanceID.Set || p.Model.Set || p.RuntimeMode.Set || p.ModelOptions.Set || p.InteractionMode.Set || p.AttachmentIDs.Set && len(p.AttachmentIDs.V) > 0) {
		return db.Invalid("Choose harness-specific configuration and remove unsupported attachments.")
	}
	projectID := current.ProjectID
	if p.ProjectID.Set {
		projectID = p.ProjectID.V
	}
	if p.ExecutionConfig.Set && (connection != current.ConnectionID || projectID != current.ProjectID || !sameSelection(current.ExecutionConfig, p.ExecutionConfig.Ptr())) {
		if err := s.validateSelection(r.Context(), connection, projectID, p.ExecutionConfig.Ptr()); err != nil {
			return err
		}
	}
	if err := validateConfigPatch(configPatch{
		ConnectionID: connection, ExecutionConfig: &p.ExecutionConfig, ProjectID: &p.ProjectID, Title: &p.Title, Prompt: &p.Prompt, ProviderInstanceID: &p.ProviderInstanceID,
		Model: &p.Model, ModelOptions: &p.ModelOptions, RuntimeMode: &p.RuntimeMode, InteractionMode: &p.InteractionMode,
		Workspace: &p.Workspace, AttachmentIDs: &p.AttachmentIDs,
	}, s.Store.IsT3); err != nil {
		return err
	}
	if p.StartMode.Set && !p.StartMode.Null && !p.StartMode.V.Valid() {
		return db.Invalid("invalid startMode %q (want auto|manual or null)", p.StartMode.V)
	}
	if p.Handoff.Set && p.Handoff.Null {
		return db.Invalid("handoff must be true or false")
	}
	if p.StartLocal.Set || p.StartTz.Set {
		if p.StartLocal.Set != p.StartTz.Set || p.StartLocal.Null != p.StartTz.Null {
			return db.Invalid("startLocal and startTz must be set (or null) together")
		}
		at, err := startAt(p.StartLocal.Ptr(), p.StartTz.Ptr(), time.Now())
		if err != nil {
			return err
		}
		p.StartAt = db.Optional[time.Time]{Set: true, Null: at == nil}
		if at != nil {
			p.StartAt.V = *at
		}
	}
	task, err := s.Store.UpdateTask(r.Context(), id, p)
	if err != nil {
		return err
	}
	if p.StartMode.Set {
		s.treeChanged()
	}
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), task))
	return nil
}

// configPatch points at the task-config fields shared by task and schedule
// patches.
type configPatch struct {
	ConnectionID       int64
	ExecutionConfig    *db.Optional[execution.Config]
	ProjectID          *db.Optional[int64]
	Title              *db.Optional[string]
	Prompt             *db.Optional[string]
	ProviderInstanceID *db.Optional[string]
	Model              *db.Optional[string]
	ModelOptions       *db.Optional[[]execution.ModelOption]
	RuntimeMode        *db.Optional[string]
	InteractionMode    *db.Optional[string]
	Workspace          *db.Optional[db.Workspace]
	AttachmentIDs      *db.Optional[[]int64]
}

// validateConfigPatch validates and normalizes the shared config fields.
func validateConfigPatch(c configPatch, isT3 func(int64) bool) error {
	var err error
	if c.ExecutionConfig != nil && c.ExecutionConfig.Set && (c.ProviderInstanceID.Set || c.Model.Set || c.ModelOptions.Set || c.RuntimeMode.Set || c.InteractionMode.Set) {
		return db.Invalid("executionConfig cannot be combined with legacy T3 configuration fields")
	}
	if c.ProjectID.Set && c.ProjectID.Null {
		return db.Invalid("projectId must not be null")
	}
	if err := requiredText("title", c.Title); err != nil {
		return err
	}
	if c.Prompt.Set && (c.Prompt.Null || strings.TrimSpace(c.Prompt.V) == "") {
		return db.Invalid("prompt must be a non-empty string")
	}
	nullableText(c.ProviderInstanceID)
	nullableText(c.Model)
	if c.ModelOptions.Set {
		if c.ModelOptions.V, err = validateOptions(c.ModelOptions.V); err != nil {
			return err
		}
		c.ModelOptions.Null = false
	}
	if err := enumField("runtimeMode", c.RuntimeMode, db.RuntimeModes); err != nil {
		return err
	}
	if err := enumField("interactionMode", c.InteractionMode, db.InteractionModes); err != nil {
		return err
	}
	if c.Workspace.Set {
		if c.Workspace.Null {
			return db.Invalid("workspace must not be null")
		}
		if c.Workspace.V, err = validateWorkspaceFor(c.Workspace.V, c.ConnectionID, isT3); err != nil {
			return err
		}
	}
	if c.AttachmentIDs.Set {
		if err := attachmentIDs(c.AttachmentIDs.V); err != nil {
			return err
		}
		c.AttachmentIDs.Null = false
	}
	return nil
}

func (s *server) deleteTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteTask(r.Context(), id); err != nil {
		return err
	}
	s.treeChanged()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// archiveTask: POST /api/tasks/{id}/archive; unarchiveTask: .../unarchive.
func (s *server) archiveTask(w http.ResponseWriter, r *http.Request) error {
	return s.setArchived(w, r, true)
}

func (s *server) unarchiveTask(w http.ResponseWriter, r *http.Request) error {
	return s.setArchived(w, r, false)
}

func (s *server) setArchived(w http.ResponseWriter, r *http.Request, archived bool) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	task, err := s.Store.ArchiveTask(r.Context(), id, archived)
	if err != nil {
		return err
	}
	s.treeChanged()
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), task))
	return nil
}

func (s *server) startTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	task, err := s.Sync.StartTask(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), task))
	return nil
}

// threadTask loads a task that must have a thread (409 otherwise).
func (s *server) threadTask(r *http.Request) (db.Task, error) {
	id, err := pathID(r)
	if err != nil {
		return db.Task{}, err
	}
	task, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return db.Task{}, err
	}
	if task.Session == nil {
		return db.Task{}, db.Conflict("task has no execution session")
	}
	return task, nil
}

func (s *server) stopTask(w http.ResponseWriter, r *http.Request) error {
	task, err := s.threadTask(r)
	if err != nil {
		return err
	}
	adapter, err := s.Sync.Adapter(task.ConnectionID)
	if err != nil {
		return err
	}
	if err := adapter.Stop(r.Context(), *task.Session); err != nil {
		return &db.HarnessError{Err: err}
	}
	return s.touchAndWrite(w, r, task.ID)
}

// touchAndWrite bumps updated_at (re-arms polling), polls the thread once so
// a short run is not missed between 5s poller ticks, and writes the task.
func (s *server) touchAndWrite(w http.ResponseWriter, r *http.Request, id int64) error {
	ctx := r.Context()
	if err := s.Store.TouchTask(ctx, id); err != nil {
		return err
	}
	task, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Sync.PollTask(ctx, task); err != nil {
		s.log.Warn("poll after thread action", "task", id, "err", err)
	} else if task, err = s.Store.GetTask(ctx, id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(s.envID(ctx), task))
	return nil
}

func (s *server) duplicateTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	task, err := s.Store.DuplicateTask(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, s.task(s.envID(r.Context()), task))
	return nil
}

// setNeeds: PUT /api/tasks/{id}/needs {needs} replaces the task's needs
// (docs/api.md "Tree rules"); a task outside a tree joins its needs' tree.
func (s *server) setNeeds(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Needs *[]int64 `json:"needs"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Needs == nil {
		return db.Invalid("needs is required (an empty list clears them)")
	}
	needs := *in.Needs
	if needs == nil {
		needs = []int64{}
	}
	task, err := s.Store.SetNeeds(r.Context(), id, needs)
	if err != nil {
		return err
	}
	s.treeChanged()
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), task))
	return nil
}
