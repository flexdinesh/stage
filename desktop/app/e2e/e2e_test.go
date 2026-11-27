//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/procnet"
)

const (
	identity  = "stage-desktoptest"
	appName   = "Stage E2E"
	buildinfo = "github.com/flexdinesh/stage/server/internal/buildinfo"
)

// bins builds the app (with the automation plugin, the test identity's
// deep-link scheme and name) and the stage CLI beside it at bin/stage,
// where the app finds its bundled CLI (PathResources is the executable's
// directory outside a bundle).
func bins(t *testing.T) (app, stage string) {
	t.Helper()
	if _, err := os.Stat("../dist/index.html"); err != nil {
		t.Fatal("UI not built: run `mise run desktop:e2e` (it builds desktop/app/ui into dist/)")
	}
	dir := t.TempDir()
	app, stage = filepath.Join(dir, "stage-e2e"), filepath.Join(dir, "bin", "stage")
	run(t, "../../../server", "go", "build", "-ldflags", "-X "+buildinfo+".Identity="+identity+" -X "+buildinfo+".Version=e2e", "-o", stage, "./cmd/stage")
	run(t, "..", "go", "build", "-tags", "e2e", "-ldflags",
		"-X github.com/egoist/mygo.packageURLSchemes="+identity+
			" -X 'main.appName="+appName+"' -X main.appIdentity="+identity+
			" -X main.appIdentifier=io.github.flexdinesh.stage-desktoptest.desktop",
		"-o", app, ".")
	return app, stage
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
	dir, err := os.MkdirTemp("", "sde")
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

func start(t *testing.T, env []string, name string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = env
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
			if p.waitExit(10*time.Second) != nil {
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

func socketClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}, Timeout: 30 * time.Second}
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
}

// isolatedEnv keeps the app and server off the user's data, service,
// single-instance lock and runtime dir; the display variables pass.
func isolatedEnv(t *testing.T, root, run string) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data-home"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_RUNTIME_DIR=" + run,
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "ALL_PROXY=http://127.0.0.1:9",
		"http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9", "all_proxy=http://127.0.0.1:9",
	}
	for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "DBUS_SESSION_BUS_ADDRESS", "GDK_BACKEND"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// TestDesktop launches the app against a real foreground `stage serve`
// (the development socket override, so no OS service is touched) and
// checks the shell's behaviors end to end.
func TestDesktop(t *testing.T) {
	app, stage := bins(t)
	root, short := t.TempDir(), shortDir(t)
	run := filepath.Join(short, "run")
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	sock, auto := filepath.Join(short, "s.sock"), filepath.Join(short, "auto.sock")
	env := isolatedEnv(t, root, run)
	serverCmd := start(t, append(append([]string{}, env...),
		"STAGE_DATA_DIR="+filepath.Join(root, "stage"), "T3_FAKE=1", "T3_FAKE_SEED=1"),
		stage, "serve", "--listen", "unix:"+sock)
	client := socketClient(sock)
	waitUntil(t, "stage serve", 30*time.Second, func() bool {
		res, err := client.Get("http://stage.invalid/api/health")
		if err != nil {
			return false
		}
		_ = res.Body.Close()
		return res.StatusCode == 200
	})

	appEnv := append(append([]string{}, env...), "STAGE_DESKTOP_SOCKET="+sock)
	appCmd := start(t, append(append([]string{}, appEnv...), "MYGO_AUTOMATION="+auto), app)
	a, err := dialAutomation(auto, 30*time.Second, appCmd.alive)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	main := mainWindow(t, a)
	eval := func(script string, out any) error {
		var res struct {
			Value json.RawMessage `json:"value"`
		}
		if err := a.call("eval", map[string]any{"window": main, "script": script}, &res, 15*time.Second); err != nil {
			return err
		}
		if out == nil || len(res.Value) == 0 {
			return nil
		}
		return json.Unmarshal(res.Value, out)
	}
	visible := func() bool {
		ws, err := a.windows()
		if err != nil {
			return false
		}
		for _, w := range ws {
			if w.ID == main {
				return w.Visible
			}
		}
		return false
	}

	// 1. The real UI renders from mygo://localhost and reaches /api over
	// the socket.
	rendered := func() bool {
		var n int
		return eval("document.getElementById('root')?.childElementCount ?? 0", &n) == nil && n > 0
	}
	waitUntil(t, "Stage UI rendered", 60*time.Second, rendered)
	var origin string
	if err := eval("location.origin", &origin); err != nil || origin != "mygo://localhost" {
		t.Fatalf("origin %q %v", origin, err)
	}
	var health map[string]any
	if err := eval("fetch('/api/health').then(r => r.json())", &health); err != nil || health["identity"] != identity {
		t.Fatalf("/api/health through the page: %v %v", health, err)
	}
	waitUntil(t, "connected (no connection screen)", 30*time.Second, func() bool {
		var text string
		return eval("document.body.innerText", &text) == nil && !strings.Contains(text, "Starting Stage")
	})

	// 2. No TCP listener in the app or the server.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		noTCPListener(t, "app", appCmd.pid())
		noTCPListener(t, "stage serve", serverCmd.pid())
	}

	// 3. A deep link handed over by a second instance navigates the
	// running one, which shows its window; the second exits.
	res, err := client.Post("http://stage.invalid/api/sync", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	var catalog struct {
		Destinations []struct {
			ConnectionID int64  `json:"connectionId"`
			WorkspaceID  string `json:"workspaceId"`
		} `json:"destinations"`
	}
	getJSON(t, client, "/api/projects?view=destinations", &catalog)
	if len(catalog.Destinations) == 0 {
		t.Fatal("no workspace")
	}
	destination := catalog.Destinations[0]
	body := fmt.Sprintf(`{"connectionId":%d,"workspaceId":%q,"title":"E2E deep link","prompt":"x"}`, destination.ConnectionID, destination.WorkspaceID)
	req, _ := http.NewRequest("POST", "http://stage.invalid/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var tk struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&tk)
	_ = res.Body.Close()
	second := start(t, appEnv, app, fmt.Sprintf("%s://tasks/%d", identity, tk.ID))
	if err := second.waitExit(15 * time.Second); err != nil {
		t.Fatalf("second instance: %v", err)
	}
	waitUntil(t, "deep link shown", 15*time.Second, func() bool {
		var path string
		return eval("location.pathname", &path) == nil && path == fmt.Sprintf("/tasks/%d", tk.ID)
	})

	// 4. Closing the window hides it; the app stays; a second launch shows
	// and focuses it.
	if err := a.call("menu", map[string]any{"window": main, "path": []string{"Window", "Close"}}, nil, 10*time.Second); err != nil {
		t.Fatalf("Window > Close: %v", err)
	}
	waitUntil(t, "window hidden", 5*time.Second, func() bool { return !visible() })
	time.Sleep(500 * time.Millisecond)
	if !appCmd.alive() {
		t.Fatal("app exited when its window closed")
	}
	second = start(t, appEnv, app)
	if err := second.waitExit(15 * time.Second); err != nil {
		t.Fatalf("second instance: %v", err)
	}
	waitUntil(t, "window shown by the second launch", 10*time.Second, visible)

	// 5. No CSP violation on any page the app showed.
	_ = eval("history.pushState(null, '', '/settings'); dispatchEvent(new PopStateEvent('popstate')); return true", nil)
	time.Sleep(time.Second)
	var violations []string
	if err := eval("window.__stageCSP ?? ['preload missing']", &violations); err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("CSP violations: %v", violations)
	}

	// 6. Quit exits the app; the server keeps running and answers.
	quitPath := []string{"File", "Quit " + appName}
	if runtime.GOOS == "darwin" {
		quitPath = []string{appName, "Quit " + appName}
	}
	_ = a.call("menu", map[string]any{"window": main, "path": quitPath}, nil, 10*time.Second)
	if err := appCmd.waitExit(15 * time.Second); err != nil {
		t.Fatalf("app after Quit: %v", err)
	}
	if !serverCmd.alive() {
		t.Fatal("the server exited with the app")
	}
	getJSON(t, client, "/api/health", &health)
	if health["status"] != "ok" {
		t.Fatalf("server after app Quit: %v", health)
	}
}

func mainWindow(t *testing.T, a *automation) int {
	t.Helper()
	var id int
	waitUntil(t, "main window", 30*time.Second, func() bool {
		ws, err := a.windows()
		if err != nil || len(ws) == 0 {
			return false
		}
		id = ws[0].ID
		return true
	})
	return id
}

func getJSON(t *testing.T, c *http.Client, path string, v any) {
	t.Helper()
	res, err := c.Get("http://stage.invalid" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}
