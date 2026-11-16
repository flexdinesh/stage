package itest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// identity is the test installation's identity: its own data, socket and
// service names, never the stable or developer ones.
const identity = "stage-desktoptest"

const buildinfo = "github.com/flexdinesh/stage/server/internal/buildinfo"

var (
	stageBin  string // the built CLI, "" when it could not be built
	buildErr  error
	buildOnce sync.Once
)

// stage builds the server CLI once per test binary.
func stage(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain to build the stage CLI")
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "stage-itest-bin")
		if err != nil {
			buildErr = err
			return
		}
		stageBin = filepath.Join(dir, "stage")
		cmd := exec.Command(goBin, "build", "-ldflags",
			"-X "+buildinfo+".Identity="+identity+" -X "+buildinfo+".Version=itest", "-o", stageBin, "./cmd/stage")
		cmd.Dir = filepath.Join("..", "..", "..", "server")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/stage: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return stageBin
}

func TestMain(m *testing.M) {
	code := m.Run()
	if stageBin != "" {
		_ = os.RemoveAll(filepath.Dir(stageBin))
	}
	os.Exit(code)
}

// env is an isolated environment: temp HOME, XDG dirs, data dir and
// runtime dir, the fake T3, and dead proxies. Nothing of the caller's
// STAGE_* or T3_* leaks in.
type env struct {
	vars []string
	sock string
	dir  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	short, err := os.MkdirTemp("", "sd") // socket paths are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	run := filepath.Join(short, "run")
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	e := &env{sock: filepath.Join(short, "s.sock"), dir: root}
	e.vars = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data-home"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"XDG_RUNTIME_DIR=" + run,
		"STAGE_DATA_DIR=" + filepath.Join(root, "stage"),
		"T3_FAKE=1", "T3_FAKE_SEED=1",
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "ALL_PROXY=http://127.0.0.1:9",
		"http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9", "all_proxy=http://127.0.0.1:9",
	}
	return e
}

// server is a running `stage serve`.
type server struct {
	cmd  *exec.Cmd
	done chan struct{}
	out  *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// serve starts `stage serve --listen unix:<sock>` and waits for health.
func (e *env) serve(t *testing.T, bin string) *server {
	t.Helper()
	cmd := exec.Command(bin, "serve", "--listen", "unix:"+e.sock)
	cmd.Env = e.vars
	s := &server{cmd: cmd, done: make(chan struct{}), out: &syncBuffer{}}
	cmd.Stdout, cmd.Stderr = s.out, s.out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(s.done) }()
	t.Cleanup(func() {
		s.kill(syscall.SIGTERM)
		if t.Failed() {
			t.Logf("stage serve output:\n%s", s.out.String())
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if c, err := net.Dial("unix", e.sock); err == nil {
			_ = c.Close()
			res, err := socketClient(e.sock).Get("http://stage.invalid/api/health")
			if err == nil {
				_ = res.Body.Close()
				if res.StatusCode == 200 {
					return s
				}
			}
		}
		select {
		case <-s.done:
			t.Fatalf("stage serve exited:\n%s", s.out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage serve not ready:\n%s", s.out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *server) pid() int { return s.cmd.Process.Pid }

func (s *server) kill(sig syscall.Signal) {
	select {
	case <-s.done:
		return
	default:
	}
	_ = s.cmd.Process.Signal(sig)
	select {
	case <-s.done:
	case <-time.After(15 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
}

// socketClient talks to the server directly (test setup, not the bridge).
func socketClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}, Timeout: 30 * time.Second}
}

// tap is a Unix socket forwarder in front of the server that counts open
// connections, to see the bridge close its upstream connection.
type tap struct {
	sock string
	open atomic.Int32
	ln   net.Listener
}

func newTap(t *testing.T, upstream string) *tap {
	t.Helper()
	dir, err := os.MkdirTemp("", "st")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	tp := &tap{sock: filepath.Join(dir, "tap.sock")}
	tp.ln, err = net.Listen("unix", tp.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.ln.Close() })
	go func() {
		for {
			c, err := tp.ln.Accept()
			if err != nil {
				return
			}
			go tp.forward(c, upstream)
		}
	}()
	return tp
}

func (tp *tap) forward(c net.Conn, upstream string) {
	tp.open.Add(1)
	defer tp.open.Add(-1)
	defer c.Close()
	u, err := net.Dial("unix", upstream)
	if err != nil {
		return
	}
	defer u.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(u, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()
	<-done
}

// streamRecorder is a concurrent http.ResponseWriter and http.Flusher, as
// MyGo's scheme writer is: it records only what was flushed.
type streamRecorder struct {
	mu      sync.Mutex
	header  http.Header
	sent    http.Header
	status  int
	pending bytes.Buffer
	flushed bytes.Buffer
}

func newStreamRecorder() *streamRecorder { return &streamRecorder{header: http.Header{}} }

func (r *streamRecorder) Header() http.Header { return r.header }

func (r *streamRecorder) WriteHeader(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == 0 {
		r.status = status
		r.sent = r.header.Clone()
	}
}

func (r *streamRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending.Write(p)
}

func (r *streamRecorder) Flush() {
	r.WriteHeader(http.StatusOK)
	r.mu.Lock()
	_, _ = r.pending.WriteTo(&r.flushed)
	r.mu.Unlock()
}

func (r *streamRecorder) result() (int, string) {
	r.Flush()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.flushed.String()
}

func (r *streamRecorder) sentHeader() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent
}

func (r *streamRecorder) body() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushed.String()
}

type sseEvent struct{ event, id, data string }

func parseSSE(s string) []sseEvent {
	var out []sseEvent
	for _, block := range strings.Split(s, "\n\n") {
		var ev sseEvent
		sc := bufio.NewScanner(strings.NewReader(block))
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			k, v, _ := strings.Cut(sc.Text(), ": ")
			switch k {
			case "event":
				ev.event = v
			case "id":
				ev.id = v
			case "data":
				ev.data = v
			}
		}
		if ev.event != "" {
			out = append(out, ev)
		}
	}
	return out
}

// waitEvents waits until the stream has n events named name.
func (r *streamRecorder) waitEvents(t *testing.T, name string, n int, timeout time.Duration) []sseEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var got []sseEvent
		for _, ev := range parseSSE(r.body()) {
			if ev.event == name {
				got = append(got, ev)
			}
		}
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d %q events, want %d: %q", len(got), name, n, r.body())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitUntil(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func decodeJSON(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}
