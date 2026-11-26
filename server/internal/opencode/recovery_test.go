package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestLiveCoordinatorRecovery(t *testing.T) {
	f := newFixture(t)
	a := f.a
	ctx := t.Context()
	var temp dbtest.DB
	pool := temp.Reset(t)
	store := db.NewStore(pool)
	if _, e := pool.ExecContext(ctx, "INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')"); e != nil {
		t.Fatal(e)
	}
	p, e := store.InsertProject(ctx, db.Project{Title: "Stage fixture", Bindings: []db.ProjectBinding{{ConnectionID: 2, ExternalProjectID: f.project, Workspace: f.dir}}})
	if e != nil {
		t.Fatal(e)
	}
	compose := func() *engine.Engine {
		return engine.NewWithAdapters(store, nil, map[int64]execution.Adapter{2: a}, nil)
	}
	for _, step := range []string{"worktree", "session", "prompt"} {
		t.Run(step, func(t *testing.T) {
			svc := compose()
			in := f.intent("lost-"+step, "worktree")
			task, e := store.CreateTask(ctx, db.NewTask{ProjectID: p.ID, ConnectionID: 2, Title: in.Title, Prompt: in.Prompt, ExecutionConfig: &in.Config})
			if e != nil {
				t.Fatal(e)
			}
			var mu sync.Mutex
			offline := false
			counts := map[string]int{}
			a.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				down := offline
				mu.Unlock()
				if down {
					return nil, errors.New("fixture disconnected")
				}
				resp, e := http.DefaultTransport.RoundTrip(req)
				if e != nil || req.Method != "POST" {
					return resp, e
				}
				op := ""
				switch {
				case req.URL.Path == "/api/worktree":
					op = "worktree"
				case req.URL.Path == "/api/session":
					op = "session"
				case strings.HasSuffix(req.URL.Path, "/prompt"):
					op = "prompt"
				}
				mu.Lock()
				counts[op]++
				drop := op == step && counts[op] == 1
				if drop {
					offline = true
				}
				mu.Unlock()
				if drop {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					return nil, errors.New("fixture lost acknowledged response")
				}
				return resp, nil
			})
			t.Cleanup(func() { a.http.Transport = nil })
			before := f.turns.Load()
			started, e := svc.Coordinator.StartTask(ctx, task.ID)
			if e == nil || started.State != db.StateLaunching {
				t.Fatalf("launch not held: %+v %v", started, e)
			}
			a2 := compose()
			a2.Coordinator.RecoverLaunching(ctx)
			mu.Lock()
			offline = false
			mu.Unlock()
			// Independent requests against the restarted coordinator share the same attempt lock.
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() { defer wg.Done(); _, _ = a2.Coordinator.StartTask(ctx, task.ID) }()
			}
			wg.Wait()
			if counts["worktree"] != 1 {
				t.Fatalf("worktree count: %v, start error: %v, stored error: %v", counts, e, started.LastError)
			}
			got, e := store.GetTask(ctx, task.ID)
			if e != nil {
				t.Fatal(e)
			}
			if step == "worktree" {
				if got.State != db.StateLaunching || got.Session != nil || counts["session"] != 0 || counts["prompt"] != 0 {
					t.Fatalf("uncertain workspace produced work: %v %+v", counts, got)
				}
				return
			}
			if got.Session == nil {
				t.Fatalf("no confirmed session: %+v counts %v", got, counts)
			}
			o := observeUntil(t, a, *got.Session, "done")
			if e = a2.Coordinator.PollTask(ctx, got); e != nil {
				t.Fatal(e)
			}
			if o.Reply == nil || counts["session"] != 1 || counts["prompt"] != 1 || f.turns.Load()-before != 1 {
				t.Fatalf("execution duplicated or incomplete: posts=%v model calls=%d reply=%+v", counts, f.turns.Load()-before, o.Reply)
			}
			mu.Lock()
			offline = true
			mu.Unlock()
			preview, e := store.CompletionPreview(ctx, task.ID)
			if e != nil || preview.BlockedReason != "" || preview.Evidence.Reply == nil || *preview.Evidence.Reply != "Stage live reply" {
				t.Fatalf("offline evidence: %+v %v", preview, e)
			}
			if _, e = store.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{RequestID: "accept", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, false); e != nil {
				t.Fatal(e)
			}
		})
	}
	// External native session, explicitly adopted through the same scoped store path.
	var external envelope[session]
	if e = a.request(ctx, "POST", "/api/session", map[string]interface{}{"location": location{Directory: f.dir}}, &external); e != nil {
		t.Fatal(e)
	}
	id := external.Data.ID
	var form envelope[struct{ ID string }]
	if e = a.request(ctx, "POST", "/api/session/"+id+"/form", map[string]interface{}{"title": "Choose", "fields": []map[string]string{{"key": "answer", "type": "string"}}}, &form); e != nil {
		t.Fatal(e)
	}
	ref := execution.SessionRef{ConnectionID: 2, ExternalSessionID: id}
	attention, err := a.Attention(ctx)
	if err != nil || !slices.Contains(attention, ref) {
		t.Fatalf("pending form absent from attention: %v %v", attention, err)
	}
	observation, e := a.Observe(ctx, ref)
	if e != nil {
		t.Fatal(e)
	}
	verified, e := a.ResolveLink(ctx, observation.Links.Web)
	if e != nil || verified != ref {
		t.Fatal(observation.Links, e)
	}
	resp, e := http.Get(observation.Links.Web)
	if e != nil {
		t.Fatal(e)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("native session URL %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	svc := compose()
	router := api.NewRouter(api.Deps{Store: store, Engine: svc})
	path := "/api/connections/2/sessions/" + id
	preview := httptest.NewRecorder()
	router.ServeHTTP(preview, httptest.NewRequest("GET", path, nil))
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), observation.Links.Web) {
		t.Fatalf("adoption preview: %d %s", preview.Code, preview.Body.String())
	}
	for _, status := range []int{201, 200} {
		result := httptest.NewRecorder()
		req := httptest.NewRequest("POST", path+"/adopt", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(result, req)
		if result.Code != status {
			t.Fatalf("adoption: %d %s", result.Code, result.Body.String())
		}
	}
	adopted, e := store.TaskBySession(ctx, ref)
	if e != nil || !adopted.Adopted || adopted.State != db.StateNeedsInput || adopted.AcceptanceID != nil {
		t.Fatalf("adoption %+v %v", adopted, e)
	}
	read := httptest.NewRecorder()
	router.ServeHTTP(read, httptest.NewRequest("GET", fmt.Sprintf("/api/connections/2/capabilities?projectId=%d", p.ID), nil))
	var catalog execution.Capabilities
	if read.Code != 200 || json.NewDecoder(read.Body).Decode(&catalog) != nil || len(catalog.Models) == 0 || len(catalog.Modes) == 0 {
		t.Fatalf("scoped API catalog: %d %s", read.Code, read.Body.String())
	}
	stop := httptest.NewRecorder()
	router.ServeHTTP(stop, httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/stop", adopted.ID), nil))
	if stop.Code != 200 {
		t.Fatalf("stop adopted scoped session: %d %s", stop.Code, stop.Body.String())
	}

	var idle envelope[session]
	if e = a.request(ctx, "POST", "/api/session", map[string]any{"location": location{Directory: f.dir}}, &idle); e != nil {
		t.Fatal(e)
	}
	idleRef := execution.SessionRef{ConnectionID: 2, ExternalSessionID: idle.Data.ID}
	req := httptest.NewRequest("POST", "/api/connections/2/sessions/"+idle.Data.ID+"/adopt", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, req)
	if result.Code != 201 {
		t.Fatalf("idle adoption: %d %s", result.Code, result.Body.String())
	}
	waiting, e := store.TaskBySession(ctx, idleRef)
	if e != nil || waiting.State != db.StateBacklog || waiting.Session == nil || waiting.LaunchedAt != nil || waiting.FinishedAt != nil || waiting.AcceptanceID != nil {
		t.Fatalf("idle adoption recorded work: %+v %v", waiting, e)
	}
	if _, e = svc.Coordinator.StartTask(ctx, waiting.ID); e == nil {
		t.Fatal("Stage started an adopted idle session")
	}
	if e = a.request(ctx, "POST", "/api/session/"+idle.Data.ID+"/prompt", map[string]string{"text": "fixture-interrupt"}, nil); e != nil {
		t.Fatal(e)
	}
	observeUntil(t, a, idleRef, "running")
	if e = svc.Coordinator.PollActive(ctx); e != nil {
		t.Fatal(e)
	}
	active, e := store.TaskBySession(ctx, idleRef)
	if e != nil || active.ID != waiting.ID || active.State != db.StateRunning || active.LaunchedAt == nil || active.FinishedAt != nil || active.AcceptanceID != nil {
		t.Fatalf("missed first native execution: %+v %v", active, e)
	}
	if e = a.Stop(ctx, idleRef); e != nil {
		t.Fatal(e)
	}
}

func TestLiveNativeReplay(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.a
	// Direct upstream replay is exercised only against the disposable harness.
	const id = "ses_stage246_native"
	const msg = "msg_stage246_native"
	create := func(title string) session {
		var result envelope[session]
		if e := a.request(ctx, "POST", "/api/session", map[string]interface{}{"id": id, "title": title, "location": location{Directory: f.dir}}, &result); e != nil {
			t.Fatal(e)
		}
		return result.Data
	}
	one, two := create("first"), create("second")
	if one.ID != two.ID || two.Title != "first" {
		t.Fatal("create replay changed recorded session")
	}
	prompt := map[string]interface{}{"id": msg, "text": "native-replay", "resume": false}
	post := func() (admitted, error) {
		var result envelope[admitted]
		e := a.request(ctx, "POST", "/api/session/"+id+"/prompt", prompt, &result)
		return result.Data, e
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			v, e := post()
			if e == nil && v.ID != msg {
				e = errors.New("wrong admission")
			}
			results <- e
		}()
	}
	for range 2 {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	var inbox envelope[[]admitted]
	if e := a.get(ctx, "/api/session/"+id+"/inbox", &inbox); e != nil || len(inbox.Data) != 1 {
		t.Fatal("pending replay", len(inbox.Data), e)
	}
	prompt["resume"] = true
	if _, e := post(); e != nil {
		t.Fatal(e)
	}
	ref := execution.SessionRef{ConnectionID: 2, ExternalSessionID: id}
	first := observeUntil(t, a, ref, "done")
	if first.Reply == nil {
		t.Fatal("first execution has no final reply")
	}
	original := f.turns.Load()
	// Changed text still resolves the original admission. It may wake the loop.
	prompt["text"] = "different"
	v, e := post()
	if e != nil || v.Payload.Text != "native-replay" {
		t.Fatal("delivered replay", v, e)
	}
	var msgs envelope[[]message]
	if e = a.get(ctx, "/api/session/"+id+"/message?type=user", &msgs); e != nil || len(msgs.Data) != 1 {
		t.Fatal("duplicate native input", e)
	}
	replayed := observeUntil(t, a, ref, "done")
	t.Logf("delivered replay: user messages=%d, model calls %d to %d, execution changed=%t", len(msgs.Data), original, f.turns.Load(), first.ExecutionID != replayed.ExecutionID)
	origin, _ := url.Parse(a.endpoint)
	// An existing reverse proxy can expose a no-auth endpoint while the
	// standalone 2.3 CLI always protects its own listener with Basic auth.
	proxy := httputil.NewSingleHostReverseProxy(origin)
	open := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("no-auth adapter sent credentials")
		}
		r.SetBasicAuth("opencode", "stage-test")
		proxy.ServeHTTP(w, r)
	}))
	defer open.Close()
	unauthenticated, e := New(2, Settings{Endpoint: open.URL})
	if e != nil {
		t.Fatal(e)
	}
	if h := unauthenticated.Health(ctx); !h.Available {
		t.Fatal(h.Reason)
	}
	denied, e := New(2, Settings{Endpoint: a.endpoint, Username: "opencode", Password: "incorrect-test-password"})
	if e != nil {
		t.Fatal(e)
	}
	if h := denied.Health(ctx); h.Available || !strings.Contains(h.Reason, "credentials") {
		t.Fatal(h)
	}
	f.stop()
	if h := a.Health(ctx); h.Available {
		t.Fatal("offline server reported healthy")
	}
	f.start(t, origin.Port())
	o := observeUntil(t, f.a, ref, "done")
	if o.ExecutionID != replayed.ExecutionID {
		t.Fatalf("unexpected execution after reconnect: %+v", o)
	}
	var originalReply envelope[message]
	if e = f.a.get(ctx, "/api/session/"+id+"/message/"+first.Reply.ItemID, &originalReply); e != nil || originalReply.Data.ID != first.Reply.ItemID {
		t.Fatal("lost original message on reconnect", e)
	}
	if o.ExecutionID != first.ExecutionID && o.Reply != nil && o.Reply.ItemID == first.Reply.ItemID {
		t.Fatal("stale reply attributed to replay")
	}
}

func TestClientContract(t *testing.T) {
	for _, version := range []string{"1.2.3", "2.3.0"} {
		t.Run(version, func(t *testing.T) {
			var writes int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected credentials")
				}
				if r.URL.Path == "/api/info" {
					fmt.Fprintf(w, `{"version":%q}`, version)
					return
				}
				fmt.Fprint(w, `{"paths":{}}`)
			}))
			defer srv.Close()
			a, e := New(2, Settings{Endpoint: srv.URL})
			if e != nil {
				t.Fatal(e)
			}
			if h := a.Health(t.Context()); h.Available || h.Reason == "" {
				t.Fatal("accepted unsupported contract")
			}
			if writes != 0 {
				t.Fatal("health check wrote to server")
			}
			link := a.sessionWebURL("ses_123")
			if _, e = a.ResolveLink(t.Context(), strings.Replace(link, srv.URL, "https://other.invalid", 1)); e == nil {
				t.Fatal("accepted another connection")
			}
		})
	}
}
