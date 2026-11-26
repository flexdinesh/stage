package workerchan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type seenLog struct {
	mu  sync.Mutex
	got []string
}

func (s *seenLog) seen(id int64, at time.Time, version string) {
	s.mu.Lock()
	s.got = append(s.got, version)
	s.mu.Unlock()
}

func (s *seenLog) n() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.got) }

func newTestHub(t *testing.T, o Options) (*Hub, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewHub(ctx, o), cancel
}

func attachReady(t *testing.T, h *Hub, id int64, version string) *Session {
	t.Helper()
	s := h.attach(id, version, nil)
	if s.alive() {
		body := json.RawMessage(`{"kinds":[],"channelKinds":["ping","launch","x"],"connections":[{"connectionId":3,"traits":[]}]}`)
		if _, err := h.Deliver(t.Context(), id, s.ID, Envelope{Kind: KindReady, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// worker answers every request on s with answer, until s ends.
func serve(s *Session, answer func(Envelope) *Envelope) {
	go func() {
		for {
			select {
			case <-s.Done():
				return
			case env := <-s.Outbound():
				if r := answer(env); r != nil {
					_, _ = s.hub.Deliver(context.Background(), s.MachineID, s.ID, *r)
				}
			}
		}
	}()
}

func TestCallResult(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "1.0")
	var got Envelope
	serve(s, func(env Envelope) *Envelope {
		got = env
		return &Envelope{ID: env.ID, Kind: KindResult, Body: json.RawMessage(`{"ok":true}`)}
	})
	raw, err := h.Call(context.Background(), 7, KindPing, 3, map[string]int{"n": 1}, time.Second)
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("call = %s, %v", raw, err)
	}
	if got.Kind != KindPing || got.ConnectionID != 3 || got.Deadline == nil || string(got.Body) != `{"n":1}` || !strings.HasPrefix(got.ID, s.ID[:6]+":") {
		t.Fatalf("request %+v", got)
	}
}

func TestCallRemoteError(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	serve(s, func(env Envelope) *Envelope {
		return &Envelope{ID: env.ID, Kind: KindResult, Error: &Error{Code: "rejected", Message: "no"}}
	})
	_, err := h.Call(context.Background(), 7, "x", 0, nil, time.Second)
	var re *RemoteError
	if !errors.As(err, &re) || re.Code != "rejected" {
		t.Fatalf("err = %v", err)
	}
}

func TestCallOffline(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	if _, err := h.Call(context.Background(), 9, KindPing, 0, nil, time.Second); !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallDeadlineIsLostReplyAndLateReplyDropped(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	reqs := make(chan Envelope, 1)
	serve(s, func(env Envelope) *Envelope { reqs <- env; return nil })
	_, err := h.Call(context.Background(), 7, KindPing, 0, nil, 50*time.Millisecond)
	if !errors.Is(err, ErrLostReply) {
		t.Fatalf("err = %v", err)
	}
	env := <-reqs
	// A late or unknown reply is ignored, not an error.
	if r, err := h.Deliver(context.Background(), 7, s.ID, Envelope{ID: env.ID, Kind: KindResult}); r != nil || err != nil {
		t.Fatalf("late reply: %v %v", r, err)
	}
	if r, err := h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "nope", Kind: KindResult}); r != nil || err != nil {
		t.Fatalf("unknown reply: %v %v", r, err)
	}
}

func TestStreamEndIsLostReply(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	serve(s, func(Envelope) *Envelope { go h.Detach(s); return nil })
	if _, err := h.Call(context.Background(), 7, KindPing, 0, nil, 5*time.Second); !errors.Is(err, ErrLostReply) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallerCancelSendsCancel(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.Call(ctx, 7, KindPing, 0, nil, 5*time.Second)
		done <- err
	}()
	req := <-s.Outbound()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	c := <-s.Outbound()
	if c.Kind != KindCancel || c.ID != req.ID {
		t.Fatalf("cancel %+v for %s", c, req.ID)
	}
}

func TestCallDeadlineFollowsContext(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, _ = h.Call(ctx, 7, KindPing, 0, nil, time.Hour) }()
	req := <-s.Outbound()
	if d, _ := ctx.Deadline(); req.Deadline == nil || req.Deadline.After(d.UTC().Add(time.Millisecond)) {
		t.Fatalf("deadline %v, ctx %v", req.Deadline, d)
	}
}

func TestReplaceEndsOlderSession(t *testing.T) {
	seen := &seenLog{}
	h, _ := newTestHub(t, Options{Seen: seen.seen})
	old := attachReady(t, h, 7, "1")
	inflight := make(chan error, 1)
	go func() {
		_, err := h.Call(context.Background(), 7, KindPing, 0, nil, 5*time.Second)
		inflight <- err
	}()
	<-old.Outbound()
	next := attachReady(t, h, 7, "2")
	if err := <-inflight; !errors.Is(err, ErrLostReply) {
		t.Fatalf("in-flight on replaced session: %v", err)
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("old session still live")
	}
	if _, err := h.Deliver(context.Background(), 7, old.ID, Envelope{Kind: KindHeartbeat}); !errors.Is(err, ErrReplaced) {
		t.Fatalf("deliver on old: %v", err)
	}
	// Detaching the old one leaves the new one in place.
	h.Detach(old)
	if p := h.Presence(7); !p.Online || p.Version != "2" {
		t.Fatalf("presence %+v", p)
	}
	if _, err := h.Deliver(context.Background(), 7, next.ID, Envelope{Kind: KindHeartbeat}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogEndsSilentSession(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	h, _ := newTestHub(t, Options{Timeout: 30 * time.Millisecond, Now: c.now})
	s := attachReady(t, h, 7, "")
	c.add(time.Second)
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("silent session not ended")
	}
	if p := h.Presence(7); p.Online {
		t.Fatalf("presence %+v", p)
	}
}

func TestPresenceAndPersistThrottle(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	seen := &seenLog{}
	h, _ := newTestHub(t, Options{Timeout: time.Hour, PersistEvery: time.Minute, Seen: seen.seen, Now: c.now})
	s := attachReady(t, h, 7, "1.2")
	if seen.n() != 1 || seen.got[0] != "1.2" {
		t.Fatalf("connect not persisted with version: %v", seen.got)
	}
	for range 5 {
		c.add(10 * time.Second)
		if _, err := h.Deliver(context.Background(), 7, s.ID, Envelope{Kind: KindHeartbeat}); err != nil {
			t.Fatal(err)
		}
	}
	if seen.n() != 1 {
		t.Fatalf("persisted %d times within a minute", seen.n())
	}
	c.add(20 * time.Second)
	_, _ = h.Deliver(context.Background(), 7, s.ID, Envelope{Kind: KindHeartbeat})
	if seen.n() != 2 {
		t.Fatalf("not persisted after a minute: %d", seen.n())
	}
	p := h.Presence(7)
	if !p.Online || !p.LastSeen.Equal(c.now()) {
		t.Fatalf("presence %+v", p)
	}
	h.Detach(s)
	if seen.n() != 3 {
		t.Fatalf("disconnect not persisted: %d", seen.n())
	}
	if p := h.Presence(7); p.Online {
		t.Fatalf("online after detach: %+v", p)
	}
}

func TestNeedsUpdateClearedByMatchingWorker(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	h.markNeedsUpdate(7, true, "0.9")
	if p := h.Presence(7); p.NeedsUpdate == nil || !p.NeedsUpdate.ServerOlder || p.Online {
		t.Fatalf("presence %+v", p)
	}
	attachReady(t, h, 7, "1.0")
	if p := h.Presence(7); p.NeedsUpdate != nil || !p.Online {
		t.Fatalf("presence after attach %+v", p)
	}
}

func TestCloseAndContextEnd(t *testing.T) {
	h, cancel := newTestHub(t, Options{})
	a := attachReady(t, h, 7, "")
	h.Close(7)
	select {
	case <-a.Done():
	default:
		t.Fatal("Close left the session live")
	}
	b := attachReady(t, h, 8, "")
	cancel()
	select {
	case <-b.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context end left the session live")
	}
	// Attaching after the hub's context ended yields an ended session.
	c := attachReady(t, h, 9, "")
	select {
	case <-c.Done():
	default:
		t.Fatal("attach after shutdown is live")
	}
}

func TestWorkerInitiatedPingAndUnknownKind(t *testing.T) {
	h, _ := newTestHub(t, Options{Version: "9.9"})
	s := attachReady(t, h, 7, "")
	r, err := h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "w1", Kind: KindPing})
	if err != nil || r == nil || r.ID != "w1" || r.Kind != KindResult {
		t.Fatalf("ping reply %+v %v", r, err)
	}
	var p Pong
	if err := json.Unmarshal(r.Body, &p); err != nil || p.Version != "9.9" {
		t.Fatalf("pong %s", r.Body)
	}
	r, _ = h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "w2", Kind: "nope"})
	if r == nil || r.Error == nil || r.Error.Code != CodeUnknownKind {
		t.Fatalf("unknown kind reply %+v", r)
	}
}

func TestSSERoundTrip(t *testing.T) {
	var b bytes.Buffer
	_ = WriteEvent(&b, EventHello, Hello{Session: "s1", MachineID: 2, HeartbeatSec: 15, TimeoutSec: 45})
	_ = WriteComment(&b, "keepalive")
	_ = WriteEvent(&b, EventRPC, Envelope{ID: "a:1", Kind: KindPing})
	b.WriteString("event: rpc\ndata: {\"kind\":\ndata: \"cancel\"}\n\n") // multi-line data
	lines := 0
	r := NewReader(&b)
	r.OnLine = func() { lines++ }
	ev, err := r.Next()
	var hello Hello
	if err != nil || ev.Name != EventHello || json.Unmarshal(ev.Data, &hello) != nil || hello.Session != "s1" {
		t.Fatalf("hello %+v %v", ev, err)
	}
	ev, err = r.Next()
	var env Envelope
	if err != nil || ev.Name != EventRPC || json.Unmarshal(ev.Data, &env) != nil || env.ID != "a:1" {
		t.Fatalf("rpc %+v %v", ev, err)
	}
	ev, err = r.Next()
	if err != nil || string(ev.Data) != "{\"kind\":\n\"cancel\"}" {
		t.Fatalf("multi-line %q %v", ev.Data, err)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
	if lines < 8 {
		t.Fatalf("OnLine saw %d lines (comments count as liveness)", lines)
	}
	if _, err := NewReader(strings.NewReader("event: x\ndata: 1")).Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated: %v", err)
	}
}

func TestCallbacksRunOnlyWhileTheirCallWaits(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	var during, after *Envelope
	serve(s, func(env Envelope) *Envelope {
		during, _ = h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "w1", Kind: "save", Call: env.ID, Body: json.RawMessage(`2`)})
		return &Envelope{ID: env.ID, Kind: KindResult, Body: json.RawMessage(`"done"`)}
	})
	var seen []string
	raw, err := h.CallWith(context.Background(), 7, "launch", 3, nil, time.Second, func(_ context.Context, env Envelope) (any, error) {
		seen = append(seen, env.Kind+string(env.Body))
		if string(env.Body) == "3" {
			return nil, &RemoteError{Code: "custom", Message: "refused"}
		}
		return map[string]bool{"saved": true}, nil
	})
	if err != nil || string(raw) != `"done"` || len(seen) != 1 || seen[0] != "save2" || during == nil || during.Error != nil || !strings.Contains(string(during.Body), "saved") {
		t.Fatalf("callback %s %v %v %+v", raw, err, seen, during)
	}
	after, _ = h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "w2", Kind: "save", Call: during.ID})
	if after == nil || after.Error == nil || after.Error.Code != CodeCallEnded {
		t.Fatalf("late callback %+v", after)
	}
}

func TestHandlersAndAttachHook(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	attached := make(chan int64, 1)
	h.OnReady(func(id int64) { attached <- id })
	h.Handle("changed", func(_ context.Context, id int64, env Envelope) (any, error) {
		if id != 7 {
			return nil, errors.New("wrong machine")
		}
		return "ok", nil
	})
	s := attachReady(t, h, 7, "")
	select {
	case id := <-attached:
		if id != 7 {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("attach hook not called")
	}
	r, err := h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "w1", Kind: "changed"})
	if err != nil || r == nil || r.Error != nil || string(r.Body) != `"ok"` {
		t.Fatalf("handler %+v %v", r, err)
	}
}

func TestCallReturnsOnlyAfterItsCallbacks(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	s := attachReady(t, h, 7, "")
	inCallback := make(chan struct{})
	delivered := make(chan *Envelope, 1)
	serve(s, func(env Envelope) *Envelope {
		go func() {
			r, _ := h.Deliver(context.Background(), 7, s.ID, Envelope{ID: "w1", Kind: "save", Call: env.ID})
			delivered <- r
		}()
		return nil // the result is lost: the caller's deadline ends the call
	})
	var finished bool
	_, err := h.CallWith(context.Background(), 7, "launch", 3, nil, 100*time.Millisecond, func(ctx context.Context, env Envelope) (any, error) {
		close(inCallback)
		<-ctx.Done() // the call stopped waiting: the callback's context ends
		finished = true
		return nil, ctx.Err()
	})
	if !errors.Is(err, ErrLostReply) || !finished {
		t.Fatalf("call returned before its callback: %v finished %v", err, finished)
	}
	<-inCallback
	if r := <-delivered; r == nil || r.Error == nil {
		t.Fatalf("cancelled callback answered %+v", r)
	}
}

func TestVersionRangeSelectsHighestOverlap(t *testing.T) {
	for _, tc := range []struct {
		local, peer VersionRange
		version     int
	}{
		{CurrentRange(), CurrentRange(), 1},
		{VersionRange{1, 4}, VersionRange{2, 5}, 4},
		{VersionRange{1, 2}, VersionRange{2, 5}, 2},
		{VersionRange{3, 4}, VersionRange{1, 2}, 0},
		{VersionRange{1, 2}, VersionRange{3, 4}, 0},
		{CurrentRange(), VersionRange{0, 2}, 0},
	} {
		got, ok := tc.local.Select(tc.peer)
		if ok != (tc.version != 0) || (ok && got != tc.version) {
			t.Fatalf("%+v and %+v: got %d, %v; want %d", tc.local, tc.peer, got, ok, tc.version)
		}
	}
}

func TestReadySnapshotGatesCallsAndReplacesTraits(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	var updates int
	h.OnReady(func(id int64) {
		updates++
		if !h.Presence(id).Online {
			t.Error("ready hook ran before online publication")
		}
	})
	initialization := func(context.Context, int64, Envelope) (any, error) { return "setup", nil }
	h.HandleInitialization("settings.get", initialization)
	h.Handle("afterReady", initialization)
	s := h.attach(7, "test", nil)
	if p := h.Presence(7); p.Online || !p.Negotiating || updates != 0 {
		t.Fatalf("before composition: %+v (ready hooks %d)", p, updates)
	}
	if _, err := h.Call(t.Context(), 7, KindPing, 0, nil, time.Second); !errors.Is(err, ErrNegotiating) {
		t.Fatalf("outbound call before ready: %v", err)
	}
	for kind, want := range map[string]string{"settings.get": "", "afterReady": CodeOperationUnavailable} {
		r, err := h.Deliver(t.Context(), 7, s.ID, Envelope{ID: "w1", Kind: kind})
		if err != nil || r == nil || (want == "" && r.Error != nil) || (want != "" && (r.Error == nil || r.Error.Code != want)) {
			t.Fatalf("%s during negotiation: %+v %v", kind, r, err)
		}
	}
	first := json.RawMessage(`{"kinds":["svc"],"channelKinds":["ping"],"connections":[{"connectionId":3,"traits":["interactive","futureTrait"]}]}`)
	second := json.RawMessage(`{"kinds":["svc"],"channelKinds":["ping"],"connections":[{"connectionId":3,"traits":[]}]}`)
	for _, body := range []json.RawMessage{first, first, second} {
		if _, err := h.Deliver(t.Context(), 7, s.ID, Envelope{Kind: KindReady, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	if got, id := h.Advertisement(7); id != s.ID || got == nil || !reflect.DeepEqual(got.Connections, []Connection{{ConnectionID: 3, Traits: []string{}}}) || updates != 2 {
		t.Fatalf("updated snapshot: %+v (session %s, ready hooks %d)", got, id, updates)
	}
	h.attach(7, "next", nil)
	if _, err := h.Deliver(t.Context(), 7, s.ID, Envelope{Kind: KindReady, Body: first}); !errors.Is(err, ErrReplaced) {
		t.Fatal(err)
	}
	if got, _ := h.Advertisement(7); got != nil || updates != 2 {
		t.Fatalf("replaced session published %+v (ready hooks %d)", got, updates)
	}
}
