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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/release"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB dbtest.DB

type env struct {
	t       *testing.T
	handler http.Handler // the /api router
	fake    *t3.Fake
	sync    *t3sync.Service
	runner  *release.Runner
	client  *client.Client
	url     string
}

func newEnv(t *testing.T) *env {
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	fake := t3.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := t3sync.New(store, fake, log)
	runner := release.NewRunner(store, svc, log)
	svc.OnTransition = runner.OnTransition
	h := api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://t3.test"},
		Store:  store, T3: fake, Sync: svc, Push: notify.New(store, "https://stage.test", log), Logger: log,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{t: t, handler: h, fake: fake, sync: svc, runner: runner, client: client.New(srv.URL), url: srv.URL}
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
		"delete_task", "start_task", "stop_task", "followup_task", "duplicate_task",
		"wait_task", "list_schedules", "get_schedule", "create_schedule", "update_schedule", "delete_schedule",
		"run_schedule", "preview_schedule", "list_sessions", "search_sessions", "get_session", "get_session_questions", "read_ref",
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
	init := cs.InitializeResult()
	if init == nil || !strings.Contains(init.Instructions, "afterMe") || !strings.Contains(init.Instructions, "read_ref") {
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
	if err := e.sync.PollActive(context.Background()); err != nil {
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

func TestLifecycleWaitTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/life")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/life"}, PollEvery: 10 * time.Millisecond})

	var a, b client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "a", "prompt": "x", "start": true}, &a)
	if a.State == db.StateBacklog || a.T3ThreadID == nil {
		t.Fatalf("start=true %+v", a)
	}
	if msg := call(t, cs, "create_task", map[string]any{"title": "b", "prompt": "y", "needs": []int64{a.ID}, "start": true}, nil); !strings.Contains(msg, "cannot also start now") {
		t.Fatalf("needs with start=true: %q", msg)
	}
	mustCall(t, cs, "create_task", map[string]any{"title": "b", "prompt": "y", "needs": []int64{a.ID}}, &b)
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

	if _, err := e.fake.AddApproval(*a.T3ThreadID); err != nil {
		t.Fatal(err)
	}
	req, _ := e.fake.AddUserInput(*a.T3ThreadID, []t3.Question{{ID: "q", Header: "H", Question: "Which?", Options: []t3.QuestionOption{{Label: "A"}}}})
	var pq client.PendingQuestions
	mustCall(t, cs, "get_session_questions", map[string]any{"id": *a.T3ThreadID}, &pq)
	if pq.PendingRequestCount != 2 || pq.ApprovalCount != 1 || len(pq.Requests) != 1 || pq.Requests[0].RequestID != req || pq.Requests[0].Questions[0].Question != "Which?" {
		t.Fatalf("questions %+v", pq)
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

func TestSearchSessionsTool(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/search")
	other := e.project("/w/search-other")
	titled := e.session(p, "Palette search")
	said := e.session(p, "What more?")
	e.fake.AddMessage(said, t3.SearchAssistant, "…the palette should find sessions…")
	e.session(other, "palette elsewhere")
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var out agenttools.SearchSessionsOut
	mustCall(t, cs, "search_sessions", map[string]any{"query": "palette", "project": p.Title}, &out)
	if out.T3 != "ok" || len(out.Results) != 2 {
		t.Fatalf("search %+v", out)
	}
	if r := out.Results[0]; r.T3ThreadID != titled || r.Match != "title" || r.State == nil || r.Snippet != nil {
		t.Fatalf("title row %+v", r)
	}
	if r := out.Results[1]; r.T3ThreadID != said || r.Match != "assistant" || r.Snippet == nil ||
		*r.Snippet != "…the palette should find sessions…" || r.Project == nil || *r.Project != p.Title {
		t.Fatalf("message row %+v", r)
	}
	if msg := call(t, cs, "search_sessions", map[string]any{"query": "p"}, nil); !strings.Contains(msg, "2 to 200") {
		t.Fatalf("short query error %q", msg)
	}
}

// A planning session queues dependent tasks, then verifies the batch.
func TestPlanningSessionBatch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/plan")
	s := e.session(p, "Planner")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: s}})
	var a, b, c client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "a", "prompt": "do a", "afterMe": true}, &a)
	mustCall(t, cs, "create_task", map[string]any{"title": "b", "prompt": "do b", "needs": []int64{a.ID}}, &b)
	mustCall(t, cs, "create_task", map[string]any{"title": "c", "prompt": "do c"}, &c)
	if b.TreeID == nil || a.TreeID == nil || *b.TreeID != *a.TreeID {
		t.Fatalf("b joins a's tree: %+v %+v", a, b)
	}

	var sd client.ThreadDetail
	mustCall(t, cs, "get_session", map[string]any{"id": s}, &sd)
	ct := sd.CreatedTasks
	if len(ct.Trees) != 1 || ct.Trees[0].Tree.ID != *a.TreeID || ct.Trees[0].Tree.Title != "Planner" ||
		len(ct.Trees[0].Tasks) != 2 || ct.Trees[0].Tasks[0].ID != a.ID || ct.Trees[0].Tasks[1].ID != b.ID ||
		len(ct.Independent) != 1 || ct.Independent[0].ID != c.ID {
		t.Fatalf("createdTasks %+v", ct)
	}
	if h := ct.Trees[0].Tasks[0]; len(h.Needs) != 1 || h.Needs[0] == b.ID {
		t.Fatalf("a needs the planner's session task: %+v", h)
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
	if err != nil || len(res.Tools) != 25 {
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

func TestGetTaskAndSessionCarryPullRequestsAndRuns(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/prs")
	s := e.session(p, "Agent")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: s, Cwd: "/w/prs"}})
	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "Ship", "prompt": "go", "start": true}, &tk)
	if tk.T3ThreadID == nil {
		t.Fatalf("not launched: %+v", tk)
	}
	pr := t3.PullRequest{Host: "github.com", Repository: "o/r", Number: 3, URL: "https://github.com/o/r/pull/3", Source: "agent", State: "merged"}
	if err := e.fake.SetThread(*tk.T3ThreadID, func(d *t3.ThreadDetail) { d.PullRequests = []t3.PullRequest{pr} }); err != nil {
		t.Fatal(err)
	}
	if err := e.sync.RefreshSession(context.Background(), *tk.T3ThreadID); err != nil {
		t.Fatal(err)
	}

	var td client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": tk.ID}, &td)
	if len(td.PullRequests) != 1 || td.PullRequests[0] != pr || td.Thread == nil || len(td.Thread.RecentRuns) != 1 || td.Thread.RunCount != 1 {
		t.Fatalf("get_task prs=%+v thread=%+v", td.PullRequests, td.Thread)
	}
	var sd client.ThreadDetail
	mustCall(t, cs, "get_session", map[string]any{"id": *tk.T3ThreadID}, &sd)
	if len(sd.Thread.PullRequests) != 1 || sd.Thread.PullRequests[0] != pr || len(sd.Thread.RecentRuns) != 1 || sd.Thread.RecentRuns[0].Ordinal != 1 {
		t.Fatalf("get_session thread=%+v", sd.Thread)
	}
}

func TestHandoffFlagTools(t *testing.T) {
	e := newEnv(t)
	e.project("/w/handoff")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/handoff"}, PollEvery: 10 * time.Millisecond})

	var a, b client.Task
	mustCall(t, cs, "create_task", map[string]any{"title": "a", "prompt": "x"}, &a)
	if !a.Handoff {
		t.Fatalf("create_task: handoff defaults on: %+v", a)
	}
	mustCall(t, cs, "create_task", map[string]any{"title": "b", "prompt": "y", "needs": []int64{a.ID}, "handoff": false}, &b)
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
	mustCall(t, cs, "create_task", map[string]any{"title": "a", "prompt": "x"}, &a)
	mustCall(t, cs, "create_task", map[string]any{"title": "b", "prompt": "y", "needs": []int64{a.ID}, "baseRef": "origin/main", "base": "predecessor"}, &b)
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
	mustCall(t, cs, "create_task", map[string]any{"title": "r", "prompt": "z"}, &r)
	if msg := call(t, cs, "update_task", map[string]any{"id": r.ID, "base": "predecessor"}, nil); !strings.Contains(msg, "base needs a worktree") {
		t.Fatal("update_task base=predecessor on a root task must fail")
	}
	if msg := call(t, cs, "create_task", map[string]any{"title": "x", "prompt": "z", "base": "predecessor"}, nil); !strings.Contains(msg, "base needs a worktree") {
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
	if r.Session.Thread.LastWord == nil || !strings.Contains(r.Session.Thread.LastWord.Text, "token refresh") {
		t.Fatalf("session last word %+v", r.Session.Thread.LastWord)
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
