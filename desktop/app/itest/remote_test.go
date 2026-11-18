package itest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/bridge"
	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// serveTCP starts `stage serve --listen 127.0.0.1:<free port>` (a remote
// server for the desktop) and waits for health; it returns its URL.
func (e *env) serveTCP(t *testing.T, bin string) (string, *server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cmd := exec.Command(bin, "serve", "--listen", addr)
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
			t.Logf("stage serve (tcp) output:\n%s", s.out.String())
		}
	})
	u := "http://" + addr
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if res, err := client.Get(u + "/api/health"); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return u, s
			}
		}
		select {
		case <-s.done:
			t.Fatalf("stage serve exited:\n%s", s.out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage serve (tcp) not ready:\n%s", s.out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestDesktopRemote drives the desktop against a real remote server (a
// `stage serve` on TCP with its own data dir) next to a local one on a
// Unix socket: the launcher switches between them (dev socket for this
// machine, no service), the bridge follows with the remote's Host and
// without the page's Origin, so the server's origin policy accepts
// unsafe methods over TCP; streams and notifications work over TCP; and
// Test connection reports the server and explains a refused hostname.
func TestDesktopRemote(t *testing.T) {
	bin := stage(t)
	le := newEnv(t)
	le.serve(t, bin)
	re := newEnv(t)
	remoteURL, rsrv := re.serveTCP(t, bin)

	store, err := target.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := store.Add("Box", remoteURL)
	if err != nil {
		t.Fatal(err)
	}

	a := newApp(t, "")
	proxy := a.proxy
	servers := make(chan target.Target, 16)
	l := launch.New(launch.Config{
		CLI: bin, AppName: "Stage Test", AppVersion: "itest",
		Dev: true, DevSocket: le.sock, TempDirs: []string{},
		Servers:  store,
		OnTarget: proxy.SetTarget,
		OnServer: func(t target.Target) { servers <- t },
		Logf:     t.Logf,
	})
	t.Cleanup(l.Close)
	l.Start()
	waitReady := func(id string) launch.State {
		t.Helper()
		var s launch.State
		waitUntil(t, "ready on "+id, 30*time.Second, func() bool {
			s = l.State()
			return s.Phase == launch.PhaseReady && s.Target.ID == id
		})
		return s
	}
	s := waitReady(target.LocalID)
	if s.ServerEpoch != 1 || s.Server.Socket != le.sock {
		t.Fatalf("local %+v", s)
	}

	// Switch to the remote: the bridge goes to it over TCP.
	if err := l.Switch(box.ID); err != nil {
		t.Fatal(err)
	}
	s = waitReady(box.ID)
	if s.ServerEpoch != 1 || s.Epoch != 2 || s.Server.ID != box.ID || s.Server.Socket != "" || s.Server.Managed || s.Foreground {
		t.Fatalf("remote %+v server %+v", s, s.Server)
	}
	if s.Server.Identity != identity || s.Server.APIVersion != s.App.APIVersion {
		t.Fatalf("remote server %+v app %+v", s.Server, s.App)
	}
	if got := proxy.Target(); got.ID != box.ID || got.URL != box.URL {
		t.Fatalf("bridge target %+v", got)
	}

	t.Run("unsafe methods over TCP with the page origin", func(t *testing.T) {
		// The page's Origin (mygo://localhost) is trusted on Unix
		// listeners only: sent as is over TCP, the remote refuses it.
		direct, _ := http.NewRequest("POST", remoteURL+"/api/sync", strings.NewReader("{}"))
		direct.Header.Set("Content-Type", "application/json")
		direct.Header.Set("Origin", bridge.PageOrigin)
		res, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(direct)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("direct POST with the page Origin: %d, want 403 (the bridge's stripping is what makes it pass)", res.StatusCode)
		}
		// Through the bridge (which strips it) the same request passes.
		r, _ := http.NewRequest("POST", "mygo://localhost/api/sync", strings.NewReader("{}"))
		r.RequestURI = r.URL.RequestURI()
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", bridge.PageOrigin)
		rec := newStreamRecorder()
		a.h.ServeHTTP(rec, r)
		if status, body := rec.result(); status != 200 {
			t.Fatalf("POST /api/sync to the remote: %d %s", status, body)
		}
		projectID, _ := bindDemoProject(t, a.json)
		a.json("POST", "/api/tasks", map[string]any{
			"projectId": projectID, "title": "Remote task", "prompt": "reply OK",
		}, 201, nil)
	})

	t.Run("event stream over TCP ends on switch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rec, done := a.stream(ctx, "/api/notifications/stream", "")
		rec.waitEvents(t, "hello", 1, 10*time.Second)
		if err := l.Switch(target.LocalID); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the remote's stream outlived the switch")
		}
		waitReady(target.LocalID)
		// The remote's task is not on this machine.
		var tasks struct {
			Tasks []task `json:"tasks"`
		}
		a.json("GET", "/api/tasks", nil, 200, &tasks)
		for _, tk := range tasks.Tasks {
			if tk.Title == "Remote task" {
				t.Fatalf("local server lists the remote's task: %+v", tk)
			}
		}
		if err := l.Switch(box.ID); err != nil {
			t.Fatal(err)
		}
		if s := waitReady(box.ID); s.ServerEpoch != 1 {
			t.Fatalf("serverEpoch %d after switching back", s.ServerEpoch)
		}
	})

	t.Run("notifications from both servers", func(t *testing.T) {
		sink := &recSink{}
		svc := alerts.New(alerts.Config{
			PrefsPath: filepath.Join(t.TempDir(), "notifications.json"), Sink: sink, Logf: t.Logf,
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
		})
		t.Cleanup(func() { _ = svc.Close() })
		svc.SetServers([]target.Target{target.Local(le.sock), box})
		svc.Start()
		waitUntil(t, "both snapshots", notifWait, func() bool { return len(svc.Badges()) == 2 })
		want := api{t: t, sock: le.sock}.badge() + tcpBadge(t, remoteURL)
		waitUntil(t, "badge is the sum", notifWait, func() bool { n, ok := sink.badge(); return ok && n == want })
	})

	t.Run("test connection", func(t *testing.T) {
		r := l.TestServer(context.Background(), remoteURL+"/")
		if !r.Reachable || r.Error != "" || r.Identity != identity || r.Version != "itest" || r.APIVersion != s.App.APIVersion {
			t.Fatalf("test %+v", r)
		}
		// A hostname the remote does not know (localhost. is not
		// "localhost" to its Host check) is refused with the setting.
		named := strings.Replace(remoteURL, "127.0.0.1", "localhost.", 1)
		r = l.TestServer(context.Background(), named)
		if !r.Reachable {
			t.Skipf("localhost. does not resolve here: %+v", r)
		}
		if !strings.Contains(r.Error, "not allowed") || r.Command != "STAGE_ALLOWED_ORIGINS="+named {
			t.Fatalf("refused hostname %+v", r)
		}
	})

	t.Run("remote down is unreachable, then back", func(t *testing.T) {
		rsrv.kill(syscall.SIGKILL)
		status, _, body := a.do("GET", "/api/health", nil, "")
		if status != 503 || !strings.Contains(body, `"code":"unavailable"`) {
			t.Fatalf("health while the remote is down: %d %s", status, body)
		}
		waitUntil(t, "reconnecting", 10*time.Second, func() bool { return l.State().Phase == launch.PhaseReconnecting })
		addr := strings.TrimPrefix(remoteURL, "http://")
		cmd := exec.Command(bin, "serve", "--listen", addr)
		cmd.Env = re.vars
		out := &syncBuffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		back := &server{cmd: cmd, done: make(chan struct{}), out: out}
		go func() { _ = cmd.Wait(); close(back.done) }()
		t.Cleanup(func() { back.kill(syscall.SIGTERM) })
		waitUntil(t, "ready again", 40*time.Second, func() bool {
			s := l.State()
			return s.Phase == launch.PhaseReady && s.Target.ID == box.ID && s.ServerEpoch == 2
		})
	})
}

// tcpBadge reads a remote's needs-input count over TCP.
func tcpBadge(t *testing.T, base string) int {
	t.Helper()
	var b struct {
		NeedsInput int `json:"needsInput"`
	}
	tg := target.Target{ID: "s-x", Kind: target.KindRemote, URL: base}
	if err := launch.TargetGetter(context.Background(), tg, "/api/badge", &b); err != nil {
		t.Fatal(fmt.Errorf("badge: %w", err))
	}
	return b.NeedsInput
}
