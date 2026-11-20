package launch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// remoteServer answers GET /api/health over TCP (httptest) as a remote
// Stage server; code 403 answers as the server's origin policy does.
type remoteServer struct {
	srv    *httptest.Server
	health atomic.Pointer[Health]
	code   atomic.Int32
	mu     sync.Mutex
	hosts  []string
	origin []string
}

func newRemoteServer(t *testing.T) *remoteServer {
	t.Helper()
	r := &remoteServer{}
	r.set(Health{Status: "ok", Service: "stage", Identity: "stage", Version: "1.3.0", APIVersion: 1, InstanceID: "r1"})
	r.code.Store(200)
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hosts = append(r.hosts, req.Host)
		r.origin = append(r.origin, req.Header.Get("Origin"))
		r.mu.Unlock()
		switch req.URL.Path {
		case "/api/health":
			code := int(r.code.Load())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			if code == 403 {
				_, _ = w.Write([]byte(`{"error":"host \"box.lan\" not allowed: add its origin to STAGE_ALLOWED_ORIGINS"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(r.health.Load())
		case "/api/harnesses/t3/status":
			_, _ = w.Write([]byte(`{"kind":"t3","connected":true,"details":{"connected":true,"publicUrl":"http://remote-t3:3773"}}`))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *remoteServer) set(h Health) { r.health.Store(&h) }

func (r *remoteServer) url(t *testing.T) string {
	u, err := target.NormalizeURL(r.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// targets records OnTarget and OnServer calls.
type targets struct {
	mu      sync.Mutex
	bridge  []target.Target
	servers []target.Target
}

func (tg *targets) onTarget(t target.Target) {
	tg.mu.Lock()
	tg.bridge = append(tg.bridge, t)
	tg.mu.Unlock()
}

func (tg *targets) onServer(t target.Target) {
	tg.mu.Lock()
	tg.servers = append(tg.servers, t)
	tg.mu.Unlock()
}

func (tg *targets) lastBridge() (target.Target, int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.bridge) == 0 {
		return target.Target{}, 0
	}
	return tg.bridge[len(tg.bridge)-1], len(tg.bridge)
}

func (tg *targets) serverIDs() []string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	var out []string
	for _, t := range tg.servers {
		out = append(out, t.ID+"="+t.Socket+t.URL)
	}
	return out
}

// withServers gives the launcher a temp store with one remote, active
// when active is true.
func withServers(t *testing.T, remoteURL string, active bool, tg *targets) (func(*Config), *target.Store, target.Target) {
	t.Helper()
	store, err := target.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	rt, err := store.Add("Box", remoteURL)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if err := store.SetActive(rt.ID); err != nil {
			t.Fatal(err)
		}
	}
	return func(c *Config) {
		c.Servers = store
		c.OnTarget = tg.onTarget
		c.OnServer = tg.onServer
	}, store, rt
}

func noServiceCalls(t *testing.T, cli *fakeCLI, allowStatus bool) {
	t.Helper()
	for _, c := range cli.calls() {
		if c == "" || c == "version --json" || (allowStatus && c == "service status --json") {
			continue
		}
		t.Errorf("remote ran %q", c)
	}
}

// TestRemoteReady: a remote target is checked over TCP with its Host and
// no Origin, never managed (no service command), and the local-only
// actions refuse.
func TestRemoteReady(t *testing.T) {
	rs := newRemoteServer(t)
	local := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("status", 0, 0, statusJSON(local.sock, 0, ""))
	tg := &targets{}
	cfg, _, rt := withServers(t, rs.url(t), true, tg)
	h := newLauncher(t, cli, cfg)
	if s := h.l.State(); s.Phase != PhaseStarting || s.Target.ID != rt.ID || *s.Step != StepServer || !strings.Contains(s.Title, "Box") {
		t.Fatalf("initial %+v", s)
	}
	s := h.start(t)
	if s.Phase != PhaseReady || s.Epoch != 1 || s.ServerEpoch != 1 || s.Foreground || s.VersionSkew {
		t.Fatalf("state %+v", s)
	}
	if s.Target != rt || s.Server == nil || s.Server.ID != rt.ID || s.Server.Socket != "" || s.Server.Managed || s.Server.Version != "1.3.0" {
		t.Fatalf("target %+v server %+v", s.Target, s.Server)
	}
	if b, _ := tg.lastBridge(); b != rt {
		t.Fatalf("OnTarget %+v", b)
	}
	rs.mu.Lock()
	host, origin := rs.hosts[0], rs.origin[0]
	rs.mu.Unlock()
	if host != rt.Host() || origin != "" {
		t.Errorf("Host %q Origin %q", host, origin)
	}
	// This machine's socket is looked up read-only for its notifications.
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		waitState(t, h.l, what, func(State) bool { return cond() })
	}
	waitFor("local probe", func() bool {
		ids := tg.serverIDs()
		return len(ids) == 2 && strings.Contains(strings.Join(ids, " "), "local="+local.sock)
	})
	if lt := h.l.LocalTarget(); lt.Socket != local.sock {
		t.Errorf("LocalTarget %+v", lt)
	}
	for name, err := range map[string]error{
		"StartServer":     h.l.StartServer(),
		"RestartServer":   h.l.RestartServer(),
		"SetLoginStartup": h.l.SetLoginStartup(context.Background(), true),
	} {
		if !errors.Is(err, ErrRemote) {
			t.Errorf("%s = %v, want ErrRemote", name, err)
		}
	}
	if err := h.l.Retry(); err != nil {
		t.Fatal(err)
	}
	if s := h.settled(t); s.Phase != PhaseReady || s.ServerEpoch != 2 {
		t.Fatalf("after retry %+v", s)
	}
	if u, err := h.l.T3PublicURL(context.Background()); err != nil || u != "http://remote-t3:3773" {
		t.Errorf("T3PublicURL %q %v", u, err)
	}
	d := h.l.Diagnostics(context.Background())
	if !strings.Contains(d, "remote "+rt.URL) || strings.Contains(d, "service status") {
		t.Errorf("diagnostics:\n%s", d)
	}
	noServiceCalls(t, cli, true)
}

// TestRemoteUnreachable: a remote that does not answer is unreachable
// (polling continues) and becomes ready when it answers.
func TestRemoteUnreachable(t *testing.T) {
	rs := newRemoteServer(t)
	rs.code.Store(503)
	cli := newFakeCLI(t)
	tg := &targets{}
	cfg, _, rt := withServers(t, rs.url(t), true, tg)
	h := newLauncher(t, cli, cfg)
	s := h.start(t)
	if s.Phase != PhaseUnreachable || !strings.Contains(s.Title, "Box") || !strings.Contains(s.Message, rt.URL) || s.Command != "" || s.ServerEpoch != 0 {
		t.Fatalf("state %+v", s)
	}
	if _, n := tg.lastBridge(); n != 0 {
		t.Fatal("unreachable remote proxied")
	}
	rs.code.Store(200)
	s = waitState(t, h.l, "ready after it answers", func(s State) bool { return s.Phase == PhaseReady })
	if s.ServerEpoch != 1 || s.Epoch != 1 {
		t.Fatalf("state %+v", s)
	}
	noServiceCalls(t, cli, true)
}

// TestRemoteRefused: the server's origin policy refusing the hostname
// (403) names the setting at once.
func TestRemoteRefused(t *testing.T) {
	rs := newRemoteServer(t)
	rs.code.Store(403)
	cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
	s := newLauncher(t, newFakeCLI(t), cfg).start(t)
	if s.Phase != PhaseUnreachable || s.Command != "STAGE_ALLOWED_ORIGINS="+rt.URL || !strings.Contains(s.Message, "not allowed") {
		t.Fatalf("state %+v", s)
	}
}

func TestRemoteIncompatible(t *testing.T) {
	rs := newRemoteServer(t)
	rs.set(Health{Status: "ok", Service: "stage", Identity: "stage", Version: "2.0.0", APIVersion: 2, InstanceID: "r1"})
	cfg, _, _ := withServers(t, rs.url(t), true, &targets{})
	s := newLauncher(t, newFakeCLI(t), cfg).start(t)
	if s.Phase != PhaseIncompatible || s.Command != "" || !strings.Contains(s.Message, "Box") || !strings.Contains(s.Fix, "Update Stage") {
		t.Fatalf("state %+v", s)
	}
}

// TestRemoteReconnect: a ready remote that stops answering is
// reconnecting, then unreachable after the window, then ready again
// (epochs bump).
func TestRemoteReconnect(t *testing.T) {
	rs := newRemoteServer(t)
	cfg, _, _ := withServers(t, rs.url(t), true, &targets{})
	h := newLauncher(t, newFakeCLI(t), cfg)
	h.start(t)
	rs.code.Store(503)
	waitState(t, h.l, "reconnecting", func(s State) bool { return s.Phase == PhaseReconnecting })
	waitState(t, h.l, "unreachable", func(s State) bool { return s.Phase == PhaseUnreachable })
	rs.code.Store(200)
	s := waitState(t, h.l, "ready again", func(s State) bool { return s.Phase == PhaseReady })
	if s.Epoch != 2 || s.ServerEpoch != 2 {
		t.Fatalf("state %+v", s)
	}
}

// TestSwitch: local → remote → local. The bridge is unset at once, then
// set to the new server when ready; ServerEpoch restarts per server while
// Epoch keeps counting; the choice persists.
func TestSwitch(t *testing.T) {
	rs := newRemoteServer(t)
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	tg := &targets{}
	cfg, store, rt := withServers(t, rs.url(t), false, tg)
	h := newLauncher(t, cli, cfg)
	if s := h.start(t); s.Phase != PhaseReady || s.Target.ID != target.LocalID || s.ServerEpoch != 1 {
		t.Fatalf("local %+v", s)
	}
	if err := h.l.Switch("s-nope"); !errors.Is(err, target.ErrNotFound) {
		t.Fatalf("Switch unknown = %v", err)
	}
	if err := h.l.Switch(rt.ID); err != nil {
		t.Fatal(err)
	}
	if s := h.l.State(); s.Target.ID != rt.ID || s.ServerEpoch != 0 || s.Server != nil || (s.Phase != PhaseStarting && s.Phase != PhaseReady) {
		t.Fatalf("right after Switch %+v", s)
	}
	s := waitState(t, h.l, "remote ready", func(s State) bool { return s.Phase == PhaseReady && s.Target.ID == rt.ID })
	if s.ServerEpoch != 1 || s.Epoch != 2 || s.Server.ID != rt.ID {
		t.Fatalf("remote %+v", s)
	}
	tg.mu.Lock()
	seq := append([]target.Target{}, tg.bridge...)
	tg.mu.Unlock()
	if len(seq) != 3 || seq[0].Socket != srv.sock || !seq[1].IsZero() || seq[2] != rt {
		t.Fatalf("OnTarget sequence %+v", seq)
	}
	if store.Active().ID != rt.ID {
		t.Fatalf("active not saved: %+v", store.Active())
	}

	// Renaming the active server only renames it.
	if _, err := store.Update(rt.ID, "Big box", rt.URL); err != nil {
		t.Fatal(err)
	}
	if err := h.l.Switch(rt.ID); err != nil {
		t.Fatal(err)
	}
	if s := h.l.State(); s.Phase != PhaseReady || s.Target.Name != "Big box" || s.ServerEpoch != 1 {
		t.Fatalf("after rename %+v", s)
	}

	if err := h.l.Switch(target.LocalID); err != nil {
		t.Fatal(err)
	}
	s = waitState(t, h.l, "local ready", func(s State) bool { return s.Phase == PhaseReady && s.Target.ID == target.LocalID })
	if s.ServerEpoch != 1 || s.Epoch != 3 || s.Server.Socket != srv.sock || !s.Server.Managed {
		t.Fatalf("back local %+v", s)
	}
	if b, _ := tg.lastBridge(); b.ID != target.LocalID || b.Socket != srv.sock {
		t.Fatalf("OnTarget %+v", b)
	}
}

// TestSwitchCancelsStart: switching away from a slow local start ends it;
// its late outcome never overwrites the new server's state.
func TestSwitchCancelsStart(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	cli.write("start.sleep", "3")
	cli.verb("start", 0, 1, reportJSON(1, "boom", "", ""))
	tg := &targets{}
	cfg, _, rt := withServers(t, rs.url(t), false, tg)
	h := newLauncher(t, cli, cfg)
	h.l.Start()
	waitState(t, h.l, "service start running", func(State) bool { return cli.called("service start") })
	if err := h.l.Switch(rt.ID); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, h.l, "remote ready", func(s State) bool { return s.Phase == PhaseReady })
	if s.Target.ID != rt.ID || s.ServerEpoch != 1 {
		t.Fatalf("state %+v", s)
	}
	// Long enough for the killed start to have reported, if it could.
	if s := h.settled(t); s.Phase != PhaseReady || s.Target.ID != rt.ID {
		t.Fatalf("stale outcome won: %+v", s)
	}
}
