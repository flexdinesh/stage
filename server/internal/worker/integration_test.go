package worker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/worker"
)

// isolate points every path Stage derives at temp dirs (never the user's
// data dir or runtime dir) and returns a short dir for socket paths.
func isolate(t *testing.T) string {
	t.Helper()
	short, err := os.MkdirTemp("", "wk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("STAGE_DATA_DIR", "")
	t.Setenv(config.WorkerConfigEnv, "")
	rt := filepath.Join(short, "rt")
	if err := os.Mkdir(rt, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("NOTIFY_SOCKET", "")
	t.Setenv("T3_FAKE", "")
	return short
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// mainServer is a foreground main server on a temp data dir, a temp socket
// and a loopback TCP port (its STAGE_PUBLIC_URL).
type mainServer struct {
	t      *testing.T
	cfg    config.Config
	url    string
	cancel context.CancelFunc
	done   chan error
}

func newMain(t *testing.T, short string) *mainServer {
	t.Helper()
	addr := freePort(t)
	ls, err := listen.ParseList("unix:" + filepath.Join(short, "main.sock") + "," + addr)
	if err != nil {
		t.Fatal(err)
	}
	m := &mainServer{t: t, url: "http://" + addr, cfg: config.Config{
		Listen: ls, DataDir: filepath.Join(t.TempDir(), "main"), PublicURL: "http://" + addr,
		T3URL: "http://127.0.0.1:1", T3PublicURL: "http://t3.test", T3ClientName: "stage",
	}}
	m.start()
	t.Cleanup(func() { m.stop() })
	return m
}

func (m *mainServer) start() {
	m.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	m.cancel, m.done = cancel, make(chan error, 1)
	go func() {
		m.done <- serve.Run(ctx, serve.Options{Config: m.cfg, T3: t3.NewFake(), Logger: quiet(), ShutdownTimeout: 10 * time.Second,
			Ready: func(serve.Ready) { close(ready) }})
	}()
	select {
	case <-ready:
	case err := <-m.done:
		m.t.Fatalf("main server did not start: %v", err)
	case <-time.After(20 * time.Second):
		m.t.Fatal("main server did not start in time")
	}
}

func (m *mainServer) stop() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	m.cancel = nil
	select {
	case err := <-m.done:
		if err != nil {
			m.t.Errorf("main server: %v", err)
		}
	case <-time.After(30 * time.Second):
		m.t.Error("main server did not stop")
	}
}

func (m *mainServer) client() *client.Client { return client.New(m.url) }

// machine is GET /api/machines/{id} through the list.
func (m *mainServer) machine(id int64) client.Machine {
	m.t.Helper()
	ms, err := m.client().Machines(context.Background())
	if err != nil {
		m.t.Fatal(err)
	}
	for _, x := range ms {
		if x.ID == id {
			return x
		}
	}
	m.t.Fatalf("machine %d not listed", id)
	return client.Machine{}
}

// eventually polls cond for up to 15s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMainAndWorker pairs a foreground main server and a worker on temp
// dirs, connects them, survives a main restart and shows the machine
// online, then revokes it.
func TestMainAndWorker(t *testing.T) {
	short := isolate(t)
	m := newMain(t, short)
	ctx := context.Background()

	p, err := m.client().PairMachine(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "w", worker.DirName, worker.ConfigFile)
	c, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath})
	if err != nil {
		t.Fatal(err)
	}
	if c.Label != "laptop" || c.URL != m.url || c.MachineID <= 1 {
		t.Fatalf("joined %+v", c)
	}
	if st, err := os.Stat(cfgPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("worker config mode: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Dir(cfgPath)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("worker dir mode: %v %v", st, err)
	}
	if got := m.machine(c.MachineID); got.Online || got.Status != "offline" {
		t.Fatalf("before run: %+v", got)
	}
	// Joining twice is refused.
	var aj *worker.AlreadyJoinedError
	if _, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath}); !errors.As(err, &aj) {
		t.Fatalf("second join: %v", err)
	}

	states := make(chan worker.State, 64)
	runCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(runCtx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet(), MinBackoff: 20 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
			OnState: func(s worker.State) { states <- s }})
	}()
	waitState := func(want string) {
		t.Helper()
		timeout := time.After(20 * time.Second)
		for {
			select {
			case s := <-states:
				if s.State == want {
					return
				}
			case err := <-done:
				t.Fatalf("worker exited waiting for %s: %v", want, err)
			case <-timeout:
				t.Fatalf("worker never reached %s", want)
			}
		}
	}
	waitState(worker.StateOnline)
	eventually(t, "machine online", func() bool { x := m.machine(c.MachineID); return x.Online && x.Status == "online" })
	ping, err := m.client().PingMachine(ctx, c.MachineID)
	if err != nil || ping.MachineID != c.MachineID {
		t.Fatalf("ping: %+v %v", ping, err)
	}

	// One worker per directory.
	var le *worker.LockedError
	if err := worker.Run(ctx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet()}); !errors.As(err, &le) {
		t.Fatalf("second worker on the dir: %v", err)
	}
	st, code := worker.ReadStatus(ctx, cfgPath, nil)
	if code != worker.StatusOnline || st.Process == nil || st.Process.State != worker.StateOnline || st.Server == nil || st.Server.Status != "online" {
		t.Fatalf("status %d %+v", code, st)
	}

	// The main server restarts: the worker reconnects on its own.
	m.stop()
	waitState(worker.StateConnecting)
	if _, code := worker.ReadStatus(ctx, cfgPath, nil); code != worker.StatusNotOnline {
		t.Fatalf("status while main is down: %d", code)
	}
	m.start()
	waitState(worker.StateOnline)
	eventually(t, "machine online after restart", func() bool { return m.machine(c.MachineID).Online })
	if x := m.machine(c.MachineID); x.LastSeenAt == nil || x.Version == nil {
		t.Fatalf("presence not recorded: %+v", x)
	}
	if _, err := m.client().PingMachine(ctx, c.MachineID); err != nil {
		t.Fatalf("ping after restart: %v", err)
	}

	// Removing the machine closes the channel at once; the worker stops,
	// revoked, without a retry loop.
	if err := m.client().RemoveMachine(ctx, c.MachineID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var ie *config.InvalidError
		if !errors.As(err, &ie) || !errors.Is(err, worker.ErrRevoked) {
			t.Fatalf("revoked worker: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not stop after revocation")
	}
	s, err := worker.ReadState(filepath.Dir(cfgPath))
	if err != nil || s == nil || s.State != worker.StateRevoked {
		t.Fatalf("final state %+v %v", s, err)
	}
	if _, code := worker.ReadStatus(ctx, cfgPath, nil); code != worker.StatusRevoked {
		t.Fatalf("status after revoke: %d", code)
	}
	// Leave with a revoked token deletes the local config.
	res, err := worker.Leave(ctx, worker.LeaveOptions{ConfigPath: cfgPath})
	if err != nil || !res.AlreadyRemoved {
		t.Fatalf("leave: %+v %v", res, err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config kept: %v", err)
	}
	// Joining again clears the old machine's final state.
	p2, err := m.client().PairMachine(ctx, "again")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Join(ctx, worker.JoinOptions{Link: p2.Link, ConfigPath: cfgPath}); err != nil {
		t.Fatal(err)
	}
	if s, err := worker.ReadState(filepath.Dir(cfgPath)); err != nil || s != nil {
		t.Fatalf("state after rejoin %+v %v", s, err)
	}
}

// TestLeaveRemovesMachine: leave removes the machine on the server, and a
// running worker stops.
func TestLeaveRemovesMachine(t *testing.T) {
	short := isolate(t)
	m := newMain(t, short)
	ctx := context.Background()
	p, err := m.client().PairMachine(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), worker.ConfigFile)
	c, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath, Label: "desk"})
	if err != nil {
		t.Fatal(err)
	}
	online := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet(), MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
			OnState: func(s worker.State) {
				if s.State == worker.StateOnline {
					select {
					case online <- struct{}{}:
					default:
					}
				}
			}})
	}()
	select {
	case <-online:
	case err := <-done:
		t.Fatalf("worker exited: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("worker not online")
	}
	res, err := worker.Leave(ctx, worker.LeaveOptions{ConfigPath: cfgPath})
	if err != nil || !res.Removed {
		t.Fatalf("leave: %+v %v", res, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, worker.ErrRevoked) {
			t.Fatalf("worker after leave: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker kept running after leave")
	}
	ms, err := m.client().Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ms {
		if x.ID == c.MachineID {
			t.Fatalf("machine %d still listed after leave", x.ID)
		}
	}
}
