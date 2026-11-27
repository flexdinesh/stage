package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/session"
	"github.com/flexdinesh/stage/desktop/app/target"
)

func sharedServer(t *testing.T) (string, string, string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "stage-shared-fixture")
	build := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", bin, "./internal/serve")
	build.Dir = filepath.Join("..", "..", "..", "server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build shared Stage fixture: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	e := newEnv(t)
	cmd := exec.Command(bin, "-test.run=^TestDesktopSharedServerProcess$")
	cmd.Env = append(e.vars, "STAGE_DESKTOP_TEST_ADDR="+addr)
	s := &server{cmd: cmd, done: make(chan struct{}), out: &syncBuffer{}}
	cmd.Stdout, cmd.Stderr = s.out, s.out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(s.done) }()
	t.Cleanup(func() { s.kill(syscall.SIGTERM) })
	ready := regexp.MustCompile(`(?m)^DESKTOP_TEST_SOCKET=(.+)$`)
	setup := regexp.MustCompile(`/auth/setup#token=([^\s]+)`)
	var socket, token []string
	waitUntil(t, "shared Stage fixture readiness", 30*time.Second, func() bool {
		select {
		case <-s.done:
			t.Fatal("shared Stage fixture exited before readiness")
		default:
		}
		out := s.out.String()
		socket, token = ready.FindStringSubmatch(out), setup.FindStringSubmatch(out)
		return len(socket) == 2 && len(token) == 2
	})
	return "http://" + addr, socket[1], token[1]
}

func TestDesktopSharedServer(t *testing.T) {
	base, socket, setupToken := sharedServer(t)
	bin := stage(t)
	e := newEnv(t)
	e.serve(t, bin)
	servers, err := target.Open("")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := servers.Add("Studio", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := servers.SetActive(remote.ID); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	open := func(change func(target.Target)) *session.Store {
		t.Helper()
		s, err := session.Open(dir, change)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a := newApp(t, "")
	var l *launch.Launcher
	var nativeAlerts *alerts.Service
	store := open(func(t target.Target) {
		l.AccountChanged(t)
		nativeAlerts.SetSession(t, a.proxy.Sessions.AlertSession(t))
	})
	a.proxy.Sessions = store
	nativeAlerts = alerts.New(alerts.Config{
		Sink: &recSink{}, RestoreSession: store.AlertSession,
		AccessEnded: func(server target.Target, id string) {
			if err := store.AlertAccessEnded(server, id); err != nil {
				t.Error(err)
			}
		},
	})
	nativeAlerts.SetServers([]target.Target{remote, target.Local(e.sock)})
	t.Cleanup(func() { _ = nativeAlerts.Close() })
	l = launch.New(launch.Config{
		CLI: bin, Dev: true, DevSocket: e.sock, Servers: servers,
		OnTarget:     func(t target.Target, h launch.Health) { a.proxy.SetServer(t, h.SharedAccess) },
		CheckSession: a.proxy.Sessions.Check,
	})
	a.proxy.AccountEpoch = func() int { return l.State().AccountEpoch }
	t.Cleanup(l.Close)
	nativeAlerts.Start()
	l.Start()
	ready := func(id string, shared bool) {
		t.Helper()
		waitUntil(t, "ready on "+id, 30*time.Second, func() bool {
			s := l.State()
			return s.Target.ID == id && s.Phase == launch.PhaseReady && s.Server.SharedAccess == shared
		})
	}
	ready(remote.ID, true)
	a.json("GET", "/api/me", nil, 403, nil)
	a.json("POST", "/api/auth/setup", map[string]string{"token": setupToken}, 403, nil)
	const email, pass = "owner@example.test", "spindrift persimmon telescope"
	browser := func(path string, in map[string]string) *http.Cookie {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), "POST", base+path, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", base)
		req.Header.Set("Content-Type", "application/json")
		tr := remote.Transport()
		defer tr.CloseIdleConnections()
		res, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("browser %s: %d", path, res.StatusCode)
		}
		for _, c := range res.Cookies() {
			if c.Name == "stage_session" {
				return c
			}
		}
		t.Fatal("browser did not receive its own Stage cookie")
		return nil
	}
	browserSession := browser("/api/auth/setup", map[string]string{"token": setupToken, "email": email, "name": "Owner", "password": pass})
	forged, err := http.NewRequestWithContext(t.Context(), "GET", "mygo://localhost/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	forged.AddCookie(browserSession)
	rec := newStreamRecorder()
	a.h.ServeHTTP(rec, forged)
	if code, _ := rec.result(); code != 401 {
		t.Fatalf("bridge accepted a page-supplied browser credential: %d", code)
	}
	a.json("GET", "/api/me", nil, 401, nil)
	signIn := func(a *app, email, pass string) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"email": email, "password": pass})
		code, h, _ := a.do("POST", "/api/auth/login", bytes.NewReader(b), "application/json")
		if code != 200 || h.Get("Set-Cookie") != "" || !strings.Contains(h.Get("Cache-Control"), "no-store") {
			t.Fatalf("desktop sign-in: status %d, unexpected response header policy", code)
		}
	}
	signIn(a, email, pass)
	a.json("GET", "/api/me", nil, 200, nil)

	var data bytes.Buffer
	mw := multipart.NewWriter(&data)
	part, err := mw.CreateFormFile("file", "dot.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pngBytes)
	_ = mw.Close()
	status, _, body := a.do("POST", "/api/uploads", &data, mw.FormDataContentType())
	if status != 201 {
		t.Fatalf("upload: %d %s", status, body)
	}
	var uploaded struct{ ID int64 }
	decodeJSON(t, body, &uploaded)
	asset := fmt.Sprintf("/api/attachments/%d", uploaded.ID)
	scopedAsset := asset + "?" + url.Values{target.AttachmentTarget: {remote.AttachmentScope(l.State().AccountEpoch)}}.Encode()
	if code, h, image := a.do("GET", scopedAsset, nil, ""); code != 200 || image != string(pngBytes) || h.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("authenticated attachment: %d", code)
	}
	stream := func() <-chan struct{} {
		t.Helper()
		recorder, done := a.stream(t.Context(), "/api/notifications/stream", "")
		recorder.waitEvents(t, "hello", 1, 5*time.Second)
		return done
	}
	ownerStream := stream()
	waitUntil(t, "authenticated native alert snapshot", 5*time.Second, func() bool {
		_, signedIn := nativeAlerts.Badges()[remote.ID]
		return signedIn
	})

	if err := l.Switch(target.LocalID); err != nil {
		t.Fatal(err)
	}
	ready(target.LocalID, false)
	select {
	case <-ownerStream:
	case <-time.After(5 * time.Second):
		t.Fatal("webview stream survived the server switch")
	}
	if _, signedIn := nativeAlerts.Badges()[remote.ID]; !signedIn {
		t.Fatal("saved server lost its native alerts on switch")
	}
	a.json("GET", scopedAsset, nil, 410, nil)
	a.json("GET", "/api/me", nil, 200, nil)
	if err := l.Switch(remote.ID); err != nil {
		t.Fatal(err)
	}
	ready(remote.ID, true)
	a.json("GET", "/api/me", nil, 200, nil)

	// Reopen the desktop store, then connect through the same installation's
	// Unix socket. That endpoint has its own credential even on this server.
	reopened := newApp(t, "")
	reopened.proxy.Sessions = open(nil)
	reopened.proxy.SetServer(remote, true)
	reopened.json("GET", "/api/me", nil, 200, nil)
	reopened.proxy.SetServer(target.Local(socket), true)
	reopened.json("GET", "/api/me", nil, 401, nil)
	signIn(reopened, email, pass)
	reopened.json("GET", "/api/me", nil, 200, nil)
	reopened.json("POST", "/api/auth/logout", nil, 204, nil)
	reopened.json("GET", "/api/me", nil, 401, nil)

	var invite struct{ URL string }
	a.json("POST", "/api/admin/invitations", map[string]string{"email": "member@example.test"}, 201, &invite)
	u, err := url.Parse(invite.URL)
	if err != nil {
		t.Fatal(err)
	}
	browser("/api/auth/invite", map[string]string{"token": strings.TrimPrefix(u.Fragment, "token="), "name": "Member", "password": pass})
	a.json("POST", "/api/auth/logout", nil, 204, nil)
	a.json("GET", scopedAsset, nil, 410, nil)
	a.json("GET", asset, nil, 401, nil)
	signIn(a, "member@example.test", pass)
	var account struct{ Email string }
	a.json("GET", "/api/me", nil, 200, &account)
	if account.Email != "member@example.test" {
		t.Fatalf("expected the member, got %s", account.Email)
	}
	memberStream := stream()
	a.json("POST", "/api/account/password", map[string]string{"currentPassword": pass, "password": "orchid winter aviary"}, 204, nil)
	select {
	case <-memberStream:
	case <-time.After(5 * time.Second):
		t.Fatal("webview stream survived credential revocation")
	}
	a.json("GET", "/api/me", nil, 401, nil)
	if err := a.proxy.Sessions.Check(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
}
