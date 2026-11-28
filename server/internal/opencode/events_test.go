package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
)

// eofFrame ends the current event stream response.
const eofFrame = "<eof>"

// connectedFrame is the frame OpenCode 2.3.2 sends first.
const connectedFrame = "data: {\"id\":\"evt_c\",\"type\":\"server.connected\",\"data\":{}}\n\n"

func frame(typ string, data any) string {
	b, err := json.Marshal(map[string]any{"id": "evt_x", "created": 1, "type": typ, "data": data})
	if err != nil {
		panic(err)
	}
	return "data: " + string(b) + "\n\n"
}

func sessionData(id string) map[string]any { return map[string]any{"sessionID": id} }

// eventOp is event.subscribe as OpenCode 2.3.2 declares it: types is one
// type, an rpc. pattern, or an array of either.
func eventOp(types ...string) json.RawMessage {
	single := []any{
		map[string]any{"type": "string", "enum": types},
		map[string]any{"type": "string", "pattern": `^rpc\.[\s\S]*?$`},
	}
	op := map[string]any{"operationId": "event.subscribe", "parameters": []any{map[string]any{
		"name": "types", "in": "query",
		"schema": map[string]any{"anyOf": append(slices.Clone(single), map[string]any{"type": "array", "items": map[string]any{"anyOf": single}})},
	}}}
	b, err := json.Marshal(op)
	if err != nil {
		panic(err)
	}
	return b
}

func allEvents() []string {
	return append(append([]string{"session.created", "session.text.delta"}, essentialEvents...), optionalEvents...)
}

type fakeSession struct {
	title      string
	archived   bool
	permission bool
}

// eventsFake is an OpenCode service with an event stream fed from frames
// and sessions Observe can read.
type eventsFake struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
	reads    map[string]int
	streams  int
	queries  []url.Values
	accepts  []string
	status   int // answers GET /api/event (0: stream)
	// feeds holds each subscription's frames, in subscription order.
	feeds []chan string
}

// feed is subscription n's frames, once it is open.
func (f *eventsFake) feed(t *testing.T, n int) chan<- string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.feeds) > n {
			feed := f.feeds[n]
			f.mu.Unlock()
			return feed
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("subscription %d never opened", n)
	return nil
}

func (f *eventsFake) session(id string) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	if s == nil {
		s = &fakeSession{title: id}
		f.sessions[id] = s
	}
	return s
}

func (f *eventsFake) update(id string, change func(*fakeSession)) {
	s := f.session(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	change(s)
}

func (f *eventsFake) stream(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.streams++
	f.queries = append(f.queries, r.URL.Query())
	f.accepts = append(f.accepts, r.Header.Get("Accept"))
	status := f.status
	frames := make(chan string, 64)
	if status == 0 {
		f.feeds = append(f.feeds, frames)
	}
	f.mu.Unlock()
	if status != 0 {
		http.Error(w, `{"_tag":"UnauthorizedError","message":"Authentication required"}`, status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flusher.Flush()
	for {
		select {
		case fr := <-frames:
			if fr == eofFrame {
				return
			}
			_, _ = io.WriteString(w, fr)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (f *eventsFake) serveSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/session/")
	if rest == "active" {
		writeJSON(w, map[string]any{"data": map[string]any{}})
		return
	}
	id, sub, _ := strings.Cut(rest, "/")
	f.mu.Lock()
	s, ok := f.sessions[id]
	var snap fakeSession
	if ok {
		snap = *s
		if sub == "" {
			f.reads[id]++
		}
	}
	f.mu.Unlock()
	if !ok {
		http.Error(w, `{"_tag":"SessionNotFoundError"}`, http.StatusNotFound)
		return
	}
	switch sub {
	case "":
		var archived int64
		if snap.archived {
			archived = 30
		}
		writeJSON(w, envelope[session]{Data: session{ID: id, Title: snap.title, ProjectID: "project", Location: location{Directory: "/repo"}, Time: wireTime{Created: 10, Updated: 20, Archived: archived}}})
	case "permission":
		list := []map[string]any{}
		if snap.permission {
			list = append(list, map[string]any{"id": "per_1", "sessionID": id})
		}
		writeJSON(w, map[string]any{"data": list})
	case "form":
		writeJSON(w, map[string]any{"data": []any{}})
	default:
		http.NotFound(w, r)
	}
}

// eventsFixture serves the fake contract with event.subscribe declaring
// types (none: no event.subscribe).
func eventsFixture(t *testing.T, types ...string) (*Adapter, *eventsFake) {
	t.Helper()
	base := opencodetest.New(t, "").Config.Handler
	rec := httptest.NewRecorder()
	base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if len(types) > 0 {
		spec.Paths["/api/event"] = map[string]json.RawMessage{"get": eventOp(types...)}
	}
	f := &eventsFake{sessions: map[string]*fakeSession{}, reads: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/info":
			base.ServeHTTP(w, r)
		case r.URL.Path == "/openapi.json":
			writeJSON(w, spec)
		case r.URL.Path == "/api/event":
			f.stream(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/session/"):
			f.serveSession(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, err := New(17, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a, f
}

type eventsRun struct {
	got  chan execution.Observation
	done chan error
}

func runEvents(t *testing.T, src execution.EventSource) eventsRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	run := eventsRun{got: make(chan execution.Observation, 64), done: make(chan error, 1)}
	go func() {
		run.done <- src.Events(ctx, func(_ context.Context, o execution.Observation) error { run.got <- o; return nil })
	}()
	t.Cleanup(func() {
		cancel()
		<-run.done
	})
	return run
}

func (r eventsRun) next(t *testing.T) execution.Observation {
	t.Helper()
	select {
	case o := <-r.got:
		return o
	case err := <-r.done:
		t.Fatalf("stream ended: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no pushed observation")
	}
	return execution.Observation{}
}

func (r eventsRun) end(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err // for the cleanup
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open")
	}
	return nil
}

func TestEventsReobserveTheSessionEachEventNames(t *testing.T) {
	a, f := eventsFixture(t, allEvents()...)
	run := runEvents(t, a)
	feed := f.feed(t, 0)
	feed <- connectedFrame
	for i, ev := range append(slices.Clone(essentialEvents), optionalEvents...) {
		id := fmt.Sprintf("ses_e%d", i)
		title := "after " + ev
		f.update(id, func(s *fakeSession) { s.title = title })
		data := any(sessionData(id))
		if ev == "form.created" {
			data = map[string]any{"form": map[string]any{"id": "frm_1", "sessionID": id, "title": "Form", "fields": []any{}}}
		}
		feed <- frame(ev, data)
		o := run.next(t)
		if o.Session != (execution.SessionRef{ConnectionID: 17, ExternalSessionID: id}) || o.Title != title {
			t.Fatalf("%s: pushed %+v", ev, o)
		}
	}
	f.update("ses_wait", func(s *fakeSession) { s.permission = true })
	feed <- frame("permission.asked", map[string]any{"id": "per_1", "sessionID": "ses_wait", "action": "bash", "resources": []string{}})
	if o := run.next(t); o.State != "needs_input" || !o.Active {
		t.Fatalf("permission: %+v", o)
	}
	f.update("ses_wait", func(s *fakeSession) { s.permission, s.archived = false, true })
	feed <- frame("session.archived", sessionData("ses_wait"))
	if o := run.next(t); o.State != "idle" || !o.Archived || o.Closed == nil || o.Closed.Signal != execution.ClosedArchived {
		t.Fatalf("archive: %+v", o)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	want := append(slices.Clone(essentialEvents), optionalEvents...)
	if got := f.queries[0]["types"]; !slices.Equal(got, want) || f.accepts[0] != "text/event-stream" {
		t.Fatalf("subscribed %v (Accept %q)", got, f.accepts[0])
	}
}

func TestEventsIgnoreUnknownEventsAndSessions(t *testing.T) {
	a, f := eventsFixture(t, allEvents()...)
	run := runEvents(t, a)
	f.session("ses_quiet")
	f.session("ses_known")
	feed := f.feed(t, 0)
	feed <- connectedFrame
	feed <- ": heartbeat\n\n"
	feed <- frame("session.text.delta", sessionData("ses_quiet"))
	feed <- frame("session.created", sessionData("ses_quiet"))
	feed <- frame("rpc.plugin.ping", sessionData("ses_quiet"))
	feed <- frame("tui.toast.show", map[string]any{})
	feed <- frame("session.status", sessionData("not a session"))
	feed <- frame("form.cancelled", map[string]any{"id": "frm_1"})
	feed <- "data:\n\n"
	feed <- connectedFrame
	feed <- "id: 7\ndata: {\"type\":\"session.idle\",\n" + "data: \"data\":{\"sessionID\":\"ses_known\"}}\n\n"
	if o := run.next(t); o.Session.ExternalSessionID != "ses_known" {
		t.Fatalf("pushed %+v", o)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reads["ses_quiet"] != 0 {
		t.Fatalf("read an unwatched session %d times", f.reads["ses_quiet"])
	}
}

func TestEventsEndWithAnErrorSoTheRunnerReconnects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames []string
		want   string
	}{
		{"overflow", []string{connectedFrame, "event: effect/http-api/stream/failure\ndata: [{\"_tag\":\"Fail\",\"error\":{}}]\n\n"}, "failed the event stream"},
		{"end", []string{connectedFrame, eofFrame}, "event stream ended"},
		{"no greeting", []string{frame("session.idle", sessionData("ses_a"))}, "did not start with server.connected"},
		{"named event", []string{connectedFrame, "event: surprise\ndata: {}\n\n"}, `unexpected OpenCode stream event "surprise"`},
		{"invalid JSON", []string{connectedFrame, "data: {\n\n"}, "invalid OpenCode event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f := eventsFixture(t, allEvents()...)
			first := runEvents(t, a)
			feed := f.feed(t, 0)
			for _, fr := range tc.frames {
				feed <- fr
			}
			if err := first.end(t); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			again := runEvents(t, a)
			f.update("ses_back", func(s *fakeSession) { s.title = "back" })
			feed = f.feed(t, 1)
			feed <- connectedFrame
			feed <- frame("session.execution.succeeded", sessionData("ses_back"))
			if o := again.next(t); o.Title != "back" {
				t.Fatalf("after reconnect %+v", o)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.streams != 2 {
				t.Fatalf("%d subscriptions", f.streams)
			}
		})
	}
}

func TestEventsEndWhenTheStreamGoesSilent(t *testing.T) {
	a, f := eventsFixture(t, allEvents()...)
	a.eventsIdle = 150 * time.Millisecond
	run := runEvents(t, a)
	feed := f.feed(t, 0)
	feed <- connectedFrame
	for range 6 { // 300 ms of heartbeats keeps it open
		time.Sleep(50 * time.Millisecond)
		feed <- ": heartbeat\n\n"
	}
	select {
	case err := <-run.done:
		t.Fatalf("ended during heartbeats: %v", err)
	default:
	}
	if err := run.end(t); !errors.Is(err, errEventsIdle) {
		t.Fatal(err)
	}
}

func TestEventsPollOnlyWithoutTheEventStream(t *testing.T) {
	without := slices.DeleteFunc(allEvents(), func(s string) bool { return s == "form.created" })
	for name, types := range map[string][]string{"no event.subscribe": nil, "missing form.created": without} {
		t.Run(name, func(t *testing.T) {
			a, f := eventsFixture(t, types...)
			if err := a.Events(t.Context(), func(context.Context, execution.Observation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.streams != 0 {
				t.Fatal("subscribed anyway")
			}
		})
	}
}

func TestInspectEventsAddsOptionalTypesWhenAdvertised(t *testing.T) {
	op := func(types ...string) operation {
		var o operation
		if err := json.Unmarshal(eventOp(types...), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	if got := inspectEvents(op(essentialEvents...)); !slices.Equal(got, essentialEvents) {
		t.Fatalf("without session.renamed: %v", got)
	}
	if got := inspectEvents(op(allEvents()...)); !slices.Equal(got, append(slices.Clone(essentialEvents), optionalEvents...)) {
		t.Fatalf("all: %v", got)
	}
	other := op(allEvents()...)
	other.OperationID = "event.list"
	if inspectEvents(other) != nil {
		t.Fatal("accepted another operation")
	}
}

func TestEventsReportRejection(t *testing.T) {
	a, f := eventsFixture(t, allEvents()...)
	f.status = http.StatusUnauthorized
	err := a.Events(t.Context(), func(context.Context, execution.Observation) error { return nil })
	if err == nil || err.Error() != rejectedReason {
		t.Fatal(err)
	}
}

func TestDirtySetCoalescesAndCaps(t *testing.T) {
	d := newDirtySet()
	for _, id := range []string{"ses_a", "ses_b", "ses_a", "ses_b", "ses_a"} {
		d.mark(id)
	}
	if got := d.take(); !slices.Equal(got, []string{"ses_a", "ses_b"}) {
		t.Fatal(got)
	}
	select {
	case <-d.ready:
	default:
		t.Fatal("not signalled")
	}
	for i := range maxDirtySessions + 10 {
		d.mark(fmt.Sprintf("ses_%d", i))
	}
	if got := d.take(); len(got) != maxDirtySessions || len(d.take()) != 0 {
		t.Fatal(len(got))
	}
}

func TestEventsThroughWorker(t *testing.T) {
	remote, f := eventsFixture(t, allEvents()...)
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{17: remote})
	src, ok := execution.As[execution.EventSource](rig.Adapter(17))
	if !ok {
		t.Fatal("worker hides the OpenCode event source")
	}
	run := runEvents(t, src)
	f.update("ses_remote", func(s *fakeSession) { s.title = "pushed through the worker" })
	feed := f.feed(t, 0)
	feed <- connectedFrame
	feed <- frame("session.idle", sessionData("ses_remote"))
	if o := run.next(t); o.Session.ExternalSessionID != "ses_remote" || o.Title != "pushed through the worker" {
		t.Fatalf("worker pushed %+v", o)
	}
}
