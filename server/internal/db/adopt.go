package db

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// AdoptInput names the target independently of the caller who wrote the note.
type AdoptInput struct {
	Session         SessionRef
	CallerSession   *SessionRef
	ProjectID       *int64
	NewProjectTitle string
	Workspace       *AdoptionWorkspace
	Title           *string
	InitialNote     *string
	TreeID          *int64
}

// AdoptionWorkspace pairs the verified workspace with its connection's complete live inventory.
type AdoptionWorkspace struct {
	Binding   ProjectBinding
	Inventory []execution.WorkspaceInfo
}

func (s *Store) AdoptSession(ctx context.Context, in AdoptInput) (Task, bool, error) {
	var id int64
	var created bool
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		id, created = 0, false
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		var err error
		id, created, err = s.adoptSession(ctx, tx, in)
		if err == nil && in.TreeID != nil {
			if err = treeExists(ctx, tx, *in.TreeID); err == nil {
				err = joinTree(ctx, tx, id, *in.TreeID)
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

func (s *Store) adoptSession(ctx context.Context, tx store.Tx, in AdoptInput) (int64, bool, error) {
	ref := in.Session
	if err := connectionUsable(ctx, tx, ref.ConnectionID); err != nil {
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
	if in.Workspace != nil {
		if err := resolveAll(ctx, tx); err != nil {
			return 0, false, err
		}
	}
	title := strings.TrimSpace(o.Title)
	if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
		title = strings.TrimSpace(*in.Title)
	}
	if title == "" {
		r := []rune(ref.ExternalSessionID)
		title = "Session " + string(r[:min(8, len(r))])
	}
	// Idle: done when the harness reports an execution, else backlog (the
	// first native execution is watched). A harness that knows its run
	// finished reports done itself.
	state := TaskState(o.State)
	if state == "idle" {
		state = StateDone
		if o.StartedAt == nil && o.EndedAt == nil {
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
	id, err = tx.Tasks().InsertAdopted(ctx, store.AdoptedInsert{ProjectID: projectID, Title: title, State: state,
		ConnectionID: ref.ConnectionID, SessionID: sid, ExecutionConfig: o.Config,
		LaunchedAt: launched, FinishedAt: finished, CreatedBy: ActingUser(ctx), SourceMachineID: ActingMachine(ctx)})
	if err != nil {
		return 0, false, err
	}
	if err = addTaskEvent(ctx, tx, id, nil, state, "adopted session"); err != nil {
		return 0, false, err
	}
	if in.InitialNote != nil && *in.InitialNote != "" {
		_, err = appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: *in.InitialNote, RequestID: "initial-note", SourceSession: in.CallerSession})
	}
	return id, true, err
}

func (s *Store) adoptionProject(ctx context.Context, tx store.Tx, in AdoptInput, o execution.Observation) (int64, error) {
	if in.ProjectID != nil {
		id, err := projectID(ctx, tx, *in.ProjectID)
		if err != nil {
			return 0, err
		}
		in.ProjectID = &id
	}
	var workspace *string
	if in.Workspace != nil {
		b := in.Workspace.Binding
		if b.ExternalProjectID != o.Project || b.ConnectionID != o.Session.ConnectionID {
			return 0, Conflict("session workspace changed; retry adoption")
		}
		workspace = &b.Workspace
		if _, err := refreshWorkspaceRows(ctx, tx, b.ConnectionID, in.Workspace.Inventory); err != nil {
			return 0, err
		}
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
	if in.Workspace == nil || in.ProjectID == nil && strings.TrimSpace(in.NewProjectTitle) == "" {
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
	b := in.Workspace.Binding
	b.ProjectID = id
	if _, err := s.bind(ctx, tx, b); err != nil {
		return 0, err
	}
	return id, nil
}
