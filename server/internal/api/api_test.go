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
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/release"
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
	failCatalog   atomic.Bool
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

func (f *flakyT3) Capabilities(ctx context.Context) (t3.Catalog, error) {
	if f.failCatalog.Load() {
		return t3.Catalog{}, errors.New("model catalog unavailable")
	}
	return f.Fake.Capabilities(ctx)
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

func TestStopWhileModelCatalogIsUnavailable(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/stop")
	a := e.task(p.ID, "running", map[string]any{"start": true})
	e.t3.failCatalog.Store(true)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", a.ID), nil, 200, &a)
	if a.State != "interrupted" || len(e.fake.Interrupted) != 1 || a.ExternalSessionID == nil || e.fake.Interrupted[0] != *a.ExternalSessionID {
		t.Fatalf("Stop did not interrupt the task's session: %+v, %v", a, e.fake.Interrupted)
	}
	page, err := e.store.ReadJournal(t.Context(), db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "events"})
	if err != nil || !slices.ContainsFunc(page.Entries, func(entry db.JournalEntry) bool {
		return entry.EventType == "task_stopped" && entry.Actor.Kind == "personal" && entry.Actor.UserID == 0
	}) {
		t.Fatalf("manual stop attribution: %+v, %v", page.Entries, err)
	}
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
	engine *engine.Engine
	runner *release.Runner
	store  *db.Store
}

const publicURL = "http://localhost:3773"

func newEnv(t *testing.T, opts ...func(*api.Deps)) *env {
	return newEnvWrapped(t, nil, opts...)
}

// newEnvWrapped is newEnv with the T3 adapter wrapped (nil: as is).
func newEnvWrapped(t *testing.T, wrap func(*t3.Adapter) execution.Adapter, opts ...func(*api.Deps)) *env {
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	// Tests complete tasks explicitly; inference tests turn auto-complete on.
	if _, err := store.SetAutoComplete(context.Background(), store.ActiveID(db.LocalMachine, "t3"), false); err != nil {
		t.Fatal(err)
	}
	fake := t3.NewFake()
	client := &flakyT3{Fake: fake}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var adapter execution.Adapter = t3.NewAdapter(client)
	if wrap != nil {
		adapter = wrap(t3.NewAdapter(client))
	}
	svc := engine.NewWithAdapters(store, testHarnesses, map[int64]execution.Adapter{dbtest.T3Connection: adapter}, log)
	runner := release.NewRunner(store, svc.Coordinator, log)
	svc.Coordinator.OnTransition = runner.OnTransition
	push := notify.New(store, "https://stage.test", log)
	runner.OnReady = push.OnReady
	deps := api.Deps{
		Config: config.Config{},
		Store:  store, Engine: svc, Push: push, Logger: log, Harnesses: testHarnesses,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	if _, ok := deps.Connectors.Get(db.LocalMachine, t3.Kind); !ok {
		withT3Setup(&deps, t3setup.Options{Client: client, Fake: true})
	}
	h := api.NewRouter(deps)
	return &env{t: t, h: h, fake: fake, t3: client, engine: svc, runner: runner, store: store}
}

// withT3Setup installs a T3 connector backed by the local machine, keeping
// an OpenCode connector already set.
func withT3Setup(d *api.Deps, opts t3setup.Options) {
	ports := machine.NewStorePorts(d.Store.OnMachine(db.LocalMachine), harness.MustRegistry(t3.Descriptor))
	opts.Settings, opts.Secrets, opts.Identity = ports.Settings(), ports.Secrets(), ports
	opts.Logger = d.Logger
	connectors := map[string]harness.Connector{t3.Kind: t3setup.NewConnector(t3setup.New(opts))}
	if c, ok := d.Connectors.Get(db.LocalMachine, opencode.Kind); ok {
		connectors[opencode.Kind] = c
	}
	d.Connectors = mustConnectors(connectors)
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
	if err := e.engine.Observe.PollActive(context.Background()); err != nil {
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
	ID            int64               `json:"id"`
	T3ProjectID   string              `json:"externalProjectId"`
	Title         string              `json:"title"`
	WorkspaceRoot string              `json:"workspaceRoot"`
	Source        string              `json:"source"`
	ArchivedAt    *string             `json:"archivedAt"`
	Bindings      []db.ProjectBinding `json:"bindings"`
}

type task struct {
	ID                int64       `json:"id"`
	StateRevision     int64       `json:"stateRevision"`
	ProjectID         int64       `json:"projectId"`
	ProjectTitle      string      `json:"projectTitle"`
	Title             string      `json:"title"`
	Prompt            string      `json:"prompt"`
	Config            *execConfig `json:"executionConfig"`
	State             string      `json:"state"`
	ExternalSessionID *string     `json:"externalSessionId"`
	Workspace         *struct {
		BindingID int64  `json:"bindingId"`
		Path      string `json:"path"`
		Source    string `json:"source"`
	} `json:"workspace"`
	TreeID        *int64  `json:"treeId"`
	Needs         []int64 `json:"needs"`
	Dependents    []int64 `json:"dependents"`
	Readiness     *string `json:"readiness"`
	StartMode     *string `json:"startMode"`
	EffStartMode  *string `json:"effectiveStartMode"`
	Adopted       bool    `json:"adopted"`
	LaunchAttempt int     `json:"launchAttempt"`
	LastError     *string `json:"lastError"`
	Links         links   `json:"links"`
	LaunchedAt    *string `json:"launchedAt"`
	FinishedAt    *string `json:"finishedAt"`
	CompletedAt   *string `json:"completedAt"`
	RunStartedAt  *string `json:"runStartedAt"`
	RunEndedAt    *string `json:"runEndedAt"`
	CreatedAt     string  `json:"createdAt"`
	Events        []struct {
		FromState *string `json:"fromState"`
		ToState   string  `json:"toState"`
		Detail    string  `json:"detail"`
	} `json:"events"`
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

func (p project) workspaceID() string {
	return db.WorkspaceID(dbtest.T3Connection, p.T3ProjectID, p.WorkspaceRoot)
}

func (e *env) project(root string) project {
	e.t.Helper()
	title := "P " + root
	ext := e.fake.AddProject(title, root)
	row, err := e.store.InsertProject(e.t.Context(), db.Project{
		Title:    title,
		Bindings: []db.ProjectBinding{{ConnectionID: dbtest.T3Connection, ExternalProjectID: ext.ID, Workspace: root}},
	})
	row = mustValue(e.t, row, err)
	e.must("GET", fmt.Sprintf("/api/connections/%d/workspaces", dbtest.T3Connection), nil, 200, nil)
	return project{ID: row.ID, Title: title, T3ProjectID: ext.ID, WorkspaceRoot: root, Source: "app", Bindings: row.Bindings}
}

func (e *env) taskBody(projectID int64, title string, extra map[string]any) map[string]any {
	e.t.Helper()
	p, err := e.store.GetProject(e.t.Context(), projectID)
	p = mustValue(e.t, p, err)
	if len(p.Bindings) != 1 {
		e.t.Fatalf("task fixture needs a single workspace: %+v", p)
	}
	b := p.Bindings[0]
	body := map[string]any{
		"projectId": projectID, "connectionId": b.ConnectionID, "workspaceId": db.WorkspaceID(b.ConnectionID, b.ExternalProjectID, b.Workspace),
		"title": title, "prompt": "reply OK",
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func (e *env) task(projectID int64, title string, extra map[string]any) task {
	e.t.Helper()
	var out task
	e.must("POST", "/api/tasks", e.taskBody(projectID, title, extra), 201, &out)
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
	if h.Status != "ok" || h.DB != "ok" || h.Service != "stage" || h.Identity != "stage" || h.APIVersion != 9 || h.Version == "" || len(h.Harnesses) != 1 || h.Harnesses[0].Kind != "t3" || !h.Harnesses[0].Connected || !h.Harnesses[0].Configured || h.Schema != sqlite.MaxVersion() || h.Engine != "sqlite" {
		t.Fatalf("health %+v", h)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/nope", nil))
	if rec.Code != 404 || rec.Header().Get("Stage-Api-Version") != "9" {
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
	tk := e.task(p.ID, "Say OK", map[string]any{"executionConfig": map[string]any{
		"model": t3.ModelKey("claude", "claude-haiku-5-5"), "options": []map[string]any{{"id": "effort", "value": "low"}},
	}})
	if tk.State != "backlog" || tk.ExternalSessionID != nil || tk.Links != (links{}) || tk.ProjectTitle != p.Title {
		t.Fatalf("created %+v", tk)
	}
	if c := tk.Config; c == nil || c.Model != t3.ModelKey("claude", "claude-haiku-5-5") || len(c.Options) != 1 {
		t.Fatalf("executionConfig %+v", tk.Config)
	}
	var raw map[string]any
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &raw)
	for _, k := range []string{"t3WebUrl", "t3AppUrl", "t3DeepLink", "providerInstanceId", "model", "modelOptions", "runtimeMode", "interactionMode", "originThreadId", "originThreadTitle", "currentLaunchBase"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("task JSON still has legacy field %q", k)
		}
	}
	if _, ok := raw["originSessionTitle"]; !ok {
		t.Fatal("task JSON lacks originSessionTitle")
	}
	if workspace := tk.Workspace; workspace == nil || workspace.BindingID != p.Bindings[0].ID || workspace.Path != p.WorkspaceRoot || workspace.Source != "primary" {
		t.Fatalf("planned workspace: %+v", workspace)
	}

	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if started.State != "running" || started.ExternalSessionID == nil || started.LaunchAttempt != 1 || started.LaunchedAt == nil {
		t.Fatalf("started %+v", started)
	}
	if workspace := started.Workspace; workspace == nil || workspace.BindingID != p.Bindings[0].ID || workspace.Path != p.WorkspaceRoot || workspace.Source != "pinned" {
		t.Fatalf("launched workspace: %+v", workspace)
	}
	threadID := *started.ExternalSessionID
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
	if got.State != "done" || got.FinishedAt == nil || got.CompletedAt != nil {
		t.Fatalf("done %+v", got)
	}
	_ = e.fake.Settle(threadID)
	e.poll()
	got = e.getTask(tk.ID)
	if got.State != "done" || got.CompletedAt != nil {
		t.Fatalf("settled %+v", got)
	}
	var completedList struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?state=done&completedFrom=2000-01-01", nil, 200, &completedList)
	if len(completedList.Tasks) != 0 {
		t.Fatalf("unaccepted task in completion filter: %+v", completedList.Tasks)
	}
	e.complete(tk.ID)
	got = e.getTask(tk.ID)
	if got.State != "done" || got.CompletedAt == nil {
		t.Fatalf("completed task %+v", got)
	}
	e.must("GET", "/api/tasks?state=done&completedFrom=2000-01-01", nil, 200, &completedList)
	if len(completedList.Tasks) != 1 || completedList.Tasks[0].ID != tk.ID {
		t.Fatalf("completedFrom past %+v", completedList.Tasks)
	}
	e.must("GET", "/api/tasks?state=done&completedFrom=2999-01-01", nil, 200, &completedList)
	if len(completedList.Tasks) != 0 {
		t.Fatalf("completedFrom future %+v", completedList.Tasks)
	}
	e.must("GET", "/api/tasks?completedFrom=nope", nil, 400, nil)
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
	// API v8: task detail has no T3 thread; the session and links stand.
	var detail map[string]any
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &detail)
	if _, ok := detail["thread"]; ok {
		t.Fatal("task detail still has thread")
	}
	if got.ExternalSessionID == nil || got.Links.Native != "t3code://threads/fake-env/"+url.PathEscape(*got.ExternalSessionID) {
		t.Fatalf("completed task native link %q", got.Links.Native)
	}
	// Accepted tasks remain observable.
	tasks, _ := e.store.PollableTasks(context.Background(), true)
	if len(tasks) != 1 {
		t.Fatalf("pollable %d", len(tasks))
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
		"prompt": "new", "executionConfig": map[string]any{"model": t3.ModelKey("claude", "claude-haiku-5-5"), "options": []map[string]any{{"id": "worktree", "value": true}, {"id": "baseRef", "value": "origin/main"}}},
	}, 200, &patched)
	if patched.Prompt != "new" || patched.Config == nil || patched.Config.Model != t3.ModelKey("claude", "claude-haiku-5-5") || len(patched.Config.Options) != 2 {
		t.Fatalf("patched %+v", patched)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"executionConfig": map[string]any{}}, 200, &patched)
	if patched.Config != nil && patched.Config.Model != "" {
		t.Fatalf("model not cleared: %+v", patched.Config)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"executionConfig": map[string]any{"permission": "yolo"}}, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"title": nil}, 400, nil)

	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	threadID := *started.ExternalSessionID
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
	if dup.State != "backlog" || dup.Title != "B" || dup.ID == tk.ID || dup.ExternalSessionID != nil || len(dup.Needs) != 0 {
		t.Fatalf("duplicate %+v", dup)
	}
	e.must("DELETE", fmt.Sprintf("/api/tasks/%d", dup.ID), nil, 204, nil)
	e.must("GET", fmt.Sprintf("/api/tasks/%d", dup.ID), nil, 404, nil)

	// Validation.
	e.must("POST", "/api/tasks", map[string]any{"connectionId": dbtest.T3Connection, "title": "x", "prompt": "y"}, 400, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "model": "m"}, 400, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": 999, "connectionId": dbtest.T3Connection, "title": "x", "prompt": "y"}, 404, nil)
	e.must("POST", "/api/tasks", "not an object", 400, nil)

	// Filters.
	var list struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?state=running,backlog&q=b", nil, 200, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].ID != tk.ID {
		t.Fatalf("filtered %+v", list.Tasks)
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
	if started.State != "running" || started.LaunchAttempt != 1 || started.ExternalSessionID == nil {
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
	if failed.State != "launching" || failed.LastError == nil || failed.LaunchAttempt != 1 || failed.ExternalSessionID != nil {
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
	_ = e.fake.SetStatus(*started.ExternalSessionID, t3.StatusFailed)
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
	_, err := e.store.BindProject(t.Context(), db.ProjectBinding{
		ProjectID: p.ID, ConnectionID: dbtest.T3Connection, ExternalProjectID: replacement.ID,
		Workspace: replacement.WorkspaceRoot, Primary: true,
	})
	mustValue(t, struct{}{}, err)
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
	if err := e.fake.SetStatus(*started.ExternalSessionID, t3.StatusFailed); err != nil {
		t.Fatal(err)
	}
	e.poll()
	e.must("POST", endpoint, nil, 200, &started)
	if e.fake.Launched[3].ProjectID != p.T3ProjectID || started.LaunchAttempt != 2 {
		t.Fatal("a new attempt must use the original workspace pin")
	}
}

// Stage never asks a harness to create a project (ADR 0007 decision 11):
// the registration route is gone.
func TestRegisterWorkspaceRouteRemoved(t *testing.T) {
	e := newEnv(t)
	e.must("POST", "/api/connections/t3/projects", map[string]any{"title": "P", "workspace": "/w"}, 404, nil)
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
	if got.State != "launching" || got.ExternalSessionID != nil || got.LastError == nil || !strings.Contains(*got.LastError, "conflicting") {
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
	e.engine.Coordinator.RecoverLaunching(context.Background())
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
	if _, err := e.engine.Discovery.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Sync imports the unbound live T3 project; the bound one is untouched.
	ps, err := e.store.ListProjects(context.Background(), "all")
	if err != nil || len(ps) != 2 {
		t.Fatalf("import: %+v %v", ps, err)
	}
	imported, err := e.store.BoundProject(context.Background(), db.ProjectBinding{ConnectionID: 1, ExternalProjectID: ext.ID, Workspace: ext.WorkspaceRoot})
	if err != nil || imported.Title != "Discovered" || imported.Origin == nil || len(imported.Bindings) != 1 || imported.Bindings[0].Rule != "new" {
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
	if _, err := e.engine.Discovery.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetProject(context.Background(), p.ID)
	if err != nil || got.Title != p.Title || got.ArchivedAt != nil || len(got.Bindings) != 1 {
		t.Fatalf("Stage metadata changed after sync: %+v %v", got, err)
	}
	moved := got.Bindings[0]
	if moved.Workspace != "/moved" || moved.Status != "available" || !moved.Primary || moved.Rule != "moved" {
		t.Fatalf("binding did not follow the workspace move: %+v", moved)
	}
	e.must("PATCH", fmt.Sprintf("/api/projects/%d", p.ID), map[string]any{"title": "Stage title", "archived": true}, 200, nil)
	if err := e.fake.SetProject(ext.ID, func(x *t3.Project) { x.DeletedAt = &at }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.engine.Discovery.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = e.store.GetProject(context.Background(), p.ID)
	if got.Title != "Stage title" || got.ArchivedAt == nil {
		t.Fatalf("Stage metadata lost: %+v", got)
	}
	if got, err = e.store.GetProject(context.Background(), imported.ID); err != nil || got.ArchivedAt != nil || got.Bindings[0].Status != "missing" {
		t.Fatalf("deletion changed Stage project metadata: %+v %v", got, err)
	}
	e.must("POST", "/api/projects", map[string]any{"title": ""}, 400, nil)
	e.must("GET", "/api/projects?archived=maybe", nil, 400, nil)
}

func TestUpdateProjectKeepsOmittedFields(t *testing.T) {
	e := newEnv(t)
	var p db.Project
	e.must("POST", "/api/projects", map[string]any{"title": "Before"}, 201, &p)
	path := fmt.Sprintf("/api/projects/%d", p.ID)
	e.must("PATCH", path, map[string]any{"archived": true}, 200, &p)
	if p.Title != "Before" || p.ArchivedAt == nil {
		t.Fatalf("archive without title: %+v", p)
	}
	archivedAt := *p.ArchivedAt
	e.must("PATCH", path, map[string]any{"title": "After"}, 200, &p)
	if p.Title != "After" || p.ArchivedAt == nil || !p.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("rename without archived unarchived or moved archivedAt: %+v", p)
	}
	e.must("PATCH", path, map[string]any{"archived": false}, 200, &p)
	if p.Title != "After" || p.ArchivedAt != nil {
		t.Fatalf("unarchive: %+v", p)
	}
	e.must("PATCH", path, map[string]any{"title": " "}, 400, nil)
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

	_ = e.fake.SetStatus(*a.ExternalSessionID, t3.StatusCompleted)
	_ = e.fake.Settle(*a.ExternalSessionID)
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
	threadA := *a.ExternalSessionID
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
	if _, err := e.engine.Discovery.FullSync(ctx); err != nil {
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
	_ = e.fake.SetStatus(*b.ExternalSessionID, t3.StatusCancelled)
	e.poll()
	e.reconcile()
	if got := e.getTask(c.ID); got.State != "backlog" {
		t.Fatalf("dependent of cancelled task %s", got.State)
	}
	_ = e.fake.SetStatus(*b.ExternalSessionID, t3.StatusRunning)
	if _, err := e.engine.Discovery.FullSync(ctx); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if got := e.getTask(b.ID); got.State != "running" {
		t.Fatalf("cancelled task with a dependent did not reopen: %s", got.State)
	}
	_ = e.fake.SetStatus(*b.ExternalSessionID, t3.StatusCompleted)
	_ = e.fake.Settle(*b.ExternalSessionID)
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
		Sessions   int    `json:"sessions"`
		Threads    *int   `json:"threads"`
		FinishedAt string `json:"finishedAt"`
	}
	e.must("POST", "/api/sync", nil, 200, &res)
	if res.Projects != 2 || res.Sessions != 1 || res.Threads != nil || res.FinishedAt == "" {
		t.Fatalf("sync %+v", res)
	}
	var reply struct {
		Details struct {
			Connected     bool    `json:"connected"`
			EnvironmentID *string `json:"environmentId"`
			PublicURL     string  `json:"publicUrl"`
		} `json:"details"`
		Sync struct {
			LastFullSyncAt *string `json:"lastFullSyncAt"`
		} `json:"sync"`
	}
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &reply)
	st := reply.Details
	if reply.Sync.LastFullSyncAt == nil || !st.Connected || st.EnvironmentID == nil || st.PublicURL != "http://127.0.0.1:3773" {
		t.Fatalf("status %+v", reply)
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

// API v8 removed the T3 catalog; models come from a connection.
func TestCatalogRemovedAndPair(t *testing.T) {
	e := newEnv(t)
	e.must("GET", "/api/catalog", nil, 404, nil)
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
	t3ID := e.store.ActiveID(db.LocalMachine, "t3")

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
	e.task(pr.ID, "A", map[string]any{"executionConfig": map[string]any{"model": t3.ModelKey("claude", "claude-haiku-5-5")}})
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
	if strings.Join(p.NotifyEvents, ",") != "needs_input,failed,ready,done" {
		t.Fatalf("default notifyEvents %v", p.NotifyEvents)
	}
	e.must("PUT", "/api/preferences/notifications", map[string]any{"events": []string{"ready", "done", "needs_input"}}, 200, &p)
	if strings.Join(p.NotifyEvents, ",") != "needs_input,ready,done" {
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
	e.must("POST", "/api/tasks", e.taskBody(p.ID, "steal", map[string]any{"attachmentIds": []int64{a1.ID}}), 400, nil)
	nine := make([]int64, 9)
	for i := range nine {
		nine[i] = int64(i + 1)
	}
	e.must("POST", "/api/tasks", e.taskBody(p.ID, "many", map[string]any{"attachmentIds": nine}), 400, nil)

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

func TestBadgeAndNeedsInputSessions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/badge")

	// The task and its session are both waiting and count once.
	tk := e.task(p.ID, "needs me", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if err := e.fake.SetThread(*started.ExternalSessionID, func(d *t3.ThreadDetail) { d.PendingRequestCount = 1 }); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if got := e.getTask(tk.ID); got.State != "needs_input" {
		t.Fatalf("task state %s", got.State)
	}

	now := time.Now().UTC()
	for _, fixture := range []t3.Thread{
		{ThreadID: "ext-wait", ProjectID: p.T3ProjectID, Title: "external", Status: t3.StatusWaiting, CreatedAt: now},
		{ThreadID: "ext-sub", ProjectID: p.T3ProjectID, Title: "sub", Status: t3.StatusWaiting, CreatedAt: now,
			ParentThreadID: "ext-wait", RelationshipToParent: "subagent"},
		{ThreadID: "ext-fork", ProjectID: p.T3ProjectID, Title: "fork", Status: t3.StatusWaiting, CreatedAt: now.Add(-time.Minute),
			ParentThreadID: "ext-wait", RelationshipToParent: "fork"},
		{ThreadID: "ext-settled", ProjectID: p.T3ProjectID, Title: "settled", Status: t3.StatusWaiting, Settled: true, CreatedAt: now},
		{ThreadID: "ext-running", ProjectID: p.T3ProjectID, Title: "running", Status: t3.StatusRunning, CreatedAt: now},
	} {
		if err := e.store.PutObservation(ctx, fixture.Observation()); err != nil {
			t.Fatal(err)
		}
	}
	for id, st := range map[string]db.TaskState{
		"ext-wait": db.StateNeedsInput, "ext-sub": db.StateNeedsInput, "ext-fork": db.StateNeedsInput, "ext-settled": db.StateNeedsInput,
		"ext-running": db.StateRunning, *started.ExternalSessionID: db.StateNeedsInput,
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
			Event: db.StateDone, Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: thread}, Title: fmt.Sprintf("n%d", i), URL: "/history?q=x",
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
	if l := pg.Notifications[0].Links; l != (links{}) {
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
			Event: db.StateDone, Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: thread}, Title: thread, URL: "/sessions/" + thread,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var tags struct {
		Tags []string `json:"tags"`
	}
	e.must("GET", "/api/notifications/unread-tags", nil, 200, &tags)
	if fmt.Sprint(tags.Tags) != "[session-1:ext-1 session-1:ext-2]" {
		t.Fatalf("unread tags %v", tags.Tags)
	}

	e.must("POST", "/api/notifications/read", map[string]any{"upTo": 1, "groups": []string{"session:1:ext-1"}}, 400, nil)
	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{}}, 400, nil)
	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{" "}}, 400, nil)
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("session:1:x%d", i)
	}
	e.must("POST", "/api/notifications/read", map[string]any{"groups": many}, 400, nil)

	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{"session:1:ext-2", "thread:1:ext-1"}}, 400, nil)
	e.must("GET", "/api/notifications/unread-tags", nil, 200, &tags)
	if fmt.Sprint(tags.Tags) != "[session-1:ext-1 session-1:ext-2]" {
		t.Fatalf("removed group key changed unread tags %v", tags.Tags)
	}

	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{"session:1:ext-1"}}, 204, nil)
	e.must("GET", "/api/notifications/unread-tags", nil, 200, &tags)
	if fmt.Sprint(tags.Tags) != "[session-1:ext-2]" {
		t.Fatalf("unread tags after group read %v", tags.Tags)
	}
}

func TestNotificationGroups(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	threads := []string{"ext-1", "ext-2"}
	for _, thread := range threads {
		now := time.Now().UTC()
		o := execution.Observation{
			Session: db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: thread},
			Project: "test", State: "done", CreatedAt: now, UpdatedAt: now,
			Links: execution.Links{Web: "https://worker.example/" + thread, Native: t3.AppThreadURL(t3.FakeEnvironmentID, thread)},
		}
		if err := e.store.PutObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	var latest int64
	for _, thread := range []string{threads[0], threads[1], threads[0]} {
		id, err := e.store.InsertNotification(ctx, db.NewNotification{
			Event: db.StateDone, Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: thread}, Title: "Same title", URL: "/history?q=Same",
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
	if len(pg.Groups) != 1 || pg.Groups[0].Key != "session:1:ext-1" || pg.Groups[0].EventCount != 2 ||
		pg.Groups[0].UnreadCount != 2 || pg.LatestID != latest || pg.Unread != 3 || pg.NextCursor == nil {
		t.Fatalf("first group page: %+v", pg)
	}
	if l := pg.Groups[0].Links; l.Native != t3.AppThreadURL(t3.FakeEnvironmentID, threads[0]) || l.Web != "https://worker.example/"+threads[0] {
		t.Fatalf("group links: %+v", l)
	}
	e.must("GET", "/api/notifications/groups?limit=1&cursor="+url.QueryEscape(*pg.NextCursor), nil, 200, &pg)
	if len(pg.Groups) != 1 || pg.Groups[0].Key != "session:1:ext-2" || pg.NextCursor != nil {
		t.Fatalf("next group page: %+v", pg)
	}
	var history struct {
		Notifications []db.Notification `json:"notifications"`
		NextBefore    *int64            `json:"nextBefore"`
	}
	e.must("GET", "/api/notifications?group=session%3A1%3Aext-1&limit=1", nil, 200, &history)
	if len(history.Notifications) != 1 || history.Notifications[0].ID != latest || history.NextBefore == nil {
		t.Fatalf("first history page: %+v", history)
	}
	e.must("GET", fmt.Sprintf("/api/notifications?group=session%%3A1%%3Aext-1&limit=1&before=%d", *history.NextBefore), nil, 200, &history)
	if len(history.Notifications) != 1 || *history.Notifications[0].ExternalSessionID != "ext-1" || history.NextBefore != nil {
		t.Fatalf("next history page: %+v", history)
	}
	for _, query := range []string{"limit=0", "cursor=x", "cursor=recent:0", "cursor=waiting:no", "cursor=other:1"} {
		e.must("GET", "/api/notifications/groups?"+query, nil, 400, nil)
	}
}

// Task detail carries the branch and directory the session's harness reports, and omits them when it reports neither.
func TestTaskDetailReportsHarnessWorkspace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/reported")
	tk := e.task(p.ID, "where", nil)
	type detail struct {
		Reported *struct {
			Branch    string `json:"branch"`
			Directory string `json:"directory"`
		} `json:"reported"`
	}
	var before detail
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &before)
	if before.Reported != nil {
		t.Fatalf("unstarted task reported %+v", *before.Reported)
	}
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	e.poll()
	ref := db.SessionRef{ConnectionID: 1, ExternalSessionID: *started.ExternalSessionID}
	o, err := e.store.Observation(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	o.Branch, o.Directory = "feat/where", "/w/wt-where"
	if err := e.store.PutObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	var after detail
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &after)
	if after.Reported == nil || after.Reported.Branch != "feat/where" || after.Reported.Directory != "/w/wt-where" {
		t.Fatalf("reported %+v", after.Reported)
	}
	o.Branch, o.Directory = "", ""
	if err := e.store.PutObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	var cleared detail
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &cleared)
	if cleared.Reported != nil {
		t.Fatalf("reported neither, got %+v", *cleared.Reported)
	}
}
