package t3

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// envServer serves /.well-known/t3/environment, counting requests; while
// gate is non-nil each request waits for it to close.
type envServer struct {
	srv  *httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	gate chan struct{}
	env  string
}

func newEnvServer(t *testing.T, env string) *envServer {
	t.Helper()
	e := &envServer{env: env}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/t3/environment" {
			http.NotFound(w, r)
			return
		}
		e.hits.Add(1)
		e.mu.Lock()
		gate := e.gate
		e.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		writeJSON(w, 200, map[string]any{"environmentId": e.env, "label": "box " + e.env, "serverVersion": "0.1.9", "platform": map[string]any{"os": "linux"}})
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *envServer) hold() chan struct{} {
	g := make(chan struct{})
	e.mu.Lock()
	e.gate = g
	e.mu.Unlock()
	return g
}

func (e *envServer) release(g chan struct{}) {
	e.mu.Lock()
	e.gate = nil
	e.mu.Unlock()
	close(g)
}

// clockClient is a client whose clock can be advanced.
func clockClient(t *testing.T, base string) (*MCPClient, *atomic.Int64) {
	t.Helper()
	var off atomic.Int64
	c := NewMCPClient(Options{BaseURL: base, PublicURL: "http://public.test", Logger: slog.New(slog.DiscardHandler)})
	c.now = func() time.Time { return time.Now().Add(time.Duration(off.Load())) }
	t.Cleanup(c.Close)
	return c, &off
}

func TestProbeCachesForTTLAndCoalesces(t *testing.T) {
	e := newEnvServer(t, "env-a")
	c, off := clockClient(t, e.srv.URL)
	ctx := ctxT(t)

	g := e.hold()
	var wg sync.WaitGroup
	results := make([]Probe, 8)
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = c.Probe(ctx) }()
	}
	// Every caller waits on the one request.
	deadline := time.Now().Add(5 * time.Second)
	for e.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	e.release(g)
	wg.Wait()
	if n := e.hits.Load(); n != 1 {
		t.Fatalf("%d requests for concurrent probes, want 1", n)
	}
	for _, p := range results {
		if !p.Reachable || p.Identity == nil || p.Identity.EnvironmentID != "env-a" || p.Identity.Label != "box env-a" || p.Identity.ServerVersion != "0.1.9" || p.CheckedAt.IsZero() {
			t.Fatalf("probe %+v", p)
		}
	}
	// Fresh: served from cache.
	off.Store(int64(9 * time.Second))
	if p := c.Probe(ctx); !p.Reachable || e.hits.Load() != 1 {
		t.Fatalf("within TTL: %+v, %d requests", p, e.hits.Load())
	}
	// Stale: refreshed.
	off.Store(int64(11 * time.Second))
	if p := c.Probe(ctx); !p.Reachable || e.hits.Load() != 2 {
		t.Fatalf("after TTL: %+v, %d requests", p, e.hits.Load())
	}
	if st := c.Status(ctx); st.EnvironmentID != "env-a" || !st.Probe.Reachable {
		t.Fatalf("status %+v", st)
	}
}

func TestFailedProbeIsCachedToo(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c, off := clockClient(t, "http://"+addr)
	ctx := ctxT(t)
	p := c.Probe(ctx)
	if p.Reachable || p.Err != "connection refused" || p.Identity != nil || p.CheckedAt.IsZero() {
		t.Fatalf("closed port probe %+v", p)
	}
	first := p.CheckedAt
	if p := c.Probe(ctx); !p.CheckedAt.Equal(first) {
		t.Fatalf("failed probe not cached: %v then %v", first, p.CheckedAt)
	}
	off.Store(int64(ProbeTTL + time.Second))
	if p := c.Probe(ctx); p.CheckedAt.Equal(first) {
		t.Fatal("stale failed probe not retried")
	}
	if st := c.Status(ctx); st.State() != StateUnreachable {
		t.Fatalf("state %s", st.State())
	}
}

func TestProbeRejectsNonT3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/t3/environment" {
			_, _ = w.Write([]byte(`{"hello":"world"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	ctx := ctxT(t)
	if p := ProbeURL(ctx, srv.Client(), srv.URL); p.Reachable || p.Err != "not a T3 server (no environment id)" {
		t.Fatalf("no env id: %+v", p)
	}
	if p := ProbeURL(ctx, srv.Client(), srv.URL+"/nope"); p.Reachable || p.Err != "not a T3 server (HTTP 404)" {
		t.Fatalf("404: %+v", p)
	}
}

func TestStatusNeverCallsT3(t *testing.T) {
	e := newEnvServer(t, "env-a")
	g := e.hold()
	defer e.release(g)
	c, _ := clockClient(t, e.srv.URL)
	start := time.Now()
	st := c.Status(context.Background())
	if time.Since(start) > 500*time.Millisecond || e.hits.Load() != 0 {
		t.Fatalf("status took %v with %d requests", time.Since(start), e.hits.Load())
	}
	if !st.Probe.CheckedAt.IsZero() || st.State() != StateUnreachable || st.EnvironmentID != "" {
		t.Fatalf("status before any probe %+v", st)
	}
	// A caller that gives up gets the cache; the probe keeps running.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if p := c.Probe(ctx); p.Reachable || !p.CheckedAt.IsZero() {
		t.Fatalf("abandoned probe %+v", p)
	}
}

func TestReconfigureSwitchesT3(t *testing.T) {
	a, b := newFakeT3(t), newFakeT3(t)
	b.mu.Lock()
	b.envID = "env-b"
	b.mu.Unlock()
	capabilitiesTool(a)
	capabilitiesTool(b)
	a.addUserPairingCode("CODE-A")
	b.addUserPairingCode("CODE-B")
	store := NewMemoryTokenStore()
	c := newTestClient(t, a, "", store)
	ctx := ctxT(t)

	if err := c.Pair(ctx, "CODE-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	if p := c.Probe(ctx); p.Identity == nil || p.Identity.EnvironmentID != "env-fake" {
		t.Fatalf("probe a %+v", p)
	}
	if got := c.WebURL(ctx, "th"); got != "http://localhost:3773/env-fake/th" {
		t.Fatalf("web url a %q", got)
	}

	c.Reconfigure(b.url()+"/", "http://b.public")
	st := c.Status(ctx)
	if !st.Probe.CheckedAt.IsZero() || st.EnvironmentID != "" || !st.HasToken {
		t.Fatalf("after reconfigure: probe and env kept, or token dropped: %+v", st)
	}
	if p := c.Probe(ctx); p.Identity == nil || p.Identity.EnvironmentID != "env-b" {
		t.Fatalf("probe b %+v", p)
	}
	if c.PublicURL() != "http://b.public" || c.WebURL(ctx, "th") != "http://b.public/env-b/th" {
		t.Fatalf("public url %q web %q", c.PublicURL(), c.WebURL(ctx, "th"))
	}
	// OAuth and MCP go to the new T3.
	if err := c.Pair(ctx, "CODE-B"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	bResource := b.lastTokenForm.Get("resource")
	b.mu.Unlock()
	if bResource != b.url()+"/mcp" {
		t.Fatalf("token resource %q", bResource)
	}
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	bCalls := len(b.mcpAuthHeaders)
	b.mu.Unlock()
	if bCalls == 0 {
		t.Fatal("no MCP request reached the new T3")
	}
	// Same URL again: nothing dropped.
	before := c.Status(ctx).Probe.CheckedAt
	c.Reconfigure(b.url(), "http://b.public")
	if after := c.Status(ctx).Probe.CheckedAt; !after.Equal(before) {
		t.Fatal("same-URL reconfigure dropped the probe cache")
	}
}

func TestReconfigureDropsInFlightProbe(t *testing.T) {
	a, b := newEnvServer(t, "env-a"), newEnvServer(t, "env-b")
	c, _ := clockClient(t, a.srv.URL)
	ctx := ctxT(t)
	g := a.hold()
	done := make(chan Probe)
	go func() { done <- c.Probe(ctx) }()
	for a.hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	c.Reconfigure(b.srv.URL, b.srv.URL)
	if p := c.Probe(ctx); p.Identity == nil || p.Identity.EnvironmentID != "env-b" {
		t.Fatalf("probe after reconfigure %+v", p)
	}
	a.release(g)
	if p := <-done; p.Identity == nil || p.Identity.EnvironmentID != "env-a" {
		t.Fatalf("old caller got %+v", p)
	}
	// The old URL's late answer does not replace the new one.
	if st := c.Status(ctx); st.EnvironmentID != "env-b" || st.Probe.Identity == nil || st.Probe.Identity.EnvironmentID != "env-b" {
		t.Fatalf("status %+v", st)
	}
}

func TestStateOrder(t *testing.T) {
	up := Probe{Reachable: true}
	for _, tc := range []struct {
		name string
		st   ConnectionStatus
		want State
	}{
		{"unreachable wins", ConnectionStatus{HasToken: true, Expired: true, Rejected: true}, StateUnreachable},
		{"unpaired", ConnectionStatus{Probe: up}, StateUnpaired},
		{"expired before rejected", ConnectionStatus{Probe: up, HasToken: true, Expired: true, Rejected: true}, StateExpired},
		{"rejected", ConnectionStatus{Probe: up, HasToken: true, Rejected: true}, StateRejected},
		{"connected", ConnectionStatus{Probe: up, HasToken: true, Connected: true}, StateConnected},
	} {
		if got := tc.st.State(); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestStatusReportsTokenProblems(t *testing.T) {
	f := newFakeT3(t)
	store := NewMemoryTokenStore()
	ctx := ctxT(t)
	_ = store.SaveToken(ctx, StoredToken{ClientID: "c", Token: "old", ExpiresAt: time.Now().Add(-time.Hour)})
	c := newTestClient(t, f, "", store)
	c.Probe(ctx)
	if st := c.Status(ctx); st.State() != StateExpired || !st.HasToken || !st.Expired || st.Connected {
		t.Fatalf("expired %+v", st)
	}
	_ = store.SaveToken(ctx, StoredToken{ClientID: "c", Token: "unknown", ExpiresAt: time.Now().Add(24 * time.Hour)})
	c2 := newTestClient(t, f, "", store)
	c2.Probe(ctx)
	if _, err := c2.Capabilities(ctx); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("rejected token call: %v", err)
	}
	if st := c2.Status(ctx); st.State() != StateRejected || !st.Rejected {
		t.Fatalf("rejected %+v", st)
	}
}

func TestFakeProbeAndReconfigure(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if st := f.Status(ctx); st.State() != StateConnected || st.Probe.Identity == nil || st.Probe.Identity.EnvironmentID != FakeEnvironmentID {
		t.Fatalf("fake status %+v", st)
	}
	f.SetProbe(Probe{Err: "connection refused"})
	if st := f.Status(ctx); st.State() != StateUnreachable || f.Probe(ctx).Err != "connection refused" {
		t.Fatalf("fake unreachable %+v", st)
	}
	f.Reconfigure("http://x", "http://y")
	if f.BaseURL != "http://x" || f.PublicURL != "http://y" {
		t.Fatalf("reconfigure not recorded: %q %q", f.BaseURL, f.PublicURL)
	}
}

// An unpaired client (no token, no admin token) is never contacted in the
// background: Run starts no probe and adapter health does not probe.
func TestUnpairedClientIsNeverContacted(t *testing.T) {
	e := newEnvServer(t, "env-a")
	c := NewMCPClient(Options{BaseURL: e.srv.URL, Logger: slog.New(slog.DiscardHandler)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	if h := NewConnectionAdapter(c, 1).Health(context.Background()); h.Available || !strings.Contains(h.Reason, "not connected") {
		t.Fatalf("unpaired health %+v", h)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if n := e.hits.Load(); n != 0 {
		t.Fatalf("unpaired client contacted T3 %d times", n)
	}
	// Paired: Run learns reachability early.
	store := NewMemoryTokenStore()
	if err := store.SaveToken(context.Background(), StoredToken{Token: "tok", ExpiresAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c = NewMCPClient(Options{BaseURL: e.srv.URL, Store: store, Logger: slog.New(slog.DiscardHandler)})
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for e.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e.hits.Load() == 0 {
		t.Fatal("paired client never probed")
	}
}
