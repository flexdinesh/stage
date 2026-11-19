package agenttools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/release"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var testDB dbtest.DB

type env struct {
	t       *testing.T
	handler http.Handler // the /api router
	fake    *t3.Fake
	sync    *t3sync.Service
	runner  *release.Runner
	client  *client.Client
	store   *db.Store
	url     string
}

func newEnv(t *testing.T) *env {
	return harnessEnv(t, nil)
}
func harnessEnv(t *testing.T, other execution.Adapter) *env {
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	fake := t3.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapters := map[int64]execution.Adapter{1: t3.NewAdapter(fake)}
	if other != nil {
		if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness) VALUES(7,'opencode')"); err != nil {
			t.Fatal(err)
		}
		adapters[7] = other
	}
	svc := t3sync.NewWithAdapters(store, adapters, log)
	runner := release.NewRunner(store, svc, log)
	svc.OnTransition = runner.OnTransition
	h := api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://t3.test"},
		Store:  store, Setup: t3setup.New(t3setup.Options{Config: config.Config{T3PublicURL: "http://localhost:3773"}, Store: store, Client: fake, Fake: true, Logger: log}), Sync: svc, Push: notify.New(store, "https://stage.test", log), Logger: log,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{store: store, t: t, handler: h, fake: fake, sync: svc, runner: runner, client: client.New(srv.URL), url: srv.URL}
}

func (e *env) project(root string) db.Project {
	e.t.Helper()
	ctx := context.Background()
	p, err := e.fake.CreateProject(ctx, t3.CreateProjectInput{Title: "P " + root, WorkspaceRoot: root})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.sync.FullSync(ctx); err != nil {
		e.t.Fatal(err)
	}
	out, err := e.store.InsertProject(ctx, db.Project{Title: p.Title, Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: p.ID, Workspace: root}}})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// session launches a running external thread in project p.
func (e *env) session(p db.Project, title string) string {
	e.t.Helper()
	ctx := context.Background()
	out, err := e.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: p.Bindings[0].ExternalProjectID, Title: title})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.sync.RefreshSession(ctx, out.ThreadID); err != nil {
		e.t.Fatal(err)
	}
	return out.ThreadID
}

// connect starts an in-memory MCP session over ops.
func connect(t *testing.T, o *agenttools.Ops) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := agenttools.NewServer(o).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call invokes a tool; out (if non-nil) receives the structured result.
// Returns the tool error text ("" on success).
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	return callMeta(t, cs, name, args, nil, out)
}
func callMeta(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, meta mcp.Meta, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: meta})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		var msgs []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msgs = append(msgs, tc.Text)
			}
		}
		return strings.Join(msgs, " ")
	}
	if out != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: decode %s: %v", name, b, err)
		}
	}
	return ""
}

func mustCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	if msg := call(t, cs, name, args, out); msg != "" {
		t.Fatalf("%s: %s", name, msg)
	}
}

func TestToolList(t *testing.T) {
	cs := connect(t, &agenttools.Ops{Client: client.New("http://127.0.0.1:1")})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	want := []string{
		"get_context", "list_projects", "list_models", "list_tasks", "get_task", "create_task", "update_task",
		"delete_task", "archive_task", "start_task", "stop_task", "duplicate_task",
		"wait_task", "list_schedules", "get_schedule", "create_schedule", "update_schedule", "delete_schedule",
		"run_schedule", "preview_schedule", "read_ref",
		"list_trees", "get_tree", "create_tree", "update_tree", "delete_tree", "add_to_tree", "remove_from_tree", "set_needs", "start_tree",
		"adopt_session", "read_journal", "get_journal_entry", "append_note",
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
	init := cs.InitializeResult()
	if init == nil || !strings.Contains(init.Instructions, "afterMe") || !strings.Contains(init.Instructions, "read_ref") ||
		!strings.Contains(init.Instructions, "trees are the only way to express dependencies") || !strings.Contains(init.Instructions, "adopt_session") {
		t.Fatalf("instructions missing: %+v", init)
	}
}

func TestCreateTaskWithCallerContext(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/agent")
	other := e.project("/w/other")
	s := e.session(p, "Planner")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: s, Cwd: "/w/other/sub"}})

	var cx agenttools.ContextOut
	mustCall(t, cs, "get_context", nil, &cx)
	if cx.ThreadID != s || cx.Project == nil || cx.Project.ID != p.ID || cx.ProjectSource != "session" || cx.TreeID != nil || cx.TreeTitle != nil {
		t.Fatalf("context %+v (session beats cwd)", cx)
	}

	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "Follow-up", "prompt": "do it", "afterMe": true, "effort": "high", "tags": []string{"agent"}}, &tk)
	if tk.ProjectID != p.ID || tk.State != db.StateBacklog || len(tk.Needs) != 1 || tk.Readiness == nil || *tk.Readiness != db.ReadinessBlocked ||
		tk.OriginThreadID == nil || *tk.OriginThreadID != s || len(tk.ModelOptions) != 1 || tk.ModelOptions[0].ID != "effort" {
		t.Fatalf("task %+v", tk)
	}
	// afterMe tracks the caller's session as a task and needs it.
	var me client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": tk.Needs[0]}, &me)
	if !me.Adopted || me.T3ThreadID == nil || *me.T3ThreadID != s || me.TreeID == nil || *me.TreeID != *tk.TreeID {
		t.Fatalf("caller task %+v", me.Task)
	}

	mustCall(t, cs, "get_context", nil, &cx)
	if cx.CallerTaskID == nil || *cx.CallerTaskID != me.ID || cx.TreeID == nil || *cx.TreeID != *tk.TreeID || cx.TreeTitle == nil {
		t.Fatalf("current tree context %+v", cx)
	}

	// Explicit project wins; arguments override the environment.
	var tk2 client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "B", "prompt": "x", "project": other.Title}, &tk2)
	if tk2.ProjectID != other.ID {
		t.Fatalf("explicit project: %+v", tk2)
	}
	var cx2 agenttools.ContextOut
	mustCall(t, cs, "get_context", map[string]any{"callerThreadId": "unknown-thread", "cwd": "/w/other/x"}, &cx2)
	if cx2.ThreadID != "unknown-thread" || cx2.Project != nil || cx2.ProjectSource != "" {
		t.Fatalf("cwd fallback %+v", cx2)
	}

	// afterMe without a session id is an error.
	noCaller := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/agent"}})
	if msg := call(t, noCaller, "create_task", map[string]any{"project": "1", "title": "c", "prompt": "x", "afterMe": true}, nil); !strings.Contains(msg, "session id") {
		t.Fatalf("afterMe without session: %q", msg)
	}
	if msg := call(t, noCaller, "create_task", map[string]any{"title": "c", "prompt": "x", "project": "nope"}, nil); !strings.Contains(msg, "no project") {
		t.Fatalf("unknown project: %q", msg)
	}

	// The session settling starts the task.
	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	_ = e.fake.Settle(s)
	if err := e.sync.PollActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.runner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": tk.ID}, &got)
	if got.State != db.StateBacklog || got.Readiness == nil || *got.Readiness != db.ReadinessBlocked {
		t.Fatalf("after settle %+v", got.Task)
	}
	var rows agenttools.TasksOut
	mustCall(t, cs, "list_tasks", map[string]any{"project": fmt.Sprint(p.ID), "states": []string{"running"}}, &rows)
	if rows.Total != 0 {
		t.Fatalf("list %+v", rows)
	}
}

func TestCreateTaskDefaultsCarryModelNotModes(t *testing.T) {
	e := newEnv(t)
	e.project("/w/defaults")
	provider, model := "claude", "claude-haiku-5-5"
	if _, err := e.store.SetPinned(t.Context(), &db.ModelDefaults{ProviderInstanceID: &provider, Model: &model, RuntimeMode: "approval-required", InteractionMode: "plan"}); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/defaults"}})
	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "d", "prompt": "x"}, &tk)
	if tk.Model == nil || *tk.Model != model || tk.InteractionMode == "plan" || tk.RuntimeMode == "approval-required" {
		t.Fatalf("pinned modes leaked: model %v runtime %q interaction %q", tk.Model, tk.RuntimeMode, tk.InteractionMode)
	}
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "e", "prompt": "x", "interactionMode": "plan", "runtimeMode": "auto"}, &tk)
	if tk.InteractionMode != "plan" || tk.RuntimeMode != "auto" {
		t.Fatalf("explicit modes dropped: runtime %q interaction %q", tk.RuntimeMode, tk.InteractionMode)
	}
}

func TestLifecycleWaitTools(t *testing.T) {
	e := newEnv(t)
	e.project("/w/life")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/life"}, PollEvery: 10 * time.Millisecond})

	var a, b client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "a", "prompt": "x", "start": true}, &a)
	if a.State == db.StateBacklog || a.T3ThreadID == nil {
		t.Fatalf("start=true %+v", a)
	}
	if msg := call(t, cs, "create_task", map[string]any{"project": "1", "title": "b", "prompt": "y", "needs": []int64{a.ID}, "start": true}, nil); !strings.Contains(msg, "cannot also start now") {
		t.Fatalf("needs with start=true: %q", msg)
	}
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "b", "prompt": "y", "needs": []int64{a.ID}}, &b)
	if !slices.Equal(b.Needs, []int64{a.ID}) || b.TreeID == nil {
		t.Fatalf("needs %+v", b)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": b.ID, "title": "b2", "baseRef": "origin/main"}, &b)
	if b.Title != "b2" || b.Workspace.Type != "worktree" || b.Workspace.BaseRef != "origin/main" {
		t.Fatalf("update %+v", b)
	}

	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
	if err := e.sync.PollActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	var w agenttools.WaitOut
	mustCall(t, cs, "wait_task", map[string]any{"id": a.ID, "timeoutSeconds": 5}, &w)
	if !w.Reached || w.Task.State != db.StateDone {
		t.Fatalf("wait %+v", w)
	}
	mustCall(t, cs, "wait_task", map[string]any{"id": b.ID, "until": []string{"running"}, "timeoutSeconds": 1}, &w)
	if w.Reached || !w.TimedOut {
		t.Fatalf("wait timeout %+v", w)
	}

	var dup client.Task
	mustCall(t, cs, "duplicate_task", map[string]any{"id": b.ID}, &dup)
	var del agenttools.DeletedOut
	mustCall(t, cs, "delete_task", map[string]any{"id": dup.ID}, &del)
	if !del.Deleted {
		t.Fatal("delete")
	}
	mustCall(t, cs, "stop_task", map[string]any{"id": a.ID}, nil)
}

// settle completes and settles a launched task's thread, then lets the
// release runner react.
func (e *env) acceptTask(tk client.Task) {
	e.t.Helper()
	if tk.T3ThreadID == nil {
		e.t.Fatalf("task %d not launched", tk.ID)
	}
	_ = e.fake.SetStatus(*tk.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*tk.T3ThreadID)
	if err := e.sync.PollActive(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	p, err := e.store.CompletionPreview(context.Background(), tk.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err = e.store.ChangeAcceptance(context.Background(), tk.ID, db.AcceptanceRequest{RequestID: fmt.Sprintf("accept-%d", tk.ID), Revision: p.Revision, EvidenceVersion: p.EvidenceVersion}, false); err != nil {
		e.t.Fatal(err)
	}
	if err := e.runner.Reconcile(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func readiness(t client.Task) string {
	if t.Readiness == nil {
		return ""
	}
	return string(*t.Readiness)
}

func TestTreeStartModeDefaultsToInherit(t *testing.T) {
	e := newEnv(t)
	e.project("/w/inherit")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/inherit"}})
	var root client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "root", "prompt": "root", "start": true}, &root)
	var tree client.TreeDetail
	mustCall(t, cs, "create_tree", map[string]any{
		"title": "Shared policy", "startMode": "manual", "tasks": []map[string]any{{"id": root.ID}},
	}, &tree)
	if len(tree.Tasks) != 1 || tree.Tasks[0].StartMode != nil {
		t.Fatalf("joining a tree must preserve default inheritance: %+v", tree.Tasks)
	}

	var members []client.Task
	for _, mode := range []string{"", "inherit", "manual", "auto"} {
		args := map[string]any{
			"project": "1",
			"title":   "member " + mode, "prompt": "work", "treeId": tree.ID, "needs": []int64{root.ID},
		}
		if mode != "" {
			args["startMode"] = mode
		}
		var member client.Task
		mustCall(t, cs, "create_task", args, &member)
		if mode == "" || mode == "inherit" {
			if member.StartMode != nil || member.EffectiveStartMode == nil || *member.EffectiveStartMode != db.StartManual {
				t.Fatalf("default/explicit inheritance for %q: %+v", mode, member)
			}
		} else if member.StartMode == nil || string(*member.StartMode) != mode {
			t.Fatalf("explicit override %q: %+v", mode, member)
		}
		members = append(members, member)
	}
	e.acceptTask(root)
	for i, member := range members {
		var got client.TaskDetail
		mustCall(t, cs, "get_task", map[string]any{"id": member.ID}, &got)
		if i == 3 {
			if got.State != db.StateRunning {
				t.Fatalf("auto exception must launch in a manual tree: %+v", got.Task)
			}
		} else if got.State != db.StateBacklog || readiness(got.Task) != "ready" {
			t.Fatalf("manual member must wait: %+v", got.Task)
		}
	}

	mustCall(t, cs, "update_tree", map[string]any{"id": tree.ID, "startMode": "auto"}, &tree)
	if err := e.runner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, member := range members[:3] {
		var got client.TaskDetail
		mustCall(t, cs, "get_task", map[string]any{"id": member.ID}, &got)
		if i < 2 {
			if got.StartMode != nil || got.State != db.StateRunning || got.EffectiveStartMode == nil || *got.EffectiveStartMode != db.StartAuto {
				t.Fatalf("inherited task must follow tree change: %+v", got.Task)
			}
		} else if got.StartMode == nil || *got.StartMode != db.StartManual || got.State != db.StateBacklog || readiness(got.Task) != "ready" {
			t.Fatalf("tree change must preserve manual exception: %+v", got.Task)
		}
	}
	var inherited client.Task
	mustCall(t, cs, "update_task", map[string]any{"id": members[2].ID, "startMode": "inherit"}, &inherited)
	if err := e.runner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": inherited.ID}, &got)
	if got.StartMode != nil || got.State != db.StateRunning {
		t.Fatalf("clearing override must follow tree and launch: %+v", got.Task)
	}
}

func TestLifecycleTreeTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/trees")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/trees"}})
	newTask := func(title string, extra map[string]any) client.Task {
		t.Helper()
		args := map[string]any{"project": "1", "title": title, "prompt": "do " + title}
		for k, v := range extra {
			args[k] = v
		}
		var tk client.Task
		mustCall(t, cs, "create_task", args, &tk)
		return tk
	}
	a, b, c := newTask("a", nil), newTask("b", nil), newTask("c", nil)

	// Fan-in: c needs a and b; manual tree.
	if msg := call(t, cs, "create_tree", map[string]any{"title": "x", "startMode": "inherit", "tasks": []map[string]any{{"id": a.ID}}}, nil); !strings.Contains(msg, "auto|manual") {
		t.Fatalf("create_tree startMode inherit: %q", msg)
	}
	var tree client.TreeDetail
	mustCall(t, cs, "create_tree", map[string]any{"title": "Ship", "startMode": "manual", "tasks": []map[string]any{
		{"id": a.ID}, {"id": b.ID}, {"id": c.ID, "needs": []int64{a.ID, b.ID}},
	}}, &tree)
	if tree.Title != "Ship" || tree.StartMode != db.StartManual || len(tree.Tasks) != 3 ||
		len(tree.Levels) != 2 || !slices.Equal(tree.Levels[0], []int64{a.ID, b.ID}) || !slices.Equal(tree.Levels[1], []int64{c.ID}) {
		t.Fatalf("create_tree %+v", tree)
	}

	var trees agenttools.TreesOut
	mustCall(t, cs, "list_trees", map[string]any{"project": p.Title}, &trees)
	if len(trees.Trees) != 1 {
		t.Fatalf("list_trees %+v", trees)
	}
	if r := trees.Trees[0]; r.ID != tree.ID || r.Tasks != 3 || r.Levels != 2 || r.States["ready"] != 2 || r.States["blocked"] != 1 ||
		len(r.States) != 2 || !slices.Equal(r.Projects, []string{p.Title}) || r.SettledAt != nil {
		t.Fatalf("tree row %+v", r)
	}

	mustCall(t, cs, "update_tree", map[string]any{"id": tree.ID, "title": "Ship it"}, &tree)
	if tree.Title != "Ship it" || tree.StartMode != db.StartManual {
		t.Fatalf("update_tree %+v", tree)
	}
	if msg := call(t, cs, "update_tree", map[string]any{"id": tree.ID, "startMode": "later"}, nil); !strings.Contains(msg, "invalid startMode") {
		t.Fatalf("update_tree bad startMode: %q", msg)
	}

	// list_tasks rows carry tree membership and readiness.
	var rows agenttools.TasksOut
	mustCall(t, cs, "list_tasks", map[string]any{"project": p.Title}, &rows)
	for _, r := range rows.Tasks {
		want := "ready"
		if r.ID == c.ID {
			want = "blocked"
		}
		if r.TreeID == nil || *r.TreeID != tree.ID || r.Readiness == nil || string(*r.Readiness) != want || r.Adopted {
			t.Fatalf("row %+v, want readiness %s", r, want)
		}
	}

	// start_tree starts the ready roots only.
	var started client.TreeStart
	mustCall(t, cs, "start_tree", map[string]any{"id": tree.ID}, &started)
	if len(started.Started) != 2 || started.Started[0].ID != a.ID || started.Started[1].ID != b.ID || len(started.Errors) != 0 {
		t.Fatalf("start_tree roots %+v", started)
	}
	e.acceptTask(started.Started[0])
	e.acceptTask(started.Started[1])
	// Manual: c reads ready instead of launching.
	var cd client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": c.ID}, &cd)
	if cd.State != db.StateBacklog || readiness(cd.Task) != "ready" || cd.ReadyAt == nil || cd.Tree == nil || cd.Tree.ID != tree.ID {
		t.Fatalf("manual c after needs settle %+v", cd.Task)
	}

	// set_needs on a loose task joins the tree; add_to_tree with needs; remove_from_tree splices.
	d := newTask("d", nil)
	var dt client.Task
	mustCall(t, cs, "set_needs", map[string]any{"taskId": d.ID, "needs": []int64{c.ID}}, &dt)
	if dt.TreeID == nil || *dt.TreeID != tree.ID || !slices.Equal(dt.Needs, []int64{c.ID}) || readiness(dt) != "blocked" {
		t.Fatalf("set_needs %+v", dt)
	}
	f := newTask("f", nil)
	mustCall(t, cs, "add_to_tree", map[string]any{"treeId": tree.ID, "taskId": f.ID, "needs": []int64{d.ID}}, &tree)
	if len(tree.Levels) != 4 || !slices.Equal(tree.Levels[3], []int64{f.ID}) {
		t.Fatalf("add_to_tree levels %+v", tree.Levels)
	}
	var removed agenttools.RemovedOut
	mustCall(t, cs, "remove_from_tree", map[string]any{"taskId": d.ID}, &removed)
	if !removed.Removed || removed.TreeID != tree.ID {
		t.Fatalf("remove_from_tree %+v", removed)
	}
	mustCall(t, cs, "get_tree", map[string]any{"id": tree.ID}, &tree)
	var ft client.Task
	for _, m := range tree.Tasks {
		if m.ID == f.ID {
			ft = m
		}
		if m.ID == d.ID {
			t.Fatalf("d still in tree %+v", tree)
		}
	}
	if !slices.Equal(ft.Needs, []int64{c.ID}) {
		t.Fatalf("f takes over d's needs: %+v", ft)
	}
	if msg := call(t, cs, "remove_from_tree", map[string]any{"taskId": d.ID}, nil); !strings.Contains(msg, "not in a tree") {
		t.Fatalf("remove_from_tree outside a tree: %q", msg)
	}
	mustCall(t, cs, "set_needs", map[string]any{"taskId": f.ID, "needs": []int64{}}, &ft)
	if len(ft.Needs) != 0 || ft.TreeID == nil {
		t.Fatalf("set_needs [] clears needs, keeps the tree: %+v", ft)
	}

	// create_task with treeId + startMode; update_task startMode and baseTaskId.
	g := newTask("g", map[string]any{"treeId": tree.ID, "needs": []int64{c.ID, f.ID}, "startMode": "auto",
		"baseRef": "origin/main", "base": "predecessor", "baseTaskId": c.ID})
	if g.TreeID == nil || *g.TreeID != tree.ID || g.StartMode == nil || *g.StartMode != db.StartAuto ||
		g.Workspace.BaseTaskID == nil || *g.Workspace.BaseTaskID != c.ID {
		t.Fatalf("create_task tree fields %+v", g)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": g.ID, "startMode": "inherit", "baseTaskId": f.ID}, &g)
	if g.StartMode != nil || g.EffectiveStartMode == nil || *g.EffectiveStartMode != db.StartManual ||
		g.Workspace.BaseTaskID == nil || *g.Workspace.BaseTaskID != f.ID || !g.Workspace.FromPredecessor() {
		t.Fatalf("update_task startMode inherit, baseTaskId: %+v", g)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": g.ID, "startMode": "manual", "baseTaskId": 0}, &g)
	if g.StartMode == nil || *g.StartMode != db.StartManual || g.Workspace.BaseTaskID != nil {
		t.Fatalf("update_task startMode manual, baseTaskId 0: %+v", g)
	}
	if msg := call(t, cs, "update_task", map[string]any{"id": g.ID, "startMode": "soon"}, nil); !strings.Contains(msg, "invalid startMode") {
		t.Fatalf("update_task bad startMode: %q", msg)
	}
	if msg := call(t, cs, "create_task", map[string]any{"project": "1", "title": "h", "prompt": "x", "baseTaskId": c.ID}, nil); !strings.Contains(msg, "base needs a worktree") {
		t.Fatalf("baseTaskId without a worktree: %q", msg)
	}

	// start_tree starts the ready c (manual) and f (a root now).
	mustCall(t, cs, "start_tree", map[string]any{"id": tree.ID}, &started)
	if len(started.Started) != 2 || started.Started[0].ID != c.ID || started.Started[1].ID != f.ID {
		t.Fatalf("start_tree ready c %+v", started)
	}
}

func TestAdoptSessionTool(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/adopt")
	me := e.session(p, "Planner")
	other := e.session(p, "Other work")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: me}})

	var out agenttools.AdoptOut
	mustCall(t, cs, "adopt_session", map[string]any{"tags": []string{"plan"}}, &out)
	if !out.Created || !out.Task.Adopted || out.Task.T3ThreadID == nil || *out.Task.T3ThreadID != me ||
		out.Task.Title != "Planner" || out.Task.Prompt != "" || !slices.Equal(out.Task.Tags, []string{"plan"}) {
		t.Fatalf("adopt own session %+v", out)
	}
	mine := out.Task
	mustCall(t, cs, "adopt_session", nil, &out)
	if out.Created || out.Task.ID != mine.ID {
		t.Fatalf("adopt again returns the task %+v", out)
	}

	// Another session by id, titled, into an existing tree.
	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "next", "prompt": "x", "needs": []int64{mine.ID}}, &tk)
	if tk.TreeID == nil {
		t.Fatalf("needs on an adopted task builds a tree %+v", tk)
	}
	mustCall(t, cs, "adopt_session", map[string]any{"threadId": other, "title": "Side quest", "initialNote": "n", "treeId": *tk.TreeID}, &out)
	if !out.Created || out.Task.Title != "Side quest" || out.Task.TreeID == nil || *out.Task.TreeID != *tk.TreeID {
		t.Fatalf("adopt other into tree %+v", out)
	}
	var notes db.JournalPage
	mustCall(t, cs, "read_journal", map[string]any{"taskId": out.Task.ID}, &notes)
	if len(notes.Entries) != 1 || notes.Entries[0].Text != "n" {
		t.Fatalf("adoption initial note %+v", notes)
	}
	var rows agenttools.TasksOut
	mustCall(t, cs, "list_tasks", nil, &rows)
	adopted := 0
	for _, r := range rows.Tasks {
		if r.Adopted {
			adopted++
		}
	}
	if adopted != 2 {
		t.Fatalf("adopted rows %+v", rows)
	}
	if msg := call(t, cs, "start_task", map[string]any{"id": mine.ID}, nil); msg == "" {
		t.Fatal("start_task on an adopted task must fail")
	}

	noCaller := connect(t, &agenttools.Ops{Client: e.client})
	if msg := call(t, noCaller, "adopt_session", nil, nil); !strings.Contains(msg, "threadId is required") {
		t.Fatalf("adopt without a session: %q", msg)
	}
	if msg := call(t, noCaller, "adopt_session", map[string]any{"threadId": "nope"}, nil); !strings.Contains(msg, "404") {
		t.Fatalf("adopt unknown session: %q", msg)
	}
}

func TestLastWordTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/word")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/word"}})
	var a client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "a", "prompt": "x", "start": true}, &a)
	if a.T3ThreadID == nil {
		t.Fatalf("not launched %+v", a)
	}
	id := *a.T3ThreadID
	if err := e.fake.SetReply(id, t3.Reply{ItemID: "i1", Text: "## Done\nFixed **the race**."}); err != nil {
		t.Fatal(err)
	}
	if err := e.fake.SetThread(id, func(d *t3.ThreadDetail) { d.ItemCount = 2 }); err != nil {
		t.Fatal(err)
	}
	_ = e.fake.SetStatus(id, t3.StatusCompleted)

	if err := e.sync.RefreshSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var got client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": a.ID}, &got)
	if got.Thread == nil || got.Thread.LastWord == nil || got.Thread.LastWord.Text != "## Done\nFixed **the race**." {
		t.Fatalf("task thread last word %+v", got.Thread)
	}
	const preview = "Done Fixed the race."
	if got.LastWordPreview == nil || *got.LastWordPreview != preview {
		t.Fatalf("task preview %v", got.LastWordPreview)
	}
	var rows agenttools.TasksOut
	mustCall(t, cs, "list_tasks", map[string]any{"project": fmt.Sprint(p.ID)}, &rows)
	if rows.Total != 1 || rows.Tasks[0].LastWordPreview == nil || *rows.Tasks[0].LastWordPreview != preview {
		t.Fatalf("list_tasks %+v", rows)
	}

}

// A planning session queues dependent tasks, then verifies the batch.
func TestPlanningSessionBatch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/plan")
	s := e.session(p, "Planner")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: s}})
	var a, b, c client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "a", "prompt": "do a", "afterMe": true}, &a)
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "b", "prompt": "do b", "needs": []int64{a.ID}}, &b)
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "c", "prompt": "do c"}, &c)
	if b.TreeID == nil || a.TreeID == nil || *b.TreeID != *a.TreeID {
		t.Fatalf("b joins a's tree: %+v %+v", a, b)
	}

	var rows agenttools.TasksOut
	mustCall(t, cs, "list_tasks", map[string]any{"originThreadId": s}, &rows)
	if rows.Total != 3 {
		t.Fatalf("batch: %+v", rows)
	}
	var td client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": c.ID}, &td)
	if td.Batch == nil || len(td.Batch.Trees) != 1 || len(td.Batch.Independent) != 1 || td.Tree != nil {
		t.Fatalf("task batch %+v", td.Batch)
	}
}

func TestScheduleTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/sched")
	s := e.session(p, "Owner")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: s}})

	var sc db.Schedule
	mustCall(t, cs, "create_schedule", map[string]any{"project": "1", "title": "nightly", "prompt": "bump", "cron": "0 2 * * *", "tz": "UTC"}, &sc)
	if sc.ProjectID != p.ID || sc.OriginThreadID == nil || *sc.OriginThreadID != s || sc.NextRunAt == nil {
		t.Fatalf("schedule %+v", sc)
	}
	mustCall(t, cs, "update_schedule", map[string]any{"id": sc.ID, "paused": true}, &sc)
	if !sc.Paused || sc.NextRunAt != nil {
		t.Fatalf("pause %+v", sc)
	}
	var prev agenttools.PreviewOut
	mustCall(t, cs, "preview_schedule", map[string]any{"cron": "@daily", "tz": "UTC"}, &prev)
	if len(prev.Next) != 3 {
		t.Fatalf("preview %+v", prev)
	}
	var run client.Task
	mustCall(t, cs, "run_schedule", map[string]any{"id": sc.ID}, &run)
	if run.ScheduleID == nil || *run.ScheduleID != sc.ID || run.OriginThreadID == nil || *run.OriginThreadID != s {
		t.Fatalf("run %+v", run)
	}
	var list agenttools.SchedulesOut
	mustCall(t, cs, "list_schedules", nil, &list)
	if len(list.Schedules) != 1 {
		t.Fatalf("list %+v", list)
	}
	var detail client.ScheduleDetail
	mustCall(t, cs, "get_schedule", map[string]any{"id": sc.ID}, &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].Task == nil {
		t.Fatalf("detail %+v", detail)
	}
	mustCall(t, cs, "delete_schedule", map[string]any{"id": sc.ID}, nil)
}

// pngBytes is a valid 1x1 PNG.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0bIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func TestImagePaths(t *testing.T) {
	e := newEnv(t)
	e.project("/w/img")
	img := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(img, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"title": "i", "prompt": "see image", "imagePaths": []string{img}, "project": "P /w/img"}

	remote := connect(t, &agenttools.Ops{Client: e.client})
	if msg := call(t, remote, "create_task", args, nil); !strings.Contains(msg, "local file access") {
		t.Fatalf("HTTP-mode imagePaths: %q", msg)
	}
	local := connect(t, &agenttools.Ops{Client: e.client, LocalFiles: true})
	var tk client.Task
	mustCall(t, local, "create_task", args, &tk)
	if len(tk.Attachments) != 1 || tk.Attachments[0].Name != "shot.png" || tk.Attachments[0].MimeType != "image/png" {
		t.Fatalf("attachments %+v", tk.Attachments)
	}
}

func TestHTTPHandler(t *testing.T) {
	e := newEnv(t)
	e.project("/w/http")
	srv := httptest.NewServer(agenttools.HTTPHandler(&agenttools.Ops{Client: e.client}))
	defer srv.Close()
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil || len(res.Tools) != 34 {
		t.Fatalf("tools %v %v", res, err)
	}
	var out agenttools.ProjectsOut
	mustCall(t, cs, "list_projects", nil, &out)
	if len(out.Projects) != 1 {
		t.Fatalf("projects %+v", out)
	}
	var cx agenttools.ContextOut
	mustCall(t, cs, "get_context", map[string]any{"cwd": "/w/http/pkg"}, &cx)
	if cx.Project != nil || cx.ProjectSource != "" {
		t.Fatalf("http context %+v", cx)
	}
}

func TestHandoffFlagTools(t *testing.T) {
	e := newEnv(t)
	e.project("/w/handoff")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/handoff"}, PollEvery: 10 * time.Millisecond})

	var a, b client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "a", "prompt": "x"}, &a)
	if !a.Handoff {
		t.Fatalf("create_task: handoff defaults on: %+v", a)
	}
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "b", "prompt": "y", "needs": []int64{a.ID}, "handoff": false}, &b)
	if b.Handoff {
		t.Fatalf("create_task handoff=false: %+v", b)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": b.ID, "handoff": true}, &b)
	if !b.Handoff {
		t.Fatalf("update_task handoff=true: %+v", b)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": b.ID, "title": "b2"}, &b)
	if !b.Handoff {
		t.Fatalf("update_task without handoff keeps it: %+v", b)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": b.ID, "handoff": false}, &b)
	if b.Handoff {
		t.Fatalf("update_task handoff=false: %+v", b)
	}
	var d client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": b.ID}, &d)
	if d.Handoff || len(d.CurrentHandoffs) != 0 {
		t.Fatalf("get_task %+v", d)
	}
}

func TestBaseTools(t *testing.T) {
	e := newEnv(t)
	e.project("/w/base")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/base"}, PollEvery: 10 * time.Millisecond})

	var a, b, r client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "a", "prompt": "x"}, &a)
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "b", "prompt": "y", "needs": []int64{a.ID}, "baseRef": "origin/main", "base": "predecessor"}, &b)
	if !b.Workspace.FromPredecessor() || b.Workspace.BaseRef != "origin/main" {
		t.Fatalf("create_task base=predecessor: %+v", b.Workspace)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": b.ID, "base": "ref"}, &b)
	if b.Workspace.Type != "worktree" || b.Workspace.FromPredecessor() || b.Workspace.BaseRef != "origin/main" {
		t.Fatalf("update_task base=ref keeps the worktree: %+v", b.Workspace)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": b.ID, "base": "predecessor"}, &b)
	if !b.Workspace.FromPredecessor() {
		t.Fatalf("update_task base=predecessor: %+v", b.Workspace)
	}
	var d client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": b.ID}, &d)
	if !d.Workspace.FromPredecessor() || d.CurrentLaunchBase != nil {
		t.Fatalf("get_task %+v", d)
	}
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "r", "prompt": "z"}, &r)
	if msg := call(t, cs, "update_task", map[string]any{"id": r.ID, "base": "predecessor"}, nil); !strings.Contains(msg, "base needs a worktree") {
		t.Fatal("update_task base=predecessor on a root task must fail")
	}
	if msg := call(t, cs, "create_task", map[string]any{"project": "1", "title": "x", "prompt": "z", "base": "predecessor"}, nil); !strings.Contains(msg, "base needs a worktree") {
		t.Fatal("create_task base without baseRef must fail")
	}
}

// The server's HTTP /mcp endpoint serves tools from the /api router
// in-process: no TCP listener is involved anywhere (the MCP client also
// reaches /mcp in-process).
func TestHTTPMCPWithoutListener(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/http-mcp")
	mux := http.NewServeMux()
	mux.Handle("/api/", e.handler)
	mux.Handle("/mcp", agenttools.HTTPHandler(&agenttools.Ops{Client: client.NewInProcess(e.handler, "unix:///run/stage.sock")}))
	// Nothing listens here; reaching it would fail the calls below.
	mux.Handle("/", http.NotFoundHandler())

	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             "http://stage/mcp",
		HTTPClient:           &http.Client{Transport: client.HandlerTransport(mux)},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	var cx agenttools.ContextOut
	mustCall(t, cs, "get_context", nil, &cx)
	if cx.APIURL != "unix:///run/stage.sock" {
		t.Fatalf("apiUrl %q", cx.APIURL)
	}
	var created struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	mustCall(t, cs, "create_task", map[string]any{"project": p.Title, "title": "via http mcp", "prompt": "go"}, &created)
	if created.ID == 0 || created.Title != "via http mcp" {
		t.Fatalf("create_task %+v", created)
	}
	got, err := e.client.Task(ctx, created.ID)
	if err != nil || got.Title != "via http mcp" {
		t.Fatalf("task not stored: %+v, %v", got, err)
	}
}

func TestReadRefTool(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/refs")
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "Fix login", "prompt": "fix it", "project": p.Title}, &tk)

	// Backlog task: no session yet.
	var r agenttools.RefOut
	mustCall(t, cs, "read_ref", map[string]any{"ref": fmt.Sprintf("[stage task #%d: Fix login](http://phone.lan:7380/tasks/%d)", tk.ID, tk.ID)}, &r)
	if r.Kind != "task" || r.Task == nil || r.Task.ID != tk.ID || r.Task.Prompt != "fix it" || r.Session != nil || !strings.Contains(r.Next, "Not launched") {
		t.Fatalf("backlog ref %+v", r)
	}

	mustCall(t, cs, "start_task", map[string]any{"id": tk.ID}, &tk)
	threadID := *tk.T3ThreadID
	if err := e.fake.SetReply(threadID, t3.Reply{Text: "Fixed the token refresh."}); err != nil {
		t.Fatal(err)
	}
	_ = e.fake.SetStatus(threadID, t3.StatusCompleted)
	if err := e.sync.PollActive(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Task ref carries its session; session ref carries its task.
	r = agenttools.RefOut{}
	mustCall(t, cs, "read_ref", map[string]any{"ref": fmt.Sprintf("#%d", tk.ID)}, &r)
	if r.Kind != "task" || r.Task == nil || r.Session == nil || r.Session.Thread.T3ThreadID != threadID || !strings.Contains(r.Next, "t3_thread_read "+threadID) {
		t.Fatalf("task ref %+v", r)
	}
	r = agenttools.RefOut{}
	mustCall(t, cs, "read_ref", map[string]any{"ref": "http://127.0.0.1:7380/sessions/" + threadID}, &r)
	if r.Kind != "session" || r.Session == nil || r.Task == nil || r.Task.ID != tk.ID {
		t.Fatalf("session ref %+v", r)
	}
	if r.Task.Thread.LastWord == nil || !strings.Contains(r.Task.Thread.LastWord.Text, "token refresh") {
		t.Fatalf("session last word %+v", r.Task.Thread.LastWord)
	}

	// External session: no task.
	ext := e.session(p, "Outside")
	r = agenttools.RefOut{}
	mustCall(t, cs, "read_ref", map[string]any{"ref": ext}, &r)
	if r.Kind != "session" || r.Task != nil || r.Session == nil || r.Session.Thread.Title != "Outside" {
		t.Fatalf("external ref %+v", r)
	}

	if msg := call(t, cs, "read_ref", map[string]any{"ref": "https://github.com/o/r/pull/3"}, nil); !strings.Contains(msg, "not a stage task or session reference") {
		t.Fatalf("bad ref error %q", msg)
	}
	if msg := call(t, cs, "read_ref", map[string]any{"ref": "#99999"}, nil); !strings.Contains(msg, "task 99999 not found") {
		t.Fatalf("missing task error %q", msg)
	}
}
