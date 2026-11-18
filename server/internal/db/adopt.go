package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// adoptStateSQL is an adopted session's initial task state from its mirror
// row th: an SQL copy of sync.MapThreadState with current = done (an idle
// session without runs reads done). Migration 0003 inlines the same CASE.
const adoptStateSQL = `CASE WHEN th.settled THEN 'settled'
 WHEN th.pending_request_count > 0 THEN 'needs_input'
 WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) IN ('preparing', 'queued', 'starting', 'running', 'waiting') THEN 'running'
 WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) = 'failed' THEN 'failed'
 WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) = 'interrupted' THEN 'interrupted'
 WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) IN ('cancelled', 'rolled_back') THEN 'cancelled'
 ELSE 'done' END`

// AdoptInput adopts a T3 session as a task ("Track as task").
type AdoptInput struct {
	ProjectID       *int64
	NewProjectTitle string
	Binding         *ProjectBinding
	ThreadID        string
	Title           *string  // nil or blank = the session title (else "Session <id8>")
	InitialNote     *string  // nil = ""
	Tags            []string // cleaned by the API
	TreeID          *int64   // join this tree (no needs)
}

// AdoptThread makes mirrored session in.ThreadID a task (docs/api.md "Adopt
// session"): prompt "", state from the session (adoptStateSQL), never
// started by stage. A session a task already owns returns that task
// (created false). Errors: unknown session 404; a subagent, an archived
// session or one whose project is not in stage 409; unknown tree 404.
func (s *Store) AdoptThread(ctx context.Context, in AdoptInput) (Task, bool, error) {
	var id int64
	var created bool
	err := s.inTx(ctx, func(tx *Tx) error {
		var err error
		var owner bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE t3_thread_id=$1 AND connection_id=1)`, in.ThreadID).Scan(&owner); err != nil {
			return err
		}
		if !owner && in.Binding != nil {
			var projectID int64
			if in.ProjectID != nil {
				projectID = *in.ProjectID
			} else {
				if strings.TrimSpace(in.NewProjectTitle) == "" {
					return Invalid("choose a Stage project or enter a new project title")
				}
				projectID, err = insertProject(ctx, tx, Project{Title: strings.TrimSpace(in.NewProjectTitle)})
				if err != nil {
					return err
				}
			}
			binding := *in.Binding
			binding.ProjectID = projectID
			if _, err = bindProject(ctx, tx, binding); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE threads SET stage_project_id=$2 WHERE t3_thread_id=$1`, in.ThreadID, projectID); err != nil {
				return err
			}
		}
		if id, created, err = adoptThread(ctx, tx, in); err != nil {
			return err
		}
		if in.TreeID != nil {
			if err := treeExists(ctx, tx, *in.TreeID); err != nil {
				return err
			}
			return joinTree(ctx, tx, id, *in.TreeID)
		}
		return nil
	})
	if err != nil {
		return Task{}, false, err
	}
	t, err := s.GetTask(ctx, id)
	return t, created, err
}

// adoptThread is AdoptThread's insert, inside tx; returns the task id.
func adoptThread(ctx context.Context, tx *Tx, in AdoptInput) (int64, bool, error) {
	var owner int64
	err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE t3_thread_id = $1 AND connection_id=1`, in.ThreadID).Scan(&owner)
	switch {
	case err == nil:
		return owner, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}
	var parent *string
	var archived bool
	var project *int64
	err = tx.QueryRow(ctx, `
SELECT th.parent_thread_id, th.archived, p.id FROM threads th
LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=1 AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1)) WHERE th.t3_thread_id = $1`, in.ThreadID).
		Scan(&parent, &archived, &project)
	if err != nil {
		return 0, false, fmt.Errorf("session %s: %w", in.ThreadID, notFound(err))
	}
	switch {
	case parent != nil:
		return 0, false, Conflict("session %s is a subagent: only a top-level session can be adopted", in.ThreadID)
	case archived:
		return 0, false, Conflict("session %s is archived", in.ThreadID)
	case project == nil:
		return 0, false, Conflict("session %s belongs to a project stage does not have", in.ThreadID)
	}
	var title *string
	if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
		v := strings.TrimSpace(*in.Title)
		title = &v
	}
	var id int64
	var state TaskState
	err = tx.QueryRow(ctx, `
INSERT INTO tasks (project_id, title, prompt, provider_instance_id, model, runtime_mode, interaction_mode,
  tags, state, t3_thread_id, launch_attempt, launched_at, finished_at, settled_at, adopted)
SELECT $2, COALESCE($3, NULLIF(trim(th.title), ''), 'Session ' || substr(th.t3_thread_id, 1, 8)), '',
  CASE WHEN th.provider_instance_id IS NOT NULL AND th.model IS NOT NULL THEN th.provider_instance_id END,
  CASE WHEN th.provider_instance_id IS NOT NULL AND th.model IS NOT NULL THEN th.model END,
  CASE WHEN th.runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')
       THEN th.runtime_mode ELSE 'full-access' END,
  CASE WHEN th.interaction_mode IN ('default', 'plan') THEN th.interaction_mode ELSE 'default' END,
  $4, st, th.t3_thread_id, 1, th.created_at,
  CASE WHEN st IN ('done', 'failed', 'interrupted', 'cancelled', 'settled') THEN COALESCE(th.run_ended_at, th.updated_at) END,
  CASE WHEN st = 'settled' THEN COALESCE(th.settled_at, th.updated_at) END, 1
FROM (SELECT th.*, `+adoptStateSQL+` AS st FROM threads th WHERE th.t3_thread_id = $1) th
RETURNING id, state`, in.ThreadID, *project, title, nonNilStrings(in.Tags)).Scan(&id, &state)
	if err != nil {
		return 0, false, err
	}
	if in.TreeID != nil {
		if err := treeExists(ctx, tx, *in.TreeID); err != nil {
			return 0, false, err
		}
		if err := joinTree(ctx, tx, id, *in.TreeID); err != nil {
			return 0, false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, NULL, $2, 'adopted session')`, id, string(state))
	if err != nil {
		return 0, false, err
	}
	if in.InitialNote != nil && *in.InitialNote != "" {
		if _, err := appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: *in.InitialNote, RequestID: "initial-note", SourceThreadID: &in.ThreadID}); err != nil {
			return 0, false, err
		}
	}
	return id, true, nil
}
