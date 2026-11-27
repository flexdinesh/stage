package api

import (
	"errors"
	"net/http"
	"slices"
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
	if f.ProjectID, err = s.projectFilter(r, "project"); err != nil {
		return err
	}
	f.States = multi(q, "state")
	for _, st := range f.States {
		if st == "settled" {
			return db.Invalid("state settled was removed in API v9; use state done (completedAt marks acceptance)")
		}
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
	if f.CompletedFrom, err = queryCompletedFrom(q); err != nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.tasks(ts)})
	return nil
}

type createTaskReq struct {
	taskConfigReq
	InitialNote string `json:"initialNote"`
}

type taskConfigReq struct {
	ConnectionID    *int64            `json:"connectionId"`
	BindingID       *int64            `json:"bindingId"`
	NeedsSession    *db.SessionRef    `json:"needsSession"`
	ExecutionConfig *execution.Config `json:"executionConfig"`
	OriginSession   *db.SessionRef    `json:"originSession"`
	ProjectID       *int64            `json:"projectId"`
	Title           string            `json:"title"`
	Prompt          string            `json:"prompt"`
	TreeID          *int64            `json:"treeId"`
	Needs           []int64           `json:"needs"`
	StartMode       *db.StartMode     `json:"startMode"`
	AttachmentIDs   []int64           `json:"attachmentIds"`
	StartLocal      *string           `json:"startLocal"`
	StartTz         *string           `json:"startTz"`
	Handoff         *bool             `json:"handoff"`
	Start           bool              `json:"start"`
}

// chooseConnection resolves the project alias and uses an explicit binding's
// connection when the caller leaves connectionId unset.
func (s *server) chooseConnection(r *http.Request, req *taskConfigReq) error {
	ctx := r.Context()
	if req.ConnectionID != nil {
		if req.ProjectID != nil {
			id, err := s.projectID(ctx, *req.ProjectID)
			if err != nil {
				return err
			}
			req.ProjectID = &id
		}
		return s.plannedConnection(ctx, *req.ConnectionID)
	}
	if req.ProjectID == nil {
		return nil
	}
	project, err := s.Store.GetProject(ctx, *req.ProjectID)
	if err != nil {
		return err
	}
	req.ProjectID = &project.ID
	if req.BindingID != nil {
		for _, b := range project.Bindings {
			if b.ID == *req.BindingID {
				req.ConnectionID = &b.ConnectionID
				return s.plannedConnection(ctx, b.ConnectionID)
			}
		}
		return db.ErrInvalidBinding
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
		return db.Invalid("connectionId is required: this project has no harness binding")
	default:
		return db.Invalid("This project has multiple harness bindings. Choose a connectionId.")
	}
}

func (req createTaskReq) validate() (db.NewTask, error) {
	in, err := req.taskConfigReq.validate()
	in.InitialNote = req.InitialNote
	return in, err
}

// validate checks the fields Stage owns; an omitted executionConfig runs
// on the harness defaults (its adapter validates it: validateSelection).
func (req taskConfigReq) validate() (db.NewTask, error) {
	in := db.NewTask{
		BindingID:       req.BindingID,
		NeedsSession:    req.NeedsSession,
		ExecutionConfig: req.ExecutionConfig,
		OriginSession:   req.OriginSession,
		Title:           strings.TrimSpace(req.Title),
		Prompt:          req.Prompt,
		TreeID:          req.TreeID,
		Needs:           req.Needs,
		StartMode:       req.StartMode,
		AttachmentIDs:   req.AttachmentIDs,
		Handoff:         req.Handoff,
	}
	if req.ConnectionID == nil {
		return in, db.Invalid("connectionId is required: choose a harness connection")
	}
	if *req.ConnectionID <= 0 {
		return in, db.Invalid("choose a positive connectionId")
	}
	in.ConnectionID = *req.ConnectionID
	if in.ExecutionConfig == nil {
		in.ExecutionConfig = &execution.Config{}
	}
	if in.StartMode != nil && !in.StartMode.Valid() {
		return in, db.Invalid("invalid startMode %q (want auto|manual)", *in.StartMode)
	}
	hasNeeds := len(in.Needs) > 0 || in.NeedsSession != nil
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
	var err error
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
	if err := decodeConfig(r, &req, true); err != nil {
		return err
	}
	if err := s.chooseConnection(r, &req.taskConfigReq); err != nil {
		return err
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	if err := s.validateTaskDestination(r.Context(), in); err != nil {
		return err
	}
	if in.NeedsSession != nil {
		in.NeedsWorkspace, err = s.prepareAdoption(r.Context(), *in.NeedsSession)
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
		if _, err := s.Engine.Coordinator.StartTask(r.Context(), task.ID); err != nil {
			s.log.Warn("start after create", "task", task.ID, "err", err)
			if noteErr := s.Store.NoteBlockedLaunch(r.Context(), task.ID, err.Error()); noteErr != nil {
				return noteErr
			}
		}
		if task, err = s.Store.GetTask(r.Context(), task.ID); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusCreated, s.task(task))
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
	s.loadTaskAttempt(ctx, &task)
	out := taskDetailJSON{taskJSON: taskJSON{Task: task}, Events: events, CurrentHandoffs: []db.Handoff{}}
	if task.Session != nil {
		obs, e := s.Store.Observation(ctx, *task.Session)
		if e == nil {
			out.Links, out.FinalReply, out.Usage = obs.Links, obs.Reply, obs.Usage
			d, _ := s.registry().Lookup(task.Harness)
			out.Facts = d.DisplayFacts(obs.Facts)
			if obs.Branch != "" || obs.Directory != "" {
				out.Reported = &reportedJSON{Branch: obs.Branch, Directory: obs.Directory}
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
		shaped := s.treeDetail(tree)
		out.Tree = &shaped
	}
	if task.LaunchAttempt > 0 {
		if out.CurrentHandoffs, err = s.Store.GetHandoffs(ctx, id, task.LaunchAttempt); err != nil {
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
	if err := decodeConfig(r, &p, true); err != nil {
		return err
	}
	current, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return err
	}
	p.ValidatedConnection, p.ValidatedProject, p.ValidatedBinding = current.ConnectionID, current.ProjectID, current.BindingID
	if err := validateConfigPatch(configPatch{
		ProjectID: &p.ProjectID, Title: &p.Title, Prompt: &p.Prompt,
		AttachmentIDs: &p.AttachmentIDs,
	}); err != nil {
		return err
	}
	original := taskDestination{current.ConnectionID, current.ProjectID, current.BindingID}
	if err := s.validateDestinationPatch(r.Context(), original, p.ConnectionID, &p.ProjectID, p.BindingID,
		current.ExecutionConfig, p.ExecutionConfig, current.Attachments, p.AttachmentIDs); err != nil {
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
	writeJSON(w, http.StatusOK, s.task(task))
	return nil
}

// configPatch points at the task-config fields shared by task and schedule
// patches.
type configPatch struct {
	ProjectID     *db.Optional[int64]
	Title         *db.Optional[string]
	Prompt        *db.Optional[string]
	AttachmentIDs *db.Optional[[]int64]
}

// validateConfigPatch validates and normalizes the shared config fields.
func validateConfigPatch(c configPatch) error {
	if c.ProjectID.Set && c.ProjectID.Null {
		return db.Invalid("projectId must not be null")
	}
	if err := requiredText("title", c.Title); err != nil {
		return err
	}
	if c.Prompt.Set && (c.Prompt.Null || strings.TrimSpace(c.Prompt.V) == "") {
		return db.Invalid("prompt must be a non-empty string")
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
	writeJSON(w, http.StatusOK, s.task(task))
	return nil
}

func (s *server) startTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	task, err := s.Engine.Coordinator.StartTask(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(task))
	return nil
}

// sessionTask loads a task that must have an execution session (409 otherwise).
func (s *server) sessionTask(r *http.Request) (db.Task, error) {
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
	task, err := s.sessionTask(r)
	if err != nil {
		return err
	}
	adapter, err := s.Engine.Connections.Adapter(task.ConnectionID)
	if err != nil {
		return err
	}
	// A catalog outage does not prevent the owning adapter from stopping a
	// running session. A negative declaration still prevents dispatch.
	caps, catalogErr := adapter.Features(r.Context())
	if op, declared := caps.Operations[execution.OpStop]; !op.Available && (catalogErr == nil || declared) {
		reason := strings.TrimSpace(op.Reason)
		if reason == "" {
			reason = "This connection does not support stopping tasks."
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: reason}
	}
	if err := adapter.Stop(r.Context(), *task.Session); err != nil {
		return &db.HarnessError{Err: err}
	}
	return s.touchAndWrite(w, r, task.ID)
}

// touchAndWrite bumps updated_at (re-arms polling), polls the session once so
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
	if err := s.Engine.Coordinator.PollTask(ctx, task); err != nil {
		s.log.Warn("poll after session action", "task", id, "err", err)
	} else if task, err = s.Store.GetTask(ctx, id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(task))
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
	writeJSON(w, http.StatusCreated, s.task(task))
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
	writeJSON(w, http.StatusOK, s.task(task))
	return nil
}
