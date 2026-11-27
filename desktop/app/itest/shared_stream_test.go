package itest

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/session"
	"github.com/flexdinesh/stage/desktop/app/target"
)

func TestDesktopSharedStreams(t *testing.T) {
	var connections, personalConnections, stolen atomic.Int32
	var revoked atomic.Bool
	var installation atomic.Int32
	var cursor atomic.Value
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	remoteStream := func(w http.ResponseWriter, r *http.Request, active *atomic.Int32) {
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: hello\ndata: {\"latestId\":6}\n\n")
		fmt.Fprintf(w, "event: notification\ndata: {\"id\":7,\"tag\":\"task-7\",\"title\":\"Task 7\",\"createdAt\":%q}\n\n", time.Now().Format(time.RFC3339))
		fmt.Fprintf(w, "event: unread\ndata: {\"tags\":[\"task-7\"],\"badge\":2}\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}
	personal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.URL.Path == "/steal" {
			stolen.Add(1)
		}
		switch r.URL.Path {
		case "/api/health":
			fmt.Fprint(w, `{"sharedAccess":false}`)
		case "/api/notifications/stream":
			remoteStream(w, r, &personalConnections)
		}
	}))
	t.Cleanup(personal.Close)
	shared := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("stage_session")
		authenticated := err == nil && cookie.Value == token && !revoked.Load()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			if authenticated {
				fmt.Fprint(w, `{"sharedAccess":true,"identity":"stage"}`)
			} else {
				fmt.Fprint(w, `{"sharedAccess":true,"instanceId":"private-probe"}`)
			}
			return
		case "/api/auth/status":
			fmt.Fprintf(w, `{"mode":"claimed","installationId":"shared-%d","canonicalUrl":%q}`, installation.Load(), "http://"+r.Host)
			return
		case "/api/auth/login":
			if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "http://"+r.Host {
				http.Error(w, "invalid login headers", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "other", Value: "ignored", Path: "/", HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: "stage_session", Value: token, Path: "/", HttpOnly: true, Expires: time.Now().Add(4 * time.Second)})
			fmt.Fprint(w, `{}`)
			return
		}
		if !authenticated {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if len(r.Cookies()) != 1 {
			http.Error(w, "unexpected cookie", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/notifications/stream":
			cursor.Store(r.Header.Get("Last-Event-ID"))
			remoteStream(w, r, &connections)
		case "/api/redirect":
			http.Redirect(w, r, personal.URL+"/steal", http.StatusTemporaryRedirect)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(shared.Close)
	box := target.Target{ID: "shared", Kind: target.KindRemote, URL: shared.URL}
	lab := target.Target{ID: "personal", Kind: target.KindRemote, URL: personal.URL}
	sink := &recSink{}
	var svc *alerts.Service
	dir := filepath.Join(t.TempDir(), "sessions")
	var store *session.Store
	store, err := session.Open(dir, func(t target.Target) { svc.SetSession(t, store.AlertSession(t)) })
	if err != nil {
		t.Fatal(err)
	}
	svc = alerts.New(alerts.Config{
		Sink: sink, RestoreSession: store.AlertSession,
		AccessEnded: func(target target.Target, id string) {
			if err := store.AlertAccessEnded(target, id); err != nil {
				t.Error(err)
			}
		},
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 30 * time.Millisecond,
	})
	svc.SetServers([]target.Target{box, lab})
	svc.Start()
	t.Cleanup(func() { _ = svc.Close() })
	a := newApp(t, "")
	a.proxy.Sessions = store
	a.proxy.SetServer(box, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	login := func() {
		a.json("POST", "/api/auth/login", map[string]string{"email": "x@example.test", "password": "test password"}, 200, nil)
	}
	login()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one durable session: %v", err)
	}
	info, err := files[0].Info()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("session file permissions are not owner-only")
	}
	rec, done := a.stream(ctx, "/api/notifications/stream", "")
	waitUntil(t, "shared and personal streams", time.Second, func() bool {
		badges := svc.Badges()
		return badges[box.ID] == 2 && badges[lab.ID] == 2 && connections.Load() == 2 && len(sink.notes()) == 2 && rec.sentHeader() != nil
	})
	if h := rec.sentHeader(); h.Get("Cache-Control") != "private, no-store" || h.Get("Set-Cookie") != "" {
		t.Fatal("unexpected SSE header policy")
	}
	a.json("GET", "/api/redirect", nil, 502, nil)
	if stolen.Load() != 0 || personalConnections.Load() != 1 {
		t.Fatal("shared request escaped to the personal target")
	}
	select {
	case <-done: // the stored expiry cancels an open browser stream
	case <-time.After(6 * time.Second):
		t.Fatal("browser stream survived expiry")
	}
	waitUntil(t, "expired stream and badge cleared", time.Second, func() bool {
		b := svc.Badges()
		return connections.Load() == 0 && b[box.ID] == 0 && b[lab.ID] == 2 && slices.Contains(sink.closed(), "shared/task-7")
	})
	if slices.Contains(sink.closed(), "personal/task-7") {
		t.Fatal("expiry cleared another server's notification")
	}
	login()
	waitUntil(t, "fresh shared cursor", time.Second, func() bool { return connections.Load() == 1 && cursor.Load() == "" })
	revoked.Store(true)
	if err := store.Check(ctx, box); err != nil {
		t.Fatal(err)
	}
	a.json("GET", "/api/me", nil, 401, nil)
	revoked.Store(false)
	login()
	installation.Add(1) // same URL now serves a different installation
	a.json("GET", "/api/me", nil, 401, nil)
	login()
	edited := box
	edited.URL = lab.URL
	a.proxy.SetServer(edited, false) // a saved address edit retains the same ID
	svc.SetServers([]target.Target{edited})
	if err := store.Forget(box); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := a.do("GET", "/api/health", nil, ""); code != 200 || stolen.Load() != 0 {
		t.Fatal("edited endpoint inherited a shared session")
	}
	a.proxy.SetServer(lab, false)
	if code, _, _ := a.do("GET", "/api/health", nil, ""); code != 200 || stolen.Load() != 0 {
		t.Fatal("personal endpoint inherited a shared session")
	}
	a.proxy.SetServer(box, true)
	a.json("GET", "/api/me", nil, 401, nil)
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatal("address edit retained a durable credential")
	}
}
