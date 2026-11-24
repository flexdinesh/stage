package db

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// AdoptInput names the target independently of the caller who wrote the note.
type AdoptInput struct {
	Session         SessionRef
	ThreadID        string // legacy T3 target
	CallerSession   *SessionRef
	ProjectID       *int64
	NewProjectTitle string
	Binding         *ProjectBinding
	Title           *string
	InitialNote     *string
	TreeID          *int64
}

func (s *Store) AdoptThread(ctx context.Context, in AdoptInput) (Task, bool, error) {
	var id int64
	var created bool
	err := s.e.Write(ctx, func(tx store.Tx) error {
		var err error
		id, created, err = s.adoptThread(ctx, tx, in)
		if err == nil && in.TreeID != nil {
			if err = treeExists(ctx, rawTx(tx), *in.TreeID); err == nil {
				err = joinTree(ctx, rawTx(tx), id, *in.TreeID)
			}
		}
		return err
	})
	if err != nil {
		return Task{}, false, err
	}
	t, err := s.GetTask(ctx, id)
	return t, created, err
}

func (s *Store) adoptThread(ctx context.Context, tx store.Tx, in AdoptInput) (int64, bool, error) {
	ref := in.Session
	if in.ThreadID != "" {
		t3, err := legacyT3Tx(ctx, tx)
		if err != nil {
			return 0, false, err
		}
		ref = SessionRef{ConnectionID: t3, ExternalSessionID: in.ThreadID}
	}
	if err := connectionUsable(ctx, tx, ref.ConnectionID); err != nil {
		return 0, false, err
	}
	isT3, err := tx.Connections().HarnessIs(ctx, ref.ConnectionID, HarnessT3)
	if err != nil {
		return 0, false, err
	}
	sid, err := ensureSession(ctx, tx, ref)
	if err != nil {
		return 0, false, err
	}
	id, err := tx.Tasks().BySessionID(ctx, sid)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return 0, false, err
	}
	o, err := tx.Observations().Get(ctx, sid)
	if err != nil {
		return 0, false, err
	}
	if o.Child || o.Parent != nil {
		return 0, false, Conflict("only a top-level session can be adopted")
	}
	if o.Archived {
		return 0, false, Conflict("session is archived")
	}
	projectID, err := s.adoptionProject(ctx, tx, in, o)
	if err != nil {
		return 0, false, err
	}
	title := strings.TrimSpace(o.Title)
	if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
		title = strings.TrimSpace(*in.Title)
	}
	if title == "" {
		r := []rune(ref.ExternalSessionID)
		title = "Session " + string(r[:min(8, len(r))])
	}
	state := TaskState(o.State)
	if state == "idle" {
		state = StateDone
		if !isT3 && o.StartedAt == nil && o.EndedAt == nil {
			state = StateBacklog
		}
	}
	launched := &o.CreatedAt
	if state == StateBacklog {
		launched = nil
	}
	var finished *time.Time
	if state.Finished() {
		finished = o.EndedAt
		if finished == nil {
			finished = &o.UpdatedAt
		}
	}
	var legacyID *string
	permission, mode := "full-access", "default"
	if isT3 {
		legacyID = &ref.ExternalSessionID
		if slices.Contains(RuntimeModes, o.Config.Permission) {
			permission = o.Config.Permission
		}
		if slices.Contains(InteractionModes, o.Config.Mode) {
			mode = o.Config.Mode
		}
	}
	id, err = tx.Tasks().InsertAdopted(ctx, store.AdoptedInsert{ProjectID: projectID, Title: title, State: state,
		ConnectionID: ref.ConnectionID, SessionID: sid, T3ThreadID: legacyID, ExecutionConfig: o.Config, RuntimeMode: permission,
		InteractionMode: mode, LaunchedAt: launched, FinishedAt: finished, CreatedBy: ActingUser(ctx), SourceMachineID: ActingMachine(ctx)})
	if err != nil {
		return 0, false, err
	}
	if err = tx.Tasks().AddEvent(ctx, id, nil, state, "adopted session"); err != nil {
		return 0, false, err
	}
	if in.InitialNote != nil && *in.InitialNote != "" {
		_, err = appendNote(ctx, rawTx(tx), JournalOwner{TaskID: &id}, AppendNoteInput{Text: *in.InitialNote, RequestID: "initial-note", SourceSession: in.CallerSession})
	}
	return id, true, err
}

func (s *Store) adoptionProject(ctx context.Context, tx store.Tx, in AdoptInput, o execution.Observation) (int64, error) {
	var workspace *string
	if in.Binding != nil {
		if in.Binding.ExternalProjectID != o.Project || in.Binding.ConnectionID != o.Session.ConnectionID {
			return 0, Conflict("session workspace changed; retry adoption")
		}
		workspace = &in.Binding.Workspace
	}
	id, found, err := tx.Projects().AdoptionTarget(ctx, o.Session.ConnectionID, o.Project, workspace)
	if err != nil {
		return 0, err
	}
	if found {
		if in.ProjectID != nil && *in.ProjectID != id || in.NewProjectTitle != "" {
			return 0, Conflict("session already has a bound Stage project")
		}
		return id, nil
	}
	if in.Binding == nil || in.ProjectID == nil && strings.TrimSpace(in.NewProjectTitle) == "" {
		return 0, Conflict("choose a bound Stage project or explicitly bind this session workspace before adoption")
	}
	if in.ProjectID != nil {
		id = *in.ProjectID
	} else {
		id, err = tx.Projects().Insert(ctx, strings.TrimSpace(in.NewProjectTitle), ActingUser(ctx), ActingMachine(ctx))
		if err != nil {
			return 0, err
		}
	}
	b := *in.Binding
	b.ProjectID = id
	active, err := tx.Projects().BoundOn(ctx, id, b.ConnectionID)
	if err != nil {
		return 0, err
	}
	if active {
		return 0, Conflict("project already has a binding; confirm the workspace in project settings")
	}
	_, err = s.bind(ctx, tx, b)
	return id, err
}
