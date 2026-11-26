package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/release"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

var testDB dbtest.DB

// flakyT3 is the fake with switchable failures.
type flakyT3 struct {
	*t3.Fake
	failLaunch atomic.Bool // T3 tool error
	// lostLaunches: the next N launches reach T3 but the response is lost
	// (transport error).
	lostLaunches  atomic.Int32
	failInterrupt atomic.Bool
	onInterrupt   func()
	offline       atomic.Bool // Status reports not connected
	// failQuestions fails t3_pending_request list (transport error).
	failQuestions atomic.Bool
	// foreignProject: the next launch answers with a thread in this project
	// (T3 deduped the clientRequestId to another client's launch).
	foreignProject atomic.Pointer[string]
	failReply      atomic.Bool // LatestReply transport failure
	// failReplyFor fails LatestReply for this thread only.
	failReplyFor atomic.Pointer[string]
	failReadFor  atomic.Pointer[string]

	// listGate, when set, makes the next ListThreads signal reached and
	// wait for gate.
	listGate atomic.Pointer[listGate]

	mu             gosync.Mutex
	requestIDs     []string // clientRequestId of every launch call
	launchMessages []string
}

// PublicURL is the configured T3 browser base, like t3.MCPClient's; the
// adapter builds links.web from it.
func (f *flakyT3) PublicURL() string { return publicURL }

func (f *flakyT3) LaunchThread(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	f.mu.Lock()
	f.requestIDs = append(f.requestIDs, in.ClientRequestID)
	f.launchMessages = append(f.launchMessages, in.Message)
	f.mu.Unlock()
	if f.failLaunch.Load() {
		return t3.LaunchResult{}, &t3.ToolError{Tool: "t3_thread_launch", Code: "invalid_request", Message: "t3 exploded. No thread was created."}
	}
	res, err := f.Fake.LaunchThread(ctx, in)
	if p := f.foreignProject.Swap(nil); err == nil && p != nil {
		res.ProjectID = *p
	}
	if err == nil && f.takeLost() {
		return t3.LaunchResult{}, errors.New("connection reset by peer")
	}
	return res, err
}

func (f *flakyT3) LatestReply(ctx context.Context, in t3.LatestReplyInput) (t3.Reply, error) {
	if p := f.failReplyFor.Load(); f.failReply.Load() || (p != nil && *p == in.ThreadID) {
		return t3.Reply{}, errors.New("t3 read timed out")
	}
	return f.Fake.LatestReply(ctx, in)
}

type listGate struct{ gate, reached chan struct{} }

func (f *flakyT3) ReadThread(ctx context.Context, id string) (t3.ThreadDetail, error) {
	if p := f.failReadFor.Load(); p != nil && *p == id {
		return t3.ThreadDetail{}, errors.New("source harness offline")
	}
	if g := f.listGate.Swap(nil); g != nil {
		g.reached <- struct{}{}
		<-g.gate
	}
	return f.Fake.ReadThread(ctx, id)
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

func (f *flakyT3) Status(ctx context.Context) t3.ConnectionStatus {
	if f.offline.Load() {
		return t3.ConnectionStatus{}
	}
	return f.Fake.Status(ctx)
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

func (f *flakyT3) ListPendingQuestions(ctx context.Context, threadID string) ([]string, error) {
	if f.failQuestions.Load() {
		return nil, errors.New("list transport failure")
	}
	return f.Fake.ListPendingQuestions(ctx, threadID)
}

type env struct {
	t      *testing.T
	h      http.Handler
	fake   *t3.Fake
	t3     *flakyT3
	sync   *t3sync.Service
	runner *release.Runner
	store  *db.Store
}

const publicURL = "http://localhost:3773"

func newEnv(t *testing.T, opts ...func(*api.Deps)) *env {
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	// Tests complete tasks explicitly; inference tests turn auto-complete on.
	if _, err := store.SetAutoComplete(context.Background(), store.LegacyT3ID(), false); err != nil {
		t.Fatal(err)
	}
	fake := t3.NewFake()
	client := &flakyT3{Fake: fake}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := t3sync.New(store, t3.NewAdapter(client), log)
	svc.Harnesses = testHarnesses
	runner := release.NewRunner(store, svc, log)
	svc.OnTransition = runner.OnTransition
	push := notify.New(store, "https://stage.test", log)
	runner.OnReady = push.OnReady
	deps := api.Deps{
		Config: config.Config{},
		Store:  store, Sync: svc, Push: push, Logger: log, Harnesses: testHarnesses,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	if deps.Setup == nil {
		deps.Setup = t3setup.New(t3setup.Options{Config: deps.Config, Settings: store.OnMachine(db.LocalMachine), Identity: machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(t3.Descriptor)), Client: client, Fake: true, Logger: log})
	}
	connectors := map[string]harness.Connector{t3.Kind: t3setup.NewConnector(deps.Setup, func(connection int64) t3setup.SyncProgress {
		p := svc.Progress(connection)
		return t3setup.SyncProgress{Running: p.Running, StartedAt: p.StartedAt, Projects: p.Projects, Threads: p.Threads}
	})}
	if c, ok := deps.Connectors.Get(db.LocalMachine, opencode.Kind); ok {
		connectors[opencode.Kind] = c
	}
	deps.Connectors = mustConnectors(connectors)
	h := api.NewRouter(deps)
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
	T3ProjectID   string  `json:"externalProjectId"`
	Title         string  `json:"title"`
	WorkspaceRoot string  `json:"workspaceRoot"`
	Source        string  `json:"source"`
	ArchivedAt    *string `json:"archivedAt"`
}

type task struct {
	ID            int64          `json:"id"`
	ProjectID     int64          `json:"projectId"`
	ProjectTitle  string         `json:"projectTitle"`
	Title         string         `json:"title"`
	Prompt        string         `json:"prompt"`
	Config        *execConfig    `json:"executionConfig"`
	Workspace     map[string]any `json:"workspace"`
	State         string         `json:"state"`
	T3ThreadID    *string        `json:"externalSessionId"`
	TreeID        *int64         `json:"treeId"`
	Needs         []int64        `json:"needs"`
	Dependents    []int64        `json:"dependents"`
	Readiness     *string        `json:"readiness"`
	StartMode     *string        `json:"startMode"`
	EffStartMode  *string        `json:"effectiveStartMode"`
	Adopted       bool           `json:"adopted"`
	LaunchAttempt int            `json:"launchAttempt"`
	LastError     *string        `json:"lastError"`
	Links         links          `json:"links"`
	LaunchedAt    *string        `json:"launchedAt"`
	FinishedAt    *string        `json:"finishedAt"`
	SettledAt     *string        `json:"settledAt"`
	RunStartedAt  *string        `json:"runStartedAt"`
	RunEndedAt    *string        `json:"runEndedAt"`
	CreatedAt     string         `json:"createdAt"`
	Events        []struct {
		FromState *string `json:"fromState"`
		ToState   string  `json:"toState"`
		Detail    string  `json:"detail"`
	} `json:"events"`
	Thread      *thread      `json:"thread"`
	Tree        *treeDetail  `json:"tree"`
	Attachments []attachment `json:"attachments"`
}

// execConfig is a task's canonical executionConfig.
type execConfig struct {
	Model      string            `json:"model"`
	Options    []json.RawMessage `json:"options"`
	Permission string            `json:"permission"`
	Mode       string            `json:"mode"`
}

// links is a session's destinations.
type links struct {
	Web     string `json:"web"`
	Native  string `json:"native"`
	Service string `json:"service"`
	Command string `json:"command"`
}

type attachment struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	SizeBytes int    `json:"sizeBytes"`
}

type thread struct {
	T3ThreadID  string  `json:"externalSessionId"`
	ProjectID   *int64  `json:"projectId"`
	Status      string  `json:"status"`
	Settled     bool    `json:"settled"`
	Source      string  `json:"source"`
	TaskID      *int64  `json:"taskId"`
	ProviderID  *string `json:"providerInstanceId"`
	Title       string  `json:"title"`
	T3ProjectID string  `json:"externalProjectId"`
	SettledAt   *string `json:"settledAt"`
}

func (e *env) project(root string) project {
	e.t.Helper()
	var p project
	e.must("POST", "/api/projects", map[string]any{"title": "P " + root}, 201, &p)
	ext := e.fake.AddProject(p.Title, root)
	e.must("PUT", fmt.Sprintf("/api/projects/%d/binding", p.ID), map[string]any{"connectionId": 1, "externalProjectId": ext.ID, "workspace": root}, 200, &p)
	p.T3ProjectID, p.WorkspaceRoot, p.Source = ext.ID, root, "app"
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
	var h api.Health
	e.must("GET", "/api/health", nil, 200, &h)
	if h.Status != "ok" || h.DB != "ok" || h.Service != "stage" || h.Identity != "stage" || h.APIVersion != 7 || h.Version == "" || len(h.Harnesses) != 1 || h.Harnesses[0].Kind != "t3" || !h.Harnesses[0].Connected || !h.Harnesses[0].Configured || h.Schema != sqlite.MaxVersion() || h.Engine != "sqlite" {
		t.Fatalf("health %+v", h)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/nope", nil))
	if rec.Code != 404 || rec.Header().Get("Stage-Api-Version") != "7" {
		t.Fatalf("404 with API version header %q", rec.Header().Get("Stage-Api-Version"))
	}
	// T3 offline: still a healthy Stage server, with T3 reported down.
	e.t3.offline.Store(true)
	e.must("GET", "/api/health", nil, 200, &h)
	if h.Status != "ok" || h.Harnesses[0].Connected {
		t.Fatalf("health with T3 offline %+v", h)
	}
	e.must("GET", "/api/nope", nil, 404, nil)
	e.must("GET", "/api/tasks/abc", nil, 400, nil)
	e.must("GET", "/api/tasks/999", nil, 404, nil)
	if h.PublicURL != nil {
		t.Fatalf("publicUrl without STAGE_PUBLIC_URL = %q, want null", *h.PublicURL)
	}
}

func TestHealthPublicURL(t *testing.T) {
	e := newEnv(t, func(d *api.Deps) { d.Config.PublicURL = "https://stage.example.ts.net/" })
	var h api.Health
	e.must("GET", "/api/health", nil, 200, &h)
	if h.PublicURL == nil || *h.PublicURL != "https://stage.example.ts.net" {
		t.Fatalf("publicUrl %v, want https://stage.example.ts.net", h.PublicURL)
	}
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
	})
	if tk.State != "backlog" || tk.T3ThreadID != nil || tk.Links != (links{}) || tk.ProjectTitle != p.Title {
		t.Fatalf("created %+v", tk)
	}
	if tk.Workspace["type"] != "root" {
		t.Fatalf("defaults %+v", tk)
	}
	// Legacy model input is accepted and reported only as executionConfig.
	if c := tk.Config; c == nil || c.Model != t3.ModelKey("claude", "claude-haiku-5-5") || len(c.Options) != 1 {
		t.Fatalf("executionConfig %+v", tk.Config)
	}
	var raw map[string]any
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &raw)
	for _, k := range []string{"t3WebUrl", "t3AppUrl", "t3DeepLink", "providerInstanceId", "model", "modelOptions", "runtimeMode", "interactionMode", "originThreadId", "originThreadTitle"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("task JSON still has legacy field %q", k)
		}
	}
	if _, ok := raw["originSessionTitle"]; !ok {
		t.Fatal("task JSON lacks originSessionTitle")
	}

	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if started.State != "running" || started.T3ThreadID == nil || started.LaunchAttempt != 1 || started.LaunchedAt == nil {
		t.Fatalf("started %+v", started)
	}
	threadID := *started.T3ThreadID
	if started.Links.Web != publicURL+"/fake-env/"+url.PathEscape(threadID) {
		t.Fatalf("web url %q", started.Links.Web)
	}
	if started.Links.Native != "t3code://threads/fake-env/"+url.PathEscape(threadID) {
		t.Fatalf("native link %q", started.Links.Native)
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
	e.must("GET", "/api/badge", nil, 200, &stats)
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
	if got.State != "done" || got.SettledAt != nil {
		t.Fatalf("settled %+v", got)
	}
	e.complete(tk.ID)
	var settledList struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?state=settled&settledFrom=2000-01-01", nil, 200, &settledList)
	if len(settledList.Tasks) != 0 {
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
	want := []string{"backlog", "launching", "running", "needs_input", "done"}
	if !slices.Equal(states, want) {
		t.Fatalf("events %v, want %v", states, want)
	}
	if got.Events[len(got.Events)-1].Detail != "execution observed done" {
		t.Fatalf("detail %q", got.Events[len(got.Events)-1].Detail)
	}
	if got.Thread == nil || got.Thread.Source != "app" || !got.Thread.Settled || got.Thread.SettledAt == nil || got.Thread.TaskID == nil || *got.Thread.TaskID != tk.ID {
		t.Fatalf("thread %+v", got.Thread)
	}
	if got.Links.Native != "t3code://threads/fake-env/"+url.PathEscape(got.Thread.T3ThreadID) {
		t.Fatalf("settled task native link %q", got.Links.Native)
	}
	// Accepted tasks remain observable.
	tasks, _ := e.store.PollableTasks(context.Background(), true)
	if len(tasks) != 1 {
		t.Fatalf("pollable %d", len(tasks))
	}
}

// A task saved with legacy interactionMode "plan", then given an
// executionConfig with the harness-default mode, launches without plan mode,
// so executionConfig must not keep reporting the stale "plan".
func TestExecutionConfigOverridesLegacyInteractionMode(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/modes")
	tk := e.task(p.ID, "A", map[string]any{"interactionMode": "plan"})
	got := e.getTask(tk.ID)
	if got.Config == nil || got.Config.Mode != "plan" {
		t.Fatalf("legacy mode synthesized as %+v", got.Config)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{
		"executionConfig": map[string]any{"model": `["claude","claude-haiku-5-5"]`, "options": []any{}, "permission": "", "mode": ""},
	}, 200, nil)
	got = e.getTask(tk.ID)
	if got.Config == nil || got.Config.Mode != "" || got.Config.Model != t3.ModelKey("claude", "claude-haiku-5-5") {
		t.Fatalf("config-backed mode %+v, want harness default", got.Config)
	}
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	d, err := e.fake.ReadThread(context.Background(), *started.T3ThreadID)
	if err != nil || d.InteractionMode != t3.InteractionDefault {
		t.Fatalf("launched mode %q err %v", d.InteractionMode, err)
	}
}

func TestTaskActions(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	tk := e.task(p.ID, "A", nil)
	// No thread yet.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", tk.ID), nil, 409, nil)
	// Backlog: everything editable; null clears; bad enum 400.
	var patched task
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{
		"prompt": "new", "providerInstanceId": "claude", "model": "claude-haiku-5-5",
		"workspace": map[string]any{"type": "worktree", "baseRef": "origin/main", "branch": nil},
	}, 200, &patched)
	if patched.Prompt != "new" || patched.Config == nil || patched.Config.Model != t3.ModelKey("claude", "claude-haiku-5-5") || patched.Workspace["type"] != "worktree" {
		t.Fatalf("patched %+v", patched)
	}
	if b, ok := patched.Workspace["branch"]; !ok || b != nil {
		t.Fatalf("worktree branch must be present and null: %v", patched.Workspace)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"providerInstanceId": nil, "model": nil}, 200, &patched)
	if patched.Config != nil && patched.Config.Model != "" {
		t.Fatalf("model not cleared: %+v", patched.Config)
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
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"tags": []string{"t"}}, 400, nil)
	e.must("GET", "/api/tasks?tag=t", nil, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"title": "B"}, 200, &patched)
	if patched.Title != "B" {
		t.Fatalf("title %q", patched.Title)
	}
	e.must("DELETE", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 409, nil)

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
	_ = e.fake.SetStatus(threadID, t3.StatusRunning)
	e.poll()
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("after follow-up %s", got.State)
	}

	var dup task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/duplicate", tk.ID), nil, 201, &dup)
	if dup.State != "backlog" || dup.Title != "B" || dup.ID == tk.ID || dup.T3ThreadID != nil || len(dup.Needs) != 0 {
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
	e.must("GET", "/api/tasks?state=running,backlog&q=b", nil, 200, &list)
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
	if got.State != "backlog" || got.LastError == nil || !strings.Contains(*got.LastError, "No thread was created") || got.LaunchAttempt != 1 {
		t.Fatalf("after failed launch %+v", got)
	}
	// Save & start: still created (201) but failed.
	created := e.task(p.ID, "B", map[string]any{"start": true})
	if created.State != "backlog" || created.LastError == nil {
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
	want := e.t3.launchRequestIDs()[0]
	if ids := e.t3.launchRequestIDs(); !slices.Equal(ids, []string{want, want}) || !validRequestID(a.ID, want) {
		t.Fatalf("request ids %v", ids)
	}

	// Lost twice: failed, and the next start reuses the attempt (T3 already
	// has the thread for that clientRequestId).
	b := e.task(p.ID, "B", nil)
	e.t3.lostLaunches.Store(2)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 502, nil)
	failed := e.getTask(b.ID)
	if failed.State != "launching" || failed.LastError == nil || failed.LaunchAttempt != 1 || failed.T3ThreadID != nil {
		t.Fatalf("after two lost responses %+v", failed)
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, &started)
	if started.State != "running" || started.LaunchAttempt != 1 {
		t.Fatalf("restart %+v", started)
	}
	ids := e.t3.launchRequestIDs()
	wantB := ids[2]
	if got := ids[2:]; !slices.Equal(got, []string{wantB, wantB, wantB}) || !validRequestID(b.ID, wantB) || wantB == want {
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
	if ids := e.t3.launchRequestIDs(); ids[len(ids)-1] == wantB || !validRequestID(b.ID, ids[len(ids)-1]) {
		t.Fatalf("new attempt reused clientRequestId %v", ids)
	}
}

func TestBindingChangeDoesNotRedirectUncertainLaunch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/original")
	pending := e.task(p.ID, "Pinned", nil)
	e.t3.lostLaunches.Store(2)
	endpoint := fmt.Sprintf("/api/tasks/%d/start", pending.ID)
	e.must("POST", endpoint, nil, 502, nil)
	replacement := e.fake.AddProject("Replacement", "/w/replacement")
	e.must("PUT", fmt.Sprintf("/api/projects/%d/binding", p.ID), map[string]any{"connectionId": 1, "externalProjectId": replacement.ID, "workspace": replacement.WorkspaceRoot}, 200, nil)
	// A moved original path is unsafe even with a valid replacement binding.
	if err := e.fake.SetProject(p.T3ProjectID, func(p *t3.Project) { p.WorkspaceRoot = "/w/moved" }); err != nil {
		t.Fatal(err)
	}
	e.must("POST", endpoint, nil, 409, nil)
	if len(e.t3.launchRequestIDs()) != 2 {
		t.Fatal("retried into a changed workspace")
	}
	if err := e.fake.SetProject(p.T3ProjectID, func(p *t3.Project) { p.WorkspaceRoot = "/w/original" }); err != nil {
		t.Fatal(err)
	}
	var started task
	e.must("POST", endpoint, nil, 200, &started)
	inputs := e.fake.Launched
	if len(inputs) != 3 || inputs[2].ProjectID != p.T3ProjectID || inputs[2].ClientRequestID != inputs[0].ClientRequestID || started.LaunchAttempt != 1 {
		t.Fatalf("redirected retry %+v", inputs)
	}
	if err := e.fake.SetStatus(*started.T3ThreadID, t3.StatusFailed); err != nil {
		t.Fatal(err)
	}
	e.poll()
	e.must("POST", endpoint, nil, 200, &started)
	if e.fake.Launched[3].ProjectID != replacement.ID || started.LaunchAttempt != 2 {
		t.Fatal("future attempt did not use new binding")
	}
}

// Stage never asks a harness to create a project (ADR 0007 decision 11):
// the registration route is gone.
func TestRegisterWorkspaceRouteRemoved(t *testing.T) {
	e := newEnv(t)
	e.must("POST", "/api/connections/t3/projects", map[string]any{"title": "P", "workspace": "/w"}, 404, nil)
}

func TestSaveAndStartWithoutBindingRetainsActionableBacklog(t *testing.T) {
	e := newEnv(t)
	var p project
	e.must("POST", "/api/projects", map[string]any{"title": "Offline"}, 201, &p)
	task := e.task(p.ID, "Plan", map[string]any{"start": true})
	if task.State != "backlog" || task.LaunchAttempt != 0 || task.LastError == nil || !strings.Contains(*task.LastError, "Bind or confirm") {
		t.Fatalf("silent launch refusal %+v", task)
	}
	if len(e.t3.launchRequestIDs()) != 0 {
		t.Fatal("unbound launch reached T3")
	}
}

// validRequestID: "task-<id>-<uuid>", unique per attempt and database.
func validRequestID(taskID int64, id string) bool {
	rest, ok := strings.CutPrefix(id, fmt.Sprintf("task-%d-", taskID))
	return ok && len(rest) == 36 && strings.Count(rest, "-") == 4
}

func TestLaunchClientRequestIDCollision(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	a := e.task(p.ID, "A", nil)
	other := "proj-elsewhere"
	e.t3.foreignProject.Store(&other)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", a.ID), nil, 502, nil)
	got := e.getTask(a.ID)
	if got.State != "launching" || got.T3ThreadID != nil || got.LastError == nil || !strings.Contains(*got.LastError, "conflicting") {
		t.Fatalf("collision adopted %+v", got)
	}
	// A conflicting reply never authorizes a fresh attempt.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", a.ID), nil, 502, nil)
	if ids := e.t3.launchRequestIDs(); len(ids) != 1 {
		t.Fatalf("conflicting launch replayed: %v", ids)
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
	e.poll()
	if got := e.getTask(a.ID); got.State != "backlog" || got.LastError == nil {
		t.Fatalf("after recover %+v", got)
	}
	// The killed launch may have reached T3: the next start reuses its
	// attempt and clientRequestId.
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", a.ID), nil, 200, &started)
	ids := e.t3.launchRequestIDs()
	if started.State != "running" || started.LaunchAttempt != 2 || len(ids) != 1 || !validRequestID(a.ID, ids[0]) {
		t.Fatalf("restart after recover %+v ids %v", started, ids)
	}
	var reqID string
	if err := testDB.Pool.QueryRowContext(context.Background(), `SELECT launch_request_id FROM tasks WHERE id = $1`, a.ID).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	if reqID != ids[0] {
		t.Fatalf("launch_request_id %q, launched with %q", reqID, ids[0])
	}
}

func TestProjects(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	ext := e.fake.AddProject("Discovered", "/w/elsewhere")
	if _, err := e.sync.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Sync imports the unbound live T3 project; the bound one is untouched.
	ps, err := e.store.ListProjects(context.Background(), "all")
	if err != nil || len(ps) != 2 {
		t.Fatalf("import: %+v %v", ps, err)
	}
	imported, err := e.store.BoundProject(context.Background(), db.ProjectBinding{ConnectionID: 1, ExternalProjectID: ext.ID, Workspace: ext.WorkspaceRoot})
	if err != nil || imported.Title != "Discovered" || imported.Origin == nil || imported.Bindings[0].Origin != db.BindingImported {
		t.Fatalf("imported %+v %v", imported, err)
	}
	var listed struct {
		Imports []db.ImportSummary `json:"imports"`
	}
	e.must("GET", "/api/projects", nil, 200, &listed)
	if len(listed.Imports) != 1 || listed.Imports[0].Count != 1 || listed.Imports[0].Harness != "t3" {
		t.Fatalf("imports %+v", listed.Imports)
	}
	at := time.Now()
	if err := e.fake.SetProject(p.T3ProjectID, func(x *t3.Project) { x.Title = "External rename"; x.ArchivedAt = &at; x.WorkspaceRoot = "/moved" }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sync.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetProject(context.Background(), p.ID)
	if err != nil || got.Title != p.Title || got.ArchivedAt != nil || got.Bindings[0].Status != "changed" {
		t.Fatalf("metadata overwritten: %+v %v", got, err)
	}
	e.must("PUT", fmt.Sprintf("/api/projects/%d/binding", p.ID), map[string]any{"connectionId": 1, "externalProjectId": p.T3ProjectID, "workspace": "/moved"}, 200, nil)
	e.must("PATCH", fmt.Sprintf("/api/projects/%d", p.ID), map[string]any{"title": "Stage title", "archived": true}, 200, nil)
	if err := e.fake.SetProject(ext.ID, func(x *t3.Project) { x.DeletedAt = &at }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sync.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = e.store.GetProject(context.Background(), p.ID)
	if got.Title != "Stage title" || got.ArchivedAt == nil {
		t.Fatalf("Stage metadata lost: %+v", got)
	}
	// The discovered project follows its deleted T3 project into archive.
	if got, _ = e.store.GetProject(context.Background(), imported.ID); got.ArchivedAt == nil {
		t.Fatalf("discovered project not archived: %+v", got)
	}
	e.must("POST", "/api/projects", map[string]any{"title": ""}, 400, nil)
	e.must("GET", "/api/projects?archived=maybe", nil, 400, nil)
}

func TestReleaseRunLoopProcessesKicks(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	a := e.task(p.ID, "s1", map[string]any{"start": true})
	b := e.task(p.ID, "s2", map[string]any{"needs": []int64{a.ID}, "startMode": "auto"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.runner.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*a.T3ThreadID)
	e.poll()
	e.complete(a.ID)
	e.runner.Kick() // user completion wakes release
	for range 200 {
		if got := e.getTask(b.ID); got.State == "running" {
			return
		}
		select {
		case <-ctx.Done():
		case <-timeAfter():
		}
	}
	t.Fatalf("runner loop did not launch s2: %+v", e.getTask(b.ID))
}

func TestCancelledTaskReopens(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one")
	ctx := context.Background()

	// A cancelled turn (e.g. denied permission) and the thread runs on.
	a := e.task(p.ID, "a", map[string]any{"start": true})
	threadA := *a.T3ThreadID
	_ = e.fake.SetStatus(threadA, t3.StatusCancelled)
	e.poll()
	if got := e.getTask(a.ID); got.State != "cancelled" {
		t.Fatalf("after cancel %s", got.State)
	}
	_ = e.fake.SetStatus(threadA, t3.StatusRunning)
	e.poll()
	if got := e.getTask(a.ID); got.State != "running" {
		t.Fatalf("cancelled task did not reopen: %s", got.State)
	}

	// Long-cancelled task resumed in T3: full sync sees the thread active
	// in the mirror and the next poll reopens it.
	_ = e.fake.SetStatus(threadA, t3.StatusCancelled)
	e.poll()
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-1 hour') WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	_ = e.fake.SetStatus(threadA, t3.StatusRunning)
	e.poll()
	if got := e.getTask(a.ID); got.State != "cancelled" {
		t.Fatalf("stale cancelled task polled before the mirror changed: %s", got.State)
	}
	if _, err := e.sync.FullSync(ctx); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if got := e.getTask(a.ID); got.State != "running" {
		t.Fatalf("resumed thread did not reopen stale cancelled task: %s", got.State)
	}

	// Cancelled with a dependent: the dependent stays blocked; the task keeps
	// following T3 and the dependent starts once it settles.
	b := e.task(p.ID, "b", map[string]any{"start": true})
	c := e.task(p.ID, "c", map[string]any{"needs": []int64{b.ID}, "startMode": "auto"})
	_ = e.fake.SetStatus(*b.T3ThreadID, t3.StatusCancelled)
	e.poll()
	e.reconcile()
	if got := e.getTask(c.ID); got.State != "backlog" {
		t.Fatalf("dependent of cancelled task %s", got.State)
	}
	_ = e.fake.SetStatus(*b.T3ThreadID, t3.StatusRunning)
	if _, err := e.sync.FullSync(ctx); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if got := e.getTask(b.ID); got.State != "running" {
		t.Fatalf("cancelled task with a dependent did not reopen: %s", got.State)
	}
	_ = e.fake.SetStatus(*b.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*b.T3ThreadID)
	e.poll()
	e.complete(b.ID)
	e.reconcile()
	if got := e.getTask(b.ID); got.State != "done" {
		t.Fatalf("need %s", got.State)
	}
	if got := e.getTask(c.ID); got.State != "running" {
		t.Fatalf("dependent after settle %s", got.State)
	}
}

func TestTargetedSyncProjectsAndTasks(t *testing.T) {
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
	_ = e.task(p.ID, "app task", map[string]any{"start": true})

	var res struct {
		Projects   int    `json:"projects"`
		Threads    int    `json:"threads"`
		FinishedAt string `json:"finishedAt"`
	}
	e.must("POST", "/api/sync", nil, 200, &res)
	if res.Projects != 2 || res.Threads != 1 || res.FinishedAt == "" {
		t.Fatalf("sync %+v", res)
	}
	var reply struct {
		Details struct {
			LastFullSyncAt *string `json:"lastFullSyncAt"`
			Connected      bool    `json:"connected"`
			EnvironmentID  *string `json:"environmentId"`
			PublicURL      string  `json:"publicUrl"`
		} `json:"details"`
	}
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &reply)
	st := reply.Details
	if st.LastFullSyncAt == nil || !st.Connected || st.EnvironmentID == nil || st.PublicURL != "http://127.0.0.1:3773" {
		t.Fatalf("status %+v", st)
	}
	var projects struct {
		Projects []project `json:"projects"`
	}
	e.must("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) != 2 {
		t.Fatalf("sync import: %+v", projects.Projects)
	}
	// Sync imported and bound the T3 project.
	if _, err := e.store.BoundProject(ctx, db.ProjectBinding{ConnectionID: 1, ExternalProjectID: ext.ID, Workspace: ext.WorkspaceRoot}); err != nil {
		t.Fatal(err)
	}

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
	e.must("POST", "/api/harnesses/t3/actions/pair", map[string]any{"code": " "}, 400, nil)
	e.must("POST", "/api/harnesses/t3/actions/pair", map[string]any{"code": "ABCD-1234"}, 200, nil)
}

func TestSnippets(t *testing.T) {
	e := newEnv(t)
	type snippet struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Body      string `json:"body"`
		ProjectID *int64 `json:"projectId"`
	}
	proj := e.project("/w/snip")
	var a snippet
	e.must("POST", "/api/snippets", map[string]any{"name": " Quick ", "body": "do the thing"}, 201, &a)
	if a.Name != "Quick" || a.Body != "do the thing" || a.ProjectID != nil {
		t.Fatalf("snippet %+v", a)
	}
	e.must("POST", "/api/snippets", map[string]any{"name": "Quick", "body": "x"}, 409, nil)
	e.must("POST", "/api/snippets", map[string]any{}, 400, nil)
	e.must("POST", "/api/snippets", map[string]any{"name": "x"}, 400, nil)
	e.must("POST", "/api/snippets", map[string]any{"name": "x", "body": "  "}, 400, nil)
	e.must("POST", "/api/snippets", map[string]any{"name": "x", "body": "b", "projectId": 999999}, 400, nil)
	var b snippet
	e.must("POST", "/api/snippets", map[string]any{"name": "Another", "body": "b", "projectId": proj.ID}, 201, &b)
	if b.ProjectID == nil || *b.ProjectID != proj.ID {
		t.Fatalf("scoped %+v", b)
	}
	e.must("PATCH", fmt.Sprintf("/api/snippets/%d", b.ID), map[string]any{"name": "Quick"}, 409, nil)
	e.must("PATCH", fmt.Sprintf("/api/snippets/%d", b.ID), map[string]any{"body": ""}, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/snippets/%d", b.ID), map[string]any{"projectId": 999999}, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/snippets/%d", a.ID), map[string]any{"body": "hi", "projectId": proj.ID}, 200, &a)
	if a.Body != "hi" || a.ProjectID == nil || a.Name != "Quick" {
		t.Fatalf("patched %+v", a)
	}
	e.must("PATCH", fmt.Sprintf("/api/snippets/%d", a.ID), map[string]any{"projectId": nil}, 200, &a)
	if a.ProjectID != nil {
		t.Fatalf("unscoped %+v", a)
	}
	var list struct {
		Snippets []snippet `json:"snippets"`
	}
	e.must("GET", "/api/snippets", nil, 200, &list)
	// Global first, then project-scoped.
	if len(list.Snippets) != 2 || list.Snippets[0].Name != "Quick" || list.Snippets[1].Name != "Another" {
		t.Fatalf("list %+v", list)
	}
	e.must("GET", fmt.Sprintf("/api/snippets/%d", a.ID), nil, 200, nil)
	e.must("DELETE", fmt.Sprintf("/api/snippets/%d", a.ID), nil, 204, nil)
	e.must("DELETE", fmt.Sprintf("/api/snippets/%d", a.ID), nil, 404, nil)
	e.must("GET", fmt.Sprintf("/api/snippets/%d", a.ID), nil, 404, nil)
	e.must("GET", "/api/templates", nil, 404, nil)
}

func timeAfter() <-chan time.Time { return time.After(10 * time.Millisecond) }

func TestPreferences(t *testing.T) {
	e := newEnv(t)
	type prefs struct {
		DefaultConnectionID *int64   `json:"defaultConnectionId"`
		NotifyEvents        []string `json:"notifyEvents"`
	}
	var p prefs
	e.must("GET", "/api/preferences", nil, 200, &p)
	if p.DefaultConnectionID != nil {
		t.Fatalf("fresh %+v", p)
	}
	t3ID := e.store.LegacyT3ID()

	// Default connection: set, reject an unknown one, clear. The old pin route is gone.
	e.must("PUT", "/api/preferences/default-connection", map[string]any{"connectionId": t3ID}, 200, &p)
	if p.DefaultConnectionID == nil || *p.DefaultConnectionID != t3ID {
		t.Fatalf("default %+v", p)
	}
	e.must("PUT", "/api/preferences/default-connection", map[string]any{"connectionId": 999}, 400, nil)
	e.must("PUT", "/api/preferences/default-connection", map[string]any{"connectionId": nil}, 200, &p)
	if p.DefaultConnectionID != nil {
		t.Fatalf("cleared default %+v", p)
	}
	e.must("PUT", "/api/preferences/pinned", map[string]any{"providerInstanceId": "claude", "model": "claude-haiku-5-5"}, 404, nil)

	// Creating a task no longer records anything in preferences.
	pr := e.project("/w/prefs")
	e.task(pr.ID, "A", map[string]any{"providerInstanceId": "codex", "model": "gpt-6", "runtimeMode": "auto"})
	e.must("GET", "/api/preferences", nil, 200, &p)
	if p.DefaultConnectionID != nil {
		t.Fatalf("after create %+v", p)
	}

	// Per-connection pin: checked against the connection, permission and mode refused, cleared by null.
	type conn struct {
		Connection db.HarnessConnection `json:"connection"`
	}
	var c conn
	key := t3.ModelKey("claude", "claude-haiku-5-5")
	path := fmt.Sprintf("/api/connections/%d/new-task-config", t3ID)
	e.must("PUT", path, map[string]any{"config": map[string]any{"model": key, "options": []map[string]any{{"id": "effort", "value": "high"}}}}, 200, &c)
	if nc := c.Connection.NewTaskConfig; nc == nil || nc.Model != key || len(nc.Options) != 1 || nc.Permission != "" || nc.Mode != "" {
		t.Fatalf("pinned %+v", c.Connection.NewTaskConfig)
	}
	var list struct {
		Connections []db.HarnessConnection `json:"connections"`
	}
	e.must("GET", "/api/connections", nil, 200, &list)
	if i := slices.IndexFunc(list.Connections, func(x db.HarnessConnection) bool { return x.ID == t3ID }); i < 0 || list.Connections[i].NewTaskConfig == nil {
		t.Fatalf("listing %+v", list.Connections)
	}
	e.must("PUT", path, map[string]any{"config": map[string]any{"model": t3.ModelKey("claude", "nope")}}, 400, nil)
	e.must("PUT", path, map[string]any{"config": map[string]any{"model": key, "options": []map[string]any{{"id": "effort", "value": "extreme"}}}}, 400, nil)
	e.must("PUT", path, map[string]any{"config": map[string]any{"model": key, "mode": "plan"}}, 400, nil)
	e.must("PUT", path, map[string]any{"config": map[string]any{"model": key, "permission": "auto"}}, 400, nil)
	e.must("PUT", "/api/connections/999/new-task-config", map[string]any{"config": nil}, 400, nil)
	e.must("PUT", path, map[string]any{"config": nil}, 200, &c)
	if c.Connection.NewTaskConfig != nil {
		t.Fatalf("unpinned %+v", c.Connection.NewTaskConfig)
	}

	// Notification filter: default all; set; validate.
	if strings.Join(p.NotifyEvents, ",") != "needs_input,failed,ready,done,settled" {
		t.Fatalf("default notifyEvents %v", p.NotifyEvents)
	}
	e.must("PUT", "/api/preferences/notifications", map[string]any{"events": []string{"settled", "ready", "done", "needs_input"}}, 200, &p)
	if strings.Join(p.NotifyEvents, ",") != "needs_input,ready,done,settled" {
		t.Fatalf("notifyEvents %+v", p)
	}
	e.must("PUT", "/api/preferences/notifications", map[string]any{"events": []string{}}, 200, &p)
	if p.NotifyEvents == nil || len(p.NotifyEvents) != 0 {
		t.Fatalf("all off %+v", p.NotifyEvents)
	}
	e.must("PUT", "/api/preferences/notifications", map[string]any{"events": []string{"running"}}, 400, nil)
	e.must("PUT", "/api/preferences/notifications", map[string]any{}, 400, nil)
	e.must("GET", "/api/preferences", nil, 200, &p)
	if len(p.NotifyEvents) != 0 {
		t.Fatalf("after invalid %v", p.NotifyEvents)
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

func TestPushSubscriptionsAndConfig(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var cfg struct {
		PublicKey string `json:"publicKey"`
	}
	e.must("GET", "/api/push/config", nil, 200, &cfg)
	key, err := base64.RawURLEncoding.DecodeString(cfg.PublicKey)
	if err != nil || len(key) != 65 || key[0] != 4 {
		t.Fatalf("publicKey %q: not an uncompressed P-256 point (%v)", cfg.PublicKey, err)
	}
	first := cfg.PublicKey
	e.must("GET", "/api/push/config", nil, 200, &cfg)
	if cfg.PublicKey != first {
		t.Fatal("VAPID key changed between calls")
	}

	const endpoint = "https://push.example.com/send/abc"
	sub := func(endpoint, p256dh, auth string) map[string]any {
		return map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": p256dh, "auth": auth}}
	}
	e.must("POST", "/api/push/subscriptions", sub("", "k", "a"), 400, nil)
	e.must("POST", "/api/push/subscriptions", sub("http://push.example.com/x", "k", "a"), 400, nil)
	e.must("POST", "/api/push/subscriptions", sub(endpoint, "", "a"), 400, nil)
	e.must("POST", "/api/push/subscriptions", sub(endpoint, "k", ""), 400, nil)

	post := func(body any, ua string) int {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/push/subscriptions", bytes.NewReader(b))
		req.Header.Set("User-Agent", ua)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post(sub(endpoint, "k1", "a1"), "Firefox"); code != 204 {
		t.Fatalf("subscribe: %d", code)
	}
	if code := post(sub(endpoint, "k2", "a2"), "Safari"); code != 204 {
		t.Fatalf("resubscribe: %d", code)
	}
	subs, err := e.store.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].P256dh != "k2" || subs[0].Auth != "a2" || subs[0].UserAgent != "Safari" {
		t.Fatalf("upsert by endpoint: %+v", subs)
	}

	e.must("POST", "/api/push/test", map[string]string{"endpoint": "https://push.example.com/unknown"}, 404, nil)
	e.must("POST", "/api/push/test", map[string]string{}, 400, nil)

	e.must("DELETE", "/api/push/subscriptions", map[string]string{}, 400, nil)
	e.must("DELETE", "/api/push/subscriptions", map[string]string{"endpoint": endpoint}, 204, nil)
	e.must("DELETE", "/api/push/subscriptions", map[string]string{"endpoint": endpoint}, 204, nil)
	if subs, _ := e.store.ListPushSubscriptions(ctx); len(subs) != 0 {
		t.Fatalf("after delete: %+v", subs)
	}
}

func TestBadgeAndNeedsInputThreads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/badge")

	// Task in needs_input; its thread also observed needs_input (task-owned: not counted twice).
	tk := e.task(p.ID, "needs me", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if err := e.fake.SetThread(*started.T3ThreadID, func(d *t3.ThreadDetail) { d.PendingRequestCount = 1 }); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if got := e.getTask(tk.ID); got.State != "needs_input" {
		t.Fatalf("task state %s", got.State)
	}

	now := time.Now().UTC()
	if err := e.store.UpsertThreads(ctx, t3.Observations([]t3.Thread{
		{ThreadID: "ext-wait", ProjectID: p.T3ProjectID, Title: "external", Status: t3.StatusWaiting, CreatedAt: now},
		{ThreadID: "ext-sub", ProjectID: p.T3ProjectID, Title: "sub", Status: t3.StatusWaiting, CreatedAt: now,
			ParentThreadID: "ext-wait", RelationshipToParent: "subagent"},
		{ThreadID: "ext-fork", ProjectID: p.T3ProjectID, Title: "fork", Status: t3.StatusWaiting, CreatedAt: now.Add(-time.Minute),
			ParentThreadID: "ext-wait", RelationshipToParent: "fork"},
		{ThreadID: "ext-settled", ProjectID: p.T3ProjectID, Title: "settled", Status: t3.StatusWaiting, Settled: true, CreatedAt: now},
		{ThreadID: "ext-running", ProjectID: p.T3ProjectID, Title: "running", Status: t3.StatusRunning, CreatedAt: now},
	})); err != nil {
		t.Fatal(err)
	}
	for id, st := range map[string]db.TaskState{
		"ext-wait": db.StateNeedsInput, "ext-sub": db.StateNeedsInput, "ext-fork": db.StateNeedsInput, "ext-settled": db.StateNeedsInput,
		"ext-running": db.StateRunning, *started.T3ThreadID: db.StateNeedsInput,
	} {
		o, err := e.store.Observation(ctx, db.SessionRef{ConnectionID: 1, ExternalSessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		o.State = string(st)
		o.Active = st == db.StateNeedsInput || st == db.StateRunning
		if err = e.store.PutObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
		if err := e.store.SetObservedState(ctx, db.SessionRef{ConnectionID: e.store.LegacyT3ID(), ExternalSessionID: id}, st); err != nil {
			t.Fatal(err)
		}
	}

	var badge struct {
		NeedsInput int `json:"needsInput"`
	}
	e.must("GET", "/api/badge", nil, 200, &badge)
	if badge.NeedsInput != 4 {
		t.Fatalf("badge %d, want 4 (settlement does not hide observed activity)", badge.NeedsInput)
	}

}

func TestNotificationsInbox(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	thread := "ext-1"
	var ids []int64
	for i := range 3 {
		id, err := e.store.InsertNotification(ctx, db.NewNotification{
			Event: db.StateDone, T3ThreadID: &thread, Title: fmt.Sprintf("n%d", i), URL: "/history?q=x",
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	type page struct {
		Notifications []struct {
			ID     int64   `json:"id"`
			Event  string  `json:"event"`
			Title  string  `json:"title"`
			ReadAt *string `json:"readAt"`
			Links  links   `json:"links"`
		} `json:"notifications"`
		Unread     int    `json:"unread"`
		NextBefore *int64 `json:"nextBefore"`
	}
	var pg page
	e.must("GET", "/api/notifications?limit=2", nil, 200, &pg)
	if len(pg.Notifications) != 2 || pg.Notifications[0].ID != ids[2] || pg.Unread != 3 ||
		pg.NextBefore == nil || *pg.NextBefore != ids[1] {
		t.Fatalf("first page %+v", pg)
	}
	if l := pg.Notifications[0].Links; l.Native != t3.AppThreadURL(t3.FakeEnvironmentID, thread) || l.Web == "" {
		t.Fatalf("notification links %+v", l)
	}
	e.must("GET", fmt.Sprintf("/api/notifications?limit=2&before=%d", *pg.NextBefore), nil, 200, &pg)
	if len(pg.Notifications) != 1 || pg.Notifications[0].ID != ids[0] || pg.NextBefore != nil {
		t.Fatalf("last page %+v", pg)
	}

	e.must("POST", "/api/notifications/read", map[string]any{}, 400, nil)
	e.must("POST", "/api/notifications/read", map[string]any{"upTo": ids[1]}, 204, nil)
	e.must("GET", "/api/notifications", nil, 200, &pg)
	if pg.Unread != 1 || pg.Notifications[0].ReadAt != nil || pg.Notifications[1].ReadAt == nil {
		t.Fatalf("after read %+v", pg)
	}
	e.must("GET", "/api/notifications?limit=0", nil, 400, nil)
	e.must("GET", "/api/notifications?before=x", nil, 400, nil)
}

func TestNotificationGroupRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, thread := range []string{"ext-1", "ext-2"} {
		if _, err := e.store.InsertNotification(ctx, db.NewNotification{
			Event: db.StateDone, T3ThreadID: &thread, Title: thread, URL: "/sessions/" + thread,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var tags struct {
		Tags []string `json:"tags"`
	}
	e.must("GET", "/api/notifications/unread-tags", nil, 200, &tags)
	if fmt.Sprint(tags.Tags) != "[thread-1:ext-1 thread-1:ext-2]" {
		t.Fatalf("unread tags %v", tags.Tags)
	}

	e.must("POST", "/api/notifications/read", map[string]any{"upTo": 1, "groups": []string{"thread:1:ext-1"}}, 400, nil)
	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{}}, 400, nil)
	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{" "}}, 400, nil)
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("thread:1:x%d", i)
	}
	e.must("POST", "/api/notifications/read", map[string]any{"groups": many}, 400, nil)

	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{"thread:1:ext-1"}}, 204, nil)
	e.must("GET", "/api/notifications/unread-tags", nil, 200, &tags)
	if fmt.Sprint(tags.Tags) != "[thread-1:ext-2]" {
		t.Fatalf("unread tags after group read %v", tags.Tags)
	}
}

func TestNotificationGroups(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	threads := []string{"ext-1", "ext-2"}
	var latest int64
	for _, thread := range []string{threads[0], threads[1], threads[0]} {
		id, err := e.store.InsertNotification(ctx, db.NewNotification{
			Event: db.StateDone, T3ThreadID: &thread, Title: "Same title", URL: "/history?q=Same",
		})
		if err != nil {
			t.Fatal(err)
		}
		latest = id
	}
	type group struct {
		db.NotificationGroup
		Links links `json:"links"`
	}
	var pg struct {
		Groups     []group `json:"groups"`
		Unread     int     `json:"unread"`
		LatestID   int64   `json:"latestId"`
		NextCursor *string `json:"nextCursor"`
	}
	e.must("GET", "/api/notifications/groups?limit=1", nil, 200, &pg)
	if len(pg.Groups) != 1 || pg.Groups[0].Key != "thread:1:ext-1" || pg.Groups[0].EventCount != 2 ||
		pg.Groups[0].UnreadCount != 2 || pg.LatestID != latest || pg.Unread != 3 || pg.NextCursor == nil {
		t.Fatalf("first group page: %+v", pg)
	}
	if l := pg.Groups[0].Links; l.Native != t3.AppThreadURL(t3.FakeEnvironmentID, threads[0]) || l.Web == "" {
		t.Fatalf("group links: %+v", l)
	}
	e.must("GET", "/api/notifications/groups?limit=1&cursor="+url.QueryEscape(*pg.NextCursor), nil, 200, &pg)
	if len(pg.Groups) != 1 || pg.Groups[0].Key != "thread:1:ext-2" || pg.NextCursor != nil {
		t.Fatalf("next group page: %+v", pg)
	}
	var history struct {
		Notifications []db.Notification `json:"notifications"`
		NextBefore    *int64            `json:"nextBefore"`
	}
	e.must("GET", "/api/notifications?group=thread%3A1%3Aext-1&limit=1", nil, 200, &history)
	if len(history.Notifications) != 1 || history.Notifications[0].ID != latest || history.NextBefore == nil {
		t.Fatalf("first history page: %+v", history)
	}
	e.must("GET", fmt.Sprintf("/api/notifications?group=thread%%3A1%%3Aext-1&limit=1&before=%d", *history.NextBefore), nil, 200, &history)
	if len(history.Notifications) != 1 || *history.Notifications[0].T3ThreadID != "ext-1" || history.NextBefore != nil {
		t.Fatalf("next history page: %+v", history)
	}
	for _, query := range []string{"limit=0", "cursor=x", "cursor=recent:0", "cursor=waiting:no", "cursor=other:1"} {
		e.must("GET", "/api/notifications/groups?"+query, nil, 400, nil)
	}
}
