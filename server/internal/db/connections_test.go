package db

import (
	"context"
	"errors"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func foreignKeyViolations(t *testing.T, pool *sqlite.DB) int {
	t.Helper()
	rows, err := pool.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n
}

// Migration 0012: connections gain identity, label, active and retirement:
// each takes its instance's identity (T3 environment, verified OpenCode
// address); one active connection per harness; data stays on it.
func seedV0012ConnectionIdentity(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,auto_complete) VALUES(2,'opencode',0)`)
	mustExec(t, pool, `UPDATE t3_connection SET environment_id='env-a',token='keep'`)
	mustExec(t, pool, `UPDATE opencode_settings SET endpoint='http://oc:4096',verified_endpoint='http://oc:4096'`)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace) VALUES(1,1,'p','/w'),(1,2,'oc','/w')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,1,'thread'),(6,2,'ses_x')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,session_id,connection_id,launch_attempt) VALUES(1,1,'t3','p','running',5,1,1),(2,1,'oc','p','running',6,2,1)`)
	mustExec(t, pool, `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES(2,'/srv','/home')`)
}

func checkV0012ConnectionIdentity(t *testing.T, pool *sqlite.DB) {
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	wantRows(t, pool, `SELECT id,harness,identity,label,active,retired_at,auto_complete FROM harness_connections ORDER BY id`,
		"1|t3|env-a|env-a|1|NULL|1", "2|opencode|http://oc:4096|http://oc:4096|1|NULL|0")
	wantRows(t, pool, `SELECT id,connection_id,session_id FROM tasks ORDER BY id`, "1|1|5", "2|2|6")
	wantRows(t, pool, `SELECT connection_id,external_project_id FROM project_bindings ORDER BY connection_id`, "1|p", "2|oc")
	wantRows(t, pool, `SELECT connection_id,harness_root,stage_root FROM shared_paths`, "2|/srv|/home")
	wantRows(t, pool, `SELECT token FROM t3_connection`, "keep")
	// Only one active connection per harness.
	mustFail(t, pool, "second active T3 connection", `INSERT INTO harness_connections(harness) VALUES('t3')`)
	// New AUTOINCREMENT ids never reuse an old one.
	var next int64
	if err := pool.QueryRow(`INSERT INTO harness_connections(harness,active,retired_at) VALUES('t3',0,stage_now()) RETURNING id`).Scan(&next); err != nil || next <= 2 {
		t.Fatalf("next id %d %v", next, err)
	}
}

// After 0012, the store reads connections, settings and tasks on their
// instance at the latest schema.
func TestConnectionIdentityMigrationKeepsDataOnTheCurrentInstance(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 11)
	seedV0012ConnectionIdentity(t, pool)
	migrateTo(t, pool, sqlite.MaxVersion()) // the store reads the latest schema
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	s := NewStore(pool)
	ctx := context.Background()
	cs, err := s.Connections(ctx)
	if err != nil || len(cs) != 2 {
		t.Fatalf("connections %+v %v", cs, err)
	}
	if c := cs[0]; c.ID != 1 || c.Harness != HarnessT3 || !c.Active || c.Label != "env-a" || c.Tasks != 1 || c.Bindings != 1 {
		t.Fatalf("t3 %+v", c)
	}
	if c := cs[1]; c.ID != 2 || c.Harness != HarnessOpenCode || !c.Active || c.AutoComplete || c.Label != "http://oc:4096" {
		t.Fatalf("opencode %+v", c)
	}
	conn, err := s.GetConnection(ctx, LocalMachine)
	if err != nil || conn.EnvironmentID == nil || *conn.EnvironmentID != "env-a" || conn.ConnectionID != 1 {
		t.Fatalf("t3 connection %+v %v", conn, err)
	}
	if _, _, verified, err := s.OpenCodeSettings(ctx, LocalMachine); err != nil || verified != "http://oc:4096" {
		t.Fatalf("verified %q %v", verified, err)
	}
	task, err := s.GetTask(ctx, 2)
	if err != nil || task.ConnectionID != 2 || task.Harness != HarnessOpenCode || !task.ConnectionActive || task.Session == nil || task.Session.ExternalSessionID != "ses_x" {
		t.Fatalf("task %+v %v", task, err)
	}
}

func TestLegacyThreadIdsMapToTheActiveT3Connection(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	first := s.LegacyT3ID()
	project := projectWithBinding(t, s, first, "/w/a")
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil {
		t.Fatal(err)
	}
	sw, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-b", "box b", 0)
	if err != nil || !sw.Changed || sw.Old != first {
		t.Fatalf("switch %+v %v", sw, err)
	}
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,state,t3_thread_id,origin_thread_id,connection_id) VALUES($1,'t','p','running','thread-b','origin-b',$2)`, project, sw.New)
	var session, origin int64
	if err := pool.QueryRow(`SELECT (SELECT connection_id FROM session_identities WHERE id=t.session_id),(SELECT connection_id FROM session_identities WHERE id=t.origin_session_id) FROM tasks t WHERE t3_thread_id='thread-b'`).Scan(&session, &origin); err != nil || session != sw.New || origin != sw.New {
		t.Fatalf("legacy ids on %d/%d, want %d (%v)", session, origin, sw.New, err)
	}
}

func connectionStore(t *testing.T) (*Store, context.Context, *sqlite.DB) {
	t.Helper()
	pool := openUnmigrated(t)
	migrateTo(t, pool, sqlite.MaxVersion())
	return NewStore(pool), context.Background(), pool
}

func projectWithBinding(t *testing.T, s *Store, connection int64, workspace string) int64 {
	t.Helper()
	p, err := s.InsertProject(context.Background(), Project{Title: workspace, Bindings: []ProjectBinding{{ConnectionID: connection, ExternalProjectID: "p" + workspace, Workspace: workspace}}})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestSwitchInstanceRetiresRevivesAndMovesUnlaunchedWork(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	first := s.LegacyT3ID()
	// First pairing stamps the connection in place.
	if sw, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil || sw.Changed || s.LegacyT3ID() != first {
		t.Fatalf("stamp %+v %v", sw, err)
	}
	project := projectWithBinding(t, s, first, "/w/a")
	backlog, err := s.CreateTask(ctx, NewTask{ProjectID: project, Title: "queued", Prompt: "p", ConnectionID: first, RuntimeMode: "full-access", InteractionMode: "default", Workspace: Workspace{Type: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,state,t3_thread_id,connection_id) VALUES($1,'ran','p','running','thread-a',$2)`, project, first)
	schedule, err := s.CreateSchedule(ctx, NewSchedule{ProjectID: project, Title: "nightly", Prompt: "p", Cron: "0 3 * * *", Tz: "UTC", ConnectionID: first, RuntimeMode: "full-access", InteractionMode: "default", Workspace: Workspace{Type: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	pin := &execution.Config{Model: `["claude","m"]`, Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}}
	if _, err := s.SetNewTaskConfig(ctx, first, pin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDefaultConnection(ctx, &first); err != nil {
		t.Fatal(err)
	}

	sw, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-b", "box b", 0)
	if err != nil || !sw.Changed || sw.Revived || sw.Old != first || sw.New == first {
		t.Fatalf("replace %+v %v", sw, err)
	}
	second := sw.New
	// The replacement takes over the pin and the default.
	if c, _ := s.GetHarnessConnection(ctx, second); c.NewTaskConfig == nil || c.NewTaskConfig.Model != pin.Model || len(c.NewTaskConfig.Options) != 1 {
		t.Fatalf("pin not carried: %+v", c.NewTaskConfig)
	}
	if p, _ := s.GetPreferences(ctx); p.DefaultConnectionID == nil || *p.DefaultConnectionID != second {
		t.Fatalf("default not carried: %v", p.DefaultConnectionID)
	}
	if _, err := s.SetDefaultConnection(ctx, &first); err == nil {
		t.Fatal("replaced connection accepted as the default")
	}
	if s.LegacyT3ID() != second || s.ConnectionActive(first) || !s.IsT3(first) {
		t.Fatalf("active %d", s.LegacyT3ID())
	}
	ran, err := s.TaskBySession(ctx, SessionRef{ConnectionID: first, ExternalSessionID: "thread-a"})
	if err != nil || ran.ConnectionID != first || ran.ConnectionActive || ran.ConnectionLabel != "box a" || ran.State != StateRunning {
		t.Fatalf("launched task %+v %v", ran, err)
	}
	if got, _ := s.GetTask(ctx, backlog.ID); got.ConnectionID != second || !got.ConnectionActive {
		t.Fatalf("unlaunched task stayed on %d", got.ConnectionID)
	}
	if got, _ := s.GetSchedule(ctx, schedule.ID); got.ConnectionID != second {
		t.Fatalf("schedule stayed on %d", got.ConnectionID)
	}
	// The binding stays on the replaced connection: nothing is bound to env-b.
	p, _ := s.GetProject(ctx, project)
	if len(p.Bindings) != 1 || p.Bindings[0].ConnectionID != first || p.Bindings[0].ConnectionActive || p.Bindings[0].ConnectionLabel != "box a" {
		t.Fatalf("bindings %+v", p.Bindings)
	}
	// New work never lands on a replaced connection.
	var conflict *ConflictError
	if _, err := s.BindProject(ctx, ProjectBinding{ProjectID: project, ConnectionID: first, ExternalProjectID: "x", Workspace: "/x"}); !errors.As(err, &conflict) || conflict.Code != "connection_retired" {
		t.Fatalf("bind on replaced: %v", err)
	}
	if _, err := s.CreateTask(ctx, NewTask{ProjectID: project, Title: "t", Prompt: "p", ConnectionID: first, RuntimeMode: "full-access", InteractionMode: "default", Workspace: Workspace{Type: "root"}}); !errors.As(err, &conflict) || conflict.Code != "connection_retired" {
		t.Fatalf("task on replaced: %v", err)
	}

	// env-a again: its connection comes back; env-b held only moved work,
	// so it is deleted rather than kept.
	sw, err = s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0)
	if err != nil || !sw.Revived || sw.New != first || sw.Old != second {
		t.Fatalf("revive %+v %v", sw, err)
	}
	if got, _ := s.GetTask(ctx, backlog.ID); got.ConnectionID != first {
		t.Fatalf("unlaunched task not back on %d: %d", first, got.ConnectionID)
	}
	if _, err := s.GetHarnessConnection(ctx, second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty connection kept: %v", err)
	}
	// The default follows the revived connection, which keeps its own pin.
	if p, _ := s.GetPreferences(ctx); p.DefaultConnectionID == nil || *p.DefaultConnectionID != first {
		t.Fatalf("default after revive: %v", p.DefaultConnectionID)
	}
	if c, _ := s.GetHarnessConnection(ctx, first); c.NewTaskConfig == nil || c.NewTaskConfig.Model != pin.Model {
		t.Fatalf("revived pin %+v", c.NewTaskConfig)
	}
	if foreignKeyViolations(t, pool) != 0 {
		t.Fatal("foreign key violations")
	}
	if p, _ := s.GetProject(ctx, project); !p.Bindings[0].ConnectionActive {
		t.Fatal("revived binding inactive")
	}
}

func TestForgetConnection(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	first := s.LegacyT3ID()
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil {
		t.Fatal(err)
	}
	project := projectWithBinding(t, s, first, "/w/a")
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,state,t3_thread_id,connection_id) VALUES($1,'ran','p','done','thread-a',$2)`, project, first)
	mustExec(t, pool, `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES($1,'/srv','/home')`, first)
	var conflict *ConflictError
	if err := s.ForgetConnection(ctx, first); !errors.As(err, &conflict) {
		t.Fatalf("forgot the active connection: %v", err)
	}
	sw, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-b", "box b", 0)
	if err != nil || !sw.Changed {
		t.Fatal(sw, err)
	}
	if err := s.ForgetConnection(ctx, first); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetHarnessConnection(ctx, first)
	if err != nil || c.ForgottenAt == nil || c.Active || c.Label != "box a" || c.Bindings != 0 {
		t.Fatalf("forgotten %+v %v", c, err)
	}
	// History stays; the task still names its connection.
	task, err := s.TaskBySession(ctx, SessionRef{ConnectionID: first, ExternalSessionID: "thread-a"})
	if err != nil || task.ConnectionLabel != "box a" || task.ConnectionActive {
		t.Fatalf("task %+v %v", task, err)
	}
	var paths int
	if err := pool.QueryRow(`SELECT count(*) FROM shared_paths WHERE connection_id=$1`, first).Scan(&paths); err != nil || paths != 0 {
		t.Fatalf("shared paths %d %v", paths, err)
	}
	if err := s.ForgetConnection(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forgot twice: %v", err)
	}
	// Pairing env-a again starts a new connection: forgotten is final.
	sw, err = s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0)
	if err != nil || sw.Revived || sw.New == first {
		t.Fatalf("forgotten revived %+v %v", sw, err)
	}
	// A replaced connection nothing references is deleted outright.
	empty := sw.Old
	projectWithBinding(t, s, empty, "/w/b")
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-c", "box c", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetConnection(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHarnessConnection(ctx, empty); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreferenced connection kept: %v", err)
	}
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
}

func TestMovedConnectionsRetireOntoTheirReplacement(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'worker')`)
	old := s.LegacyT3ID()
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box", 0); err != nil {
		t.Fatal(err)
	}
	sw, err := s.SwitchInstance(ctx, 2, t3.Descriptor, "env-a", "box", 0)
	if err != nil || sw.New == old {
		t.Fatal(sw, err)
	}
	moved := sw.New
	// Another identity on the worker's OpenCode never pairs with anything.
	if _, err := s.SwitchInstance(ctx, 2, harness.Descriptor{Kind: HarnessOpenCode, Name: "OpenCode", Hosting: harness.Connected}, "http://oc", "oc", 0); err != nil {
		t.Fatal(err)
	}
	moves, err := s.MovedConnections(ctx)
	if err != nil || len(moves) != 2 || moves[0] != (Move{Old: old, New: moved}) || moves[1] != (Move{Old: moved, New: old}) {
		t.Fatalf("moves %+v %v", moves, err)
	}
	project := projectWithBinding(t, s, old, "/w/a")
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,state,t3_thread_id,connection_id) VALUES($1,'ran','p','done','thread-a',$2)`, project, old)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,connection_id) VALUES(50,$1,'planned','p','backlog',$2)`, project, old)
	if _, err := s.SetDefaultConnection(ctx, &old); err != nil {
		t.Fatal(err)
	}
	var conflict *ConflictError
	if err := s.RetireConnection(ctx, old, old); !errors.As(err, &conflict) {
		t.Fatalf("retired onto itself: %v", err)
	}
	if err := s.RetireConnection(ctx, old, moved); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetHarnessConnection(ctx, old)
	if err != nil || c.Active || c.RetiredAt == nil || c.ForgottenAt != nil || c.Tasks != 1 || c.Bindings != 1 {
		t.Fatalf("retired %+v %v", c, err)
	}
	var backlog int64
	if err := pool.QueryRow(`SELECT connection_id FROM tasks WHERE id=50`).Scan(&backlog); err != nil || backlog != moved {
		t.Fatalf("backlog on %d %v", backlog, err)
	}
	if p, err := s.GetPreferences(ctx); err != nil || p.DefaultConnectionID == nil || *p.DefaultConnectionID != moved {
		t.Fatalf("default %+v %v", p, err)
	}
	if moves, err := s.MovedConnections(ctx); err != nil || len(moves) != 0 {
		t.Fatalf("moves after retiring %+v %v", moves, err)
	}
	if err := s.RetireConnection(ctx, old, moved); !errors.As(err, &conflict) {
		t.Fatalf("retired twice: %v", err)
	}
	if s.LegacyT3ID() != 0 {
		t.Fatalf("local T3 still active: %d", s.LegacyT3ID())
	}
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
}

func TestRetireAndForgetLetsGoOfADisconnectedConnection(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	used := s.LegacyT3ID()
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box", 0); err != nil {
		t.Fatal(err)
	}
	project := projectWithBinding(t, s, used, "/w/a")
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,state,t3_thread_id,connection_id) VALUES($1,'ran','p','done','thread-a',$2)`, project, used)
	if _, err := s.SetDefaultConnection(ctx, &used); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireAndForget(ctx, used); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetHarnessConnection(ctx, used)
	if err != nil || c.Active || c.ForgottenAt == nil || c.Bindings != 0 || c.Tasks != 1 {
		t.Fatalf("forgotten %+v %v", c, err)
	}
	if p, err := s.GetPreferences(ctx); err != nil || p.DefaultConnectionID != nil {
		t.Fatalf("default %+v %v", p, err)
	}
	if err := s.RetireAndForget(ctx, used); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forgot twice: %v", err)
	}
	// An empty disconnected connection is deleted outright.
	sw, err := s.SwitchInstance(ctx, LocalMachine, harness.Descriptor{Kind: HarnessOpenCode, Name: "OpenCode", Hosting: harness.Connected}, "http://oc", "oc", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetireAndForget(ctx, sw.New); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHarnessConnection(ctx, sw.New); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty connection kept: %v", err)
	}
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
}
