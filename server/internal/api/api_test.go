package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/api"
	"github.com/flexdinesh/t3-stage/server/internal/chains"
	"github.com/flexdinesh/t3-stage/server/internal/config"
	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/db/dbtest"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("t3stage_test_api")
	if testDB.Skip != "" {
		fmt.Println("api integration tests skipped:", testDB.Skip)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

// flakyT3 is the fake with switchable launch failures.
type flakyT3 struct {
	*t3.Fake
	failLaunch atomic.Bool
}

func (f *flakyT3) LaunchThread(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	if f.failLaunch.Load() {
		return t3.LaunchResult{}, errors.New("t3 exploded")
	}
	return f.Fake.LaunchThread(ctx, in)
}

type env struct {
	t      *testing.T
	h      http.Handler
	fake   *t3.Fake
	t3     *flakyT3
	sync   *t3sync.Service
	runner *chains.Runner
	store  *db.Store
}

const publicURL = "http://controlbox:3773"

func newEnv(t *testing.T) *env {
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	fake := t3.NewFake()
	client := &flakyT3{Fake: fake}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := t3sync.New(store, client, log)
	runner := chains.NewRunner(store, svc, client, log)
	svc.OnTransition = runner.OnTransition
	h := api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: publicURL},
		Store:  store, T3: client, Sync: svc, Chains: runner, Logger: log,
	})
	return &env{t: t, h: h, fake: fake, t3: client, sync: svc, runner: runner, store: store}
}

// do sends a request; out (if non-nil) receives the decoded body.
func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	if rec.Code >= 400 {
		var eb struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil || eb.Error == "" {
			e.t.Fatalf("%s %s: status %d without {error} body: %q", method, path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code
}

func (e *env) must(method, path string, body any, want int, out any) {
	e.t.Helper()
	if got := e.do(method, path, body, out); got != want {
		e.t.Fatalf("%s %s: status %d, want %d", method, path, got, want)
	}
}

func (e *env) poll() {
	e.t.Helper()
	if err := e.sync.PollActive(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) evaluate() {
	e.t.Helper()
	if err := e.runner.EvaluateAll(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

type project struct {
	ID            int64   `json:"id"`
	T3ProjectID   string  `json:"t3ProjectId"`
	Title         string  `json:"title"`
	WorkspaceRoot string  `json:"workspaceRoot"`
	Source        string  `json:"source"`
	ArchivedAt    *string `json:"archivedAt"`
}

type task struct {
	ID            int64             `json:"id"`
	ProjectID     int64             `json:"projectId"`
	ProjectTitle  string            `json:"projectTitle"`
	Title         string            `json:"title"`
	Prompt        string            `json:"prompt"`
	Model         *string           `json:"model"`
	ModelOptions  []json.RawMessage `json:"modelOptions"`
	Workspace     map[string]any    `json:"workspace"`
	Tags          []string          `json:"tags"`
	State         string            `json:"state"`
	T3ThreadID    *string           `json:"t3ThreadId"`
	ChainID       *int64            `json:"chainId"`
	LaunchAttempt int               `json:"launchAttempt"`
	LastError     *string           `json:"lastError"`
	T3WebURL      *string           `json:"t3WebUrl"`
	T3AppURL      *string           `json:"t3AppUrl"`
	LaunchedAt    *string           `json:"launchedAt"`
	FinishedAt    *string           `json:"finishedAt"`
	SettledAt     *string           `json:"settledAt"`
	CreatedAt     string            `json:"createdAt"`
	Events        []struct {
		FromState *string `json:"fromState"`
		ToState   string  `json:"toState"`
		Detail    string  `json:"detail"`
	} `json:"events"`
	Thread *thread `json:"thread"`
}

type thread struct {
	T3ThreadID  string  `json:"t3ThreadId"`
	ProjectID   *int64  `json:"projectId"`
	Status      string  `json:"status"`
	Settled     bool    `json:"settled"`
	Source      string  `json:"source"`
	TaskID      *int64  `json:"taskId"`
	T3WebURL    *string `json:"t3WebUrl"`
	T3AppURL    *string `json:"t3AppUrl"`
	ProviderID  *string `json:"providerInstanceId"`
	Title       string  `json:"title"`
	T3ProjectID string  `json:"t3ProjectId"`
}

type chain struct {
	ID            int64  `json:"id"`
	State         string `json:"state"`
	CurrentTaskID *int64 `json:"currentTaskId"`
	Steps         []task `json:"steps"`
}

func (e *env) project(root string) project {
	e.t.Helper()
	var p project
	e.must("POST", "/api/projects", map[string]any{"title": "P " + root, "workspaceRoot": root}, 201, &p)
	return p
}

func (e *env) task(projectID int64, title string, extra map[string]any) task {
	e.t.Helper()
	body := map[string]any{"projectId": projectID, "title": title, "prompt": "reply OK"}
	for k, v := range extra {
		body[k] = v
	}
	var out task
	e.must("POST", "/api/tasks", body, 201, &out)
	return out
}

func (e *env) getTask(id int64) task {
	e.t.Helper()
	var out task
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

func (e *env) getChain(id int64) chain {
	e.t.Helper()
	var out chain
	e.must("GET", fmt.Sprintf("/api/chains/%d", id), nil, 200, &out)
	return out
}

func TestHealthAndNotFound(t *testing.T) {
	e := newEnv(t)
	var h map[string]string
	e.must("GET", "/api/health", nil, 200, &h)
	if h["status"] != "ok" || h["db"] != "ok" {
		t.Fatalf("health %v", h)
	}
	e.must("GET", "/api/nope", nil, 404, nil)
	e.must("GET", "/api/tasks/abc", nil, 400, nil)
	e.must("GET", "/api/tasks/999", nil, 404, nil)
}

func TestTaskLifecycle(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	if p.Source != "app" || p.T3ProjectID == "" {
		t.Fatalf("project %+v", p)
	}
	tk := e.task(p.ID, "Say OK", map[string]any{
		"providerInstanceId": "claude", "model": "claude-haiku-5-5",
		"modelOptions": []map[string]any{{"id": "effort", "value": "low"}},
		"tags":         []string{"smoke", " smoke ", ""},
	})
	if tk.State != "backlog" || tk.T3ThreadID != nil || tk.T3WebURL != nil || tk.ProjectTitle != p.Title {
		t.Fatalf("created %+v", tk)
	}
	if len(tk.Tags) != 1 || tk.Workspace["type"] != "root" || len(tk.ModelOptions) != 1 {
		t.Fatalf("defaults %+v", tk)
	}

	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if started.State != "running" || started.T3ThreadID == nil || started.LaunchAttempt != 1 || started.LaunchedAt == nil {
		t.Fatalf("started %+v", started)
	}
	threadID := *started.T3ThreadID
	if started.T3WebURL == nil || *started.T3WebURL != publicURL+"/fake-env/"+threadID {
		t.Fatalf("web url %v", started.T3WebURL)
	}
	if started.T3AppURL == nil || *started.T3AppURL != "t3-thread://fake-env/"+threadID {
		t.Fatalf("app url %v", started.T3AppURL)
	}
	// Launch payload reached T3.
	d, err := e.fake.ReadThread(context.Background(), threadID)
	if err != nil || d.Model != "claude-haiku-5-5" || d.ProjectID != p.T3ProjectID || d.Title != "Say OK" {
		t.Fatalf("fake thread %+v %v", d, err)
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 409, nil)

	e.poll()
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("after poll %s", got.State)
	}
	_ = e.fake.SetThread(threadID, func(d *t3.ThreadDetail) { d.PendingRequestCount = 1 })
	e.poll()
	if got := e.getTask(tk.ID); got.State != "needs_input" {
		t.Fatalf("pending -> %s", got.State)
	}
	var stats struct {
		NeedsInput int `json:"needsInput"`
	}
	e.must("GET", "/api/stats", nil, 200, &stats)
	if stats.NeedsInput != 1 {
		t.Fatalf("needsInput %d", stats.NeedsInput)
	}
	_ = e.fake.SetThread(threadID, func(d *t3.ThreadDetail) { d.PendingRequestCount = 0 })
	_ = e.fake.SetStatus(threadID, t3.StatusCompleted)
	e.poll()
	got := e.getTask(tk.ID)
	if got.State != "completed" || got.FinishedAt == nil || got.SettledAt != nil {
		t.Fatalf("completed %+v", got)
	}
	_ = e.fake.Settle(threadID)
	e.poll()
	got = e.getTask(tk.ID)
	if got.State != "settled" || got.SettledAt == nil {
		t.Fatalf("settled %+v", got)
	}
	var states []string
	for _, ev := range got.Events {
		states = append(states, ev.ToState)
	}
	want := []string{"backlog", "launching", "running", "needs_input", "completed", "settled"}
	if !slices.Equal(states, want) {
		t.Fatalf("events %v, want %v", states, want)
	}
	if got.Events[len(got.Events)-1].Detail != "thread settled" {
		t.Fatalf("detail %q", got.Events[len(got.Events)-1].Detail)
	}
	if got.Thread == nil || got.Thread.Source != "app" || !got.Thread.Settled || got.Thread.TaskID == nil || *got.Thread.TaskID != tk.ID {
		t.Fatalf("thread %+v", got.Thread)
	}
	// Settled tasks are no longer polled.
	tasks, _ := e.store.PollableTasks(context.Background())
	if len(tasks) != 0 {
		t.Fatalf("pollable %d", len(tasks))
	}
}

func TestTaskActions(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	tk := e.task(p.ID, "A", nil)
	// No thread yet.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", tk.ID), map[string]any{"message": "hi"}, 409, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", tk.ID), nil, 409, nil)
	// Backlog: everything editable; null clears; bad enum 400.
	var patched task
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{
		"prompt": "new", "providerInstanceId": "claude", "model": "claude-haiku-5-5",
		"workspace": map[string]any{"type": "worktree", "baseRef": "origin/main", "branch": nil},
	}, 200, &patched)
	if patched.Prompt != "new" || patched.Model == nil || patched.Workspace["type"] != "worktree" {
		t.Fatalf("patched %+v", patched)
	}
	if b, ok := patched.Workspace["branch"]; !ok || b != nil {
		t.Fatalf("worktree branch must be present and null: %v", patched.Workspace)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"providerInstanceId": nil, "model": nil}, 200, &patched)
	if patched.Model != nil {
		t.Fatal("model not cleared")
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"runtimeMode": "yolo"}, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"title": nil}, 400, nil)

	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	threadID := *started.T3ThreadID
	if d, _ := e.fake.ReadThread(context.Background(), threadID); d.Branch != "" {
		t.Fatalf("branch %q", d.Branch)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"prompt": "x"}, 409, nil)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"title": "B", "notes": "n", "tags": []string{"t"}}, 200, &patched)
	if patched.Title != "B" {
		t.Fatalf("title %q", patched.Title)
	}
	e.must("DELETE", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 409, nil)

	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", tk.ID), map[string]any{"message": "more", "mode": "steer"}, 200, nil)
	if len(e.fake.Sent) != 1 || e.fake.Sent[0].Mode != t3.SendSteer || e.fake.Sent[0].Message != "more" {
		t.Fatalf("sent %+v", e.fake.Sent)
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", tk.ID), map[string]any{"message": " "}, 400, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", tk.ID), map[string]any{"message": "x", "mode": "bad"}, 400, nil)

	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", tk.ID), nil, 200, nil)
	if !slices.Contains(e.fake.Interrupted, threadID) {
		t.Fatal("not interrupted")
	}
	e.poll()
	if got := e.getTask(tk.ID); got.State != "interrupted" || got.FinishedAt == nil {
		t.Fatalf("after stop %+v", got)
	}
	// Recently touched interrupted task keeps being polled: a follow-up that
	// restarts the thread is picked up.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", tk.ID), map[string]any{"message": "again"}, 200, nil)
	_ = e.fake.SetStatus(threadID, t3.StatusRunning)
	e.poll()
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("after follow-up %s", got.State)
	}

	var dup task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/duplicate", tk.ID), nil, 201, &dup)
	if dup.State != "backlog" || dup.Title != "B" || dup.ID == tk.ID || dup.T3ThreadID != nil || dup.ChainID != nil {
		t.Fatalf("duplicate %+v", dup)
	}
	e.must("DELETE", fmt.Sprintf("/api/tasks/%d", dup.ID), nil, 204, nil)
	e.must("GET", fmt.Sprintf("/api/tasks/%d", dup.ID), nil, 404, nil)

	// Validation.
	e.must("POST", "/api/tasks", map[string]any{"title": "x", "prompt": "y"}, 400, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "model": "m"}, 400, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": 999, "title": "x", "prompt": "y"}, 404, nil)
	e.must("POST", "/api/tasks", "not an object", 400, nil)

	// Filters.
	var list struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?state=running,backlog&tag=t&q=b", nil, 200, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].ID != tk.ID {
		t.Fatalf("filtered %+v", list.Tasks)
	}
	e.must("GET", "/api/tasks?state=settled", nil, 200, &list)
	if len(list.Tasks) != 0 {
		t.Fatalf("state filter %d", len(list.Tasks))
	}
	e.must("GET", "/api/tasks?state=nope", nil, 400, nil)
	e.must("GET", fmt.Sprintf("/api/tasks?project=%d&from=2000-01-01&to=2999-01-01", p.ID), nil, 200, &list)
	if len(list.Tasks) != 1 {
		t.Fatalf("project/date filter %d", len(list.Tasks))
	}
	e.must("GET", "/api/tasks?to=2000-01-01", nil, 200, &list)
	if len(list.Tasks) != 0 {
		t.Fatalf("to filter %d", len(list.Tasks))
	}
}

func TestLaunchFailure(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	tk := e.task(p.ID, "A", nil)
	e.t3.failLaunch.Store(true)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 502, nil)
	got := e.getTask(tk.ID)
	if got.State != "failed" || got.LastError == nil || *got.LastError != "t3 exploded" || got.LaunchAttempt != 1 {
		t.Fatalf("after failed launch %+v", got)
	}
	// Save & start: still created (201) but failed.
	created := e.task(p.ID, "B", map[string]any{"start": true})
	if created.State != "failed" || created.LastError == nil {
		t.Fatalf("save & start failure %+v", created)
	}
	// Retry from failed works and clears lastError.
	e.t3.failLaunch.Store(false)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if started.State != "running" || started.LastError != nil || started.LaunchAttempt != 2 {
		t.Fatalf("retry %+v", started)
	}
	ok := e.task(p.ID, "C", map[string]any{"start": true})
	if ok.State != "running" {
		t.Fatalf("save & start %+v", ok)
	}
}

func TestProjects(t *testing.T) {
	e := newEnv(t)
	ext := e.fake.AddProject("External", "/w/ext")
	var linked project
	e.must("POST", "/api/projects", map[string]any{"title": " Mine ", "workspaceRoot": "/w/ext/"}, 201, &linked)
	if linked.Source != "t3" || linked.T3ProjectID != ext.ID || linked.Title != "Mine" || linked.WorkspaceRoot != "/w/ext" {
		t.Fatalf("linked %+v", linked)
	}
	app := e.project("/w/app")
	e.must("POST", "/api/projects", map[string]any{"title": "dup", "workspaceRoot": "/w/app"}, 409, nil)
	e.must("POST", "/api/projects", map[string]any{"title": "", "workspaceRoot": "/w/x"}, 400, nil)
	t3Projects, _ := e.fake.ListProjects(context.Background())
	if len(t3Projects) != 2 {
		t.Fatalf("t3 projects %d", len(t3Projects))
	}

	var got project
	e.must("PATCH", fmt.Sprintf("/api/projects/%d", app.ID), map[string]any{"archived": true}, 200, &got)
	if got.ArchivedAt == nil {
		t.Fatal("not archived")
	}
	var list struct {
		Projects []project `json:"projects"`
	}
	e.must("GET", "/api/projects", nil, 200, &list)
	if len(list.Projects) != 1 || list.Projects[0].ID != linked.ID {
		t.Fatalf("active %+v", list.Projects)
	}
	e.must("GET", "/api/projects?archived=true", nil, 200, &list)
	if len(list.Projects) != 1 || list.Projects[0].ID != app.ID {
		t.Fatalf("archived %+v", list.Projects)
	}
	e.must("GET", "/api/projects?archived=all", nil, 200, &list)
	if len(list.Projects) != 2 {
		t.Fatalf("all %d", len(list.Projects))
	}
	e.must("GET", "/api/projects?archived=maybe", nil, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/projects/%d", app.ID), map[string]any{"archived": false, "title": "Renamed"}, 200, &got)
	if got.ArchivedAt != nil || got.Title != "Renamed" {
		t.Fatalf("unarchive %+v", got)
	}
	e.must("GET", fmt.Sprintf("/api/projects/%d", app.ID), nil, 200, &got)
	e.must("GET", "/api/projects/999", nil, 404, nil)
}

func TestChainAdvancesOnlyAfterSettle(t *testing.T) {
	e := newEnv(t)
	p1 := e.project("/w/one")
	p2 := e.project("/w/two")
	a := e.task(p1.ID, "step 1", nil)
	b := e.task(p2.ID, "step 2", nil) // cross-project

	var c chain
	e.must("POST", "/api/chains", map[string]any{"title": "ch", "steps": []int64{a.ID, b.ID}}, 201, &c)
	if c.State != "idle" || c.CurrentTaskID == nil || *c.CurrentTaskID != a.ID || len(c.Steps) != 2 {
		t.Fatalf("created %+v", c)
	}
	e.must("POST", fmt.Sprintf("/api/chains/%d/resume", c.ID), nil, 409, nil)
	e.must("POST", fmt.Sprintf("/api/chains/%d/start", c.ID), nil, 200, &c)
	if c.State != "running" || c.Steps[0].State != "running" || c.Steps[1].State != "backlog" {
		t.Fatalf("started %+v", c)
	}
	e.must("POST", fmt.Sprintf("/api/chains/%d/start", c.ID), nil, 409, nil)
	thread1 := *c.Steps[0].T3ThreadID

	_ = e.fake.SetStatus(thread1, t3.StatusCompleted)
	e.poll()
	e.evaluate()
	c = e.getChain(c.ID)
	if c.Steps[0].State != "completed" || c.Steps[1].State != "backlog" || *c.CurrentTaskID != a.ID {
		t.Fatalf("completed but unsettled must wait: %+v", c)
	}

	_ = e.fake.Settle(thread1)
	e.poll()
	e.evaluate()
	c = e.getChain(c.ID)
	if c.Steps[0].State != "settled" || c.Steps[1].State != "running" || *c.CurrentTaskID != b.ID {
		t.Fatalf("after settle step 2 must run: %+v", c)
	}
	thread2 := *c.Steps[1].T3ThreadID
	if thread2 == thread1 {
		t.Fatal("chain step must get a new thread")
	}
	d, _ := e.fake.ReadThread(context.Background(), thread2)
	if d.ProjectID != p2.T3ProjectID {
		t.Fatalf("step 2 launched in %s, want %s", d.ProjectID, p2.T3ProjectID)
	}
	_ = e.fake.SetStatus(thread2, t3.StatusCompleted)
	_ = e.fake.Settle(thread2)
	e.poll()
	e.evaluate()
	c = e.getChain(c.ID)
	if c.State != "done" || c.CurrentTaskID != nil {
		t.Fatalf("chain done %+v", c)
	}
	var list struct {
		Chains []chain `json:"chains"`
	}
	e.must("GET", "/api/chains", nil, 200, &list)
	if len(list.Chains) != 1 || len(list.Chains[0].Steps) != 2 {
		t.Fatalf("list %+v", list)
	}
}

func TestChainRunLoopProcessesKicks(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	a := e.task(p.ID, "s1", nil)
	b := e.task(p.ID, "s2", nil)
	var c chain
	e.must("POST", "/api/chains", map[string]any{"title": "ch", "steps": []int64{a.ID, b.ID}}, 201, &c)
	e.must("POST", fmt.Sprintf("/api/chains/%d/start", c.ID), nil, 200, &c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.runner.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	thread1 := *c.Steps[0].T3ThreadID
	_ = e.fake.SetStatus(thread1, t3.StatusCompleted)
	_ = e.fake.Settle(thread1)
	e.poll() // transition hook kicks the runner loop
	for range 200 {
		if got := e.getTask(b.ID); got.State == "running" {
			return
		}
		select {
		case <-ctx.Done():
		case <-timeAfter():
		}
	}
	t.Fatalf("runner loop did not launch step 2: %+v", e.getTask(b.ID))
}

func TestChainPauseResumeSkip(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	a := e.task(p.ID, "s1", nil)
	b := e.task(p.ID, "s2", nil)
	cTask := e.task(p.ID, "s3", nil)
	var c chain
	e.must("POST", "/api/chains", map[string]any{"title": "ch", "steps": []int64{a.ID, b.ID}}, 201, &c)
	// Append via create.
	appended := e.task(p.ID, "s4", map[string]any{"chainId": c.ID, "start": true})
	if appended.ChainID == nil || appended.State != "backlog" {
		t.Fatalf("appended %+v (start must be ignored with chainId)", appended)
	}
	// Reorder/add/remove via PATCH while idle.
	e.must("PATCH", fmt.Sprintf("/api/chains/%d", c.ID), map[string]any{"steps": []int64{a.ID, b.ID, cTask.ID}}, 200, &c)
	if len(c.Steps) != 3 || c.Steps[2].ID != cTask.ID {
		t.Fatalf("patched %+v", c)
	}
	if got := e.getTask(appended.ID); got.ChainID != nil {
		t.Fatal("removed step still chained")
	}
	e.must("POST", fmt.Sprintf("/api/chains/%d/pause", c.ID), nil, 409, nil)
	e.must("POST", fmt.Sprintf("/api/chains/%d/start", c.ID), nil, 200, &c)
	thread1 := *c.Steps[0].T3ThreadID

	// Step failure pauses the chain.
	_ = e.fake.SetStatus(thread1, t3.StatusFailed)
	e.poll()
	c = e.getChain(c.ID)
	if c.State != "paused" || c.Steps[0].State != "failed" {
		t.Fatalf("failure must pause: %+v", c)
	}
	e.must("POST", fmt.Sprintf("/api/chains/%d/resume", c.ID), nil, 409, nil)

	// Skip the failed step: cancelled (detail skipped), chain stays paused.
	e.must("POST", fmt.Sprintf("/api/chains/%d/skip", c.ID), nil, 200, &c)
	if c.State != "paused" || c.Steps[0].State != "cancelled" || c.Steps[1].State != "backlog" || *c.CurrentTaskID != b.ID {
		t.Fatalf("after skip %+v", c)
	}
	skipped := e.getTask(a.ID)
	if last := skipped.Events[len(skipped.Events)-1]; last.Detail != "skipped" || last.ToState != "cancelled" {
		t.Fatalf("skip event %+v", last)
	}
	e.must("PATCH", fmt.Sprintf("/api/chains/%d", c.ID), map[string]any{"steps": []int64{b.ID, cTask.ID}}, 409, nil)

	// Resume launches step 2.
	e.must("POST", fmt.Sprintf("/api/chains/%d/resume", c.ID), nil, 200, &c)
	if c.State != "running" || c.Steps[1].State != "running" {
		t.Fatalf("after resume %+v", c)
	}
	thread2 := *c.Steps[1].T3ThreadID

	// Pause does not stop the active thread.
	e.must("POST", fmt.Sprintf("/api/chains/%d/pause", c.ID), nil, 200, &c)
	if c.State != "paused" || slices.Contains(e.fake.Interrupted, thread2) {
		t.Fatalf("pause %+v", c)
	}
	e.must("POST", fmt.Sprintf("/api/chains/%d/pause", c.ID), nil, 409, nil)
	e.must("POST", fmt.Sprintf("/api/chains/%d/resume", c.ID), nil, 200, &c)

	// Skip an active step on a running chain: interrupt + advance; the
	// cancelled step is not re-polled into interrupted.
	e.must("POST", fmt.Sprintf("/api/chains/%d/skip", c.ID), nil, 200, &c)
	if !slices.Contains(e.fake.Interrupted, thread2) {
		t.Fatal("skip must interrupt the active thread")
	}
	if c.State != "running" || c.Steps[1].State != "cancelled" || c.Steps[2].State != "running" {
		t.Fatalf("skip running %+v", c)
	}
	e.poll()
	if got := e.getTask(b.ID); got.State != "cancelled" {
		t.Fatalf("skipped step re-polled to %s", got.State)
	}
	// Skip last step → no current step → done.
	e.must("POST", fmt.Sprintf("/api/chains/%d/skip", c.ID), nil, 200, &c)
	if c.State != "done" || c.CurrentTaskID != nil {
		t.Fatalf("skip last %+v", c)
	}
	e.must("POST", fmt.Sprintf("/api/chains/%d/skip", c.ID), nil, 409, nil)

	// Validation.
	e.must("POST", "/api/chains", map[string]any{"title": ""}, 400, nil)
	e.must("POST", "/api/chains", map[string]any{"title": "x", "steps": []int64{a.ID}}, 409, nil) // in another chain
	e.must("POST", "/api/chains", map[string]any{"title": "x", "steps": []int64{999}}, 404, nil)
	var empty chain
	e.must("POST", "/api/chains", map[string]any{"title": "empty"}, 201, &empty)
	e.must("POST", fmt.Sprintf("/api/chains/%d/start", empty.ID), nil, 409, nil)
	e.must("GET", "/api/chains/999", nil, 404, nil)
}

func TestSyncHistoryAndStats(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/app")
	ext := e.fake.AddProject("Ext", "/w/ext")
	// Threads created directly in T3.
	for i, title := range []string{"alpha one", "beta two", "gamma three"} {
		res, err := e.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: ext.ID, Title: title,
			ModelSelection: &t3.ModelSelection{InstanceID: "codex", Model: "gpt"}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			_ = e.fake.SetStatus(res.ThreadID, t3.StatusCompleted)
			_ = e.fake.Settle(res.ThreadID)
		}
	}
	tk := e.task(p.ID, "app task", map[string]any{"start": true})

	var res struct {
		Projects   int    `json:"projects"`
		Threads    int    `json:"threads"`
		FinishedAt string `json:"finishedAt"`
	}
	e.must("POST", "/api/sync", nil, 200, &res)
	if res.Projects != 2 || res.Threads != 4 || res.FinishedAt == "" {
		t.Fatalf("sync %+v", res)
	}
	var st struct {
		LastFullSyncAt *string `json:"lastFullSyncAt"`
		Connected      bool    `json:"connected"`
		EnvironmentID  *string `json:"environmentId"`
		PublicURL      string  `json:"publicUrl"`
	}
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if st.LastFullSyncAt == nil || !st.Connected || st.EnvironmentID == nil || st.PublicURL != publicURL {
		t.Fatalf("status %+v", st)
	}
	var projects struct {
		Projects []project `json:"projects"`
	}
	e.must("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) != 2 {
		t.Fatalf("imported projects %+v", projects.Projects)
	}
	var extProject project
	for _, pr := range projects.Projects {
		if pr.T3ProjectID == ext.ID {
			extProject = pr
		}
	}
	if extProject.Source != "t3" {
		t.Fatalf("imported project %+v", extProject)
	}

	type page struct {
		Threads []thread `json:"threads"`
		Total   int      `json:"total"`
	}
	var pg page
	e.must("GET", "/api/threads", nil, 200, &pg)
	if pg.Total != 4 || len(pg.Threads) != 4 || pg.Threads[0].TaskID == nil || *pg.Threads[0].TaskID != tk.ID {
		t.Fatalf("all threads (newest first) %+v", pg)
	}
	e.must("GET", "/api/threads?source=app", nil, 200, &pg)
	if pg.Total != 1 || pg.Threads[0].Source != "app" || pg.Threads[0].ProjectID == nil || *pg.Threads[0].ProjectID != p.ID {
		t.Fatalf("source=app %+v", pg)
	}
	e.must("GET", "/api/threads?source=t3", nil, 200, &pg)
	if pg.Total != 3 {
		t.Fatalf("source=t3 %+v", pg)
	}
	e.must("GET", "/api/threads?settled=true", nil, 200, &pg)
	if pg.Total != 1 || pg.Threads[0].Title != "alpha one" {
		t.Fatalf("settled %+v", pg)
	}
	e.must("GET", "/api/threads?status=running&status=completed", nil, 200, &pg)
	if pg.Total != 4 {
		t.Fatalf("status %+v", pg)
	}
	e.must("GET", "/api/threads?status=completed", nil, 200, &pg)
	if pg.Total != 1 {
		t.Fatalf("status completed %+v", pg)
	}
	e.must("GET", "/api/threads?q=BETA", nil, 200, &pg)
	if pg.Total != 1 || pg.Threads[0].Title != "beta two" {
		t.Fatalf("q %+v", pg)
	}
	e.must("GET", "/api/threads?provider=codex", nil, 200, &pg)
	if pg.Total != 3 {
		t.Fatalf("provider %+v", pg)
	}
	e.must("GET", fmt.Sprintf("/api/threads?project=%d", extProject.ID), nil, 200, &pg)
	if pg.Total != 3 {
		t.Fatalf("project %+v", pg)
	}
	e.must("GET", "/api/threads?limit=2&offset=1", nil, 200, &pg)
	if pg.Total != 4 || len(pg.Threads) != 2 {
		t.Fatalf("paging %+v", pg)
	}
	e.must("GET", "/api/threads?from=2999-01-01", nil, 200, &pg)
	if pg.Total != 0 || pg.Threads == nil {
		t.Fatalf("from future %+v", pg)
	}
	e.must("GET", "/api/threads?status=bogus", nil, 400, nil)
	e.must("GET", "/api/threads?source=x", nil, 400, nil)
	e.must("GET", "/api/threads?limit=0", nil, 400, nil)
	e.must("GET", "/api/threads?settled=maybe", nil, 400, nil)

	var stats struct {
		From         string `json:"from"`
		TZ           string `json:"tz"`
		ThreadsByDay []struct {
			Day   string `json:"day"`
			Count int    `json:"count"`
		} `json:"threadsByDay"`
		ThreadsByProject []struct {
			ProjectID   *int64 `json:"projectId"`
			T3ProjectID string `json:"t3ProjectId"`
			Title       string `json:"title"`
			Count       int    `json:"count"`
		} `json:"threadsByProject"`
		ThreadsByStatus []struct {
			Status string `json:"status"`
			Count  int    `json:"count"`
		} `json:"threadsByStatus"`
		TasksByState []struct {
			State string `json:"state"`
			Count int    `json:"count"`
		} `json:"tasksByState"`
	}
	e.must("GET", "/api/stats?tz=Australia/Sydney", nil, 200, &stats)
	if stats.TZ != "Australia/Sydney" || len(stats.ThreadsByDay) == 0 {
		t.Fatalf("stats %+v", stats)
	}
	total := 0
	for _, d := range stats.ThreadsByDay {
		total += d.Count
	}
	if total != 4 {
		t.Fatalf("threadsByDay total %d", total)
	}
	if len(stats.ThreadsByProject) != 2 || stats.ThreadsByProject[0].Count != 3 || stats.ThreadsByProject[0].ProjectID == nil {
		t.Fatalf("byProject %+v", stats.ThreadsByProject)
	}
	if len(stats.TasksByState) != 1 || stats.TasksByState[0].State != "running" || stats.TasksByState[0].Count != 1 {
		t.Fatalf("tasksByState %+v", stats.TasksByState)
	}
	e.must("GET", "/api/stats?from=2999-01-01&to=2999-02-01", nil, 200, &stats)
	if len(stats.ThreadsByDay) != 0 || stats.ThreadsByDay == nil {
		t.Fatalf("empty range %+v", stats.ThreadsByDay)
	}
	e.must("GET", "/api/stats?tz=Mars/Base", nil, 400, nil)
}

func TestCatalogAndPair(t *testing.T) {
	e := newEnv(t)
	e.fake.Catalog.Providers = append(e.fake.Catalog.Providers, t3.Provider{
		ProviderInstanceID: "broken", Constraints: []string{"not authenticated"},
	}, t3.Provider{ProviderInstanceID: "bare", Models: []t3.Model{{ID: "m1"}}})
	var cat struct {
		Providers []struct {
			ProviderInstanceID string `json:"providerInstanceId"`
			DisplayName        string `json:"displayName"`
			Models             []struct {
				ID      string `json:"id"`
				Label   string `json:"label"`
				Options []struct {
					ID      string `json:"id"`
					Values  []any  `json:"values"`
					Default any    `json:"default"`
				} `json:"options"`
			} `json:"models"`
			RuntimeModes []string `json:"runtimeModes"`
		} `json:"providers"`
		FetchedAt *string `json:"fetchedAt"`
	}
	e.must("GET", "/api/catalog", nil, 200, &cat)
	if len(cat.Providers) != 2 || cat.FetchedAt == nil {
		t.Fatalf("catalog %+v", cat)
	}
	claude, bare := cat.Providers[0], cat.Providers[1]
	if claude.Models[0].Options[0].Default != "medium" || len(claude.Models[0].Options[0].Values) != 3 || len(claude.RuntimeModes) != 4 {
		t.Fatalf("claude %+v", claude)
	}
	if bare.DisplayName != "bare" || bare.Models[0].Label != "m1" || bare.Models[0].Options == nil || bare.RuntimeModes != nil {
		t.Fatalf("fallbacks %+v", bare)
	}
	e.must("POST", "/api/t3/pair", map[string]any{"code": " "}, 400, nil)
	e.must("POST", "/api/t3/pair", map[string]any{"code": "ABCD-1234"}, 200, nil)
}

func TestTemplates(t *testing.T) {
	e := newEnv(t)
	type tmpl struct {
		ID                 int64   `json:"id"`
		Name               string  `json:"name"`
		Prompt             string  `json:"prompt"`
		ProviderInstanceID *string `json:"providerInstanceId"`
		ModelOptions       []any   `json:"modelOptions"`
		RuntimeMode        string  `json:"runtimeMode"`
		InteractionMode    string  `json:"interactionMode"`
	}
	var a tmpl
	e.must("POST", "/api/templates", map[string]any{"name": "Quick", "providerInstanceId": "claude", "model": "claude-haiku-5-5"}, 201, &a)
	if a.RuntimeMode != "full-access" || a.InteractionMode != "default" || a.ModelOptions == nil || a.ProviderInstanceID == nil {
		t.Fatalf("template %+v", a)
	}
	e.must("POST", "/api/templates", map[string]any{"name": "Quick"}, 409, nil)
	e.must("POST", "/api/templates", map[string]any{}, 400, nil)
	e.must("POST", "/api/templates", map[string]any{"name": "x", "interactionMode": "chat"}, 400, nil)
	var b tmpl
	e.must("POST", "/api/templates", map[string]any{"name": "Another", "interactionMode": "plan"}, 201, &b)
	e.must("PATCH", fmt.Sprintf("/api/templates/%d", b.ID), map[string]any{"name": "Quick"}, 409, nil)
	e.must("PATCH", fmt.Sprintf("/api/templates/%d", a.ID), map[string]any{"prompt": "hi", "providerInstanceId": nil}, 200, &a)
	if a.Prompt != "hi" || a.ProviderInstanceID != nil || a.Name != "Quick" {
		t.Fatalf("patched %+v", a)
	}
	var list struct {
		Templates []tmpl `json:"templates"`
	}
	e.must("GET", "/api/templates", nil, 200, &list)
	if len(list.Templates) != 2 || list.Templates[0].Name != "Another" {
		t.Fatalf("list %+v", list)
	}
	e.must("GET", fmt.Sprintf("/api/templates/%d", a.ID), nil, 200, nil)
	e.must("DELETE", fmt.Sprintf("/api/templates/%d", a.ID), nil, 204, nil)
	e.must("DELETE", fmt.Sprintf("/api/templates/%d", a.ID), nil, 404, nil)
	e.must("GET", fmt.Sprintf("/api/templates/%d", a.ID), nil, 404, nil)
}

func timeAfter() <-chan time.Time { return time.After(10 * time.Millisecond) }
