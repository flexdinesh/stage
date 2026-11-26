package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/cronexpr"
	"github.com/flexdinesh/stage/server/internal/db"
)

// scheduleRunsLimit: runs returned on a schedule detail read.
const scheduleRunsLimit = 50

type scheduleRunJSON struct {
	db.ScheduleRun
	Task *taskJSON `json:"task"`
}

type scheduleDetailJSON struct {
	db.Schedule
	Runs []scheduleRunJSON `json:"runs"`
}

func (s *server) scheduleJSON(sc db.Schedule) db.Schedule {
	return sc
}

func (s *server) listSchedules(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	var f db.ScheduleFilter
	var err error
	if f.ProjectID, err = queryInt64(q, "project"); err != nil {
		return err
	}
	if v := strings.TrimSpace(q.Get("dueWithin")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return db.Invalid("invalid dueWithin %q (want a duration, e.g. 24h)", v)
		}
		before := time.Now().Add(d)
		f.DueBefore = &before
	}
	out, err := s.Store.ListSchedules(r.Context(), f)
	if err != nil {
		return err
	}
	for i := range out {
		out[i] = s.scheduleJSON(out[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
	return nil
}

type createScheduleReq struct {
	taskConfigReq
	Notes  string `json:"notes"`
	Cron   string `json:"cron"`
	Tz     string `json:"tz"`
	Paused bool   `json:"paused"`
}

// validCron checks cron in zone tz (400 with the parse error).
func validCron(cron, tz string) error {
	if _, err := cronexpr.Parse(cron, tz); err != nil {
		return db.Invalid("%s", err.Error())
	}
	return nil
}

// errSchedulePredecessor: a schedule's runs have no predecessor.
var errSchedulePredecessor = db.Invalid("workspace.base predecessor is not a schedule option (schedule runs have no needs)")

func (s *server) createSchedule(w http.ResponseWriter, r *http.Request) error {
	var req createScheduleReq
	if err := decodeStrict(r, &req); err != nil {
		return err
	}
	if req.TreeID != nil || req.Needs != nil || req.NeedsSession != nil || req.StartMode != nil ||
		req.Start || req.StartLocal != nil || req.StartTz != nil {
		return db.Invalid("treeId, needs, needsSession, startMode, start, startLocal and startTz are not schedule fields")
	}
	if err := s.chooseConnection(r, &req.taskConfigReq); err != nil {
		return err
	}
	in, err := req.validate(s.Store.IsT3, s.worktreeRule)
	if err != nil {
		return err
	}
	if err := s.validateSelection(r.Context(), in.ConnectionID, in.ProjectID, in.ExecutionConfig); err != nil {
		return err
	}
	if len(in.AttachmentIDs) > 0 {
		if err := s.requireImages(r.Context(), in.ConnectionID, in.ProjectID); err != nil {
			return err
		}
	}
	if in.Workspace.FromPredecessor() {
		return errSchedulePredecessor
	}
	if err := validCron(req.Cron, req.Tz); err != nil {
		return err
	}
	sc, err := s.Store.CreateSchedule(r.Context(), db.NewSchedule{
		ExecutionConfig: in.ExecutionConfig, ConnectionID: in.ConnectionID,
		ProjectID: in.ProjectID, Title: in.Title, Prompt: in.Prompt,
		ProviderInstanceID: in.ProviderInstanceID, Model: in.Model, ModelOptions: in.ModelOptions,
		RuntimeMode: in.RuntimeMode, InteractionMode: in.InteractionMode, Workspace: in.Workspace,
		Notes: req.Notes, AttachmentIDs: in.AttachmentIDs,
		Cron: req.Cron, Tz: req.Tz, Paused: req.Paused, OriginSession: in.OriginSession,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, s.scheduleJSON(sc))
	return nil
}

func (s *server) getSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	sc, err := s.Store.GetSchedule(ctx, id)
	if err != nil {
		return err
	}
	runs, err := s.Store.ScheduleRuns(ctx, id, scheduleRunsLimit)
	if err != nil {
		return err
	}
	env := s.envID(ctx)
	out := scheduleDetailJSON{Schedule: s.scheduleJSON(sc), Runs: make([]scheduleRunJSON, len(runs))}
	for i, run := range runs {
		out.Runs[i] = scheduleRunJSON{ScheduleRun: run}
		if run.Task != nil {
			tj := s.task(env, *run.Task)
			out.Runs[i].Task = &tj
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) updateSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var p db.SchedulePatch
	if err := decode(r, &p); err != nil {
		return err
	}
	current, err := s.Store.GetSchedule(r.Context(), id)
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
	if !s.Store.IsT3(connection) && (p.ProviderInstanceID.Set || p.Model.Set || p.RuntimeMode.Set || p.ModelOptions.Set || p.InteractionMode.Set) {
		return db.Invalid("Choose harness-specific configuration.")
	}
	projectID := current.ProjectID
	if p.ProjectID.Set {
		projectID = p.ProjectID.V
	}
	if err := s.requirePatchImages(r.Context(), connection, projectID, connection != current.ConnectionID, current.Attachments, p.AttachmentIDs); err != nil {
		return err
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
	}, s.worktreeRule); err != nil {
		return err
	}
	if p.Workspace.Set && p.Workspace.V.FromPredecessor() {
		return errSchedulePredecessor
	}
	if p.Notes.Set && p.Notes.Null {
		p.Notes.V, p.Notes.Null = "", false
	}
	for name, o := range map[string]db.Optional[string]{"cron": p.Cron, "tz": p.Tz} {
		if o.Set && o.Null {
			return db.Invalid("%s must not be null", name)
		}
	}
	if p.Paused.Set && p.Paused.Null {
		return db.Invalid("paused must not be null")
	}
	sc, err := s.Store.UpdateSchedule(r.Context(), id, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.scheduleJSON(sc))
	return nil
}

func (s *server) deleteSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSchedule(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// runSchedule is Run now: spawn a task and start it (launch errors land on
// the task).
func (s *server) runSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	task, err := s.Store.RunScheduleNow(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.Sync.StartTask(ctx, task.ID); err != nil {
		s.log.Warn("start schedule run", "schedule", id, "task", task.ID, "err", err)
	}
	if task, err = s.Store.GetTask(ctx, task.ID); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, s.task(s.envID(ctx), task))
	return nil
}

// previewSchedule returns the next three fires of cron in tz.
func (s *server) previewSchedule(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Cron string `json:"cron"`
		Tz   string `json:"tz"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	spec, err := cronexpr.Parse(body.Cron, body.Tz)
	if err != nil {
		return db.Invalid("%s", err.Error())
	}
	next := []time.Time{}
	t := time.Now()
	for range 3 {
		if t = spec.Next(t); t.IsZero() {
			break
		}
		next = append(next, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": next})
	return nil
}
