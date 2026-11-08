package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
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
	f.Tag = strings.TrimSpace(q.Get("tag"))
	f.Q = strings.TrimSpace(q.Get("q"))
	if f.From, err = queryTime(q, "from", time.UTC); err != nil {
		return err
	}
	if f.To, err = queryTime(q, "to", time.UTC); err != nil {
		return err
	}
	if f.SettledFrom, err = queryTime(q, "settledFrom", time.UTC); err != nil {
		return err
	}
	ts, err := s.Store.ListTasks(r.Context(), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.tasks(s.envID(r.Context()), ts)})
	return nil
}

type createTaskReq struct {
	ProjectID          *int64           `json:"projectId"`
	Title              string           `json:"title"`
	Prompt             string           `json:"prompt"`
	ProviderInstanceID *string          `json:"providerInstanceId"`
	Model              *string          `json:"model"`
	ModelOptions       []t3.ModelOption `json:"modelOptions"`
	RuntimeMode        *string          `json:"runtimeMode"`
	InteractionMode    *string          `json:"interactionMode"`
	Workspace          *db.Workspace    `json:"workspace"`
	Tags               []string         `json:"tags"`
	Notes              *string          `json:"notes"`
	AfterTaskID        *int64           `json:"afterTaskId"`
	AttachmentIDs      []int64          `json:"attachmentIds"`
	Start              bool             `json:"start"`
}

func (req createTaskReq) validate() (db.NewTask, error) {
	in := db.NewTask{
		Title:              strings.TrimSpace(req.Title),
		Prompt:             req.Prompt,
		ProviderInstanceID: trimmedPtr(req.ProviderInstanceID),
		Model:              trimmedPtr(req.Model),
		RuntimeMode:        "full-access",
		InteractionMode:    "default",
		Workspace:          db.Workspace{Type: "root"},
		Tags:               cleanTags(req.Tags),
		AfterTaskID:        req.AfterTaskID,
		AttachmentIDs:      req.AttachmentIDs,
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
		if in.Workspace, err = validateWorkspace(*req.Workspace); err != nil {
			return in, err
		}
	}
	if req.Notes != nil {
		in.Notes = *req.Notes
	}
	return in, nil
}

func (s *server) createTask(w http.ResponseWriter, r *http.Request) error {
	var req createTaskReq
	if err := decode(r, &req); err != nil {
		return err
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	task, err := s.Store.CreateTask(r.Context(), in)
	if err != nil {
		return err
	}
	if err := s.Store.SetLastUsed(r.Context(), db.ModelDefaults{
		ProviderInstanceID: in.ProviderInstanceID, Model: in.Model, ModelOptions: in.ModelOptions,
		RuntimeMode: in.RuntimeMode, InteractionMode: in.InteractionMode,
	}); err != nil {
		s.log.Warn("record last-used model", "task", task.ID, "err", err)
	}
	if req.Start && req.AfterTaskID == nil {
		// Start errors after save: the task stays created (failed + lastError).
		if _, err := s.Sync.StartTask(r.Context(), task.ID); err != nil {
			s.log.Warn("start after create", "task", task.ID, "err", err)
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
	seq, err := s.Store.Sequence(ctx, id)
	if err != nil {
		return err
	}
	out := taskDetailJSON{taskJSON: s.task(env, task), Events: events, Sequence: s.tasks(env, seq)}
	if task.T3ThreadID != nil {
		th, err := s.Store.GetThread(ctx, *task.T3ThreadID)
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
	if err := decode(r, &p); err != nil {
		return err
	}
	if p.ProjectID.Set && p.ProjectID.Null {
		return db.Invalid("projectId must not be null")
	}
	if err := requiredText("title", &p.Title); err != nil {
		return err
	}
	if p.Prompt.Set && (p.Prompt.Null || strings.TrimSpace(p.Prompt.V) == "") {
		return db.Invalid("prompt must be a non-empty string")
	}
	nullableText(&p.ProviderInstanceID)
	nullableText(&p.Model)
	if p.ModelOptions.Set {
		if p.ModelOptions.V, err = validateOptions(p.ModelOptions.V); err != nil {
			return err
		}
		p.ModelOptions.Null = false
	}
	if err := enumField("runtimeMode", &p.RuntimeMode, db.RuntimeModes); err != nil {
		return err
	}
	if err := enumField("interactionMode", &p.InteractionMode, db.InteractionModes); err != nil {
		return err
	}
	if p.Workspace.Set {
		if p.Workspace.Null {
			return db.Invalid("workspace must not be null")
		}
		if p.Workspace.V, err = validateWorkspace(p.Workspace.V); err != nil {
			return err
		}
	}
	if p.Tags.Set {
		p.Tags.V, p.Tags.Null = cleanTags(p.Tags.V), false
	}
	if p.Notes.Set && p.Notes.Null {
		p.Notes.V, p.Notes.Null = "", false
	}
	if p.AttachmentIDs.Set {
		if err := attachmentIDs(p.AttachmentIDs.V); err != nil {
			return err
		}
		p.AttachmentIDs.Null = false
	}
	task, err := s.Store.UpdateTask(r.Context(), id, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), task))
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
	w.WriteHeader(http.StatusNoContent)
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
	if task.T3ThreadID == nil {
		return db.Task{}, db.Conflict("task has no thread")
	}
	return task, nil
}

// followupTask sends a message (t3_thread_send) or, with attachmentIds
// (staged uploads), the images and optional message
// (t3_thread_send_attachments, no mode). Sent images are deleted; on failure
// they stay staged so the user can resend.
func (s *server) followupTask(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Message       string  `json:"message"`
		Mode          *string `json:"mode"`
		AttachmentIDs []int64 `json:"attachmentIds"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if len(body.AttachmentIDs) > 0 {
		return s.followupImages(w, r, body.Message, body.Mode, body.AttachmentIDs)
	}
	if strings.TrimSpace(body.Message) == "" {
		return db.Invalid("message is required")
	}
	mode := t3.SendAuto
	if body.Mode != nil {
		if err := validEnum("mode", *body.Mode, sendModes); err != nil {
			return err
		}
		mode = t3.SendMode(*body.Mode)
	}
	task, err := s.threadTask(r)
	if err != nil {
		return err
	}
	if err := s.T3.SendMessage(r.Context(), *task.T3ThreadID, body.Message, mode); err != nil {
		return &db.T3Error{Err: err}
	}
	return s.touchAndWrite(w, r, task.ID)
}

func (s *server) followupImages(w http.ResponseWriter, r *http.Request, message string, mode *string, ids []int64) error {
	if mode != nil {
		return db.Invalid("mode is not supported with images")
	}
	if err := attachmentIDs(ids); err != nil {
		return err
	}
	task, err := s.threadTask(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	images, err := s.Store.StagedAttachments(ctx, ids)
	if err != nil {
		return err
	}
	uploaded, err := s.Sync.UploadImages(ctx, images)
	if err != nil {
		return &db.T3Error{Err: err}
	}
	if err := s.T3.SendAttachments(ctx, *task.T3ThreadID, message, uploaded); err != nil {
		s.Sync.DiscardUploads(ctx, uploaded)
		return &db.T3Error{Err: err}
	}
	if err := s.Store.DeleteStagedAttachments(ctx, ids); err != nil {
		s.log.Warn("delete sent follow-up images", "task", task.ID, "err", err)
	}
	return s.touchAndWrite(w, r, task.ID)
}

func (s *server) stopTask(w http.ResponseWriter, r *http.Request) error {
	task, err := s.threadTask(r)
	if err != nil {
		return err
	}
	if err := s.T3.Interrupt(r.Context(), *task.T3ThreadID); err != nil {
		return &db.T3Error{Err: err}
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
