//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/fixture/bridge"
	"github.com/flexdinesh/stage/desktop/fixture/procnet"
)

type result struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	MS     int    `json:"ms"`
	Detail string `json:"detail"`
}

type counts struct {
	Streams int `json:"streams"`
	Slow    int `json:"slow"`
}

// bins builds the app (with the automation plugin) and the stub once.
func bins(t *testing.T) (app, stagestub string) {
	t.Helper()
	if _, err := os.Stat("../dist/index.html"); err != nil {
		t.Fatal("probe page not built: run `mise run fixture:e2e` (it builds desktop/fixture/web into dist/)")
	}
	dir := t.TempDir()
	app, stagestub = filepath.Join(dir, "fixture"), filepath.Join(dir, "stagestub")
	run(t, "..", "go", "build", "-tags", "e2e", "-o", app, ".")
	run(t, "..", "go", "build", "-o", stagestub, "./cmd/stagestub")
	return app, stagestub
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// shortDir is a temp dir short enough for Unix socket paths.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sfe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// proc is a started process, reaped in the background so exits are seen.
type proc struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func (p *proc) pid() int { return p.cmd.Process.Pid }

func (p *proc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *proc) waitExit(timeout time.Duration) error {
	select {
	case <-p.done:
		return p.err
	case <-time.After(timeout):
		return fmt.Errorf("still running after %s", timeout)
	}
}

// start runs a process whose output goes to the test log; it is stopped at
// cleanup if still running.
func start(t *testing.T, env []string, name string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = testWriter{t, filepath.Base(name)}
	cmd.Stderr = testWriter{t, filepath.Base(name)}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		if p.alive() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			if p.waitExit(5*time.Second) != nil {
				_ = cmd.Process.Kill()
				<-p.done
			}
		}
	})
	return p
}

type testWriter struct {
	t      *testing.T
	prefix string
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("[%s] %s", w.prefix, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func waitSocket(t *testing.T, path string) {
	t.Helper()
	waitUntil(t, "socket "+path, 10*time.Second, func() bool {
		fi, err := os.Stat(path)
		return err == nil && fi.Mode()&os.ModeSocket != 0
	})
}

func waitUntil(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func stubCounts(t *testing.T, client *http.Client) counts {
	t.Helper()
	res, err := client.Get(bridge.Origin + "/api/fixture/streams")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var c counts
	if err := json.NewDecoder(res.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func noTCPListener(t *testing.T, who string, pid int) {
	t.Helper()
	addrs, err := procnet.ListeningTCP(pid)
	if err != nil {
		t.Fatalf("%s: %v", who, err)
	}
	if len(addrs) != 0 {
		t.Fatalf("%s (pid %d) listens on TCP: %v", who, pid, addrs)
	}
	t.Logf("%s (pid %d): no TCP listener", who, pid)
}

// proxyEnv points every proxy variable at a dead port: the bridges must
// ignore it.
var proxyEnv = []string{
	"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "ALL_PROXY=http://127.0.0.1:9",
	"http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9", "all_proxy=http://127.0.0.1:9",
}

func TestBridges(t *testing.T) {
	app, stagestub := bins(t)
	for _, name := range []string{"scheme", "fetch"} {
		t.Run(name, func(t *testing.T) { testBridge(t, app, stagestub, name) })
	}
}

func testBridge(t *testing.T, app, stagestub, name string) {
	dir := shortDir(t)
	sock, auto := filepath.Join(dir, "stage.sock"), filepath.Join(dir, "auto.sock")
	stubCmd := start(t, nil, stagestub, "-socket", sock)
	waitSocket(t, sock)
	client := bridge.UnixClient(sock)

	env := append([]string{
		"STAGE_FIXTURE_SOCKET=" + sock,
		"STAGE_FIXTURE_BRIDGE=" + name,
		"STAGE_FIXTURE_AUTORUN=1",
	}, proxyEnv...)
	appCmd := start(t, append(env, "MYGO_AUTOMATION="+auto), app)
	a, err := dialAutomation(auto, 30*time.Second, appCmd.alive)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()

	// 1. The probe's check suite.
	waitUntil(t, "probe checks", 120*time.Second, func() bool {
		var done bool
		return a.eval("window.stageFixture?.done === true", &done, 10*time.Second) == nil && done
	})
	var results []result
	if err := a.eval("window.stageFixture.results", &results, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	var envInfo map[string]any
	_ = a.eval("window.stageFixture.env", &envInfo, 10*time.Second)
	t.Logf("environment: %v", envInfo)
	for _, r := range results {
		mark := "pass"
		if !r.OK {
			mark = "FAIL"
			t.Errorf("check %s failed: %s", r.Name, r.Detail)
		}
		t.Logf("%-14s %s %5d ms  %s", r.Name, mark, r.MS, r.Detail)
	}
	if len(results) == 0 {
		t.Fatal("no check results")
	}

	// 2. No TCP listener in the app or the stub.
	noTCPListener(t, "app", appCmd.pid())
	noTCPListener(t, "stub", stubCmd.pid())

	// 3. A reload ends the page's open stream on the server.
	holdStream(t, a, client)
	_ = a.eval("location.replace('/?bridge="+name+"'); return true", nil, 5*time.Second)
	waitUntil(t, "stream closed after reload", 5*time.Second, func() bool { return stubCounts(t, client).Streams == 0 })
	waitUntil(t, "page back after reload", 10*time.Second, func() bool {
		var ok bool
		return a.eval("window.stageFixture !== undefined", &ok, 5*time.Second) == nil && ok
	})

	// 4. A second launch hands over to the first and exits.
	second := start(t, env, app)
	if err := second.waitExit(15 * time.Second); err != nil {
		t.Fatalf("second instance: %v", err)
	}

	// 5. Closing the window hides it; the app keeps running.
	holdStream(t, a, client)
	_ = a.eval("await window.stageFixture.closeWindow(); return true", nil, 5*time.Second)
	waitUntil(t, "window hidden", 5*time.Second, func() bool {
		ws, err := a.windows()
		return err == nil && len(ws) > 0 && !ws[0].Visible
	})
	time.Sleep(500 * time.Millisecond)
	if !appCmd.alive() {
		t.Fatal("app exited when its window closed")
	}
	var visibility string
	_ = a.eval("document.visibilityState", &visibility, 5*time.Second)
	t.Logf("after close: app alive, document.visibilityState=%q, open streams=%d", visibility, stubCounts(t, client).Streams)

	// 6. Explicit Quit exits the app and ends its streams; the server stays.
	_ = a.eval("window.stageFixture.quit(); return true", nil, 5*time.Second)
	if err := appCmd.waitExit(15 * time.Second); err != nil {
		t.Fatalf("app after Quit: %v", err)
	}
	waitUntil(t, "streams closed after Quit", 5*time.Second, func() bool { return stubCounts(t, client).Streams == 0 })
	if !stubCmd.alive() {
		t.Fatal("the server (stub) exited with the app")
	}
	res, err := client.Get(bridge.Origin + "/api/health")
	if err != nil {
		t.Fatalf("server after app Quit: %v", err)
	}
	_ = res.Body.Close()
}

func holdStream(t *testing.T, a *automation, client *http.Client) {
	t.Helper()
	var opened bool
	if err := a.eval("window.stageFixture.holdStream()", &opened, 15*time.Second); err != nil || !opened {
		t.Fatalf("holdStream: opened=%v err=%v", opened, err)
	}
	waitUntil(t, "held stream open on the server", 5*time.Second, func() bool { return stubCounts(t, client).Streams == 1 })
}

// TestRealUI (STAGE_FIXTURE_REAL_UI=1) loads the real Stage web build
// (apps/web/dist) through bridge B against a real `stage serve` on a temp
// socket and data dir: no web change, deep links, /api over the socket.
func TestRealUI(t *testing.T) {
	if os.Getenv("STAGE_FIXTURE_REAL_UI") != "1" {
		t.Skip("set STAGE_FIXTURE_REAL_UI=1 (mise run fixture:e2e builds apps/web/dist when set)")
	}
	uiDir, err := filepath.Abs("../../../apps/web/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(uiDir, "index.html")); err != nil {
		t.Fatal("apps/web/dist not built")
	}
	app, _ := bins(t)
	stage := filepath.Join(t.TempDir(), "stage")
	run(t, "../../../server", "go", "build", "-o", stage, "./cmd/stage")

	dir := shortDir(t)
	sock, auto := filepath.Join(dir, "stage.sock"), filepath.Join(dir, "auto.sock")
	stageCmd := start(t, []string{
		"STAGE_ENV=dev",
		"STAGE_DATA_DIR=" + filepath.Join(dir, "data"),
		"STAGE_LISTEN=unix:" + sock,
		"T3_URL=http://127.0.0.1:9", // never the machine's real T3
	}, stage, "serve")
	waitSocket(t, sock)

	appCmd := start(t, []string{
		"STAGE_FIXTURE_SOCKET=" + sock,
		"STAGE_FIXTURE_BRIDGE=scheme",
		"STAGE_FIXTURE_UI_DIR=" + uiDir,
		"MYGO_AUTOMATION=" + auto,
	}, app)
	a, err := dialAutomation(auto, 30*time.Second, appCmd.alive)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()

	rendered := func() bool {
		var n int
		return a.eval("document.getElementById('root')?.childElementCount ?? 0", &n, 5*time.Second) == nil && n > 0
	}
	waitUntil(t, "Stage UI rendered", 30*time.Second, rendered)
	var health map[string]string
	if err := a.eval("fetch('/api/health').then(r => r.json())", &health, 10*time.Second); err != nil || health["status"] != "ok" {
		t.Fatalf("/api/health through the page: %v %v", health, err)
	}
	_ = a.eval("location.assign('/schedules'); return true", nil, 5*time.Second)
	waitUntil(t, "deep link rendered", 30*time.Second, func() bool {
		var path string
		return a.eval("location.pathname", &path, 5*time.Second) == nil && path == "/schedules" && rendered()
	})
	noTCPListener(t, "app", appCmd.pid())
	noTCPListener(t, "stage serve", stageCmd.pid())
}
