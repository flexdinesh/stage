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
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
	return nil
}

type createScheduleReq struct {
	createTaskReq
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
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.TreeID != nil || req.Needs != nil || req.NeedsSessionID != nil || req.StartMode != nil ||
		req.Start || req.StartLocal != nil || req.StartTz != nil {
		return db.Invalid("treeId, needs, needsSessionId, startMode, start, startLocal and startTz are not schedule fields")
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	if in.Workspace.FromPredecessor() {
		return errSchedulePredecessor
	}
	if err := validCron(req.Cron, req.Tz); err != nil {
		return err
	}
	sc, err := s.Store.CreateSchedule(r.Context(), db.NewSchedule{
		ProjectID: in.ProjectID, Title: in.Title, Prompt: in.Prompt,
		ProviderInstanceID: in.ProviderInstanceID, Model: in.Model, ModelOptions: in.ModelOptions,
		RuntimeMode: in.RuntimeMode, InteractionMode: in.InteractionMode, Workspace: in.Workspace,
		Tags: in.Tags, Notes: in.Notes, AttachmentIDs: in.AttachmentIDs,
		Cron: req.Cron, Tz: req.Tz, Paused: req.Paused, OriginThreadID: in.OriginThreadID,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, sc)
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
	out := scheduleDetailJSON{Schedule: sc, Runs: make([]scheduleRunJSON, len(runs))}
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
	if err := validateConfigPatch(configPatch{
		ProjectID: &p.ProjectID, Title: &p.Title, Prompt: &p.Prompt, ProviderInstanceID: &p.ProviderInstanceID,
		Model: &p.Model, ModelOptions: &p.ModelOptions, RuntimeMode: &p.RuntimeMode, InteractionMode: &p.InteractionMode,
		Workspace: &p.Workspace, Tags: &p.Tags, Notes: &p.Notes, AttachmentIDs: &p.AttachmentIDs,
	}); err != nil {
		return err
	}
	if p.Workspace.Set && p.Workspace.V.FromPredecessor() {
		return errSchedulePredecessor
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
	writeJSON(w, http.StatusOK, sc)
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
