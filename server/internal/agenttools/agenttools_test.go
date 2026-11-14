package agenttools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/links"
	"github.com/flexdinesh/stage/server/internal/notify"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("stage_test_agenttools")
	if testDB.Skip != "" {
		fmt.Println("agenttools integration tests skipped:", testDB.Skip)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

type env struct {
	t      *testing.T
	fake   *t3.Fake
	sync   *t3sync.Service
	runner *links.Runner
	client *client.Client
	url    string
}

func newEnv(t *testing.T) *env {
	if testDB.Skip != "" {
		t.Skip(testDB.Skip)
	}
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	fake := t3.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := t3sync.New(store, fake, log)
	runner := links.NewRunner(store, svc, log)
	svc.OnTransition = runner.OnTransition
	h := api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://t3.test"},
		Store:  store, T3: fake, Sync: svc, Push: notify.New(store, "https://stage.test", log), Logger: log,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{t: t, fake: fake, sync: svc, runner: runner, client: client.New(srv.URL), url: srv.URL}
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
	ps, err := e.client.Projects(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, x := range ps {
		if x.T3ProjectID == p.ID {
			return x
		}
	}
	e.t.Fatalf("project %s not mirrored", root)
	return db.Project{}
}

// session launches a running external thread in project p.
func (e *env) session(p db.Project, title string) string {
	e.t.Helper()
	ctx := context.Background()
	out, err := e.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: p.T3ProjectID, Title: title})
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
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
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
		"delete_task", "start_task", "stop_task", "followup_task", "duplicate_task", "link_task", "unlink_task",
		"wait_task", "list_schedules", "get_schedule", "create_schedule", "update_schedule", "delete_schedule",
		"run_schedule", "preview_schedule", "list_sessions", "get_session",
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
	init := cs.InitializeResult()
	if init == nil || !strings.Contains(init.Instructions, "afterMe") {
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
	if cx.ThreadID != s || cx.Project == nil || cx.Project.ID != p.ID || cx.ProjectSource != "session" {
		t.Fatalf("context %+v (session beats cwd)", cx)
	}

	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "Follow-up", "prompt": "do it", "afterMe": true, "effort": "high", "tags": []string{"agent"}}, &tk)
	if tk.ProjectID != p.ID || tk.State != db.StateBacklog || tk.AfterThreadID == nil || *tk.AfterThreadID != s ||
		tk.OriginThreadID == nil || *tk.OriginThreadID != s || len(tk.ModelOptions) != 1 || tk.ModelOptions[0].ID != "effort" {
		t.Fatalf("task %+v", tk)
	}

	// Explicit project wins; arguments override the environment.
	var tk2 client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "B", "prompt": "x", "project": other.Title}, &tk2)
	if tk2.ProjectID != other.ID {
		t.Fatalf("explicit project: %+v", tk2)
	}
	var cx2 agenttools.ContextOut
	mustCall(t, cs, "get_context", map[string]any{"callerThreadId": "unknown-thread", "cwd": "/w/other/x"}, &cx2)
	if cx2.ThreadID != "unknown-thread" || cx2.Project == nil || cx2.Project.ID != other.ID || cx2.ProjectSource != "cwd" {
		t.Fatalf("cwd fallback %+v", cx2)
	}

	// afterMe without a session id is an error.
	noCaller := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/agent"}})
	if msg := call(t, noCaller, "create_task", map[string]any{"title": "c", "prompt": "x", "afterMe": true}, nil); !strings.Contains(msg, "session id") {
		t.Fatalf("afterMe without session: %q", msg)
	}
	if msg := call(t, noCaller, "create_task", map[string]any{"title": "c", "prompt": "x", "project": "nope"}, nil); !strings.Contains(msg, "no project") {
		t.Fatalf("unknown project: %q", msg)
	}

	// The session settling starts the task.
	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	_ = e.fake.Settle(s)
	if err := e.sync.WatchThreads(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.runner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": tk.ID}, &got)
	if got.State != db.StateRunning {
		t.Fatalf("after settle %+v", got.Task)
	}
	var rows agenttools.TasksOut
	mustCall(t, cs, "list_tasks", map[string]any{"project": fmt.Sprint(p.ID), "states": []string{"running"}}, &rows)
	if rows.Total != 1 || rows.Tasks[0].ID != tk.ID {
		t.Fatalf("list %+v", rows)
	}
}

func TestLifecycleLinkWaitTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/life")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/life"}, PollEvery: 10 * time.Millisecond})

	var a, b client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "a", "prompt": "x", "start": true}, &a)
	if a.State == db.StateBacklog || a.T3ThreadID == nil {
		t.Fatalf("start=true %+v", a)
	}
	mustCall(t, cs, "create_task", map[string]any{"title": "b", "prompt": "y"}, &b)
	mustCall(t, cs, "link_task", map[string]any{"id": b.ID, "afterTaskId": a.ID}, &b)
	if b.AfterTaskID == nil || *b.AfterTaskID != a.ID {
		t.Fatalf("link %+v", b)
	}
	mustCall(t, cs, "unlink_task", map[string]any{"id": b.ID}, &b)
	if b.AfterTaskID != nil {
		t.Fatalf("unlink %+v", b)
	}
	if msg := call(t, cs, "link_task", map[string]any{"id": b.ID}, nil); msg == "" {
		t.Fatal("link without target must fail")
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
	mustCall(t, cs, "followup_task", map[string]any{"id": a.ID, "message": "more"}, nil)
	mustCall(t, cs, "stop_task", map[string]any{"id": a.ID}, nil)

	var sessions agenttools.SessionsOut
	mustCall(t, cs, "list_sessions", map[string]any{"project": p.Title}, &sessions)
	if sessions.Total != 1 || sessions.Sessions[0].TaskID == nil || *sessions.Sessions[0].TaskID != a.ID {
		t.Fatalf("sessions %+v", sessions)
	}
	var sd client.ThreadDetail
	mustCall(t, cs, "get_session", map[string]any{"id": *a.T3ThreadID}, &sd)
	if sd.Task == nil || sd.Task.ID != a.ID {
		t.Fatalf("session %+v", sd)
	}
}

func TestLastWordTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/word")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/word"}})
	var a client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "a", "prompt": "x", "start": true}, &a)
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

	// get_session refreshes the session, which reads the reply.
	var sd client.ThreadDetail
	mustCall(t, cs, "get_session", map[string]any{"id": id}, &sd)
	if sd.Thread.LastWord == nil || sd.Thread.LastWord.Text != "## Done\nFixed **the race**." || sd.Thread.LastWord.Truncated {
		t.Fatalf("session last word %+v", sd.Thread.LastWord)
	}
	var got client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": a.ID}, &got)
	if got.Thread == nil || got.Thread.LastWord == nil || got.Thread.LastWord.Text != sd.Thread.LastWord.Text {
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
	var sessions agenttools.SessionsOut
	mustCall(t, cs, "list_sessions", map[string]any{"project": p.Title}, &sessions)
	if sessions.Total != 1 || sessions.Sessions[0].LastWordPreview == nil || *sessions.Sessions[0].LastWordPreview != preview {
		t.Fatalf("list_sessions %+v", sessions)
	}
}

func TestScheduleTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/sched")
	s := e.session(p, "Owner")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: s}})

	var sc db.Schedule
	mustCall(t, cs, "create_schedule", map[string]any{"title": "nightly", "prompt": "bump", "cron": "0 2 * * *", "tz": "UTC"}, &sc)
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
	if err != nil || len(res.Tools) != 24 {
		t.Fatalf("tools %v %v", res, err)
	}
	var out agenttools.ProjectsOut
	mustCall(t, cs, "list_projects", nil, &out)
	if len(out.Projects) != 1 {
		t.Fatalf("projects %+v", out)
	}
	var cx agenttools.ContextOut
	mustCall(t, cs, "get_context", map[string]any{"cwd": "/w/http/pkg"}, &cx)
	if cx.Project == nil || cx.ProjectSource != "cwd" {
		t.Fatalf("http context %+v", cx)
	}
}

func TestProjectForPath(t *testing.T) {
	ps := []db.Project{{ID: 1, WorkspaceRoot: "/w"}, {ID: 2, WorkspaceRoot: "/w/app"}, {ID: 3, WorkspaceRoot: "/w/app-two"}, {ID: 4, WorkspaceRoot: "rel"}}
	for dir, want := range map[string]int64{"/w/app/src": 2, "/w/app": 2, "/w/app-two/x": 3, "/w/x": 1, "/other": 0, "": 0, "/w/app/": 2} {
		p, ok := agentctx.ProjectForPath(ps, dir)
		if (want == 0) == ok || (ok && p.ID != want) {
			t.Fatalf("%q -> %+v %v, want %d", dir, p, ok, want)
		}
	}
}
