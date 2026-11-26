package db

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// movedT3 retires the local machine's T3 onto the same environment on a
// worker (machine 2) and returns the worker's connection.
func movedT3(t *testing.T) (*Store, *sqlite.DB, int64) {
	t.Helper()
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'worker')`)
	old := s.ActiveID(LocalMachine, "t3")
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box", 0); err != nil {
		t.Fatal(err)
	}
	sw, err := s.SwitchInstance(ctx, 2, t3.Descriptor, "env-a", "box", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetireConnection(ctx, old, sw.New); err != nil {
		t.Fatal(err)
	}
	if s.ActiveID(LocalMachine, "t3") != 0 {
		t.Fatalf("local T3 still active: %d", s.ActiveID(LocalMachine, "t3"))
	}
	return s, pool, sw.New
}

// A T3 thread with a parent on a worker's T3 is observed on its own
// connection with its latest parent, after the local machine's T3 was retired.
func TestMovedT3ObservationKeepsParentOnItsConnection(t *testing.T) {
	s, pool, moved := movedT3(t)
	projectWithBinding(t, s, moved, "/w/a")
	now := time.Now().UTC()
	o := execution.Observation{Session: SessionRef{ConnectionID: moved, ExternalSessionID: "child"}, Project: "p/w/a", Title: "Child", State: "running", Active: true, CreatedAt: now, UpdatedAt: now,
		Parent: &SessionRef{ConnectionID: moved, ExternalSessionID: "parent"}}
	if err := s.PutObservation(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	o.UpdatedAt = now.Add(time.Second)
	o.Parent = &SessionRef{ConnectionID: moved, ExternalSessionID: "parent-2"}
	if err := s.PutObservation(t.Context(), o); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.Observation(t.Context(), SessionRef{ConnectionID: moved, ExternalSessionID: "child"})
	if err != nil || got.Parent == nil || got.Parent.ConnectionID != moved || got.Parent.ExternalSessionID != "parent-2" {
		t.Fatalf("parent %+v, want %d parent-2: %v", got.Parent, moved, err)
	}
	var nulls int
	if err := pool.QueryRow(`SELECT count(*) FROM session_identities WHERE connection_id IS NULL`).Scan(&nulls); err != nil || nulls != 0 {
		t.Fatalf("%d identities without a connection: %v", nulls, err)
	}
}

// State changes of a task on a worker's T3 journal the event under the task's
// own session, after the local machine's T3 was retired.
func TestMovedT3TaskEventJournalsItsSession(t *testing.T) {
	s, pool, moved := movedT3(t)
	project := projectWithBinding(t, s, moved, "/w/a")
	mustExec(t, pool, `INSERT INTO session_identities(connection_id,external_id) VALUES($1,'thread-a')`, moved)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,connection_id,session_id) VALUES(7,$1,'ran','p','running',$2,(SELECT id FROM session_identities WHERE connection_id=$2 AND external_id='thread-a'))`, project, moved)
	if _, err := s.TransitionTask(t.Context(), Transition{TaskID: 7, To: StateDone, Detail: "finished"}); err != nil {
		t.Fatal(err)
	}
	var session, journaled int64
	err := pool.QueryRow(`SELECT t.session_id,j.source_session_id FROM tasks t JOIN journal_entries j ON j.task_id=t.id AND j.kind='event' WHERE t.id=7`).Scan(&session, &journaled)
	if err != nil || journaled != session {
		t.Fatalf("event session %d, task session %d: %v", journaled, session, err)
	}
	var conn int64
	if err := pool.QueryRow(`SELECT connection_id FROM session_identities WHERE id=$1`, journaled).Scan(&conn); err != nil || conn != moved {
		t.Fatalf("event session on %d, want %d: %v", conn, moved, err)
	}
}

// Unconfirmed records the cause, a backoff and the session the harness
// returned; a stale revision is a conflict, never a silent no-op.
func TestUnconfirmedRecordsObservedSession(t *testing.T) {
	s, pool, moved := movedT3(t)
	project := projectWithBinding(t, s, moved, "/w/a")
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,connection_id,launch_attempt) VALUES(9,$1,'w','p','launching',$2,1)`, project, moved)
	mustExec(t, pool, `INSERT INTO execution_attempts(task_id,attempt,phase,revision) VALUES(9,1,'unconfirmed',3)`)
	ctx := t.Context()
	a, err := s.Attempt(ctx, 9, 1)
	if err != nil || a.Session != nil {
		t.Fatalf("fresh attempt %+v %v", a, err)
	}
	// Destinations are resolved when read; only the resume command is kept.
	seen := Observed(execution.Observation{Session: SessionRef{ConnectionID: moved, ExternalSessionID: "thr"}, Title: "Work", State: "running",
		Links: execution.Links{Web: "https://t3/thread", Command: "claude attach thr"}})
	if seen.Links == nil || *seen.Links != (execution.Links{Command: "claude attach thr"}) {
		t.Fatalf("observed links %+v", seen.Links)
	}
	seen.Links.Web = "https://t3/thread"
	if err := s.Unconfirmed(ctx, a, errors.New("record failed"), seen); err != nil {
		t.Fatal(err)
	}
	got, err := s.Attempt(ctx, 9, 1)
	if err != nil || got.Error != "record failed" || got.RetryAt == nil || got.RetryCount != 1 || got.Session == nil {
		t.Fatalf("after unconfirmed %+v %v", got, err)
	}
	if got.Session.SessionRef != seen.SessionRef || got.Session.Title != "Work" || got.Session.State != "running" ||
		got.Session.Links == nil || *got.Session.Links != (execution.Links{Command: "claude attach thr"}) {
		t.Fatalf("observed %+v", *got.Session)
	}
	if none := Observed(execution.Observation{Session: seen.SessionRef, Links: execution.Links{Web: "https://t3/thread"}}); none.Links != nil {
		t.Fatalf("a session without a resume command kept links %+v", none.Links)
	}
	if task, err := s.GetTask(ctx, 9); err != nil || task.LastError == nil || *task.LastError != "record failed" {
		t.Fatalf("task last error %+v %v", task.LastError, err)
	}
	// nil keeps what was seen; the backoff grows.
	if err := s.Unconfirmed(ctx, got, errors.New("again"), nil); err != nil {
		t.Fatal(err)
	}
	again, err := s.Attempt(ctx, 9, 1)
	if err != nil || again.Session == nil || again.Session.ExternalSessionID != "thr" || again.RetryCount != 2 || !again.RetryAt.After(*got.RetryAt) {
		t.Fatalf("second unconfirmed %+v %v", again, err)
	}
	stale := again
	stale.Revision--
	var conflict *ConflictError
	if err := s.Unconfirmed(ctx, stale, errors.New("stale"), nil); !errors.As(err, &conflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if after, _ := s.Attempt(ctx, 9, 1); !strings.Contains(after.Error, "again") {
		t.Fatalf("stale write landed: %q", after.Error)
	}
}

// Migration 0025 keeps existing threads and attempts, drops the parent
// triggers and adds an empty observed_session.
func TestLaunchObservedSessionMigration(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 24)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,launch_attempt) VALUES(1,1,'w','p','launching',1)`)
	mustExec(t, pool, `INSERT INTO execution_attempts(task_id,attempt,phase,error) VALUES(1,1,'unconfirmed','kept')`)
	mustExec(t, pool, `INSERT INTO threads(t3_thread_id,t3_project_id,title,status,archived,created_at,updated_at,synced_at,parent_thread_id) VALUES('child','proj','Child','running',0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','parent')`)
	var before int64
	if err := pool.QueryRow(`SELECT parent_session_id FROM threads WHERE t3_thread_id='child'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	migrateTo(t, pool, 25)
	var after int64
	if err := pool.QueryRow(`SELECT parent_session_id FROM threads WHERE t3_thread_id='child'`).Scan(&after); err != nil || after != before {
		t.Fatalf("parent session %d, want %d: %v", after, before, err)
	}
	var reason string
	var observed *string
	if err := pool.QueryRow(`SELECT error,observed_session FROM execution_attempts WHERE task_id=1`).Scan(&reason, &observed); err != nil || reason != "kept" || observed != nil {
		t.Fatalf("attempt %q %v: %v", reason, observed, err)
	}
	var triggers int
	if err := pool.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'threads_parent_session_id_%'`).Scan(&triggers); err != nil || triggers != 0 {
		t.Fatalf("%d parent triggers left: %v", triggers, err)
	}
	if _, err := pool.Exec(`UPDATE execution_attempts SET observed_session='[]' WHERE task_id=1`); err == nil {
		t.Fatal("non-object observed_session accepted")
	}
	if foreignKeyViolations(t, pool) != 0 {
		t.Fatal("foreign key violations after 0025")
	}
}
