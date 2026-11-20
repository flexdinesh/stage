package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// fakeCLI is a shell script standing in for the bundled stage CLI. Each
// `service <verb>` prints <verb>.out (or <verb>.<n>.out for its n-th call),
// <verb>.err to stderr, sleeps <verb>.sleep seconds and exits with
// <verb>.code; `version --json` prints version.json. Every call is
// appended to calls.
type fakeCLI struct {
	t   *testing.T
	dir string
	exe string
}

const fakeScript = `#!/bin/sh
d=$(dirname "$0")
echo "$*" >> "$d/calls"
case "$1" in
version) cat "$d/version.json"; exit "$(cat "$d/version.code" 2>/dev/null || echo 0)";;
service) v=$2;;
*) exit 2;;
esac
n=$(grep -c "^service $v" "$d/calls")
p="$d/$v"
[ -f "$d/$v.$n.code" ] && p="$d/$v.$n"
[ -f "$p.sleep" ] && sleep "$(cat "$p.sleep")"
[ -f "$p.out" ] && cat "$p.out"
[ -f "$p.err" ] && cat "$p.err" >&2
exit "$(cat "$p.code" 2>/dev/null || echo 0)"
`

func newFakeCLI(t *testing.T) *fakeCLI {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "stage")
	if err := os.WriteFile(exe, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeCLI{t: t, dir: dir, exe: exe}
	f.write("version.json", `{"identity":"stage-test","version":"1.2.0","commit":"abc","apiVersion":1}`)
	return f
}

func (f *fakeCLI) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// verb sets what `service <verb>` (or its n-th call, n > 0) answers.
func (f *fakeCLI) verb(verb string, n, code int, out string) {
	name := verb
	if n > 0 {
		name = fmt.Sprintf("%s.%d", verb, n)
	}
	f.write(name+".code", fmt.Sprint(code))
	f.write(name+".out", out)
}

func (f *fakeCLI) calls() []string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f *fakeCLI) called(prefix string) bool {
	for _, c := range f.calls() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// fakeServer answers GET /api/health on a Unix socket.
type fakeServer struct {
	sock   string
	health atomic.Pointer[Health]
	code   atomic.Int32
	mu     sync.Mutex
	srv    *http.Server
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "sl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s := &fakeServer{sock: filepath.Join(dir, "s.sock")}
	s.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "1.2.0", APIVersion: 1, InstanceID: "i1"})
	s.code.Store(200)
	s.start(t)
	t.Cleanup(s.stop)
	return s
}

func (s *fakeServer) set(h Health) { s.health.Store(&h) }

func (s *fakeServer) start(t *testing.T) {
	t.Helper()
	_ = os.Remove(s.sock)
	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != target.SocketHost {
			http.Error(w, "bad host", 403)
			return
		}
		switch r.URL.Path {
		case "/api/health":
			w.WriteHeader(int(s.code.Load()))
			_ = json.NewEncoder(w).Encode(s.health.Load())
		case "/api/harnesses/t3/status":
			_, _ = w.Write([]byte(`{"kind":"t3","connected":true,"details":{"connected":true,"publicUrl":"http://t3.example:3773/"}}`))
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	s.mu.Lock()
	s.srv = srv
	s.mu.Unlock()
}

func (s *fakeServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		_ = s.srv.Close()
		s.srv = nil
	}
}

func fastTiming() Timing {
	return Timing{
		Version: 5 * time.Second, Start: 5 * time.Second, Restart: 5 * time.Second,
		Status: 5 * time.Second, Command: 5 * time.Second,
		StartPoll: 1500 * time.Millisecond, StartPollEvery: 50 * time.Millisecond,
		Ready:       time.Second,
		HealthEvery: 50 * time.Millisecond, HealthTimeout: 500 * time.Millisecond,
		BackoffMin: 20 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
		ReconnectWindow: 300 * time.Millisecond,
	}
}

type harness struct {
	l       *Launcher
	cli     *fakeCLI
	sockets chan string
}

func newLauncher(t *testing.T, cli *fakeCLI, mutate func(*Config)) *harness {
	t.Helper()
	h := &harness{cli: cli, sockets: make(chan string, 16)}
	cfg := Config{
		CLI: cli.exe, AppName: "Stage Test", AppVersion: "1.2.0",
		TempDirs: []string{"/nonexistent-temp"},
		Timing:   fastTiming(),
		OnTarget: func(t target.Target) { h.sockets <- t.Socket },
		Logf:     t.Logf,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h.l = New(cfg)
	t.Cleanup(h.l.Close)
	return h
}

func (h *harness) start(t *testing.T) State {
	t.Helper()
	h.l.Start()
	return h.settled(t)
}

// settled waits until the launcher left the starting phase.
func (h *harness) settled(t *testing.T) State {
	t.Helper()
	return waitState(t, h.l, "settled", func(s State) bool { return s.Phase != PhaseStarting && !s.Busy })
}

func waitState(t *testing.T, l *Launcher, what string, cond func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		s := l.State()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			b, _ := json.MarshalIndent(s, "", "  ")
			t.Fatalf("timed out waiting for %s; state %s", what, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func statusJSON(sock string, code int, extra string) string {
	state := map[int]string{0: "running", 1: "failed", 3: "starting", 4: "stopped", 5: "stopped"}[code]
	ready := code == 0
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"schema":1,"identity":"stage-test","state":%q,"ready":%v,"socket":{"path":%q,"exists":true,"locked":true},`+
		`"lock":{"held":true,"holder":{"pid":4242,"managed":%v},"ownedByService":%v},"problems":[]%s}`, state, ready, sock, ready, ready, extra)
}

func reportJSON(code int, msg, fix, status string) string {
	if status == "" {
		status = "null"
	}
	return fmt.Sprintf(`{"schema":1,"command":"start","ok":%v,"code":%d,"message":%q,"fix":%q,"warnings":[],"notes":[],"logTail":["line 1","line 2"],"status":%s}`,
		code == 0, code, msg, fix, status)
}

func TestReady(t *testing.T) {
	srv := newFakeServer(t)
	pub := "https://stage.example.ts.net"
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "1.2.0", APIVersion: 1, InstanceID: "i1", PublicURL: &pub})
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	h := newLauncher(t, cli, nil)
	s := h.start(t)
	if s.Phase != PhaseReady || s.Step != nil || s.Epoch != 1 || s.VersionSkew || s.Foreground || s.Dev {
		t.Fatalf("state %+v", s)
	}
	if s.Server == nil || !s.Server.Managed || s.Server.Socket != srv.sock || s.Server.PublicURL == nil || *s.Server.PublicURL != pub || s.Server.Identity != "stage-test" {
		t.Fatalf("server %+v", s.Server)
	}
	if s.App.Identity != "stage-test" || s.App.APIVersion != 1 || s.App.Name != "Stage Test" {
		t.Fatalf("app %+v", s.App)
	}
	if got := <-h.sockets; got != srv.sock {
		t.Fatalf("OnTarget %q", got)
	}
	if s.Target.ID != target.LocalID || s.ServerEpoch != 1 || s.Server.ID != target.LocalID {
		t.Fatalf("target %+v serverEpoch %d server %+v", s.Target, s.ServerEpoch, s.Server)
	}
	if !cli.called("service start --exe " + cli.exe + " --wait 30s --json") {
		t.Fatalf("calls %v", cli.calls())
	}
	if u, err := h.l.T3PublicURL(context.Background()); err != nil || u != "http://t3.example:3773" {
		t.Fatalf("T3PublicURL %q %v", u, err)
	}
}

// TestStateJSON: the State JSON has exactly the TS HostState fields.
func TestStateJSON(t *testing.T) {
	b, err := json.Marshal(New(Config{TempDirs: []string{}}).State())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"phase", "step", "title", "message", "fix", "command", "problems", "logTail", "app", "server", "foreground", "versionSkew", "target", "epoch", "serverEpoch", "busy", "dev"}
	if len(m) != len(want) {
		t.Fatalf("fields %v, want %v", m, want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %s", k, b)
		}
	}
	if m["server"] != nil || m["step"] != "service" {
		t.Fatalf("initial %s", b)
	}
	srv, _ := json.Marshal(Server{})
	if string(srv) != `{"id":"","version":"","apiVersion":0,"identity":"","publicUrl":null,"socket":"","managed":false,"pid":null}` {
		t.Fatalf("server JSON %s", srv)
	}
	tg, _ := json.Marshal(m["target"])
	if string(tg) != `{"id":"local","kind":"local","name":"This machine","url":""}` {
		t.Fatalf("target JSON %s", tg)
	}
}

func TestFailedExit1(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("start", 0, 1, reportJSON(1, "stage server exited", "see the log", `{"state":"failed","problems":[{"code":"start_limit_hit","message":"gave up","fix":"retry"}]}`))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseFailed || s.Message != "stage server exited" || s.Fix != "see the log" {
		t.Fatalf("state %+v", s)
	}
	if strings.Join(s.LogTail, "|") != "line 1|line 2" || len(s.Problems) != 1 || s.Problems[0].Code != "start_limit_hit" {
		t.Fatalf("logTail %v problems %v", s.LogTail, s.Problems)
	}
}

// TestFailedNotJSON: a usage error (exit 2) or any output that is not a
// report fails, keeping stderr.
func TestFailedNotJSON(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("start", 0, 2, "")
	cli.write("start.err", "usage: stage service start\nunknown flag --wait\n")
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseFailed || !strings.Contains(s.Message, "exited 2") || len(s.LogTail) != 2 || s.LogTail[1] != "unknown flag --wait" {
		t.Fatalf("state %+v", s)
	}
}

func TestFailedNoStatus(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", ""))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseFailed || !strings.Contains(s.Message, "without a socket") {
		t.Fatalf("state %+v", s)
	}
}

// TestWaitExpired: exit 3 polls status until ready.
func TestWaitExpired(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 3, reportJSON(3, "still starting", "", statusJSON(srv.sock, 3, "")))
	cli.verb("status", 1, 3, statusJSON(srv.sock, 3, ""))
	cli.verb("status", 2, 3, statusJSON(srv.sock, 3, ""))
	cli.verb("status", 0, 0, statusJSON(srv.sock, 0, ""))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseReady {
		t.Fatalf("state %+v", s)
	}
	n := 0
	for _, c := range cli.calls() {
		if c == "service status --json" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("status calls %d (%v)", n, cli.calls())
	}
}

// TestWaitExpiredTimeout: a service that stays starting fails after the
// bounded poll, with the log tail.
func TestWaitExpiredTimeout(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 3, reportJSON(3, "still starting", "", ""))
	cli.verb("status", 0, 3, statusJSON(srv.sock, 3, ""))
	cli.verb("logs", 0, 0, "log a\nlog b\n")
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseFailed || !strings.Contains(s.Title, "too long") || strings.Join(s.LogTail, "|") != "log a|log b" {
		t.Fatalf("state %+v", s)
	}
}

// TestForegroundHealthy: exit 5 with a foreground server that answers on
// the socket with this identity and API connects to it.
func TestForegroundHealthy(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 5, reportJSON(5, "data dir held by pid 4242", "stop it", statusJSON(srv.sock, 5, `"x":1`)))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseReady || !s.Foreground || s.Server == nil || s.Server.Managed || s.Server.PID == nil || *s.Server.PID != 4242 {
		t.Fatalf("state %+v server %+v", s, s.Server)
	}
}

// TestForegroundUnusable: a foreground holder that does not answer on the
// desktop socket (TCP-only by default) is a conflict with the fix.
func TestForegroundUnusable(t *testing.T) {
	srv := newFakeServer(t)
	srv.stop()
	cli := newFakeCLI(t)
	cli.verb("start", 0, 5, reportJSON(5, "data dir held by pid 4242", "stop it", statusJSON(srv.sock, 5, "")))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseConflict || !strings.Contains(s.Message, "pid 4242") || s.Command != "stage serve --listen unix,127.0.0.1:7380" || s.Fix == "" {
		t.Fatalf("state %+v", s)
	}
}

func TestForegroundOtherAPI(t *testing.T) {
	srv := newFakeServer(t)
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "0.9.0", APIVersion: 2, InstanceID: "i1"})
	cli := newFakeCLI(t)
	cli.verb("start", 0, 5, reportJSON(5, "held", "", statusJSON(srv.sock, 5, "")))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseConflict || !strings.Contains(s.Message, "API v2") {
		t.Fatalf("state %+v", s)
	}
}

func TestUnsupported(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("start", 0, 6, reportJSON(6, "no systemd user manager", "log in through a pam_systemd session", `{"state":"unknown","problems":[{"code":"manager_unavailable","message":"no manager","fix":"log in"}]}`))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseUnsupported || s.Message != "no systemd user manager" || s.Fix != "log in through a pam_systemd session" ||
		!strings.HasSuffix(s.Command, " serve --listen unix") || len(s.Problems) != 1 {
		t.Fatalf("state %+v", s)
	}
}

func TestMisconfigured(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("start", 0, 7, reportJSON(7, "service.env is invalid", "fix service.env", `{"state":"stopped","problems":[{"code":"service_env_invalid","message":"bad key FOO","fix":"remove FOO"}]}`))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseMisconfigured || s.Fix != "fix service.env" || len(s.Problems) != 1 || s.Problems[0].Fix != "remove FOO" {
		t.Fatalf("state %+v", s)
	}
}

// TestIncompatible: another API version blocks; RestartServer restarts
// onto the bundled CLI and connects.
func TestIncompatible(t *testing.T) {
	srv := newFakeServer(t)
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "2.0.0", APIVersion: 2, InstanceID: "i1"})
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	cli.verb("restart", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	h := newLauncher(t, cli, nil)
	s := h.start(t)
	if s.Phase != PhaseIncompatible || !strings.Contains(s.Message, "API v2") || s.Server == nil || s.Server.APIVersion != 2 {
		t.Fatalf("state %+v", s)
	}
	select {
	case got := <-h.sockets:
		t.Fatalf("incompatible server proxied: %q", got)
	default:
	}
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "1.2.0", APIVersion: 1, InstanceID: "i2"})
	if err := h.l.RestartServer(); err != nil {
		t.Fatal(err)
	}
	s = h.settled(t)
	if s.Phase != PhaseReady || !cli.called("service restart --exe "+cli.exe+" --wait 30s --json") {
		t.Fatalf("after restart %+v calls %v", s, cli.calls())
	}
}

func TestVersionSkew(t *testing.T) {
	srv := newFakeServer(t)
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "1.1.0", APIVersion: 1, InstanceID: "i1"})
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseReady || !s.VersionSkew || s.Server.Version != "1.1.0" {
		t.Fatalf("state %+v", s)
	}
}

// TestHealth503: a server whose health fails (database down) is not
// ready.
func TestHealth503(t *testing.T) {
	srv := newFakeServer(t)
	srv.code.Store(503)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	s := newLauncher(t, cli, nil).start(t)
	if s.Phase != PhaseFailed || !strings.Contains(s.Message, "503") {
		t.Fatalf("state %+v", s)
	}
}

func TestStartTimeout(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, "")
	cli.write("start.sleep", "5")
	h := newLauncher(t, cli, func(c *Config) { c.Timing.Start = 300 * time.Millisecond })
	s := h.start(t)
	if s.Phase != PhaseFailed || !strings.Contains(s.Message, "did not finish within 300ms") {
		t.Fatalf("state %+v", s)
	}
}

// TestReconnectThenStopped: a ready server that goes away reconnects, then
// after the window asks status (stopped); when it comes back the state is
// ready again with a new epoch.
func TestReconnectThenStopped(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	cli.verb("status", 0, 4, statusJSON(srv.sock, 4, ""))
	h := newLauncher(t, cli, nil)
	if s := h.start(t); s.Phase != PhaseReady || s.Epoch != 1 {
		t.Fatalf("state %+v", s)
	}
	srv.stop()
	waitState(t, h.l, "reconnecting", func(s State) bool { return s.Phase == PhaseReconnecting })
	s := waitState(t, h.l, "stopped", func(s State) bool { return s.Phase == PhaseStopped })
	if s.Fix == "" || s.Server == nil {
		t.Fatalf("stopped %+v", s)
	}
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "1.2.0", APIVersion: 1, InstanceID: "i2"})
	srv.start(t)
	s = waitState(t, h.l, "ready again", func(s State) bool { return s.Phase == PhaseReady })
	if s.Epoch != 2 {
		t.Fatalf("epoch %d", s.Epoch)
	}
}

// TestNewInstanceBumpsEpoch: a restart between two checks (new instance
// id) counts as a reconnect.
func TestNewInstanceBumpsEpoch(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	h := newLauncher(t, cli, nil)
	h.start(t)
	srv.set(Health{Status: "ok", Service: "stage", Identity: "stage-test", Version: "1.3.0", APIVersion: 1, InstanceID: "i2"})
	s := waitState(t, h.l, "epoch 2", func(s State) bool { return s.Epoch == 2 })
	if s.Phase != PhaseReady || !s.VersionSkew {
		t.Fatalf("state %+v", s)
	}
}

// TestDevOverrideIgnoredInRelease: STAGE_DESKTOP_SOCKET does nothing in a
// release build; the service path runs.
func TestDevOverrideIgnoredInRelease(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 7, reportJSON(7, "refused", "", ""))
	s := newLauncher(t, cli, func(c *Config) { c.Dev, c.DevSocket = false, srv.sock }).start(t)
	if s.Phase != PhaseMisconfigured || s.Dev || !cli.called("service start") {
		t.Fatalf("state %+v calls %v", s, cli.calls())
	}
}

// TestDevOverride: a development build talks to the dev server without
// any service command.
func TestDevOverride(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	h := newLauncher(t, cli, func(c *Config) { c.Dev, c.DevSocket = true, srv.sock })
	s := h.start(t)
	if s.Phase != PhaseReady || !s.Dev || !s.Foreground || s.Server.Managed || s.App.Identity != "stage-test" {
		t.Fatalf("state %+v", s)
	}
	if _, err := os.Stat(filepath.Join(cli.dir, "calls")); err == nil {
		t.Fatalf("CLI ran in dev: %v", cli.calls())
	}
	if err := h.l.RestartServer(); err == nil {
		t.Fatal("RestartServer in dev: want an error")
	}
	if d := h.l.Diagnostics(context.Background()); !strings.Contains(d, "STAGE_DESKTOP_SOCKET") || !strings.Contains(d, "identity stage-test") || cli.called("service") {
		t.Fatalf("dev diagnostics %q / calls %v", d, cli.calls())
	}
}

// TestDevWithoutCLI: no bundled CLI in a dev run; the server's identity
// stands in.
func TestDevWithoutCLI(t *testing.T) {
	srv := newFakeServer(t)
	h := newLauncher(t, &fakeCLI{exe: "/nonexistent/stage"}, func(c *Config) { c.Dev, c.DevSocket = true, srv.sock })
	s := h.start(t)
	if s.Phase != PhaseReady || s.App.Identity != "stage-test" || s.App.APIVersion != 1 {
		t.Fatalf("state %+v", s)
	}
}

// TestUnstableLocation: an app run from a temporary dir is refused before
// any CLI call.
func TestUnstableLocation(t *testing.T) {
	cli := newFakeCLI(t) // in t.TempDir(), under the system temp dir
	s := newLauncher(t, cli, func(c *Config) { c.TempDirs = nil }).start(t)
	if s.Phase != PhaseMisconfigured || !strings.Contains(s.Message, "temporary directory") || s.Fix == "" {
		t.Fatalf("state %+v", s)
	}
	if _, err := os.Stat(filepath.Join(cli.dir, "calls")); err == nil {
		t.Fatalf("CLI ran: %v", cli.calls())
	}
}

func TestValidateLocation(t *testing.T) {
	temp := []string{"/tmp", "/var/folders"}
	for exe, ok := range map[string]bool{
		"/opt/stage-desktop/bin/stage":                         true,
		"/Applications/Stage.app/Contents/Resources/bin/stage": true,
		"/home/u/.local/share/stage-desktop/bin/stage":         true,
		"bin/stage":        false,
		"/tmp/x/bin/stage": false,
		"/var/folders/ab/T/AppTranslocation/X/d/Stage.app/Contents/Resources/bin/stage":    false,
		"/private/var/folders/x/AppTranslocation/Y/Stage.app/Contents/Resources/bin/stage": false,
		"/Users/u/.Trash/Stage.app/Contents/Resources/bin/stage":                           false,
		"/home/u/.cache/go-build/ab/exe/stage":                                             false,
	} {
		if err := ValidateLocation(exe, temp); (err == nil) != ok {
			t.Errorf("ValidateLocation(%q) = %v, want ok %v", exe, err, ok)
		}
	}
}

func TestBusy(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	h := newLauncher(t, cli, nil)
	h.start(t)
	cli.write("start.sleep", "1")
	errs := make(chan error, 2)
	go func() { errs <- h.l.Retry() }()
	waitState(t, h.l, "busy", func(s State) bool { return s.Busy })
	if err := h.l.StartServer(); err != ErrBusy {
		t.Fatalf("concurrent StartServer: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if s := h.settled(t); s.Phase != PhaseReady || s.Epoch != 2 {
		t.Fatalf("after retry %+v", s)
	}
}

func TestDiagnostics(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ready", "", statusJSON(srv.sock, 0, "")))
	cli.verb("status", 0, 0, `{"state":"running","t3":{"token":"s3cr3t"},"definition":{"pinnedEnv":[{"key":"T3_ADMIN_TOKEN","value":"hunter2"},{"key":"XDG_DATA_HOME","value":"/x"}]}}`)
	cli.verb("logs", 0, 0, "first\nlast\n")
	h := newLauncher(t, cli, nil)
	h.start(t)
	d := h.l.Diagnostics(context.Background())
	for _, want := range []string{"Stage Test 1.2.0", "identity stage-test", "api v1", srv.sock, "state      ready", `"/x"`, "first\nlast"} {
		if !strings.Contains(d, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, d)
		}
	}
	for _, secret := range []string{"s3cr3t", "hunter2"} {
		if strings.Contains(d, secret) {
			t.Errorf("diagnostics leak %q:\n%s", secret, d)
		}
	}
}

func TestLoginStartup(t *testing.T) {
	cli := newFakeCLI(t)
	cli.verb("enable", 0, 0, `{"code":0}`)
	cli.verb("disable", 0, 7, `{"code":7,"message":"definition is shadowed"}`)
	var on atomic.Bool
	h := newLauncher(t, cli, func(c *Config) {
		c.OpenAtLogin = on.Load
		c.SetOpenAtLogin = func(v bool) error { on.Store(v); return nil }
	})
	if err := h.l.SetLoginStartup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !h.l.LoginStartup() || !cli.called("service enable --exe "+cli.exe+" --json") {
		t.Fatalf("enable: %v %v", h.l.LoginStartup(), cli.calls())
	}
	err := h.l.SetLoginStartup(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "definition is shadowed") || h.l.LoginStartup() {
		t.Fatalf("disable: %v", err)
	}
}
