package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *Store) GetTask(ctx context.Context, id int64) (Task, error) {
	task, err := read(ctx, s, func(tx store.Tx) (Task, error) { return tx.Tasks().Get(ctx, id) })
	task.WorkspaceID = selectorOf(task.ConnectionID, task.WorkspacePin)
	return task, err
}

type TaskFilter = store.TaskFilter

func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	return read(ctx, s, func(tx store.Tx) ([]Task, error) { return tx.Tasks().List(ctx, f) })
}

// PollableTasks: tasks whose session may still change state. Active tasks
// always; done tasks updated in the last hour always, older uncompleted
// ones only when includeStale (the poller passes it at most once a minute);
// failed/interrupted/cancelled when touched in the last 10 minutes (e.g. a
// follow-up restarted the session) or the mirror shows the session active.
// An adopted backlog session is watched for its first native execution.
func (s *Store) PollableTasks(ctx context.Context, includeStale bool) ([]Task, error) {
	return read(ctx, s, func(tx store.Tx) ([]Task, error) { return tx.Tasks().Pollable(ctx, includeStale) })
}

func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]TaskEvent, error) {
	return read(ctx, s, func(tx store.Tx) ([]TaskEvent, error) { return tx.Tasks().Events(ctx, taskID) })
}

// NewTask is a validated create input. ConnectionID is required; a nil
// ExecutionConfig is the harness defaults.
type NewTask struct {
	ExecutionConfig     *execution.Config
	ConnectionID        int64
	OriginSession       *SessionRef
	ProjectID           int64
	BindingID           *int64
	WorkspaceID         string
	UnresolvedWorkspace bool
	workspacePin        *WorkspacePin
	Title               string
	Prompt              string
	InitialNote         string
	AttachmentIDs       []int64 // staged images to attach (setAttachments)
	StartAt             *time.Time
	StartTz             *string // set with StartAt (IANA zone)
	ScheduleID          *int64  // spawning schedule
	Handoff             *bool   // append the needs' handoff at launch; nil = default (on)
	// TreeID: join this tree (docs/api.md "Tree rules"); nil with Needs joins
	// the needs' tree (or a new one).
	TreeID *int64
	// Needs: tasks this one needs (setNeeds rules).
	Needs []int64
	// NeedsSession ("afterMe"): adopt this session (or take its owning
	// task) and need it, before Needs.
	NeedsSession   *SessionRef
	NeedsWorkspace *AdoptionWorkspace
	StartMode      *StartMode // nil = inherit the tree's
}

// errConnectionRequired: no harness connection was chosen.
var errConnectionRequired = Invalid("connectionId is required: choose a harness connection")

// errStartAndNeeds: a task waits on a start time or on needs, not both.
var errStartAndNeeds = Invalid("a task can have a start time or needs, not both")

// CreateTask inserts a backlog task (+ creation event) and places it in its
// tree with its needs (the whole insert fails on a rule violation).
func (s *Store) CreateTask(ctx context.Context, in NewTask) (Task, error) {
	id, err := write(ctx, s, func(tx store.Tx) (int64, error) {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return 0, err
		}
		if err := connectionUsable(ctx, tx, in.ConnectionID); err != nil {
			return 0, err
		}
		in, err := taskCreationDestination(ctx, tx, in)
		if err != nil {
			return 0, err
		}
		return s.insertTask(ctx, tx, in, nil)
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func taskCreationDestination(ctx context.Context, tx store.Tx, in NewTask) (NewTask, error) {
	if in.UnresolvedWorkspace {
		if in.BindingID != nil || in.WorkspaceID != "" || in.StartAt != nil {
			return in, DestinationRequired()
		}
		project, err := projectID(ctx, tx, in.ProjectID)
		in.ProjectID = project
		return in, err
	}
	project, pin, err := creationDestination(ctx, tx, in.ConnectionID, in.ProjectID, in.BindingID, in.WorkspaceID)
	in.ProjectID, in.workspacePin = project, pin
	return in, err
}

// insertTask is createTask's insert, inside tx: insert, adopt
// NeedsSession, resolve and join the tree, set needs.
func (s *Store) insertTask(ctx context.Context, tx store.Tx, in NewTask, extra func(tx store.Tx, id int64) error) (int64, error) {
	if in.StartAt != nil && (len(in.Needs) > 0 || in.NeedsSession != nil) {
		return 0, errStartAndNeeds
	}
	if err := requireProject(ctx, tx, in.ProjectID); err != nil {
		return 0, err
	}
	var originID *int64
	if in.ConnectionID == 0 {
		return 0, errConnectionRequired
	}
	if err := requireConnection(ctx, tx, in.ConnectionID); err != nil {
		return 0, err
	}
	config := in.ExecutionConfig
	if config == nil {
		config = &execution.Config{Options: []execution.ModelOption{}}
	}
	if in.OriginSession != nil {
		value, err := ensureSession(ctx, tx, *in.OriginSession)
		if err != nil {
			return 0, err
		}
		originID = &value
	}
	id, err := tx.Tasks().Insert(ctx, store.TaskInsert{
		ProjectID: in.ProjectID, BindingID: in.BindingID, WorkspacePin: in.workspacePin, Title: in.Title, Prompt: in.Prompt,
		StartAt: in.StartAt, StartTz: in.StartTz, ScheduleID: in.ScheduleID, Handoff: in.Handoff,
		StartMode: in.StartMode, ConnectionID: in.ConnectionID, OriginSessionID: originID, ExecutionConfig: config,
		CreatedBy: ActingUser(ctx), SourceMachineID: ActingMachine(ctx),
	})
	if err != nil {
		return 0, err
	}

	needs := slices.Clone(in.Needs)
	if in.NeedsSession != nil {
		owner, _, err := s.adoptSession(ctx, tx, AdoptInput{Workspace: in.NeedsWorkspace, Session: *in.NeedsSession})
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
	if err := addTaskEvent(ctx, tx, id, nil, StateBacklog, "created"); err != nil {
		return 0, err
	}
	if len(in.AttachmentIDs) > 0 {
		if err := setOwned(ctx, tx, store.OwnerTask, id, in.AttachmentIDs); err != nil {
			return 0, err
		}
	}
	if in.InitialNote != "" {
		if _, err := appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: in.InitialNote, RequestID: "initial-note", SourceSession: in.OriginSession}); err != nil {
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

// TaskPatch is a partial task update (JSON body of PATCH /api/tasks/:id).
// A null bindingId selects the primary. A null startMode inherits the tree mode.
type TaskPatch struct {
	ValidatedConnection int64                      `json:"-"`
	ValidatedProject    int64                      `json:"-"`
	ValidatedBinding    *int64                     `json:"-"`
	ValidatedWorkspace  *WorkspacePin              `json:"-"`
	ConnectionID        Optional[int64]            `json:"connectionId"`
	ExecutionConfig     Optional[execution.Config] `json:"executionConfig"`
	ProjectID           Optional[int64]            `json:"projectId"`
	BindingID           Optional[int64]            `json:"bindingId"`
	WorkspaceID         Optional[string]           `json:"workspaceId"`
	Title               Optional[string]           `json:"title"`
	Prompt              Optional[string]           `json:"prompt"`
	AttachmentIDs       Optional[[]int64]          `json:"attachmentIds"` // the exact image set; null = none
	Handoff             Optional[bool]             `json:"handoff"`       // append the needs' handoff at launch
	StartMode           Optional[StartMode]        `json:"startMode"`     // null = inherit the tree's
	// One-shot start: the API converts startLocal (wall time in startTz) into
	// StartAt; StartAt and StartTz are set (or null) together.
	StartLocal Optional[string]    `json:"startLocal"`
	StartTz    Optional[string]    `json:"startTz"`
	StartAt    Optional[time.Time] `json:"-"`
}

// restrictedSet reports whether a field editable only in backlog is present.
func (p TaskPatch) restrictedSet() bool {
	return p.ConnectionID.Set || p.ExecutionConfig.Set || p.ProjectID.Set || p.BindingID.Set || p.WorkspaceID.Set || p.Prompt.Set || p.AttachmentIDs.Set ||
		p.StartAt.Set || p.Handoff.Set || p.StartMode.Set
}

// UpdateTask applies a validated patch. Outside backlog only the title
// may change (Conflict otherwise).
func (s *Store) UpdateTask(ctx context.Context, id int64, p TaskPatch) (Task, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		if err := tx.Tasks().Lock(ctx, id); err != nil {
			return err
		}
		e, err := tx.Tasks().EditState(ctx, id)
		if err != nil {
			return err
		}
		if p.ValidatedConnection != 0 && (e.ConnectionID != p.ValidatedConnection || e.ProjectID != p.ValidatedProject ||
			!equalPtr(e.BindingID, p.ValidatedBinding) || !equalPtr(e.WorkspacePin, p.ValidatedWorkspace)) {
			return Conflict("The destination changed during editing. Refresh and choose the task settings again.")
		}
		if e.State != StateBacklog && p.restrictedSet() {
			return Conflict("task is %s: only the title is editable outside backlog", e.State)
		}
		if e.Adopted && p.restrictedSet() {
			return Conflict("only the title is editable for an adopted session")
		}
		if p.StartAt.Set && !p.StartAt.Null && e.Needs > 0 {
			return errStartAndNeeds
		}
		if p.StartAt.Set {
			if err := tx.Tasks().SetStart(ctx, id, p.StartAt.Ptr(), p.StartTz.Ptr()); err != nil {
				return err
			}
		}
		if p.AttachmentIDs.Set {
			if err := setOwned(ctx, tx, store.OwnerTask, id, p.AttachmentIDs.V); err != nil {
				return err
			}
		}
		u := store.TaskUpdate{ConnectionID: p.ConnectionID, ExecutionConfig: p.ExecutionConfig, ProjectID: p.ProjectID,
			Title: p.Title, Prompt: p.Prompt, Handoff: p.Handoff, StartMode: p.StartMode}
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
		u.BindingID, u.WorkspacePin, err = destinationPatch(ctx, tx, e.ProjectID, e.ConnectionID, u.ProjectID, p.ConnectionID, p.BindingID, p.WorkspaceID)
		if err != nil {
			return err
		}
		if !u.ConnectionID.Set && !u.ExecutionConfig.Set && !p.ProjectID.Set && !u.BindingID.Set && !u.WorkspacePin.Set && !p.Title.Set && !p.Prompt.Set &&
			!p.Handoff.Set && !p.StartMode.Set && !p.AttachmentIDs.Set && !p.StartAt.Set {
			return nil
		}
		if err := tx.Tasks().Update(ctx, id, u); err != nil {
			return err
		}
		eventCtx := ctx
		if p.StartAt.Set {
			eventCtx = WithWorkCause(ctx, Cause{Kind: "start_time"})
		}
		return addTaskEventType(eventCtx, tx, id, &e.State, e.State, "Task edited", nil, "task_edited")
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// DeleteTask removes a task without notes or execution history;
// recorded work can only be archived (Conflict). It is spliced out of its
// tree first (its dependents take over its needs).
func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		state, unstarted, err := tx.Tasks().Deletability(ctx, id)
		if err != nil {
			return err
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
		if steerer, err := tx.Steering().ActiveByTask(ctx, id); err == nil {
			if err := endSteerer(ctx, tx, steerer, store.SteeringTaskRemoved); err != nil {
				return err
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		return tx.Tasks().Delete(ctx, id)
	})
}

// ArchiveTask archives (archived) or unarchives task id. Archiving needs a
// standalone task (a tree member is closed with its tree) that is not
// launching, running or needing input (Conflict).
func (s *Store) ArchiveTask(ctx context.Context, id int64, archived bool) (Task, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		tree, err := tx.Tasks().TreeID(ctx, id)
		if err != nil {
			return err
		}
		if !archived {
			return tx.Tasks().SetArchived(ctx, id, false)
		}
		if tree != nil {
			return Conflict("task %d is in tree %d: archive the tree, or remove it from the tree first", id, *tree)
		}
		if err := noActiveTask(tx.Trees().ActiveTask(ctx, id)); err != nil {
			return err
		}
		return tx.Tasks().SetArchived(ctx, id, true)
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// DuplicateTask copies a task's config and images into a new backlog task
// (no tree, no needs). An adopted session cannot be duplicated (Conflict).
func (s *Store) DuplicateTask(ctx context.Context, id int64) (Task, error) {
	return s.duplicateTask(ctx, id, nil)
}

// DuplicateTaskWithDraft saves a confirmed backlog copy with its own destination and source link.
func (s *Store) DuplicateTaskWithDraft(ctx context.Context, id int64, draft NewTask) (Task, error) {
	return s.duplicateTask(ctx, id, &draft)
}

func (s *Store) duplicateTask(ctx context.Context, id int64, draft *NewTask) (Task, error) {
	dst, err := write(ctx, s, func(tx store.Tx) (int64, error) {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return 0, err
		}
		if err := tx.Tasks().Lock(ctx, id); err != nil {
			return 0, err
		}
		src, err := tx.Tasks().Get(ctx, id)
		if err != nil {
			return 0, err
		}
		if src.Adopted {
			return 0, Conflict("task %d tracks an adopted session: it has no prompt to duplicate", id)
		}
		in := NewTask{
			ProjectID: src.ProjectID, ConnectionID: src.ConnectionID, BindingID: src.BindingID,
			ExecutionConfig: src.ExecutionConfig, Title: src.Title, Prompt: src.Prompt, workspacePin: src.WorkspacePin,
		}
		if draft != nil {
			if err := connectionUsable(ctx, tx, draft.ConnectionID); err != nil {
				return 0, err
			}
			in, err = taskCreationDestination(ctx, tx, *draft)
			if err != nil {
				return 0, err
			}
			// Source images stay on the original. Staged uploads and selected copies
			// are attached together in this transaction.
			in.AttachmentIDs = nil
		} else if in.workspacePin == nil && src.LaunchAttempt > 0 {
			frozen, err := tx.Attempts().Destination(ctx, id, src.LaunchAttempt)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return 0, err
			}
			if err == nil && frozen.ConnectionID == src.ConnectionID {
				in.workspacePin = &WorkspacePin{ExternalID: frozen.ExternalProjectID, Root: frozen.Workspace}
			}
		}
		return s.insertTask(ctx, tx, in, func(tx store.Tx, dst int64) error {
			var err error
			if draft == nil {
				err = tx.Attachments().CopyToTask(ctx, store.OwnerTask, src.ID, dst)
			} else {
				err = attachDuplicateImages(ctx, tx, src.ID, dst, draft.AttachmentIDs)
			}
			if err != nil {
				return err
			}
			return tx.Journal().Duplicated(ctx, dst, src.ID, ActingUser(ctx), ActingMachine(ctx), WorkAttribution(ctx))
		})
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, dst)
}

// TouchTask bumps updated_at (re-arms polling of failed/interrupted tasks).
func (s *Store) TouchTask(ctx context.Context, id int64) error {
	found, err := write(ctx, s, func(tx store.Tx) (bool, error) { return tx.Tasks().Touch(ctx, id) })
	if err == nil && !found {
		return ErrNotFound
	}
	return err
}

// AddTaskEvent records detail on task id's timeline (and journal) at its
// current state, without changing it.
func (s *Store) AddTaskEvent(ctx context.Context, id int64, detail string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		ls, err := tx.Tasks().LaunchState(ctx, id)
		if err != nil {
			return err
		}
		state := ls.State
		return addTaskEvent(ctx, tx, id, &state, state, detail)
	})
}

type (
	// Transition is a guarded task state change.
	Transition = store.Transition
	// TransitionResult reports what happened.
	TransitionResult = store.TransitionResult
)

// TransitionTask applies t atomically: state, timestamps (finishedAt on first
// entry into a finished state) and a task_events row.
func (s *Store) TransitionTask(ctx context.Context, t Transition) (TransitionResult, error) {
	var res TransitionResult
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		res = TransitionResult{}
		eventCtx := ctx
		if t.BeginAttempt {
			if err := tx.Projects().LockIdentity(ctx); err != nil {
				return err
			}
		}
		// Launch claim: one attempt begins per task and attempt number.
		if err := tx.Tasks().Lock(ctx, t.TaskID); err != nil {
			return err
		}
		ls, err := tx.Tasks().LaunchState(ctx, t.TaskID)
		if err != nil {
			return err
		}
		res.From, res.Attempt, res.RequestID = ls.State, ls.Attempt, ls.RequestID
		if (len(t.From) > 0 && !slices.Contains(t.From, res.From)) || (res.From == t.To && !t.BeginAttempt) {
			return nil
		}
		if t.BeginAttempt {
			if err := checkLaunchClaim(ctx, tx, t, res.Attempt); err != nil {
				return err
			}
		}
		var sessionID *int64
		if t.Session != nil {
			id, err := ensureSession(ctx, tx, *t.Session)
			if err != nil {
				return err
			}
			sessionID = &id
		}
		res.Attempt, res.RequestID, err = tx.Tasks().ApplyTransition(ctx, t, sessionID)
		if err != nil {
			return err
		}
		if t.BeginAttempt {
			a := tx.Attempts()
			cause := WorkAttribution(ctx).Cause
			switch {
			case t.RequireAuto:
				eventCtx, err = releaseAttribution(ctx, tx, t.TaskID)
			case cause != nil && cause.Kind == "scheduled_start":
				eventCtx, err = scheduledStart(ctx, tx, t.TaskID)
			}
			if err != nil {
				return err
			}
			if err := a.Begin(ctx, t.TaskID, res.Attempt, WorkAttribution(eventCtx)); err != nil {
				return err
			}
			if err := pinDestination(ctx, tx, t.TaskID, res.Attempt, t.LaunchCheck); err != nil {
				return err
			}
			claimed, err := a.Claim(ctx, t.TaskID, res.Attempt)
			if err != nil {
				return err
			}
			if claimed {
				if err := a.PinInputs(ctx, t.TaskID, res.Attempt); err != nil {
					return err
				}
			}
		}
		from := res.From
		if err := addTaskEventAt(eventCtx, tx, t.TaskID, &from, t.To, t.Detail, t.ObservedAt); err != nil {
			return err
		}
		if t.To.Active() {
			if err := reopenOnActivity(ctx, tx, t.TaskID, t.To, t.ObservedAt); err != nil {
				return err
			}
		}
		res.Changed = true
		res.Attribution = WorkAttribution(eventCtx)
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
func reopenOnActivity(ctx context.Context, tx store.Tx, id int64, to TaskState, observedAt *time.Time) error {
	label := strings.ReplaceAll(string(to), "_", " ")
	unarchived, err := tx.Tasks().Unarchive(ctx, id)
	if err != nil {
		return err
	}
	if unarchived {
		if err := addTaskEventAt(ctx, tx, id, &to, to, "unarchived: became "+label, observedAt); err != nil {
			return err
		}
	}
	return nil
}

// nonNilIDs is ids, or [] for nil.
func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
