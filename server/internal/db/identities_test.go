package db

import (
	"context"
	"github.com/flexdinesh/stage/server/internal/t3"
	"testing"
)

func TestIdentityUpgradePreservesHistory(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 7)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(71,'external-p','Stage title','/old','t3')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,t3_thread_id,origin_thread_id,launch_attempt) VALUES(81,71,'Task','Prompt','same','uncached-source',1)`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,source_thread_id,text,request_id) VALUES(901,'note',81,'uncached-source','Keep exact text','keep')`)
	mustExec(t, pool, `INSERT INTO task_acceptances(id,task_id,provenance,evidence,note) VALUES(61,81,'user','{"threadId":"same","reply":"exact reply"}','Accepted')`)
	mustExec(t, pool, `UPDATE t3_connection SET token='preserve-token',environment_id='environment',t3_url='http://existing'`)
	migrateTo(t, pool, 8)
	s := NewStore(pool)
	ctx := context.Background()
	p, err := s.GetProject(ctx, 71)
	if err != nil || p.Title != "Stage title" || len(p.Bindings) != 1 || p.Bindings[0].ExternalProjectID != "external-p" {
		t.Fatalf("project %+v %v", p, err)
	}
	task, err := s.GetTask(ctx, 81)
	if err != nil || task.Session == nil || task.Session.ConnectionID != 1 || task.Session.ExternalSessionID != "same" || task.OriginSession == nil || task.OriginSession.ExternalSessionID != "uncached-source" {
		t.Fatalf("task %+v %v", task, err)
	}
	var text, evidence, token string
	if err = pool.QueryRow(`SELECT text FROM journal_entries WHERE id=901`).Scan(&text); err != nil || text != "Keep exact text" {
		t.Fatal(text, err)
	}
	if err = pool.QueryRow(`SELECT evidence FROM task_acceptances WHERE id=61`).Scan(&evidence); err != nil || evidence != `{"threadId":"same","reply":"exact reply"}` {
		t.Fatal(evidence, err)
	}
	if err = pool.QueryRow(`SELECT token FROM t3_connection`).Scan(&token); err != nil || token != "preserve-token" {
		t.Fatal("credentials changed", err)
	}
	dest, err := s.LaunchDestination(ctx, 81, 1)
	if err != nil || dest.Workspace != "/old" {
		t.Fatal(dest, err)
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(`SELECT count(*) FROM project_bindings`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration repeated", count, err)
	}
	rows, err := pool.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("invalid migration references")
	}
}

func TestSessionsAreConnectionScoped(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 8)
	s := NewStore(pool)
	ctx := context.Background()
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	a, err := s.EnsureSession(ctx, SessionRef{ConnectionID: 1, ExternalSessionID: "same"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnsureSession(ctx, SessionRef{ConnectionID: 2, ExternalSessionID: "same"})
	if err != nil || a == b {
		t.Fatal("collision", a, b, err)
	}
	p, err := s.InsertProject(ctx, Project{Title: "Offline"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,session_id) VALUES(1,?,'T3','p',1,?),(2,?,'OC','p',2,?)`, p.ID, a, p.ID, b)
	for conn, want := range map[int64]int64{1: 1, 2: 2} {
		got, err := s.TaskBySession(ctx, SessionRef{ConnectionID: conn, ExternalSessionID: "same"})
		if err != nil || got.ID != want || got.Session.ConnectionID != conn {
			t.Fatalf("owner %+v %v", got, err)
		}
	}
}

func TestBindingOwnershipAndPinnedDestination(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 8)
	s := NewStore(pool)
	ctx := context.Background()
	p, err := s.InsertProject(ctx, Project{Title: "Stage", Bindings: []ProjectBinding{{ConnectionID: 1, ExternalProjectID: "p", Workspace: "/old"}}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt) VALUES(1,?,'task','p')`, p.ID)
	if _, err = s.TransitionTask(ctx, Transition{TaskID: 1, To: StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindProject(ctx, ProjectBinding{ProjectID: p.ID, ConnectionID: 1, ExternalProjectID: "new", Workspace: "/new"}); err != nil {
		t.Fatal(err)
	}
	dest, err := s.LaunchDestination(ctx, 1, 1)
	if err != nil || dest.Workspace != "/old" {
		t.Fatal("claim redirected", dest, err)
	}
	if _, err = s.UpsertT3Projects(ctx, []t3.Project{{ID: "new", Title: "External title", WorkspaceRoot: "/moved"}}); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProject(ctx, p.ID)
	if err != nil || p.Title != "Stage" || p.ArchivedAt != nil || p.Bindings[0].Status != "changed" {
		t.Fatal("metadata changed", p, err)
	}
	q, err := s.InsertProject(ctx, Project{Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindProject(ctx, ProjectBinding{ProjectID: q.ID, ConnectionID: 1, ExternalProjectID: "new", Workspace: "/new"}); err == nil {
		t.Fatal("ambiguous binding accepted")
	}
	if _, err = s.UpsertT3Projects(ctx, nil); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProject(ctx, p.ID)
	if err != nil || p.Title != "Stage" || p.Bindings[0].Status != "missing" {
		t.Fatal(p, err)
	}
}

func TestScopedHistoryNeverReadsAnotherConnection(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 8)
	s := NewStore(pool)
	ctx := context.Background()
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	p, err := s.InsertProject(ctx, Project{Title: "Offline"})
	if err != nil {
		t.Fatal(err)
	}
	a := SessionRef{ConnectionID: 1, ExternalSessionID: "same"}
	b := SessionRef{ConnectionID: 2, ExternalSessionID: "same"}
	bid, err := s.EnsureSession(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,session_id) VALUES(1,?,'OC','p',2,?)`, p.ID, bid)
	mustExec(t, pool, `INSERT INTO threads(t3_thread_id,t3_project_id,title,status,last_word_text,created_at,updated_at) VALUES('same','unbound','T3','idle','T3 private reply',stage_now(),stage_now())`)
	preview, err := s.CompletionPreview(ctx, 1)
	if err != nil || preview.Evidence.Reply != nil || preview.Evidence.Session == nil || *preview.Evidence.Session != b {
		t.Fatalf("cross-connection evidence %+v %v", preview, err)
	}
	for _, ref := range []SessionRef{a, b} {
		if _, err = s.InsertNotification(ctx, NewNotification{Event: StateDone, Session: &ref, Title: "Reply", URL: "/"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"thread:1:same", "thread:2:same"} {
		events, err := s.ListNotificationHistory(ctx, key, 10, nil)
		if err != nil || len(events) != 1 || NotificationKey(events[0]) != key {
			t.Fatalf("history %s %+v %v", key, events, err)
		}
	}
	id := int64(1)
	in := AppendNoteInput{Text: "From OpenCode", RequestID: "scoped", SourceSession: &b}
	note, err := s.AppendNote(ctx, JournalOwner{TaskID: &id}, in)
	if err != nil || note.SourceSession == nil || *note.SourceSession != b {
		t.Fatal(note, err)
	}
	again, err := s.AppendNote(ctx, JournalOwner{TaskID: &id}, in)
	if err != nil || again.ID != note.ID {
		t.Fatal("retry", again, err)
	}
	in.SourceSession = &a
	if _, err = s.AppendNote(ctx, JournalOwner{TaskID: &id}, in); err == nil {
		t.Fatal("different connection reused note request")
	}
}
