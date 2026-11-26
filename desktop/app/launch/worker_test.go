package launch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// workerJSON is a `worker service status --json` document: the service
// state, readiness, and the worker (joined to url, channel state, last
// error); login is the definition's login startup.
func workerJSON(state string, ready, joined bool, url, channel, lastErr string, login bool) string {
	w := `{"config":"/w/worker.json","joined":false,"holder":null,"ownedByService":false}`
	if joined {
		w = fmt.Sprintf(`{"config":"/w/worker.json","joined":true,"url":%q,"machineId":4,"holder":{"pid":77},"ownedByService":%v,"state":%q,"lastError":%q}`,
			url, ready, channel, lastErr)
	}
	return fmt.Sprintf(`{"schema":1,"identity":"stage-test","role":"worker","state":%q,"ready":%v,"definition":{"present":%v,"loginStartup":%v},"worker":%s,"problems":[]}`,
		state, ready, joined, login, w)
}

const notJoinedJSON = `{"schema":1,"identity":"stage-test","role":"worker","state":"not-installed","ready":false,"definition":{"present":false},` +
	`"worker":{"config":"/w/worker.json","joined":false,"holder":null,"ownedByService":false},"problems":[{"code":"worker_not_joined","message":"no worker config","fix":"join"}]}`

// joinedTo makes the fake worker service report an online worker of url.
func joinedTo(cli *fakeCLI, url string, login bool) {
	cli.verb("worker-status", 0, 0, workerJSON("running", true, true, url, "online", "", login))
}

// callsSince is the fake CLI's calls from the first one starting with
// from, without status reads.
func callsSince(cli *fakeCLI, from string) []string {
	var out []string
	on := false
	for _, c := range cli.calls() {
		if strings.HasPrefix(c, from) {
			on = true
		}
		if on && !strings.HasSuffix(c, "status --json") {
			out = append(out, c)
		}
	}
	return out
}

type joinedLog struct {
	mu  sync.Mutex
	got []bool
}

func (j *joinedLog) on(v bool) {
	j.mu.Lock()
	j.got = append(j.got, v)
	j.mu.Unlock()
}

func (j *joinedLog) values() []bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.got)
}

// TestWorkerLocalTarget: while this machine is a worker, its own target
// shows the worker phase: no service start, no socket, nothing bridged.
func TestWorkerLocalTarget(t *testing.T) {
	cli := newFakeCLI(t)
	joinedTo(cli, "https://stage.example.com", false)
	bridged := 0
	h := newLauncher(t, cli, func(c *Config) { c.OnTarget = func(target.Target) { bridged++ } })
	s := h.start(t)
	if s.Phase != PhaseWorker || s.Title != "This machine is a worker of stage.example.com" || s.Worker == nil {
		t.Fatalf("state %+v", s)
	}
	if w := s.Worker; w.Status != WorkerOnline || w.ServerID != nil || w.MachineID != 4 || w.URL != "https://stage.example.com" {
		t.Fatalf("worker %+v", w)
	}
	if !strings.Contains(s.Message, "data stays") || !strings.Contains(s.Fix, "Leave stage.example.com") {
		t.Errorf("copy %+v", s)
	}
	if cli.called("service start") || cli.called("service status") || bridged != 0 {
		t.Fatalf("calls %v, bridged %d", cli.calls(), bridged)
	}
	if err := h.l.Retry(); err != nil {
		t.Fatal(err)
	}
	if s := h.settled(t); s.Phase != PhaseWorker || cli.called("service start") {
		t.Fatalf("after retry %+v %v", s, cli.calls())
	}
}

// TestWorkerRemoteActive: a worker of the active remote matches its saved
// server, and this machine's socket is never probed (it is the worker's
// gateway), so no local notification stream starts.
func TestWorkerRemoteActive(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	tg := &targets{}
	cfg, _, rt := withServers(t, rs.url(t), true, tg)
	joinedTo(cli, rt.URL, false)
	h := newLauncher(t, cli, cfg)
	if s := h.start(t); s.Phase != PhaseReady {
		t.Fatalf("state %+v", s)
	}
	w := waitState(t, h.l, "worker", func(s State) bool { return s.Worker != nil }).Worker
	if w.ServerID == nil || *w.ServerID != rt.ID || w.ServerName != "Box" || w.Status != WorkerOnline {
		t.Fatalf("worker %+v", w)
	}
	time.Sleep(100 * time.Millisecond)
	for _, id := range tg.serverIDs() {
		if strings.HasPrefix(id, "local=") {
			t.Fatalf("local probed: %v", tg.serverIDs())
		}
	}
	if cli.called("service status") {
		t.Fatalf("calls %v", cli.calls())
	}
}

// TestWorkerPublicURLMatch: a worker joined through the server's public
// URL matches the active remote saved under another address.
func TestWorkerPublicURLMatch(t *testing.T) {
	rs := newRemoteServer(t)
	pub := "https://stage.example.com"
	h0 := *rs.health.Load()
	h0.PublicURL = &pub
	rs.set(h0)
	cli := newFakeCLI(t)
	cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
	joinedTo(cli, pub, false)
	h := newLauncher(t, cli, cfg)
	h.start(t)
	if err := h.l.Retry(); err != nil { // a refresh after the server (and its public URL) is known
		t.Fatal(err)
	}
	w := waitState(t, h.l, "matched worker", func(s State) bool { return s.Worker != nil && s.Worker.ServerID != nil }).Worker
	if *w.ServerID != rt.ID || w.ServerName != "Box" {
		t.Fatalf("worker %+v", w)
	}
}

// TestJoinWorker: join, uninstall this machine's server service, start
// the worker service; login startup moves to the worker.
func TestJoinWorker(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	tg := &targets{}
	cfg, _, rt := withServers(t, rs.url(t), true, tg)
	cli.verb("worker-status", 0, 0, notJoinedJSON)
	cli.after("worker-status", "worker join", 0, workerJSON("running", true, true, rt.URL, "online", "", false))
	cli.verb("status", 0, 0, statusJSON("/run/s.sock", 0, `"definition":{"loginStartup":true}`))
	var joined joinedLog
	h := newLauncher(t, cli, func(c *Config) { cfg(c); c.OnWorker = joined.on })
	h.start(t)
	link := rt.URL + "/join#stage_pc_1_secret"
	if err := h.l.JoinWorker(context.Background(), link); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"worker join --json " + link,
		"service uninstall --wait 30s --json",
		"worker service start --exe " + cli.exe + " --wait 30s --json",
		"worker service enable --exe " + cli.exe + " --json",
	}
	if got := callsSince(cli, "worker join"); !slices.Equal(got, want) {
		t.Fatalf("calls\n%q\nwant\n%q", got, want)
	}
	s := h.l.State()
	if s.Worker == nil || s.Worker.Status != WorkerOnline || *s.Worker.ServerID != rt.ID || s.Phase != PhaseReady {
		t.Fatalf("state %+v worker %+v", s, s.Worker)
	}
	if got := joined.values(); !slices.Equal(got, []bool{true}) {
		t.Fatalf("OnWorker %v", got)
	}
	if err := h.l.JoinWorker(context.Background(), link); err == nil || !strings.Contains(err.Error(), "already a worker of Box") {
		t.Fatalf("second join: %v", err)
	}
}

// TestJoinWorkerNeedsRemote: joining is for the active remote only.
func TestJoinWorkerNeedsRemote(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	cli.verb("start", 0, 0, reportJSON(0, "ok", "", statusJSON(srv.sock, 0, "")))
	h := newLauncher(t, cli, nil)
	h.start(t)
	if err := h.l.JoinWorker(context.Background(), "https://x/join#c"); err == nil || cli.called("worker join") {
		t.Fatalf("join on this machine: %v %v", err, cli.calls())
	}
}

// TestJoinWorkerUndo: when this machine's server cannot be stopped, or no
// service manager can run the worker, the join is undone.
func TestJoinWorkerUndo(t *testing.T) {
	for name, tc := range map[string]struct {
		setup   func(cli *fakeCLI)
		want    string
		started bool
	}{
		"server cannot stop": {
			setup: func(cli *fakeCLI) {
				cli.verb("uninstall", 0, 7, reportJSON(7, "stage.service was not written by `stage service`; leaving it untouched", "", ""))
			},
			want: "could not be stopped: stage.service was not written",
		},
		"no service manager": {
			setup: func(cli *fakeCLI) {
				cli.verb("worker-start", 0, 6, reportJSON(6, "systemd --user is not reachable", "", ""))
			},
			want:    "needs a per-user service manager",
			started: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rs := newRemoteServer(t)
			cli := newFakeCLI(t)
			cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
			cli.verb("worker-status", 0, 0, notJoinedJSON)
			tc.setup(cli)
			h := newLauncher(t, cli, cfg)
			h.start(t)
			err := h.l.JoinWorker(context.Background(), rt.URL+"/join#c")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v", err)
			}
			if !cli.called("worker leave --json") || cli.called("worker service start") != tc.started {
				t.Fatalf("calls %v", cli.calls())
			}
			if h.l.Worker() != nil {
				t.Fatalf("worker %+v", h.l.Worker())
			}
		})
	}
}

// TestJoinWorkerFails: a refused code changes nothing.
func TestJoinWorkerFails(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
	cli.verb("worker-status", 0, 0, notJoinedJSON)
	cli.write("worker-join.code", "1")
	cli.write("worker-join.err", "error: pairing code expired or already used\n")
	h := newLauncher(t, cli, cfg)
	h.start(t)
	err := h.l.JoinWorker(context.Background(), rt.URL+"/join#c")
	if err == nil || err.Error() != "join: pairing code expired or already used" {
		t.Fatalf("err %v", err)
	}
	if cli.called("service uninstall") || cli.called("worker service start") {
		t.Fatalf("calls %v", cli.calls())
	}
}

// TestLeaveWorker: from the worker screen, leaving stops the worker,
// leaves the main server, moves login startup back and starts this
// machine's server.
func TestLeaveWorker(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	joinedTo(cli, "https://stage.example.com", true)
	cli.after("worker-status", "worker leave", 0, notJoinedJSON)
	cli.verb("start", 0, 0, reportJSON(0, "ok", "", statusJSON(srv.sock, 0, "")))
	var joined joinedLog
	h := newLauncher(t, cli, func(c *Config) { c.OnWorker = joined.on })
	if s := h.start(t); s.Phase != PhaseWorker {
		t.Fatalf("state %+v", s)
	}
	if err := h.l.LeaveWorker(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"worker service uninstall --wait 30s --json",
		"worker leave --json",
		"service enable --exe " + cli.exe + " --json",
	}
	if got := callsSince(cli, "worker service uninstall")[:3]; !slices.Equal(got, want) {
		t.Fatalf("calls\n%q\nwant\n%q", got, want)
	}
	s := waitState(t, h.l, "this machine's server", func(s State) bool { return s.Phase == PhaseReady })
	if s.Worker != nil || s.Server == nil || s.Server.Socket != srv.sock {
		t.Fatalf("state %+v", s)
	}
	if got := joined.values(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("OnWorker %v", got)
	}
}

// TestLeaveWorkerUnreachable: when the main server cannot be told, the
// error says why and leave --local forgets the worker here.
func TestLeaveWorkerUnreachable(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
	joinedTo(cli, rt.URL, false)
	cli.after("worker-status", "worker leave --local", 0, notJoinedJSON)
	cli.write("worker-leave.code", "1")
	cli.write("worker-leave.err", "error: leave: dial tcp 203.0.113.7:443: connect: connection refused\n")
	h := newLauncher(t, cli, cfg)
	h.start(t)
	waitState(t, h.l, "worker", func(s State) bool { return s.Worker != nil })
	err := h.l.LeaveWorker(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "connection refused") || h.l.Worker() == nil {
		t.Fatalf("err %v worker %+v", err, h.l.Worker())
	}
	cli.write("worker-leave.code", "0")
	cli.write("worker-leave.err", "")
	if err := h.l.LeaveWorker(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !cli.called("worker leave --local --json") || h.l.Worker() != nil {
		t.Fatalf("calls %v worker %+v", cli.calls(), h.l.Worker())
	}
	if cli.called("service enable") || cli.called("service start") {
		t.Fatalf("remote active: this machine's server touched: %v", cli.calls())
	}
}

// TestRestartWorker restarts the worker service onto the bundled CLI.
func TestRestartWorker(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
	cli.verb("worker-status", 0, 0, workerJSON("failed", false, true, rt.URL, "", "", false))
	cli.after("worker-status", "worker service restart", 0, workerJSON("running", true, true, rt.URL, "online", "", false))
	h := newLauncher(t, cli, cfg)
	h.start(t)
	if w := waitState(t, h.l, "worker", func(s State) bool { return s.Worker != nil }).Worker; w.Status != WorkerFailed {
		t.Fatalf("worker %+v", w)
	}
	if err := h.l.RestartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cli.called("worker service restart --exe "+cli.exe+" --wait 30s --json") || h.l.Worker().Status != WorkerOnline {
		t.Fatalf("calls %v worker %+v", cli.calls(), h.l.Worker())
	}
}

// TestWorkerLoginStartup: while a worker, login startup is the worker
// service's, also with a remote active.
func TestWorkerLoginStartup(t *testing.T) {
	rs := newRemoteServer(t)
	cli := newFakeCLI(t)
	cfg, _, rt := withServers(t, rs.url(t), true, &targets{})
	joinedTo(cli, rt.URL, false)
	h := newLauncher(t, cli, cfg)
	h.start(t)
	waitState(t, h.l, "worker", func(s State) bool { return s.Worker != nil })
	if err := h.l.SetLoginStartup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := h.l.SetLoginStartup(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !cli.called("worker service enable --exe "+cli.exe+" --json") || !cli.called("worker service disable --json") || cli.called("service enable") {
		t.Fatalf("calls %v", cli.calls())
	}
	d := h.l.Diagnostics(context.Background())
	if !strings.Contains(d, "worker     machine 4 of Box") || !strings.Contains(d, "stage worker service status --json") {
		t.Fatalf("diagnostics:\n%s", d)
	}
}

// TestWorkerStatusMapping: the worker service's status reads as the
// page's worker status.
func TestWorkerStatusMapping(t *testing.T) {
	u := "https://stage.example.com"
	for _, tc := range []struct {
		doc  string
		want WorkerStatus
		msg  string
	}{
		{workerJSON("running", true, true, u, "online", "", false), WorkerOnline, ""},
		{workerJSON("running", true, true, u, "connecting", "dial tcp: refused", false), WorkerOffline, "Not connected to stage.example.com: dial tcp: refused"},
		{workerJSON("stopped", false, true, u, "stopped", "", false), WorkerOffline, "The worker service is not running."},
		{workerJSON("not-installed", false, true, u, "", "", false), WorkerOffline, "The worker service is not running."},
		{workerJSON("starting", false, true, u, "", "", false), WorkerStarting, ""},
		{workerJSON("failed", false, true, u, "", "", false), WorkerFailed, "The worker service failed."},
		{workerJSON("running", false, true, u, "needs_update", "Update Stage on the main machine.", false), WorkerNeedsUpdate, "Update Stage on the main machine."},
		{workerJSON("stopped", false, true, u, "revoked", "", false), WorkerRemoved, "stage.example.com removed this machine."},
	} {
		cli := newFakeCLI(t)
		cli.verb("worker-status", 0, 0, tc.doc)
		l := New(Config{CLI: cli.exe, TempDirs: []string{}, Timing: fastTiming()})
		w := l.refreshWorker(context.Background())
		l.Close()
		if w == nil || w.Status != tc.want || w.Message != tc.msg {
			t.Errorf("%s: worker %+v, want %s %q", tc.doc, w, tc.want, tc.msg)
		}
	}
	cli := newFakeCLI(t)
	cli.verb("worker-status", 0, 0, notJoinedJSON)
	l := New(Config{CLI: cli.exe, TempDirs: []string{}, Timing: fastTiming()})
	defer l.Close()
	if w := l.refreshWorker(context.Background()); w != nil {
		t.Fatalf("not joined: %+v", w)
	}
}

// TestWorkerDev: a development run has no worker.
func TestWorkerDev(t *testing.T) {
	srv := newFakeServer(t)
	cli := newFakeCLI(t)
	joinedTo(cli, "https://stage.example.com", false)
	h := newLauncher(t, cli, func(c *Config) { c.Dev, c.DevSocket = true, srv.sock })
	if s := h.start(t); s.Phase != PhaseReady || s.Worker != nil {
		t.Fatalf("state %+v", s)
	}
	if err := h.l.JoinWorker(context.Background(), "x"); err != ErrWorkerDev || cli.called("worker") {
		t.Fatalf("join %v, calls %v", err, cli.calls())
	}
}
