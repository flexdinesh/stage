package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// launchedTask creates a task in project and launches it on the local T3
// under thread, ending running.
func launchedTask(t *testing.T, s *Store, ctx context.Context, project int64, thread string) int64 {
	t.Helper()
	task, err := s.CreateTask(ctx, NewTask{ProjectID: project, Title: "t", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default", Workspace: Workspace{Type: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, Transition{TaskID: task.ID, To: StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, Transition{TaskID: task.ID, To: StateRunning, ThreadID: &thread}); err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func threadProject(t *testing.T, pool *sqlite.DB, thread string) *int64 {
	t.Helper()
	var p *int64
	if err := pool.QueryRow(`SELECT stage_project_id FROM threads WHERE t3_thread_id = ?`, thread).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A newly mirrored T3 thread takes its task's project, else the single
// active binding of its connection for its harness project, else none.
func TestMirroredThreadProject(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	t3 := s.LegacyT3ID()
	a := projectWithBinding(t, s, t3, "/w/a")
	b := projectWithBinding(t, s, t3, "/w/b")
	launchedTask(t, s, ctx, a, "thr-task")
	now := time.Now().UTC()
	observe := func(thread, project string) {
		t.Helper()
		o := execution.Observation{Session: SessionRef{ConnectionID: t3, ExternalSessionID: thread}, Project: project, Title: thread, State: "running", Active: true, CreatedAt: now, UpdatedAt: now}
		if err := s.PutObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	observe("thr-task", "p/w/b")
	observe("thr-bound", "p/w/b")
	observe("thr-none", "p/w/zzz")
	for thread, want := range map[string]*int64{"thr-task": &a, "thr-bound": &b, "thr-none": nil} {
		got := threadProject(t, pool, thread)
		if (got == nil) != (want == nil) || got != nil && *got != *want {
			t.Fatalf("%s project %v, want %v", thread, got, want)
		}
	}
}

// An acceptance records the task's session as its source.
func TestAcceptanceSourceSessionIsTasks(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	project := projectWithBinding(t, s, s.LegacyT3ID(), "/w/a")
	id := launchedTask(t, s, ctx, project, "thr-accept")
	if _, err := s.TransitionTask(ctx, Transition{TaskID: id, To: StateDone}); err != nil {
		t.Fatal(err)
	}
	p, err := s.CompletionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeAcceptance(ctx, id, AcceptanceRequest{RequestID: "accept", Revision: p.Revision, EvidenceVersion: p.EvidenceVersion}, false); err != nil {
		t.Fatal(err)
	}
	var source, session *int64
	if err := pool.QueryRow(`SELECT a.source_session_id, t.session_id FROM task_acceptances a JOIN tasks t ON t.id = a.task_id WHERE a.task_id = ?`, id).Scan(&source, &session); err != nil {
		t.Fatal(err)
	}
	if source == nil || session == nil || *source != *session {
		t.Fatalf("acceptance source %v, task session %v", source, session)
	}
}

func handoffSession(t *testing.T, pool *sqlite.DB, task int64) string {
	t.Helper()
	var ref string
	if err := pool.QueryRow(`SELECT ifnull((SELECT connection_id||':'||external_id FROM session_identities WHERE id = h.source_session_id), '-')
 FROM task_handoffs h WHERE h.task_id = ?`, task).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

// A handoff's source session is its evidence's session, else its legacy
// thread id under the local machine's active T3.
func TestHandoffSourceSession(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	t3 := s.LegacyT3ID()
	project := projectWithBinding(t, s, t3, "/w/a")
	src := launchedTask(t, s, ctx, project, "thr-src")
	for _, tc := range []struct {
		h    Handoff
		want string
	}{
		{Handoff{Session: &SessionRef{ConnectionID: t3, ExternalSessionID: "thr-src"}, SourceThreadID: "thr-src"}, fmt.Sprintf("%d:thr-src", t3)},
		{Handoff{SourceThreadID: "legacy-thr"}, fmt.Sprintf("%d:legacy-thr", t3)},
		{Handoff{}, "-"},
	} {
		task := launchedTask(t, s, ctx, project, fmt.Sprintf("thr-%s", tc.want))
		h := tc.h
		h.Attempt, h.Status, h.SourceTaskID, h.Text = 1, HandoffSent, &src, "result"
		if err := s.RecordHandoff(ctx, task, h); err != nil {
			t.Fatal(err)
		}
		if got := handoffSession(t, pool, task); got != tc.want {
			t.Fatalf("handoff session %s, want %s", got, tc.want)
		}
	}
}
