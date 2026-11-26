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
	return read(ctx, s, func(tx store.Tx) (Task, error) { return tx.Tasks().Get(ctx, id) })
}

type TaskFilter = store.TaskFilter

func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	return read(ctx, s, func(tx store.Tx) ([]Task, error) { return tx.Tasks().List(ctx, f) })
}

// PollableTasks: tasks whose thread may still change state. Active tasks
// always; done tasks updated in the last hour always, older uncompleted
// ones only when includeStale (the poller passes it at most once a minute);
// failed/interrupted/cancelled when touched in the last 10 minutes (e.g. a
// follow-up restarted the thread) or the mirror shows the thread active.
// An adopted backlog session is watched for its first native execution.
func (s *Store) PollableTasks(ctx context.Context, includeStale bool) ([]Task, error) {
	return read(ctx, s, func(tx store.Tx) ([]Task, error) { return tx.Tasks().Pollable(ctx, includeStale) })
}

func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]TaskEvent, error) {
	return read(ctx, s, func(tx store.Tx) ([]TaskEvent, error) { return tx.Tasks().Events(ctx, taskID) })
}

// NewTask is a validated create input. A T3 task without ExecutionConfig
// stores the legacy model inputs (ProviderInstanceID, Model, ModelOptions,
// RuntimeMode, InteractionMode) translated to one (legacyT3Config).
type NewTask struct {
	ExecutionConfig    *execution.Config
	ConnectionID       int64
	OriginSession      *SessionRef
	ProjectID          int64
	Title              string
	Prompt             string
	ProviderInstanceID *string
	Model              *string
	ModelOptions       []execution.ModelOption
	RuntimeMode        string // "" = full-access
	InteractionMode    string // "" = default
	Workspace          Workspace
	InitialNote        string
	AttachmentIDs      []int64 // staged images to attach (setAttachments)
	StartAt            *time.Time
	StartTz            *string // set with StartAt (IANA zone)
	ScheduleID         *int64  // spawning schedule
	Handoff            *bool   // append the needs' handoff at launch; nil = default (on)
	// TreeID: join this tree (docs/api.md "Tree rules"); nil with Needs joins
	// the needs' tree (or a new one).
	TreeID *int64
	// Needs: tasks this one needs (setNeeds rules).
	Needs []int64
	// NeedsThreadID ("afterMe"): adopt this T3 session (or take its owning
	// task) and need it, before Needs.
	NeedsThreadID *string
	NeedsSession  *SessionRef
	NeedsBinding  *ProjectBinding
	StartMode     *StartMode // nil = inherit the tree's
}

// errStartAndNeeds: a task waits on a start time or on needs, not both.
var errStartAndNeeds = Invalid("a task can have a start time or needs, not both")

// CreateTask inserts a backlog task (+ creation event) and places it in its
// tree with its needs (the whole insert fails on a rule violation).
func (s *Store) CreateTask(ctx context.Context, in NewTask) (Task, error) {
	return s.createTask(ctx, in, nil)
}

// createTask is CreateTask with extra run inside the insert transaction.
func (s *Store) createTask(ctx context.Context, in NewTask, extra func(tx store.Tx, id int64) error) (Task, error) {
	id, err := write(ctx, s, func(tx store.Tx) (int64, error) { return s.insertTask(ctx, tx, in, extra) })
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// insertTask is createTask's insert, inside tx: insert, adopt
// NeedsThreadID, resolve and join the tree, set needs.
func (s *Store) insertTask(ctx context.Context, tx store.Tx, in NewTask, extra func(tx store.Tx, id int64) error) (int64, error) {
	if in.StartAt != nil && (len(in.Needs) > 0 || in.NeedsThreadID != nil || in.NeedsSession != nil) {
		return 0, errStartAndNeeds
	}
	if err := requireProject(ctx, tx, in.ProjectID); err != nil {
		return 0, err
	}
	var originID *int64
	if in.ConnectionID == 0 {
		t3, err := legacyT3Tx(ctx, tx)
		if err != nil {
			return 0, err
		}
		in.ConnectionID = t3
	}
	if err := connectionUsable(ctx, tx, in.ConnectionID); err != nil {
		return 0, err
	}
	config, err := legacyT3Config(ctx, tx, in.ConnectionID, in.ExecutionConfig, in.ProviderInstanceID, in.Model, in.ModelOptions, in.RuntimeMode, in.InteractionMode)
	if err != nil {
		return 0, err
	}
	if in.OriginSession != nil {
		value, err := ensureSession(ctx, tx, *in.OriginSession)
		if err != nil {
			return 0, err
		}
		originID = &value
	}
	id, err := tx.Tasks().Insert(ctx, store.TaskInsert{
		ProjectID: in.ProjectID, Title: in.Title, Prompt: in.Prompt, Workspace: in.Workspace,
		StartAt: in.StartAt, StartTz: in.StartTz, ScheduleID: in.ScheduleID, Handoff: in.Handoff,
		StartMode: in.StartMode, ConnectionID: in.ConnectionID, OriginSessionID: originID, ExecutionConfig: config,
		CreatedBy: ActingUser(ctx), SourceMachineID: ActingMachine(ctx),
	})
	if err != nil {
		return 0, err
	}

	needs := slices.Clone(in.Needs)
	if in.NeedsThreadID != nil || in.NeedsSession != nil {
		input := AdoptInput{Binding: in.NeedsBinding}
		if in.NeedsSession != nil {
			input.Session = *in.NeedsSession
		} else {
			input.ThreadID = *in.NeedsThreadID
		}
		owner, _, err := s.adoptThread(ctx, tx, input)
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
	if err := checkBaseTask(ctx, tx, id); err != nil {
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
// Nullable fields: providerInstanceId, model, startMode (Null clears). The
// legacy T3 model fields set their part of the stored executionConfig
// (mergeLegacyT3).
type TaskPatch struct {
	ValidatedConnection int64                             `json:"-"`
	ValidatedProject    int64                             `json:"-"`
	ConnectionID        Optional[int64]                   `json:"connectionId"`
	ExecutionConfig     Optional[execution.Config]        `json:"executionConfig"`
	ProjectID           Optional[int64]                   `json:"projectId"`
	Title               Optional[string]                  `json:"title"`
	Prompt              Optional[string]                  `json:"prompt"`
	ProviderInstanceID  Optional[string]                  `json:"providerInstanceId"`
	Model               Optional[string]                  `json:"model"`
	ModelOptions        Optional[[]execution.ModelOption] `json:"modelOptions"`
	RuntimeMode         Optional[string]                  `json:"runtimeMode"`
	InteractionMode     Optional[string]                  `json:"interactionMode"`
	Workspace           Optional[Workspace]               `json:"workspace"`
	AttachmentIDs       Optional[[]int64]                 `json:"attachmentIds"` // the exact image set; null = none
	Handoff             Optional[bool]                    `json:"handoff"`       // append the needs' handoff at launch
	StartMode           Optional[StartMode]               `json:"startMode"`     // null = inherit the tree's
	// One-shot start: the API converts startLocal (wall time in startTz) into
	// StartAt; StartAt and StartTz are set (or null) together.
	StartLocal Optional[string]    `json:"startLocal"`
	StartTz    Optional[string]    `json:"startTz"`
	StartAt    Optional[time.Time] `json:"-"`
}

// restrictedSet reports whether a field editable only in backlog is present.
func (p TaskPatch) restrictedSet() bool {
	return p.ConnectionID.Set || p.ExecutionConfig.Set || p.ProjectID.Set || p.Prompt.Set || p.ProviderInstanceID.Set || p.Model.Set ||
		p.ModelOptions.Set || p.RuntimeMode.Set || p.InteractionMode.Set || p.Workspace.Set || p.AttachmentIDs.Set ||
		p.StartAt.Set || p.Handoff.Set || p.StartMode.Set
}

// UpdateTask applies a validated patch. Outside backlog only the title
// may change (Conflict otherwise).
func (s *Store) UpdateTask(ctx context.Context, id int64, p TaskPatch) (Task, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		e, err := tx.Tasks().EditState(ctx, id)
		if err != nil {
			return err
		}
		if p.ValidatedConnection != 0 && (e.ConnectionID != p.ValidatedConnection || e.ProjectID != p.ValidatedProject) {
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
		legacy, err := mergeLegacyT3(e.ExecutionConfig, p.ProviderInstanceID, p.Model, p.ModelOptions, p.RuntimeMode, p.InteractionMode)
		if err != nil {
			return err
		}
		u := store.TaskUpdate{ConnectionID: p.ConnectionID, ExecutionConfig: p.ExecutionConfig, ProjectID: p.ProjectID,
			Title: p.Title, Prompt: p.Prompt, Workspace: p.Workspace, Handoff: p.Handoff, StartMode: p.StartMode}
		if legacy != nil {
			u.ExecutionConfig = Optional[execution.Config]{Set: true, V: *legacy}
		}
		if p.ConnectionID.Set {
			if err := checkDestinationChange(ctx, tx, e.ConnectionID, p.ConnectionID.V, p.ExecutionConfig, p.Workspace); err != nil {
				return err
			}
			toT3, err := tx.Connections().HarnessIs(ctx, p.ConnectionID.V, HarnessT3)
			if err != nil {
				return err
			}
			if p.ConnectionID.V != e.ConnectionID && !toT3 {
				hasImages, err := tx.Tasks().HasAttachments(ctx, id)
				if err != nil {
					return err
				}
				if hasImages {
					return Invalid("Remove image attachments before switching to OpenCode.")
				}
			}
		}
		if p.ProjectID.Set {
			if err := requireProject(ctx, tx, p.ProjectID.V); err != nil {
				return err
			}
		}
		if !u.ConnectionID.Set && !u.ExecutionConfig.Set && !p.ProjectID.Set && !p.Title.Set && !p.Prompt.Set &&
			!p.Workspace.Set && !p.Handoff.Set && !p.StartMode.Set && !p.AttachmentIDs.Set && !p.StartAt.Set {
			return nil
		}
		if err := tx.Tasks().Update(ctx, id, u); err != nil {
			return err
		}
		if p.Workspace.Set {
			return checkBaseTask(ctx, tx, id)
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// checkBaseTask: workspace.baseTaskId, when set, names one of the task's
// needs (Invalid otherwise).
func checkBaseTask(ctx context.Context, tx store.Tx, id int64) error {
	base, isNeed, err := tx.Tasks().BaseTask(ctx, id)
	if err != nil {
		return err
	}
	if base != nil && !isNeed {
		return Invalid("workspace.baseTaskId: task %d is not a need of this task", *base)
	}
	return nil
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
	src, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if src.Adopted {
		return Task{}, Conflict("task %d tracks an adopted session: it has no prompt to duplicate", id)
	}
	ws := src.Workspace
	ws.BaseTaskID = nil
	copyImages := func(tx store.Tx, dst int64) error {
		if err := tx.Attachments().CopyToTask(ctx, store.OwnerTask, src.ID, dst); err != nil {
			return err
		}
		return tx.Journal().Duplicated(ctx, dst, src.ID, ActingUser(ctx), ActingMachine(ctx))
	}
	return s.createTask(ctx, NewTask{
		ProjectID: src.ProjectID, ConnectionID: src.ConnectionID, ExecutionConfig: src.ExecutionConfig, Title: src.Title, Prompt: src.Prompt,
		Workspace: ws,
	}, copyImages)
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
// entry into a finished state, settledAt on settled) and a task_events row.
func (s *Store) TransitionTask(ctx context.Context, t Transition) (TransitionResult, error) {
	var res TransitionResult
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		res = TransitionResult{}
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
			if err := a.Begin(ctx, t.TaskID, res.Attempt); err != nil {
				return err
			}
			if err := pinDestination(ctx, tx, t.TaskID, res.Attempt); err != nil {
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
		if err := addTaskEvent(ctx, tx, t.TaskID, &from, t.To, t.Detail); err != nil {
			return err
		}
		if t.To.Active() {
			if err := reopenOnActivity(ctx, tx, t.TaskID, t.To); err != nil {
				return err
			}
		}
		res.Changed = true
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
func reopenOnActivity(ctx context.Context, tx store.Tx, id int64, to TaskState) error {
	label := strings.ReplaceAll(string(to), "_", " ")
	unarchived, err := tx.Tasks().Unarchive(ctx, id)
	if err != nil {
		return err
	}
	if unarchived {
		if err := addTaskEvent(ctx, tx, id, &to, to, "unarchived: became "+label); err != nil {
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

// legacyT3Config is a create's execution config: config when set, else on a
// T3 connection the legacy T3 model inputs translated (docs/api.md "Create
// task"; "" runtime and interaction modes take the T3 defaults full-access
// and default), else nil.
func legacyT3Config(ctx context.Context, tx store.Tx, connection int64, config *execution.Config,
	provider, model *string, options []execution.ModelOption, runtime, interaction string) (*execution.Config, error) {
	if config != nil {
		return config, nil
	}
	t3, err := tx.Connections().HarnessIs(ctx, connection, HarnessT3)
	if err != nil || !t3 {
		return nil, err
	}
	if runtime == "" {
		runtime = "full-access"
	}
	if interaction == "" {
		interaction = "default"
	}
	c := store.LegacyT3Config(provider, model, options, runtime, interaction)
	return &c, nil
}

// mergeLegacyT3 is the stored config with an edit's legacy T3 model fields
// applied, nil when none is set: providerInstanceId and model set the model
// together (both null clears it), modelOptions the options, runtimeMode the
// permission and interactionMode the mode. The rest of the config is kept.
func mergeLegacyT3(current *execution.Config, provider, model Optional[string],
	options Optional[[]execution.ModelOption], runtime, interaction Optional[string]) (*execution.Config, error) {
	if !provider.Set && !model.Set && !options.Set && !runtime.Set && !interaction.Set {
		return nil, nil
	}
	if provider.Set != model.Set || provider.Null != model.Null {
		return nil, Invalid("providerInstanceId and model must be set together")
	}
	c := execution.Config{Options: []execution.ModelOption{}}
	if current != nil {
		c = *current
		c.Options = slices.Clone(c.Options)
	}
	if provider.Set {
		c.Model = store.LegacyT3ModelKey(provider.Ptr(), model.Ptr())
	}
	if options.Set {
		c.Options = slices.Clone(options.V)
	}
	if c.Options == nil {
		c.Options = []execution.ModelOption{}
	}
	if runtime.Set {
		c.Permission = runtime.V
	}
	if interaction.Set {
		c.Mode = interaction.V
	}
	return &c, nil
}
