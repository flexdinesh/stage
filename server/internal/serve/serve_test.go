package serve_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/filelock"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// countingT3 counts launches, to prove a restart relaunches nothing.
type countingT3 struct {
	*t3.Fake
	launches atomic.Int32
}

func (c *countingT3) LaunchThread(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	c.launches.Add(1)
	return c.Fake.LaunchThread(ctx, in)
}

func newT3() *countingT3 { return &countingT3{Fake: t3.NewFake()} }

// isolate points every path Stage derives at temp dirs (never the user's
// data dir or runtime dir) and returns a short dir for socket paths.
func isolate(t *testing.T) string {
	t.Helper()
	short, err := os.MkdirTemp("", "srv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
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

func cfg(t *testing.T, dataDir string, listens ...string) config.Config {
	t.Helper()
	addrs, err := listen.ParseList(strings.Join(listens, ","))
	if err != nil {
		t.Fatal(err)
	}
	return config.Config{Listen: addrs, DataDir: dataDir}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type inst struct {
	ready  serve.Ready
	cancel context.CancelFunc
	done   chan error
}

// start runs an instance and waits until it serves.
func start(t *testing.T, c config.Config, tc t3.Client) *inst {
	t.Helper()
	return startLogged(t, c, tc, quiet())
}

// startLogged is start with logger.
func startLogged(t *testing.T, c config.Config, tc t3.Client, logger *slog.Logger) *inst {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan serve.Ready, 1)
	i := &inst{cancel: cancel, done: make(chan error, 1)}
	go func() {
		i.done <- serve.Run(ctx, serve.Options{Config: c, HarnessOverrides: map[string]any{t3.Kind: tc}, Logger: logger, Ready: func(r serve.Ready) { readyCh <- r }, ShutdownTimeout: 10 * time.Second})
	}()
	select {
	case i.ready = <-readyCh:
	case err := <-i.done:
		cancel()
		t.Fatalf("instance failed to start: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("instance did not start")
	}
	t.Cleanup(func() { i.stop(t) })
	return i
}

// stop cancels the instance and returns Run's result.
func (i *inst) stop(t *testing.T) error {
	t.Helper()
	i.cancel()
	select {
	case err, ok := <-i.done:
		if !ok {
			return nil
		}
		close(i.done)
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("instance did not stop")
		return nil
	}
}

// runErr runs an instance expected to fail at startup.
func runErr(t *testing.T, c config.Config) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := serve.Run(ctx, serve.Options{Config: c, HarnessOverrides: map[string]any{t3.Kind: newT3()}, Logger: quiet(), Ready: func(serve.Ready) { t.Error("second instance became ready") }})
	if err == nil {
		t.Fatal("instance started; want a startup error")
	}
	return err
}

func clientFor(a listen.Addr) *client.Client { return client.New(a.URL()) }

// httpFor is a raw HTTP client and base URL for an address.
func httpFor(a listen.Addr) (*http.Client, string) {
	if a.Network != "unix" {
		return &http.Client{}, a.URL()
	}
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", a.Address)
	}}}, "http://stage"
}

func health(t *testing.T, a listen.Addr) client.Health {
	t.Helper()
	h, err := clientFor(a).Health(context.Background())
	if err != nil {
		t.Fatalf("health over %s: %v", a, err)
	}
	return h
}

var projectSeq atomic.Int32

// t3Connection is the T3 connection of the instance at a. A fresh install
// has none: the refreshed T3 status stamps the connected T3's environment,
// creating it.
func t3Connection(t *testing.T, a listen.Addr) int64 {
	t.Helper()
	hc, base := httpFor(a)
	res, err := hc.Get(base + "/api/harnesses/t3/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	for _, h := range health(t, a).Harnesses {
		if h.Kind == "t3" && h.ConnectionID != 0 {
			return h.ConnectionID
		}
	}
	t.Fatal("no T3 connection")
	return 0
}

// startTask creates a project in T3, syncs it and starts a task through a.
func startTask(t *testing.T, fake *countingT3, a listen.Addr) client.Task {
	t.Helper()
	ctx := context.Background()
	root := fmt.Sprintf("/w%d", projectSeq.Add(1))
	ext, err := fake.CreateProject(ctx, t3.CreateProjectInput{Title: "P " + root, WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	conn := t3Connection(t, a)
	hc, base := httpFor(a)
	c := clientFor(a)
	res, err := hc.Post(base+"/api/projects", "application/json", strings.NewReader(`{"title":"Test project"}`))
	if err != nil {
		t.Fatal(err)
	}
	var project struct {
		ID int64 `json:"id"`
	}
	if err = json.NewDecoder(res.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("create project: %d", res.StatusCode)
	}
	body, _ := json.Marshal(map[string]any{"connectionId": conn, "externalProjectId": ext.ID, "workspace": root})
	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/api/projects/%d/binding", base, project.ID), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	projectID := project.ID
	switch res.StatusCode {
	case 200:
	case 409:
		// The sync loop's discovery imported the workspace first: use the
		// project it bound.
		projectID = boundProject(t, c, conn, ext.ID)
	default:
		t.Fatalf("bind project: %d", res.StatusCode)
	}
	task, err := c.CreateTask(ctx, client.CreateTask{ProjectID: projectID, Title: "T", Prompt: "do it", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if task.ExternalSessionID == nil {
		t.Fatalf("task not launched: %+v", task)
	}
	return task
}

// boundProject is the project bound to external project ext on connection conn.
func boundProject(t *testing.T, c *client.Client, conn int64, ext string) int64 {
	t.Helper()
	ps, err := c.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		for _, b := range p.Bindings {
			if b.ConnectionID == conn && b.ExternalProjectID == ext {
				return p.ID
			}
		}
	}
	t.Fatalf("bind project: 409 and no project is bound to %s", ext)
	return 0
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s still exists: %v", path, err)
	}
}

func free(t *testing.T, dataDir string, sockets ...string) {
	t.Helper()
	if held, _ := filelock.Held(filepath.Join(dataDir, instance.LockFile)); held {
		t.Errorf("data dir lock still held")
	}
	for _, s := range sockets {
		if held, _ := filelock.Held(listen.LockPath(s)); held {
			t.Errorf("socket lock %s still held", s)
		}
		gone(t, s)
	}
}

func TestListenerModes(t *testing.T) {
	short := isolate(t)
	t.Run("socket only (default private socket)", func(t *testing.T) {
		data := t.TempDir()
		i := start(t, cfg(t, data, "unix"), newT3())
		if len(i.ready.Listeners) != 1 {
			t.Fatalf("listeners %+v", i.ready.Listeners)
		}
		a := i.ready.Listeners[0]
		if !a.Private || !strings.HasPrefix(a.Address, os.Getenv("XDG_RUNTIME_DIR")+"/stage/stage-") {
			t.Fatalf("default socket %+v", a)
		}
		st, err := os.Lstat(a.Address)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("socket mode %v, %v", st.Mode().Perm(), err)
		}
		h := health(t, a)
		if h.Service != "stage" || h.Status != "ok" || h.APIVersion != 9 || h.InstanceID != i.ready.Meta.InstanceID || h.StartedAt == nil {
			t.Fatalf("health %+v", h)
		}
		if err := i.stop(t); err != nil {
			t.Fatal(err)
		}
		free(t, data, a.Address)
	})
	t.Run("tcp only", func(t *testing.T) {
		data := t.TempDir()
		i := start(t, cfg(t, data, freePort(t)), newT3())
		a := i.ready.Listeners[0]
		if a.Network != "tcp" || health(t, a).Status != "ok" {
			t.Fatalf("tcp listener %+v", a)
		}
		if err := i.stop(t); err != nil {
			t.Fatal(err)
		}
		free(t, data)
	})
	t.Run("socket and tcp share one runtime", func(t *testing.T) {
		data := t.TempDir()
		sock := filepath.Join(short, "both.sock")
		fake := newT3()
		i := start(t, cfg(t, data, "unix:"+sock, freePort(t)), fake)
		u, tc := i.ready.Listeners[0], i.ready.Listeners[1]
		if hu, ht := health(t, u), health(t, tc); hu.InstanceID != ht.InstanceID {
			t.Fatalf("two instances behind one process: %s vs %s", hu.InstanceID, ht.InstanceID)
		}
		task := startTask(t, fake, u)
		got, err := clientFor(tc).Task(context.Background(), task.ID)
		if err != nil || got.ExternalSessionID == nil || *got.ExternalSessionID != *task.ExternalSessionID {
			t.Fatalf("task created over unix not visible over tcp: %+v, %v", got, err)
		}
		st, err := os.Lstat(sock)
		if err != nil || st.Mode().Perm() != 0o660 {
			t.Fatalf("explicit socket mode %v, %v", st.Mode().Perm(), err)
		}
		lock, err := os.ReadFile(filepath.Join(data, instance.LockFile))
		if err != nil || !strings.Contains(string(lock), "unix://"+sock) || !strings.Contains(string(lock), tc.URL()) {
			t.Fatalf("lock record %s, %v", lock, err)
		}
		if err := i.stop(t); err != nil {
			t.Fatal(err)
		}
		free(t, data, sock)
		if fake.launches.Load() != 1 {
			t.Fatalf("launches %d", fake.launches.Load())
		}
	})
}

// A second server on the same data dir (any address) fails before binding;
// among concurrent starts exactly one wins.
func TestDataDirOwnership(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	first := start(t, cfg(t, data, "unix:"+filepath.Join(short, "a.sock")), newT3())
	other := filepath.Join(short, "b.sock")
	port := freePort(t)
	err := runErr(t, cfg(t, data, "unix:"+other, port))
	var le *instance.LockedError
	if !errors.As(err, &le) || le.Holder == nil || le.Holder.PID != os.Getpid() || !strings.Contains(err.Error(), "a.sock") {
		t.Fatalf("second instance: %v", err)
	}
	gone(t, other)
	if l, err := net.Listen("tcp", port); err != nil {
		t.Fatalf("loser bound its tcp port: %v", err)
	} else {
		_ = l.Close()
	}
	// Another spelling of the data dir is the same owner.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	if err := runErr(t, cfg(t, link, "unix:"+other)); !errors.As(err, &le) {
		t.Fatalf("symlinked data dir: %v", err)
	}
	if err := first.stop(t); err != nil {
		t.Fatal(err)
	}

	// Concurrent starts on a fresh data dir.
	data2 := t.TempDir()
	const n = 4
	var wg sync.WaitGroup
	var readyCount atomic.Int32
	errs := make([]error, n)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := cfg(t, data2, "unix:"+filepath.Join(short, fmt.Sprintf("c%d.sock", k)))
			errs[k] = serve.Run(ctx, serve.Options{Config: c, HarnessOverrides: map[string]any{t3.Kind: newT3()}, Logger: quiet(), ShutdownTimeout: 5 * time.Second, Ready: func(serve.Ready) {
				readyCount.Add(1)
				time.AfterFunc(time.Second, cancel)
			}})
		}()
	}
	wg.Wait()
	locked := 0
	for k, err := range errs {
		if errors.As(err, &le) {
			locked++
			gone(t, filepath.Join(short, fmt.Sprintf("c%d.sock", k)))
		} else if err != nil {
			t.Errorf("instance %d: %v", k, err)
		}
	}
	if readyCount.Load() != 1 || locked != n-1 {
		t.Fatalf("ready %d, locked %d; want 1 and %d", readyCount.Load(), locked, n-1)
	}
}

// Two data dirs, one socket path: the second fails, the first keeps it.
func TestSocketPathCollision(t *testing.T) {
	short := isolate(t)
	sock := filepath.Join(short, "s.sock")
	first := start(t, cfg(t, t.TempDir(), "unix:"+sock), newT3())
	data2 := t.TempDir()
	err := runErr(t, cfg(t, data2, "unix:"+sock))
	if !strings.Contains(err.Error(), "in use by another stage server") {
		t.Fatalf("collision: %v", err)
	}
	if h := health(t, first.ready.Listeners[0]); h.InstanceID != first.ready.Meta.InstanceID {
		t.Fatal("socket taken over")
	}
	gone(t, filepath.Join(data2, sqlite.DBFile))
	free(t, data2)
}

// A failed bind leaves nothing behind and never opens the database.
func TestPartialBindFailure(t *testing.T) {
	short := isolate(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	data := t.TempDir()
	sock := filepath.Join(short, "p.sock")
	err = runErr(t, cfg(t, data, "unix:"+sock, busy.Addr().String()))
	if !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Fatalf("error %v", err)
	}
	free(t, data, sock)
	gone(t, filepath.Join(data, sqlite.DBFile))
	// Nothing stale blocks the next start.
	i := start(t, cfg(t, data, "unix:"+sock), newT3())
	if err := i.stop(t); err != nil {
		t.Fatal(err)
	}
}

// An open notification stream does not hold up shutdown, and shutdown
// cleans up sockets and locks.
func TestStreamShutdown(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	sock := filepath.Join(short, "st.sock")
	fake := newT3()
	i := start(t, cfg(t, data, "unix:"+sock, freePort(t)), fake)
	for _, a := range i.ready.Listeners {
		_ = startTask(t, fake, a)
		hc, base := httpFor(a)
		res, err := hc.Get(base + "/api/notifications/stream")
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("stream over %s: %v %v", a, res, err)
		}
		lines := bufio.NewReader(res.Body)
		if _, err := lines.ReadString('\n'); err != nil {
			t.Fatalf("stream over %s not open: %v", a, err)
		}
		eof := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, lines); close(eof) }()
		t.Cleanup(func() { _ = res.Body.Close() })
		defer func(a listen.Addr) {
			select {
			case <-eof:
			case <-time.After(5 * time.Second):
				t.Errorf("stream over %s still open after shutdown", a)
			}
		}(a)
	}
	began := time.Now()
	if err := i.stop(t); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(began); d > 5*time.Second {
		t.Fatalf("shutdown took %v with open streams", d)
	}
	free(t, data, sock)
}

// After a graceful restart on the same data dir and socket, state is kept
// and nothing is launched again.
func TestRestartKeepsStateWithoutRelaunch(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	sock := filepath.Join(short, "r.sock")
	fake := newT3()
	c := cfg(t, data, "unix:"+sock)
	i := start(t, c, fake)
	task := startTask(t, fake, i.ready.Listeners[0])
	if err := i.stop(t); err != nil {
		t.Fatal(err)
	}
	j := start(t, cfg(t, data, "unix:"+sock), fake)
	got, err := clientFor(j.ready.Listeners[0]).Task(context.Background(), task.ID)
	if err != nil || got.ExternalSessionID == nil || *got.ExternalSessionID != *task.ExternalSessionID {
		t.Fatalf("task after restart %+v, %v", got, err)
	}
	// Give the poller, link runner and scheduler a moment to act.
	time.Sleep(300 * time.Millisecond)
	if n := fake.launches.Load(); n != 1 {
		t.Fatalf("launches after restart %d, want 1", n)
	}
	if j.ready.Meta.InstanceID == i.ready.Meta.InstanceID {
		t.Fatal("instance id reused")
	}
}

// helperEnv runs serve.Run in a subprocess (T3_FAKE) to be SIGKILLed.
const helperEnv = "SERVE_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv("SERVE_TEST_STAGE_CLI") == "1" {
		os.Exit(cli.Run(context.Background(), os.Args[1:], cli.OSEnv()))
	}
	if spec := os.Getenv(helperEnv); spec != "" {
		data, sock, ok := strings.Cut(spec, "|")
		if !ok {
			os.Exit(2)
		}
		addrs, err := listen.ParseList("unix:" + sock)
		if err != nil {
			os.Exit(2)
		}
		c := config.Config{Listen: addrs, DataDir: data}
		_ = serve.Run(context.Background(), serve.Options{Config: c, Logger: quiet(), Ready: func(serve.Ready) { fmt.Println("ready") }})
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// A server killed with SIGKILL leaves a stale socket file and its lock
// record; the next start replaces the socket and keeps the data.
func TestRestartAfterCrash(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	sock := filepath.Join(short, "k.sock")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+data+"|"+sock, "T3_FAKE=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "ready\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("helper: %q %v", line, err)
	}
	a := listen.Addr{Network: "unix", Address: sock}
	hc, base := httpFor(a)
	req, _ := http.NewRequest("POST", base+"/api/snippets", strings.NewReader(`{"name":"kept","body":"survives a crash"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil || res.StatusCode != 201 {
		_ = cmd.Process.Kill()
		t.Fatalf("create snippet: %v %v", res, err)
	}
	_ = res.Body.Close()
	err = runErr(t, cfg(t, data, "unix:"+filepath.Join(short, "other.sock")))
	var le *instance.LockedError
	if !errors.As(err, &le) || le.Holder == nil || le.Holder.PID != cmd.Process.Pid {
		t.Fatalf("while helper runs: %v", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("expected the killed server's socket file to remain: %v", err)
	}
	i := start(t, cfg(t, data, "unix:"+sock), newT3())
	hc, base = httpFor(i.ready.Listeners[0])
	res, err = hc.Get(base + "/api/snippets")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), "survives a crash") {
		t.Fatalf("snippets after crash restart: %s", body)
	}
}

// systemd gets READY=1 once serving and STOPPING=1 on shutdown, in order.
func TestReadinessNotification(t *testing.T) {
	short := isolate(t)
	ns := filepath.Join(short, "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: ns, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	t.Setenv("NOTIFY_SOCKET", ns)
	i := start(t, cfg(t, t.TempDir(), "unix"), newT3())
	read := func() string {
		buf := make([]byte, 64)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		return string(buf[:n])
	}
	if got := read(); got != "READY=1" {
		t.Fatalf("first notification %q", got)
	}
	if err := i.stop(t); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "STOPPING=1" {
		t.Fatalf("second notification %q", got)
	}
}

// The origin policy guards /api on real listeners, while the Go client,
// bridge B and same-origin browsers get through.
func TestOriginPolicyOnListeners(t *testing.T) {
	short := isolate(t)
	c := cfg(t, t.TempDir(), "unix:"+filepath.Join(short, "o.sock"), freePort(t))
	c.AllowedOrigins = []string{"https://m.tail.ts.net"}
	i := start(t, c, newT3())
	u, tc := i.ready.Listeners[0], i.ready.Listeners[1]
	var seq int
	send := func(a listen.Addr, host, origin, ctype string) int {
		hc, base := httpFor(a)
		seq++
		req, _ := http.NewRequest("POST", base+"/api/snippets", strings.NewReader(fmt.Sprintf(`{"name":"n%d","body":"b"}`, seq)))
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("Content-Type", ctype)
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	_, port, _ := net.SplitHostPort(tc.Address)
	for _, x := range []struct {
		name                string
		a                   listen.Addr
		host, origin, ctype string
		want                int
	}{
		{"go client unix", u, "stage", "", "application/json", 201},
		{"bridge B", u, "stage.invalid", "mygo://localhost", "application/json", 201},
		{"same-origin browser", tc, "127.0.0.1:" + port, "http://127.0.0.1:" + port, "application/json", 201},
		{"allowed proxy", tc, "m.tail.ts.net", "https://m.tail.ts.net", "application/json", 201},
		{"rebinding", tc, "evil.example:" + port, "", "application/json", 403},
		{"stage host over tcp", tc, "stage", "", "application/json", 403},
		{"cross-site", tc, "127.0.0.1:" + port, "https://evil.example", "application/json", 403},
		{"text/plain", tc, "127.0.0.1:" + port, "", "text/plain", 415},
	} {
		if got := send(x.a, x.host, x.origin, x.ctype); got != x.want {
			t.Errorf("%s: %d, want %d", x.name, got, x.want)
		}
	}
	// HTTP MCP (no Origin) on loopback still works.
	hc, base := httpFor(tc)
	req, _ := http.NewRequest("POST", base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("HTTP MCP initialize: %d", res.StatusCode)
	}
	// Static UI is not origin-checked.
	req, _ = http.NewRequest("GET", base+"/", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, err := hc.Do(req); err != nil || res.StatusCode == 403 {
		t.Fatalf("UI navigation from another site: %v %v", res, err)
	} else {
		_ = res.Body.Close()
	}
}
