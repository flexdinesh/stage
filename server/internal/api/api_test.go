package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/api"
	"github.com/flexdinesh/t3-stage/server/internal/config"
	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/db/dbtest"
	"github.com/flexdinesh/t3-stage/server/internal/links"
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

// flakyT3 is the fake with switchable failures.
type flakyT3 struct {
	*t3.Fake
	failLaunch atomic.Bool // T3 tool error
	// lostLaunches: the next N launches reach T3 but the response is lost
	// (transport error).
	lostLaunches  atomic.Int32
	failInterrupt atomic.Bool
	onInterrupt   func()

	mu         gosync.Mutex
	requestIDs []string // clientRequestId of every launch call
}

func (f *flakyT3) LaunchThread(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	f.mu.Lock()
	f.requestIDs = append(f.requestIDs, in.ClientRequestID)
	f.mu.Unlock()
	if f.failLaunch.Load() {
		return t3.LaunchResult{}, &t3.ToolError{Tool: "t3_thread_launch", Code: "invalid_request", Message: "t3 exploded"}
	}
	res, err := f.Fake.LaunchThread(ctx, in)
	if err == nil && f.takeLost() {
		return t3.LaunchResult{}, errors.New("connection reset by peer")
	}
	return res, err
}

func (f *flakyT3) takeLost() bool {
	for {
		n := f.lostLaunches.Load()
		if n <= 0 {
			return false
		}
		if f.lostLaunches.CompareAndSwap(n, n-1) {
			return true
		}
	}
}

func (f *flakyT3) launchRequestIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requestIDs)
}

func (f *flakyT3) Interrupt(ctx context.Context, threadID string) error {
	if f.onInterrupt != nil {
		f.onInterrupt()
	}
	if f.failInterrupt.Load() {
		return errors.New("interrupt transport failure")
	}
	return f.Fake.Interrupt(ctx, threadID)
}

type env struct {
	t      *testing.T
	h      http.Handler
	fake   *t3.Fake
	t3     *flakyT3
	sync   *t3sync.Service
	runner *links.Runner
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
	runner := links.NewRunner(store, svc, log)
	svc.OnTransition = runner.OnTransition
	h := api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: publicURL},
		Store:  store, T3: client, Sync: svc, Logger: log,
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

func (e *env) reconcile() {
	e.t.Helper()
	if err := e.runner.Reconcile(context.Background()); err != nil {
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
	AfterTaskID   *int64            `json:"afterTaskId"`
	NextTaskID    *int64            `json:"nextTaskId"`
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
	Thread      *thread      `json:"thread"`
	Sequence    []task       `json:"sequence"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	SizeBytes int    `json:"sizeBytes"`
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
	if got.State != "done" || got.FinishedAt == nil || got.SettledAt != nil {
		t.Fatalf("done %+v", got)
	}
	_ = e.fake.Settle(threadID)
	e.poll()
	got = e.getTask(tk.ID)
	if got.State != "settled" || got.SettledAt == nil {
		t.Fatalf("settled %+v", got)
	}
	var settledList struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?state=settled&settledFrom=2000-01-01", nil, 200, &settledList)
	if len(settledList.Tasks) != 1 || settledList.Tasks[0].ID != tk.ID {
		t.Fatalf("settledFrom past %+v", settledList.Tasks)
	}
	e.must("GET", "/api/tasks?state=settled&settledFrom=2999-01-01", nil, 200, &settledList)
	if len(settledList.Tasks) != 0 {
		t.Fatalf("settledFrom future %+v", settledList.Tasks)
	}
	e.must("GET", "/api/tasks?settledFrom=nope", nil, 400, nil)
	var states []string
	for _, ev := range got.Events {
		states = append(states, ev.ToState)
	}
	want := []string{"backlog", "launching", "running", "needs_input", "done", "settled"}
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
	tasks, _ := e.store.PollableTasks(context.Background(), true)
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
	if dup.State != "backlog" || dup.Title != "B" || dup.ID == tk.ID || dup.T3ThreadID != nil || dup.AfterTaskID != nil {
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
	if got.State != "failed" || got.LastError == nil || *got.LastError != "t3 t3_thread_launch: invalid_request: t3 exploded" || got.LaunchAttempt != 1 {
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

func TestLaunchTransportFailureReusesClientRequestID(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")

	// Response lost once: retried with the same clientRequestId, one thread.
	a := e.task(p.ID, "A", nil)
	e.t3.lostLaunches.Store(1)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", a.ID), nil, 200, &started)
	if started.State != "running" || started.LaunchAttempt != 1 || started.T3ThreadID == nil {
		t.Fatalf("after one lost response %+v", started)
	}
	want := fmt.Sprintf("task-%d-1", a.ID)
	if ids := e.t3.launchRequestIDs(); !slices.Equal(ids, []string{want, want}) {
		t.Fatalf("request ids %v", ids)
	}

	// Lost twice: failed, and the next start reuses the attempt (T3 already
	// has the thread for that clientRequestId).
	b := e.task(p.ID, "B", nil)
	e.t3.lostLaunches.Store(2)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 502, nil)
	failed := e.getTask(b.ID)
	if failed.State != "failed" || failed.LastError == nil || failed.LaunchAttempt != 1 || failed.T3ThreadID != nil {
		t.Fatalf("after two lost responses %+v", failed)
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, &started)
	if started.State != "running" || started.LaunchAttempt != 1 {
		t.Fatalf("restart %+v", started)
	}
	wantB := fmt.Sprintf("task-%d-1", b.ID)
	ids := e.t3.launchRequestIDs()
	if got := ids[2:]; !slices.Equal(got, []string{wantB, wantB, wantB}) {
		t.Fatalf("request ids %v", ids)
	}
	page, _ := e.fake.ListThreads(context.Background(), t3.ListThreadsInput{AllProjects: true})
	if page.Total != 2 {
		t.Fatalf("threads %d, want 2 (no duplicate launch)", page.Total)
	}
	// After a successful launch, the next start is a new attempt.
	_ = e.fake.SetStatus(*started.T3ThreadID, t3.StatusFailed)
	e.poll()
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, &started)
	if started.LaunchAttempt != 2 {
		t.Fatalf("new attempt %+v", started)
	}
}

func TestRecoverLaunching(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	a := e.task(p.ID, "A", nil)
	if _, err := e.store.TransitionTask(context.Background(), db.Transition{TaskID: a.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	e.sync.RecoverLaunching(context.Background())
	if got := e.getTask(a.ID); got.State != "failed" || got.LastError == nil {
		t.Fatalf("after recover %+v", got)
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

func TestLinkedTaskStartsOnlyAfterSettle(t *testing.T) {
	e := newEnv(t)
	p1 := e.project("/w/one")
	p2 := e.project("/w/two")
	a := e.task(p1.ID, "step 1", map[string]any{"start": true})
	b := e.task(p2.ID, "step 2", map[string]any{"afterTaskId": a.ID, "start": true}) // start ignored when linked
	if b.State != "backlog" || b.AfterTaskID == nil || *b.AfterTaskID != a.ID {
		t.Fatalf("linked %+v", b)
	}
	if got := e.getTask(a.ID); got.NextTaskID == nil || *got.NextTaskID != b.ID || len(got.Sequence) != 2 ||
		got.Sequence[0].ID != a.ID || got.Sequence[1].ID != b.ID {
		t.Fatalf("a %+v", got)
	}
	thread1 := *a.T3ThreadID

	_ = e.fake.SetStatus(thread1, t3.StatusCompleted)
	e.poll()
	e.reconcile()
	if got := e.getTask(b.ID); got.State != "backlog" {
		t.Fatalf("done but unsettled must wait: %+v", got)
	}

	_ = e.fake.Settle(thread1)
	e.poll()
	e.reconcile()
	got := e.getTask(b.ID)
	if got.State != "running" || got.T3ThreadID == nil || *got.T3ThreadID == thread1 || got.AfterTaskID == nil {
		t.Fatalf("after settle step 2 must run in a new thread, keeping its link: %+v", got)
	}
	d, _ := e.fake.ReadThread(context.Background(), *got.T3ThreadID)
	if d.ProjectID != p2.T3ProjectID {
		t.Fatalf("step 2 launched in %s, want %s", d.ProjectID, p2.T3ProjectID)
	}
}

func TestLinkRunLoopProcessesKicks(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	a := e.task(p.ID, "s1", map[string]any{"start": true})
	b := e.task(p.ID, "s2", map[string]any{"afterTaskId": a.ID})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.runner.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*a.T3ThreadID)
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

func TestLinkRulesAndStartNow(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", nil)
	c := e.task(p.ID, "c", nil)

	// "Then run" from a = link b after a.
	var got task
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", b.ID), map[string]any{"afterTaskId": a.ID}, 200, &got)
	if got.AfterTaskID == nil || *got.AfterTaskID != a.ID {
		t.Fatalf("linked %+v", got)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", c.ID), map[string]any{"afterTaskId": a.ID}, 409, nil) // a taken
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", a.ID), map[string]any{"afterTaskId": c.ID}, 409, nil) // a not backlog
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", c.ID), map[string]any{"afterTaskId": 999}, 404, nil)  // missing
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "afterTaskId": a.ID}, 409, nil)

	// A failed predecessor leaves its follower waiting.
	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusFailed)
	e.poll()
	e.reconcile()
	if got = e.getTask(b.ID); got.State != "backlog" {
		t.Fatalf("follower of failed task %+v", got)
	}

	// Start now: b is unlinked from the unfinished a and launched.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, &got)
	if got.State != "running" || got.AfterTaskID != nil {
		t.Fatalf("start now %+v", got)
	}
	if got = e.getTask(a.ID); got.NextTaskID != nil {
		t.Fatalf("a still has a follower %+v", got)
	}

	// Unlink with null; delete splices.
	d := e.task(p.ID, "d", map[string]any{"afterTaskId": c.ID})
	f := e.task(p.ID, "f", map[string]any{"afterTaskId": d.ID})
	e.must("DELETE", fmt.Sprintf("/api/tasks/%d", d.ID), nil, 204, nil)
	if got = e.getTask(f.ID); got.AfterTaskID == nil || *got.AfterTaskID != c.ID {
		t.Fatalf("splice %+v", got)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", f.ID), map[string]any{"afterTaskId": nil}, 200, &got)
	if got.AfterTaskID != nil {
		t.Fatalf("unlink %+v", got)
	}
}

func TestFollowupDoesNotReviveReleasedSettledTask(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	a := e.task(p.ID, "s1", map[string]any{"start": true})
	b := e.task(p.ID, "s2", map[string]any{"afterTaskId": a.ID})
	threadA := *a.T3ThreadID
	_ = e.fake.SetStatus(threadA, t3.StatusCompleted)
	_ = e.fake.Settle(threadA)
	e.poll()
	e.reconcile()
	if got := e.getTask(b.ID); got.State != "running" {
		t.Fatalf("follower %+v", got)
	}

	// Follow-up on the settled task whose follower already ran: stays settled.
	_ = e.fake.SetThread(threadA, func(d *t3.ThreadDetail) { d.Settled = false; d.Status = t3.StatusRunning })
	var got task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", a.ID), map[string]any{"message": "more"}, 200, &got)
	if got.State != "settled" {
		t.Fatalf("settled task after follow-up: %s", got.State)
	}
	if th := e.getTask(a.ID).Thread; th == nil || th.Status != "running" {
		t.Fatalf("thread mirror not refreshed: %+v", th)
	}

	// A settled unlinked task still reopens on follow-up.
	solo := e.task(p.ID, "solo", map[string]any{"start": true})
	_ = e.fake.SetStatus(*solo.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*solo.T3ThreadID)
	e.poll()
	_ = e.fake.SetThread(*solo.T3ThreadID, func(d *t3.ThreadDetail) { d.Settled = false; d.Status = t3.StatusRunning })
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", solo.ID), map[string]any{"message": "more"}, 200, &got)
	if got.State != "running" {
		t.Fatalf("standalone settled task after follow-up: %s", got.State)
	}
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

func TestHistorySubagentsFilter(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	if err := e.store.UpsertThreads(context.Background(), []t3.Thread{
		{ThreadID: "root-1", ProjectID: "proj-1", Title: "root", Status: t3.StatusCompleted, CreatedAt: now.Add(-2 * time.Minute)},
		{ThreadID: "sub-1", ProjectID: "proj-1", Title: "sub", Status: t3.StatusRunning, CreatedAt: now.Add(-time.Minute),
			ParentThreadID: "root-1", RelationshipToParent: "subagent"},
	}); err != nil {
		t.Fatal(err)
	}
	var pg struct {
		Threads []thread `json:"threads"`
		Total   int      `json:"total"`
	}
	for path, want := range map[string]int{
		"/api/threads":                                  2,
		"/api/threads?subagents=include":                2,
		"/api/threads?subagents=exclude":                1,
		"/api/threads?subagents=exclude&status=running": 0,
	} {
		e.must("GET", path, nil, 200, &pg)
		if pg.Total != want || len(pg.Threads) != want {
			t.Fatalf("%s: total=%d len=%d want %d", path, pg.Total, len(pg.Threads), want)
		}
	}
	e.must("GET", "/api/threads?subagents=exclude", nil, 200, &pg)
	if pg.Threads[0].T3ThreadID != "root-1" {
		t.Fatalf("exclude kept %+v", pg.Threads)
	}
	e.must("GET", "/api/threads?subagents=only", nil, 400, nil)

	var st struct {
		ThreadsByDay []struct {
			Count int `json:"count"`
		} `json:"threadsByDay"`
		ThreadsByProject []struct {
			Count int `json:"count"`
		} `json:"threadsByProject"`
		ThreadsByStatus []struct {
			Status string `json:"status"`
			Count  int    `json:"count"`
		} `json:"threadsByStatus"`
	}
	for path, want := range map[string]int{
		"/api/stats":                   2,
		"/api/stats?subagents=include": 2,
		"/api/stats?subagents=exclude": 1,
	} {
		e.must("GET", path, nil, 200, &st)
		day, project, status := 0, 0, 0
		for _, c := range st.ThreadsByDay {
			day += c.Count
		}
		for _, c := range st.ThreadsByProject {
			project += c.Count
		}
		for _, c := range st.ThreadsByStatus {
			status += c.Count
		}
		if day != want || project != want || status != want {
			t.Fatalf("%s: day=%d project=%d status=%d want %d", path, day, project, status, want)
		}
	}
	e.must("GET", "/api/stats?subagents=bogus", nil, 400, nil)
}

func TestPreferences(t *testing.T) {
	e := newEnv(t)
	type defaults struct {
		ProviderInstanceID *string `json:"providerInstanceId"`
		Model              *string `json:"model"`
		ModelOptions       []struct {
			ID    string `json:"id"`
			Value any    `json:"value"`
		} `json:"modelOptions"`
		RuntimeMode     string `json:"runtimeMode"`
		InteractionMode string `json:"interactionMode"`
	}
	type prefs struct {
		Pinned   *defaults `json:"pinned"`
		LastUsed *defaults `json:"lastUsed"`
	}
	var p prefs
	e.must("GET", "/api/preferences", nil, 200, &p)
	if p.Pinned != nil || p.LastUsed != nil {
		t.Fatalf("fresh %+v", p)
	}

	// Creating a task records its model config as last used.
	pr := e.project("/w/prefs")
	e.task(pr.ID, "A", map[string]any{
		"providerInstanceId": "codex", "model": "gpt-6", "modelOptions": []map[string]any{{"id": "effort", "value": "high"}},
		"runtimeMode": "auto", "interactionMode": "plan",
	})
	e.must("GET", "/api/preferences", nil, 200, &p)
	lu := p.LastUsed
	if lu == nil || *lu.ProviderInstanceID != "codex" || *lu.Model != "gpt-6" || lu.RuntimeMode != "auto" ||
		lu.InteractionMode != "plan" || len(lu.ModelOptions) != 1 || lu.ModelOptions[0].Value != "high" || p.Pinned != nil {
		t.Fatalf("last used %+v", p)
	}

	// Pin, validate, clear.
	e.must("PUT", "/api/preferences/pinned", map[string]any{"providerInstanceId": "claude", "model": "claude-haiku-5-5"}, 200, &p)
	if p.Pinned == nil || *p.Pinned.Model != "claude-haiku-5-5" || p.Pinned.RuntimeMode != "full-access" ||
		p.Pinned.InteractionMode != "default" || p.Pinned.ModelOptions == nil || p.LastUsed == nil {
		t.Fatalf("pinned %+v", p)
	}
	e.must("PUT", "/api/preferences/pinned", map[string]any{"model": "x"}, 400, nil)
	e.must("PUT", "/api/preferences/pinned", map[string]any{"runtimeMode": "yolo"}, 400, nil)
	e.must("PUT", "/api/preferences/pinned", nil, 400, nil)
	e.must("PUT", "/api/preferences/pinned", json.RawMessage("null"), 200, &p)
	if p.Pinned != nil || p.LastUsed == nil {
		t.Fatalf("cleared %+v", p)
	}
}

// pngBytes is a valid 1x1 PNG (sniffs as image/png).
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0bIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

// upload posts data as multipart field "file"; returns status and body.
func (e *env) upload(name string, data []byte) (int, attachment) {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		e.t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/uploads", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var a attachment
	if rec.Code == 201 {
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			e.t.Fatal(err)
		}
	}
	return rec.Code, a
}

func (e *env) mustUpload(name string) attachment {
	e.t.Helper()
	code, a := e.upload(name, pngBytes)
	if code != 201 {
		e.t.Fatalf("upload %s: status %d", name, code)
	}
	return a
}

func TestUploadValidationAndServe(t *testing.T) {
	e := newEnv(t)
	a := e.mustUpload("../../shot.png")
	if a.Name != "shot.png" || a.MimeType != "image/png" || a.SizeBytes != len(pngBytes) {
		t.Fatalf("upload = %+v", a)
	}
	if code, _ := e.upload("x.png", []byte("<svg onload=alert(1)>")); code != 400 {
		t.Fatalf("non-image upload: status %d, want 400", code)
	}
	big := append(append([]byte{}, pngBytes...), make([]byte, db.MaxAttachmentBytes)...)
	if code, _ := e.upload("big.png", big); code != 400 {
		t.Fatalf("oversize upload: status %d, want 400", code)
	}
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/attachments/%d", a.ID), nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Fatalf("get attachment: %d %v", rec.Code, rec.Header())
	}
	e.must("DELETE", fmt.Sprintf("/api/attachments/%d", a.ID), nil, 204, nil)
	e.must("GET", fmt.Sprintf("/api/attachments/%d", a.ID), nil, 404, nil)
}

func TestTaskImagesLaunchAndEdit(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/img")
	a1, a2, a3 := e.mustUpload("a.png"), e.mustUpload("b.png"), e.mustUpload("c.png")

	tk := e.task(p.ID, "with images", map[string]any{"attachmentIds": []int64{a1.ID, a2.ID}})
	if len(tk.Attachments) != 2 || tk.Attachments[0].ID != a1.ID || tk.Attachments[1].Name != "b.png" {
		t.Fatalf("attachments = %+v", tk.Attachments)
	}
	// An attached image can't be reused by another task.
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "steal", "prompt": "x", "attachmentIds": []int64{a1.ID}}, 400, nil)
	nine := make([]int64, 9)
	for i := range nine {
		nine[i] = int64(i + 1)
	}
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "many", "prompt": "x", "attachmentIds": nine}, 400, nil)

	// Backlog edit sets the exact set: drop a1, add a3.
	var got task
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"attachmentIds": []int64{a2.ID, a3.ID}}, 200, &got)
	if len(got.Attachments) != 2 || got.Attachments[0].ID != a2.ID || got.Attachments[1].ID != a3.ID {
		t.Fatalf("after edit = %+v", got.Attachments)
	}
	e.must("GET", fmt.Sprintf("/api/attachments/%d", a1.ID), nil, 404, nil)

	// Duplicate copies images.
	var dup task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/duplicate", tk.ID), nil, 201, &dup)
	if len(dup.Attachments) != 2 || dup.Attachments[0].ID == a2.ID || dup.Attachments[0].Name != "b.png" {
		t.Fatalf("duplicate attachments = %+v", dup.Attachments)
	}

	// Launch uploads each image to T3 and passes them to t3_thread_launch.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &got)
	if got.State != "running" {
		t.Fatalf("state %s (%v)", got.State, got.LastError)
	}
	if len(e.fake.Launched) != 1 || len(e.fake.Launched[0].Attachments) != 2 || e.fake.Launched[0].Attachments[0].Name != "b.png" ||
		!bytes.Equal(e.fake.Uploads[0].Data, pngBytes) {
		t.Fatalf("launched = %+v uploads = %d", e.fake.Launched, len(e.fake.Uploads))
	}
	// Read-only after backlog.
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"attachmentIds": []int64{}}, 409, nil)
	e.must("DELETE", fmt.Sprintf("/api/attachments/%d", a2.ID), nil, 409, nil)

	// A failed launch discards that launch's uploads; a retry uploads fresh ones.
	e.t3.failLaunch.Store(true)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", dup.ID), nil, 502, nil)
	if len(e.fake.Discarded) != 2 || e.fake.Discarded[0] != e.fake.Uploads[2].ID {
		t.Fatalf("discarded = %v", e.fake.Discarded)
	}
	e.t3.failLaunch.Store(false)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", dup.ID), nil, 200, &got)
	last := e.fake.Launched[len(e.fake.Launched)-1]
	if len(last.Attachments) != 2 || last.Attachments[0].ID != e.fake.Uploads[4].ID {
		t.Fatalf("retry attachments = %+v", last.Attachments)
	}
}

func TestFollowupWithImages(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/fimg")
	tk := e.task(p.ID, "f", map[string]any{"start": true})
	img := e.mustUpload("followup.png")
	path := fmt.Sprintf("/api/tasks/%d/followup", tk.ID)
	e.must("POST", path, map[string]any{"message": "x", "mode": "steer", "attachmentIds": []int64{img.ID}}, 400, nil)
	e.must("POST", path, map[string]any{"attachmentIds": []int64{999999}}, 400, nil)
	e.must("POST", path, map[string]any{"message": "see this", "attachmentIds": []int64{img.ID}}, 200, nil)
	if len(e.fake.SentAttachments) != 1 || e.fake.SentAttachments[0].Message != "see this" ||
		len(e.fake.SentAttachments[0].Attachments) != 1 || e.fake.SentAttachments[0].Attachments[0].Name != "followup.png" {
		t.Fatalf("sent = %+v", e.fake.SentAttachments)
	}
	// Sent images are deleted.
	e.must("GET", fmt.Sprintf("/api/attachments/%d", img.ID), nil, 404, nil)
	if len(e.fake.Sent) != 0 {
		t.Fatalf("plain send used: %+v", e.fake.Sent)
	}
}
