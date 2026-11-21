package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

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
	err := s.inTx(ctx, func(tx *Tx) error {
		var err error
		id, created, err = adoptThread(ctx, tx, in)
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

func adoptThread(ctx context.Context, tx *Tx, in AdoptInput) (int64, bool, error) {
	ref := in.Session
	if in.ThreadID != "" {
		t3, err := legacyT3(ctx, tx)
		if err != nil {
			return 0, false, err
		}
		ref = SessionRef{ConnectionID: t3, ExternalSessionID: in.ThreadID}
	}
	if err := connectionInUse(ctx, tx, ref.ConnectionID); err != nil {
		return 0, false, err
	}
	isT3, err := harnessIs(ctx, tx, ref.ConnectionID, HarnessT3)
	if err != nil {
		return 0, false, err
	}
	sid, err := sessionIdentity(ctx, tx, ref)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, "SELECT id FROM tasks WHERE session_id=$1", sid).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	var o execution.Observation
	if err = tx.QueryRow(ctx, "SELECT observation FROM execution_observations WHERE session_id=$1", sid).Scan(&o); err != nil {
		return 0, false, notFound(err)
	}
	if o.Child || o.Parent != nil {
		return 0, false, Conflict("only a top-level session can be adopted")
	}
	if o.Archived {
		return 0, false, Conflict("session is archived")
	}
	projectID, err := adoptionProject(ctx, tx, in, o)
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
	err = tx.QueryRow(ctx, `INSERT INTO tasks(project_id,title,prompt,state,connection_id,session_id,t3_thread_id,
	 execution_config,runtime_mode,interaction_mode,adopted,launch_attempt,launched_at,finished_at,created_by_user_id)
	 VALUES($1,$2,'',$3,$4,$5,$6,$7,$8,$9,1,1,$10,$11,$12) RETURNING id`, projectID, title, state, ref.ConnectionID, sid, legacyID, o.Config, permission, mode, launched, finished, ActingUser(ctx)).Scan(&id)
	if err != nil {
		return 0, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO task_events(task_id,to_state,detail) VALUES($1,$2,'adopted session')`, id, state); err != nil {
		return 0, false, err
	}
	if in.InitialNote != nil && *in.InitialNote != "" {
		_, err = appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: *in.InitialNote, RequestID: "initial-note", SourceSession: in.CallerSession})
	}
	return id, true, err
}

func adoptionProject(ctx context.Context, tx *Tx, in AdoptInput, o execution.Observation) (int64, error) {
	q := "SELECT project_id FROM project_bindings WHERE active=1 AND status='available' AND connection_id=$1 AND external_project_id=$2"
	args := []any{o.Session.ConnectionID, o.Project}
	if in.Binding != nil {
		if in.Binding.ExternalProjectID != o.Project || in.Binding.ConnectionID != o.Session.ConnectionID {
			return 0, Conflict("session workspace changed; retry adoption")
		}
		q += " AND workspace=$3"
		args = append(args, in.Binding.Workspace)
	}
	var id int64
	err := tx.QueryRow(ctx, "SELECT min(project_id) FROM ("+q+") HAVING count(*)=1", args...).Scan(&id)
	if err == nil {
		if in.ProjectID != nil && *in.ProjectID != id || in.NewProjectTitle != "" {
			return 0, Conflict("session already has a bound Stage project")
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if in.Binding == nil || in.ProjectID == nil && strings.TrimSpace(in.NewProjectTitle) == "" {
		return 0, Conflict("choose a bound Stage project or explicitly bind this session workspace before adoption")
	}
	if in.ProjectID != nil {
		id = *in.ProjectID
	} else {
		id, err = insertProject(ctx, tx, Project{Title: strings.TrimSpace(in.NewProjectTitle)})
		if err != nil {
			return 0, err
		}
	}
	b := *in.Binding
	b.ProjectID = id
	var active bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM project_bindings WHERE active=1 AND project_id=$1 AND connection_id=$2)", id, b.ConnectionID).Scan(&active); err != nil {
		return 0, err
	}
	if active {
		return 0, Conflict("project already has a binding; confirm the workspace in project settings")
	}
	_, err = bindProject(ctx, tx, b)
	return id, err
}
