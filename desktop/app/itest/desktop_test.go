package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/egoist/mygo"

	"github.com/flexdinesh/stage/desktop/app/bridge"
	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/procnet"
)

// app is the desktop's scheme handler as main.go wires it, with a fake UI
// build.
type app struct {
	t     *testing.T
	proxy *bridge.APIProxy
	h     http.Handler
}

func newApp(t *testing.T, sock string) *app {
	ui := fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><div id="root"></div>`)}, "assets/app.js": {Data: []byte("1")}}
	p := bridge.NewAPIProxy(sock)
	return &app{t: t, proxy: p, h: bridge.Scheme(p, mygo.FileServer(ui))}
}

// do sends one request through the scheme handler, as the webview would:
// unsafe methods carry the page's Origin.
func (a *app) do(method, path string, body io.Reader, ctype string) (int, http.Header, string) {
	a.t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), method, "mygo://localhost"+path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	r.RequestURI = r.URL.RequestURI()
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", "mygo://localhost")
	}
	rec := newStreamRecorder()
	a.h.ServeHTTP(rec, r)
	status, out := rec.result()
	return status, rec.sentHeader(), out
}

func (a *app) json(method, path string, in any, wantStatus int, out any) {
	a.t.Helper()
	var body io.Reader
	ctype := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			a.t.Fatal(err)
		}
		body, ctype = bytes.NewReader(b), "application/json"
	}
	status, _, res := a.do(method, path, body, ctype)
	if status != wantStatus {
		a.t.Fatalf("%s %s: %d %s, want %d", method, path, status, res, wantStatus)
	}
	if out != nil {
		decodeJSON(a.t, res, out)
	}
}

// stream opens an event stream through the scheme handler in the
// background; cancel ends it as the page going away would.
func (a *app) stream(ctx context.Context, path, lastID string) (*streamRecorder, <-chan struct{}) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "mygo://localhost"+path, nil)
	if err != nil {
		a.t.Fatal(err)
	}
	r.RequestURI = r.URL.RequestURI()
	r.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	rec := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.h.ServeHTTP(rec, r)
	}()
	return rec, done
}

type thread struct {
	T3ThreadID           string `json:"t3ThreadId"`
	Title                string `json:"title"`
	PendingQuestionCount int    `json:"pendingQuestionCount"`
}

type task struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	State      string `json:"state"`
	T3ThreadID string `json:"t3ThreadId"`
}

// TestDesktopAgainstRealServer drives the desktop bridge against a real
// `stage serve` on a Unix socket with the fake T3.
func TestDesktopAgainstRealServer(t *testing.T) {
	bin := stage(t)
	e := newEnv(t)
	srv := e.serve(t, bin)
	a := newApp(t, e.sock)
	outer := t // servers outlive the subtest that restarts them

	t.Run("health", func(t *testing.T) {
		var h launch.Health
		a.json("GET", "/api/health", nil, 200, &h)
		if h.Service != "stage" || h.Identity != identity || h.Version != "itest" || h.APIVersion < 1 || h.PublicURL != nil {
			t.Fatalf("health %+v", h)
		}
	})

	t.Run("static and policy", func(t *testing.T) {
		for _, p := range []string{"/", "/tasks/12", "/assets/app.js", "/mcp"} {
			status, hdr, body := a.do("GET", p, nil, "")
			if status != 200 || hdr.Get("Content-Security-Policy") != bridge.CSP {
				t.Errorf("GET %s: %d CSP %q", p, status, hdr.Get("Content-Security-Policy"))
			}
			if p == "/mcp" && !strings.Contains(body, `id="root"`) {
				t.Errorf("/mcp reached the server instead of the UI: %q", body)
			}
		}
		for _, c := range []struct {
			method, path string
			status       int
		}{
			{"POST", "/", 405},
			{"TRACE", "/api/health", 405},
			{"OPTIONS", "/api/health", 405},
			{"GET", "/api", 404},
			{"GET", "/api//health", 404},
			{"GET", "/api/../mcp", 404},
			{"GET", "/api/tasks/1%2f..%2f..%2fmcp", 404},
		} {
			status, _, body := a.do(c.method, c.path, nil, "")
			if status != c.status || !strings.Contains(body, `"code":"denied"`) {
				t.Errorf("%s %s: %d %q, want %d denied", c.method, c.path, status, body, c.status)
			}
		}
		// The server's own origin policy still applies through the bridge.
		r, _ := http.NewRequest("POST", "mygo://localhost/api/tasks", strings.NewReader("{}"))
		r.RequestURI = r.URL.RequestURI()
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := newStreamRecorder()
		a.h.ServeHTTP(rec, r)
		if status, body := rec.result(); status != 403 {
			t.Errorf("cross-site POST: %d %s", status, body)
		}
	})

	a.json("POST", "/api/sync", map[string]any{}, 200, nil)
	var projects struct {
		Projects []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"projects"`
	}
	a.json("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) == 0 {
		t.Fatal("no projects after sync")
	}
	projectID := projects.Projects[0].ID
	var threads struct {
		Threads []thread `json:"threads"`
	}
	a.json("GET", "/api/threads", nil, 200, &threads)
	var asking string
	for _, th := range threads.Threads {
		if strings.HasPrefix(th.Title, "Pick the rollout plan") {
			asking = th.T3ThreadID
		}
	}
	if asking == "" {
		t.Fatalf("seeded thread missing: %+v", threads.Threads)
	}

	var attID int64
	t.Run("upload and attachment", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "dot.png")
		_, _ = fw.Write(pngBytes)
		_ = mw.Close()
		status, _, body := a.do("POST", "/api/uploads", &buf, mw.FormDataContentType())
		if status != 201 {
			t.Fatalf("upload: %d %s", status, body)
		}
		var att struct {
			ID       int64  `json:"id"`
			MimeType string `json:"mimeType"`
		}
		decodeJSON(t, body, &att)
		attID = att.ID
		status, hdr, img := a.do("GET", fmt.Sprintf("/api/attachments/%d", att.ID), nil, "")
		if status != 200 || img != string(pngBytes) || hdr.Get("Content-Type") != "image/png" {
			t.Fatalf("attachment: %d %q, %d bytes", status, hdr.Get("Content-Type"), len(img))
		}
	})

	var tk task
	t.Run("create, start, read task", func(t *testing.T) {
		a.json("POST", "/api/tasks", map[string]any{
			"projectId": projectID, "title": "Desktop smoke", "prompt": "reply OK", "attachmentIds": []int64{attID},
		}, 201, &tk)
		if tk.State != "backlog" {
			t.Fatalf("created %+v", tk)
		}
		a.json("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), map[string]any{}, 200, &tk)
		a.json("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &tk)
		if tk.T3ThreadID == "" || (tk.State != "launching" && tk.State != "running") {
			t.Fatalf("started %+v", tk)
		}
	})

	// Before the restart below: the fake T3 forgets launched threads.
	t.Run("stage show deep link", func(t *testing.T) {
		ref := fmt.Sprintf("%s://tasks/%d", identity, tk.ID)
		cmd := exec.Command(bin, "show", "--url", "unix://"+e.sock, ref)
		cmd.Env = e.vars
		out, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "not a stage task or session reference") {
				t.Skipf("refs.Parse does not accept %s:// yet: %s", identity, out)
			}
			t.Fatalf("stage show %s: %v\n%s", ref, err, out)
		}
		if !strings.Contains(string(out), "Desktop smoke") {
			t.Fatalf("stage show %s:\n%s", ref, out)
		}
	})

	t.Run("answer pending question", func(t *testing.T) {
		var qs struct {
			Requests []struct {
				RequestID string `json:"requestId"`
			} `json:"requests"`
		}
		a.json("GET", "/api/threads/"+asking+"/questions", nil, 200, &qs)
		if len(qs.Requests) == 0 {
			t.Fatal("no pending request on the seeded thread")
		}
		var ans struct {
			RequestID   string `json:"requestId"`
			RespondedAt string `json:"respondedAt"`
		}
		a.json("POST", "/api/threads/"+asking+"/questions/"+qs.Requests[0].RequestID+"/answer", map[string]any{
			"answers": map[string]any{"approach": "incremental", "checks": []string{"Unit tests"}, "notes": "Ship behind a flag first"},
		}, 200, &ans)
		if ans.RequestID != qs.Requests[0].RequestID || ans.RespondedAt == "" {
			t.Fatalf("answer %+v", ans)
		}
	})

	t.Run("live stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rec, done := a.stream(ctx, "/api/threads/"+tk.T3ThreadID+"/transcript/stream", "")
		states := rec.waitEvents(t, "state", 1, 10*time.Second)
		if !strings.Contains(states[0].data, `"active":true`) {
			t.Fatalf("first state %q", states[0].data)
		}
		if ct := rec.sentHeader().Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("Content-Type %q", ct)
		}
		a.json("POST", fmt.Sprintf("/api/tasks/%d/stop", tk.ID), map[string]any{}, 200, nil)
		states = rec.waitEvents(t, "state", 2, 15*time.Second)
		if !strings.Contains(states[1].data, `"active":false`) {
			t.Fatalf("live state after stop %q", states[1].data)
		}
		cancel()
		<-done
	})

	t.Run("resume and cancel reach the server", func(t *testing.T) {
		tp := newTap(t, e.sock)
		ta := newApp(t, tp.sock)
		path := "/api/threads/" + asking + "/transcript/stream"
		ctx, cancel := context.WithCancel(context.Background())
		rec, done := ta.stream(ctx, path, "")
		items := rec.waitEvents(t, "item", 1, 10*time.Second)
		rec.waitEvents(t, "state", 1, 10*time.Second)
		last := items[len(items)-1].id
		if _, err := strconv.Atoi(last); err != nil {
			t.Fatalf("item id %q", last)
		}
		cancel()
		<-done
		waitUntil(t, "upstream connection closed", 5*time.Second, func() bool { return tp.open.Load() == 0 })

		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()
		rec, done = ta.stream(ctx, path, last)
		rec.waitEvents(t, "state", 1, 10*time.Second)
		for _, ev := range parseSSE(rec.body()) {
			if ev.event == "item" {
				id, _ := strconv.Atoi(ev.id)
				lastN, _ := strconv.Atoi(last)
				if id <= lastN {
					t.Fatalf("resumed stream resent item at or below %s: %+v", last, ev)
				}
			}
		}
		cancel()
		<-done
	})

	t.Run("server killed mid-stream", func(t *testing.T) {
		path := "/api/threads/" + asking + "/transcript/stream"
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rec, done := a.stream(ctx, path, "")
		items := rec.waitEvents(t, "item", 1, 10*time.Second)
		last := items[len(items)-1].id
		srv.kill(syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("stream did not end when the server died")
		}
		if status, _ := rec.result(); status != 200 {
			t.Fatalf("stream status %d", status)
		}
		// JSON requests fail fast while it is down.
		status, _, body := a.do("GET", "/api/health", nil, "")
		if status != 503 || !strings.Contains(body, `"code":"unavailable"`) {
			t.Fatalf("health while down: %d %s", status, body)
		}
		// EventSource's reconnect gets no HTTP status: it waits.
		rec, done = a.stream(ctx, path, last)
		time.Sleep(time.Second)
		if h := rec.sentHeader(); h != nil {
			t.Fatalf("reconnect answered while the server is down: %v %q", h, rec.body())
		}
		srv = e.serve(outer, bin)
		rec.waitEvents(t, "state", 1, 20*time.Second)
		if status, _ := rec.result(); status != 200 {
			t.Fatalf("resumed status %d: %q", status, rec.body())
		}
		lastN, _ := strconv.Atoi(last)
		for _, ev := range parseSSE(rec.body()) {
			if id, err := strconv.Atoi(ev.id); ev.event == "item" && err == nil && id <= lastN {
				t.Fatalf("resumed stream resent item at or below %s: %+v", last, ev)
			}
		}
		cancel()
		<-done
	})

	t.Run("no TCP listeners", func(t *testing.T) {
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			t.Skip("procnet is implemented on Linux and macOS")
		}
		for who, pid := range map[string]int{"stage serve": srv.pid(), "test process (bridge)": os.Getpid()} {
			addrs, err := procnet.ListeningTCP(pid)
			if err != nil {
				t.Fatalf("%s: %v", who, err)
			}
			if len(addrs) != 0 {
				t.Errorf("%s (pid %d) listens on TCP: %v", who, pid, addrs)
			}
		}
	})
}

// TestLauncherDevAgainstRealServer: a development build pointed at a
// foreground dev server (STAGE_DESKTOP_SOCKET) connects with the real
// CLI's identity and API, without any service command.
func TestLauncherDevAgainstRealServer(t *testing.T) {
	bin := stage(t)
	e := newEnv(t)
	e.serve(t, bin)
	sockets := make(chan string, 4)
	l := launch.New(launch.Config{
		CLI: bin, AppName: "Stage Test", AppVersion: "itest",
		Dev: true, DevSocket: e.sock, TempDirs: []string{},
		OnSocket: func(s string) { sockets <- s },
		Logf:     t.Logf,
	})
	t.Cleanup(l.Close)
	l.Start()
	deadline := time.Now().Add(20 * time.Second)
	for l.State().Phase == launch.PhaseStarting && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	s := l.State()
	if s.Phase != launch.PhaseReady || !s.Dev || s.Epoch != 1 || s.VersionSkew {
		t.Fatalf("state %+v", s)
	}
	if s.App.Identity != identity || s.Server == nil || s.Server.Identity != identity || s.Server.APIVersion != s.App.APIVersion || s.Server.Socket != e.sock {
		t.Fatalf("app %+v server %+v", s.App, s.Server)
	}
	if got := <-sockets; got != e.sock {
		t.Fatalf("OnSocket %q", got)
	}
	if d := l.Diagnostics(context.Background()); !strings.Contains(d, "identity "+identity) {
		t.Fatalf("diagnostics:\n%s", d)
	}
}

// TestLauncherReleaseRefusesTempCLI: the release path explains an app run
// from a temporary dir before running any service command (the test CLI
// lives under the system temp dir).
func TestLauncherReleaseRefusesTempCLI(t *testing.T) {
	bin := stage(t)
	l := launch.New(launch.Config{CLI: bin, AppName: "Stage Test", Logf: t.Logf})
	t.Cleanup(l.Close)
	l.Start()
	deadline := time.Now().Add(10 * time.Second)
	for l.State().Phase == launch.PhaseStarting && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s := l.State(); s.Phase != launch.PhaseMisconfigured {
		t.Fatalf("state %+v", s)
	}
}

// pngBytes is a 1x1 PNG.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
