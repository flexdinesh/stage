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
	if _, err := s.TransitionTask(ctx, Transition{TaskID: task.ID, To: StateRunning, Session: &SessionRef{ConnectionID: s.LegacyT3ID(), ExternalSessionID: thread}}); err != nil {
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

func triggerNames(t *testing.T, pool *sqlite.DB) map[string]bool {
	t.Helper()
	rows, err := pool.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names[n] = true
	}
	return names
}

// Migration 0026 drops the business-rule triggers and keeps every row; the
// other compatibility triggers stay (#417).
func TestJournalTriggersMigration(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 25)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(1,'T')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tree_id) VALUES(1,1,'a','p','backlog',1),(2,1,'b','p','backlog',1)`)
	mustExec(t, pool, `INSERT INTO task_needs(task_id,need_id) VALUES(2,1)`)
	mustExec(t, pool, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES(1,NULL,'backlog','created')`)
	var before int
	if err := pool.QueryRow(`SELECT count(*) FROM journal_entries`).Scan(&before); err != nil || before == 0 {
		t.Fatalf("seeded journal %d %v", before, err)
	}
	migrateTo(t, pool, 26)
	names := triggerNames(t, pool)
	for _, gone := range []string{"task_event_journal", "journal_note_protection", "journal_tree_created", "journal_tree_config", "journal_tree_status",
		"journal_membership", "journal_need_added", "journal_need_removed", "journal_member_created", "thread_project_insert", "acceptance_session_insert",
		"task_handoffs_source_session_id_insert", "task_handoffs_source_session_id_update"} {
		if names[gone] {
			t.Errorf("trigger %s survived 0026", gone)
		}
	}
	for _, kept := range []string{"tasks_session_id_insert", "journal_entries_source_session_id_insert", "tasks_connection_insert"} {
		if !names[kept] {
			t.Errorf("compatibility trigger %s dropped before #417", kept)
		}
	}
	var after, rev int
	if err := pool.QueryRow(`SELECT (SELECT count(*) FROM journal_entries), (SELECT membership_revision FROM task_trees WHERE id = 1)`).Scan(&after, &rev); err != nil || after != before || rev != 2 {
		t.Fatalf("journal %d -> %d, revision %d: %v", before, after, rev, err)
	}
	mustExec(t, pool, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES(1,'backlog','backlog','raw')`)
	if err := pool.QueryRow(`SELECT count(*) FROM journal_entries`).Scan(&after); err != nil || after != before {
		t.Fatalf("a raw insert journaled after 0026: %d -> %d %v", before, after, err)
	}
}
