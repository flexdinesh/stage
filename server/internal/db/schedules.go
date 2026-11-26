package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/cronexpr"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type (
	// Schedule is a recurring task template (docs/api.md "Schedules").
	Schedule = store.Schedule
	// ScheduleRun is one fire of a schedule.
	ScheduleRun = store.ScheduleRun
	// ScheduleFilter: ProjectID; DueBefore = unpaused with next_run_at <= it.
	ScheduleFilter = store.ScheduleFilter
	// DueTask is a backlog task whose one-shot start has come.
	DueTask = store.DueTask
)

// Schedule run outcomes.
const (
	RunStarted = "started"
	RunSkipped = "skipped"
	RunMissed  = "missed"
	RunManual  = "manual"
)

func (s *Store) GetSchedule(ctx context.Context, id int64) (Schedule, error) {
	return read(ctx, s, func(tx store.Tx) (Schedule, error) { return tx.Schedules().Get(ctx, id) })
}

// ListSchedules orders by next fire (paused last), then id.
func (s *Store) ListSchedules(ctx context.Context, f ScheduleFilter) ([]Schedule, error) {
	return read(ctx, s, func(tx store.Tx) ([]Schedule, error) { return tx.Schedules().List(ctx, f) })
}

// NewSchedule is a validated create input (cron and tz already parse).
// ConnectionID is required; a nil ExecutionConfig is the harness defaults.
type NewSchedule struct {
	ConnectionID    int64
	ExecutionConfig *execution.Config
	OriginSession   *SessionRef
	ProjectID       int64
	BindingID       *int64
	Title           string
	Prompt          string
	Notes           string
	AttachmentIDs   []int64
	Cron            string
	Tz              string
	Paused          bool
}

// nextRun is the first fire after now, or nil while paused.
func nextRun(cron, tz string, paused bool, now time.Time) (*time.Time, error) {
	spec, err := cronexpr.Parse(cron, tz)
	if err != nil {
		return nil, Invalid("%s", err.Error())
	}
	if paused {
		return nil, nil
	}
	n := spec.Next(now)
	if n.IsZero() {
		return nil, nil
	}
	return &n, nil
}

func (s *Store) CreateSchedule(ctx context.Context, in NewSchedule) (Schedule, error) {
	if err := validateScheduleNotes(in.Notes); err != nil {
		return Schedule{}, err
	}
	next, err := nextRun(in.Cron, in.Tz, in.Paused, time.Now())
	if err != nil {
		return Schedule{}, err
	}
	id, err := write(ctx, s, func(tx store.Tx) (int64, error) {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return 0, err
		}
		project, err := projectID(ctx, tx, in.ProjectID)
		if err != nil {
			return 0, err
		}
		if err := validateBinding(ctx, tx, project, in.ConnectionID, in.BindingID); err != nil {
			return 0, err
		}
		connection := in.ConnectionID
		if connection == 0 {
			return 0, errConnectionRequired
		}
		if err := requireConnection(ctx, tx, connection); err != nil {
			return 0, err
		}
		config := in.ExecutionConfig
		if config == nil {
			config = &execution.Config{Options: []execution.ModelOption{}}
		}
		var originID *int64
		if in.OriginSession != nil {
			value, err := ensureSession(ctx, tx, *in.OriginSession)
			if err != nil {
				return 0, err
			}
			originID = &value
		}
		id, err := tx.Schedules().Insert(ctx, store.ScheduleInsert{
			ProjectID: project, BindingID: in.BindingID, Title: in.Title, Prompt: in.Prompt,
			Notes: in.Notes, Cron: strings.TrimSpace(in.Cron), Tz: in.Tz, Paused: in.Paused, NextRunAt: next,
			OriginSessionID: originID, ExecutionConfig: config,
			ConnectionID: connection, CreatedBy: ActingUser(ctx), SourceMachineID: ActingMachine(ctx),
		})
		if err != nil {
			return 0, err
		}
		if len(in.AttachmentIDs) > 0 {
			return id, setOwned(ctx, tx, store.OwnerSchedule, id, in.AttachmentIDs)
		}
		return id, nil
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.GetSchedule(ctx, id)
}

// SchedulePatch is a partial schedule update (JSON body of PATCH
// /api/schedules/:id).
type SchedulePatch struct {
	ValidatedConnection int64                      `json:"-"`
	ValidatedProject    int64                      `json:"-"`
	ValidatedBinding    *int64                     `json:"-"`
	ConnectionID        Optional[int64]            `json:"connectionId"`
	ExecutionConfig     Optional[execution.Config] `json:"executionConfig"`
	ProjectID           Optional[int64]            `json:"projectId"`
	BindingID           Optional[int64]            `json:"bindingId"`
	Title               Optional[string]           `json:"title"`
	Prompt              Optional[string]           `json:"prompt"`
	Notes               Optional[string]           `json:"notes"`
	AttachmentIDs       Optional[[]int64]          `json:"attachmentIds"` // the exact image set; null = none
	Cron                Optional[string]           `json:"cron"`
	Tz                  Optional[string]           `json:"tz"`
	Paused              Optional[bool]             `json:"paused"`
}

// UpdateSchedule applies a validated patch. Changing cron, tz or paused
// recomputes next_run_at from now (a resume never fires a missed time).
func (s *Store) UpdateSchedule(ctx context.Context, id int64, p SchedulePatch) (Schedule, error) {
	if p.Notes.Set {
		if err := validateScheduleNotes(p.Notes.V); err != nil {
			return Schedule{}, err
		}
	}
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		if err := tx.Schedules().Lock(ctx, id); err != nil {
			return err
		}
		e, err := tx.Schedules().EditState(ctx, id)
		if err != nil {
			return err
		}
		cron, tz, paused := e.Cron, e.Tz, e.Paused
		if p.ValidatedConnection != 0 && (e.ConnectionID != p.ValidatedConnection || e.ProjectID != p.ValidatedProject || !equalPtr(e.BindingID, p.ValidatedBinding)) {
			return Conflict("The destination changed during editing. Refresh and choose the task settings again.")
		}
		if p.AttachmentIDs.Set {
			if err := setOwned(ctx, tx, store.OwnerSchedule, id, p.AttachmentIDs.V); err != nil {
				return err
			}
		}
		u := store.ScheduleUpdate{ConnectionID: p.ConnectionID, ExecutionConfig: p.ExecutionConfig, ProjectID: p.ProjectID,
			Title: p.Title, Prompt: p.Prompt, Notes: p.Notes}
		if p.ConnectionID.Set {
			if err := checkDestinationChange(ctx, tx, e.ConnectionID, p.ConnectionID.V, p.ExecutionConfig); err != nil {
				return err
			}
		}
		if u.ProjectID.Set {
			u.ProjectID.V, err = projectID(ctx, tx, u.ProjectID.V)
			if err != nil {
				return err
			}
		}
		u.BindingID, err = bindingPatch(ctx, tx, e.ProjectID, e.ConnectionID, u.ProjectID, p.ConnectionID, p.BindingID)
		if err != nil {
			return err
		}
		if p.Cron.Set || p.Tz.Set || p.Paused.Set {
			if p.Cron.Set {
				cron = strings.TrimSpace(p.Cron.V)
				u.Cron = Optional[string]{Set: true, V: cron}
			}
			if p.Tz.Set {
				tz = p.Tz.V
				u.Tz = Optional[string]{Set: true, V: tz}
			}
			if p.Paused.Set {
				paused = p.Paused.V
				u.Paused = Optional[bool]{Set: true, V: paused}
			}
			next, err := nextRun(cron, tz, paused, time.Now())
			if err != nil {
				return err
			}
			u.NextRunAt = Optional[time.Time]{Set: true, Null: next == nil}
			if next != nil {
				u.NextRunAt.V = *next
			}
		}
		if !u.ConnectionID.Set && !u.ExecutionConfig.Set && !p.ProjectID.Set && !u.BindingID.Set && !p.Title.Set && !p.Prompt.Set &&
			!p.Notes.Set && !u.NextRunAt.Set && !p.AttachmentIDs.Set {
			return nil
		}
		return tx.Schedules().Update(ctx, id, u)
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.GetSchedule(ctx, id)
}

// DeleteSchedule removes a schedule, its runs and images; spawned tasks stay
// (schedule_id becomes NULL).
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	found, err := write(ctx, s, func(tx store.Tx) (bool, error) { return tx.Schedules().Delete(ctx, id) })
	if err == nil && !found {
		return ErrNotFound
	}
	return err
}

// ScheduleRuns returns the newest limit runs, each with its spawned task.
func (s *Store) ScheduleRuns(ctx context.Context, id int64, limit int) ([]ScheduleRun, error) {
	return read(ctx, s, func(tx store.Tx) ([]ScheduleRun, error) {
		runs, err := tx.Schedules().Runs(ctx, id, limit)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for _, r := range runs {
			if r.TaskID != nil {
				ids = append(ids, *r.TaskID)
			}
		}
		if len(ids) == 0 {
			return runs, nil
		}
		tasks, err := tx.Tasks().ByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		byID := make(map[int64]Task, len(tasks))
		for _, t := range tasks {
			byID[t.ID] = t
		}
		for i, r := range runs {
			if r.TaskID == nil {
				continue
			}
			if t, ok := byID[*r.TaskID]; ok {
				runs[i].Task = &t
			}
		}
		return runs, nil
	})
}

func validateScheduleNotes(notes string) error {
	if !utf8.ValidString(notes) || len(notes) > 16*1024 {
		return Invalid("notes: UTF-8, at most 16 KiB")
	}
	return nil
}

// Legacy templates had no size limit. Number their parts without dropping text,
// including whitespace-only chunks, while keeping each note within 16 KiB.
func scheduleNoteParts(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if len(text) <= 16*1024 {
		return []string{text}
	}
	const heading = "Schedule template (part %d of %d)\n\n"
	// Byte length bounds both the part count and its widest possible number.
	limit := 16*1024 - len(fmt.Sprintf(heading, len(text), len(text)))
	var parts []string
	for len(text) > 0 {
		end := min(limit, len(text))
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		parts = append(parts, text[:end])
		text = text[end:]
	}
	for i := range parts {
		parts[i] = fmt.Sprintf(heading, i+1, len(parts)) + parts[i]
	}
	return parts
}

// spawnTask inserts a backlog task from schedule id's config and images. The
// task and its initial notes attribute to the schedule's creator.
func (s *Store) spawnTask(ctx context.Context, tx store.Tx, id int64, startAt *time.Time) (int64, error) {
	tpl, err := tx.Schedules().Template(ctx, id)
	if err != nil {
		return 0, err
	}
	in := NewTask{ProjectID: tpl.ProjectID, BindingID: tpl.BindingID, Title: tpl.Title, Prompt: tpl.Prompt,
		ConnectionID: tpl.ConnectionID, OriginSession: tpl.OriginSession, ExecutionConfig: tpl.ExecutionConfig}
	tz, notes := tpl.Tz, tpl.Notes
	// The schedule spawns the task, not the request that ran it: no source
	// machine.
	ctx = withoutMachine(WithUser(ctx, tpl.CreatedBy))
	in.ScheduleID = &id
	if startAt != nil {
		in.StartAt, in.StartTz = startAt, &tz
	}
	return s.insertTask(ctx, tx, in, func(tx store.Tx, task int64) error {
		if err := tx.Attachments().CopyToTask(ctx, store.OwnerSchedule, id, task); err != nil {
			return err
		}
		parts := scheduleNoteParts(notes)
		for i, text := range parts {
			key := "initial-note"
			if len(parts) > 1 {
				key = fmt.Sprintf("initial-note-%d", i+1)
			}
			if _, err := appendNote(ctx, tx, JournalOwner{TaskID: &task}, AppendNoteInput{Text: text, RequestID: key, SourceSession: in.OriginSession}); err != nil {
				return err
			}
		}
		return nil
	})
}

// DueSchedules: unpaused schedules whose next fire is at or before now.
func (s *Store) DueSchedules(ctx context.Context, now time.Time, connection int64) ([]int64, error) {
	return read(ctx, s, func(tx store.Tx) ([]int64, error) { return tx.Schedules().Due(ctx, now, connection) })
}

// DueOneShots: backlog tasks with start_at at or before now, oldest first
// (archived tasks and members of a done or archived tree wait).
func (s *Store) DueOneShots(ctx context.Context, now time.Time) ([]DueTask, error) {
	return s.DueForConnection(ctx, now, 0)
}
func (s *Store) DueForConnection(ctx context.Context, now time.Time, connection int64) ([]DueTask, error) {
	return read(ctx, s, func(tx store.Tx) ([]DueTask, error) { return tx.Schedules().DueTasks(ctx, now, connection) })
}

// FireResult is what FireSchedule did ("" outcome: not due, nothing written).
type FireResult struct {
	Outcome string
	TaskID  *int64
}

// FireSchedule handles one due fire of schedule id at now, in one
// transaction with the row locked (docs/api.md "Schedule rules"): a fire
// older than grace is recorded missed; a previous spawned task still pending
// or active skips it; else a task is spawned with start_at = the fire time.
// next_run_at always advances past now.
func (s *Store) FireSchedule(ctx context.Context, id int64, now time.Time, grace time.Duration) (FireResult, error) {
	var res FireResult
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		res = FireResult{}
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		sc := tx.Schedules()
		// One spawn per fire time: the next fire advances under the lock.
		if err := sc.Lock(ctx, id); err != nil {
			return err
		}
		fs, err := sc.FireState(ctx, id)
		if err != nil {
			return err
		}
		cron, tz, paused, next := fs.Cron, fs.Tz, fs.Paused, fs.NextRunAt
		if paused || next == nil || next.After(now) {
			return nil
		}
		spec, err := cronexpr.Parse(cron, tz)
		if err != nil {
			return fmt.Errorf("schedule %d: %w", id, err)
		}
		fireAt := *next
		advance := spec.Next(now)
		switch {
		case now.Sub(fireAt) > grace:
			n := 0
			for t := fireAt; !t.IsZero() && !t.After(now) && n < 100000; t = spec.Next(t) {
				n++
			}
			detail := fmt.Sprintf("missed %d fires", n)
			if n == 1 {
				detail = "missed 1 fire"
			}
			res.Outcome = RunMissed
			err = sc.InsertRun(ctx, id, fireAt, RunMissed, nil, detail)
		default:
			var prev int64
			var prevState TaskState
			prev, prevState, err = sc.PendingTask(ctx, id)
			switch {
			case err == nil:
				res.Outcome = RunSkipped
				err = sc.InsertRun(ctx, id, fireAt, RunSkipped, nil, fmt.Sprintf("#%d still %s", prev, stateWords(prevState)))
			case errors.Is(err, ErrNotFound):
				var task int64
				if task, err = s.spawnTask(ctx, tx, id, &fireAt); err != nil {
					return err
				}
				res.Outcome, res.TaskID = RunStarted, &task
				err = sc.InsertRun(ctx, id, fireAt, RunStarted, &task, "")
			}
		}
		if err != nil {
			return err
		}
		var nextAt *time.Time
		if !advance.IsZero() {
			nextAt = &advance
		}
		return sc.SetNextRun(ctx, id, nextAt)
	})
	if err != nil {
		return FireResult{}, err
	}
	return res, nil
}

// stateWords renders a state for run details ("needs input", "pending").
func stateWords(s TaskState) string {
	if s == StateBacklog {
		return "pending"
	}
	return strings.ReplaceAll(string(s), "_", " ")
}

// RunScheduleNow spawns a backlog task from the schedule (no start time) and
// records a manual run; the caller starts it.
func (s *Store) RunScheduleNow(ctx context.Context, id int64) (Task, error) {
	var task int64
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		if err := tx.Schedules().Lock(ctx, id); err != nil {
			return err
		}
		exists, err := tx.Schedules().Exists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if task, err = s.spawnTask(ctx, tx, id, nil); err != nil {
			return err
		}
		return tx.Schedules().InsertRun(ctx, id, time.Now(), RunManual, &task, "run now")
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, task)
}
