package alerts

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

func testSession(t *testing.T, server target.Target, id string) Session {
	return Session{ID: id, Prepare: func(r *http.Request) error {
		if r.URL.String() != server.BaseURL()+streamPath || r.Host != server.Host() {
			t.Errorf("credential prepared for %s (Host %q)", r.URL, r.Host)
			return ErrAccessEnded
		}
		r.AddCookie(&http.Cookie{Name: "stage_session", Value: id + "-credential"})
		return nil
	}}
}

func TestSessionAccessEndedClearsOnlyItsServer(t *testing.T) {
	ended := make(chan string, 1)
	m := newMulti(t, `{"version":2,"enabled":true,"servers":{"local":{"cursor":5},"s-box":{"cursor":40,"shown":["s-box/task-40"]}}}`, func(cfg *Config) {
		cfg.AccessEnded = func(server target.Target, id string) { ended <- server.ID + ":" + id }
	})
	lc := m.local.next(t)
	_ = m.remote.next(t)
	if lc.session != "" || lc.lastEventID != "5" {
		t.Fatalf("personal stream: credential %q, cursor %q", lc.session, lc.lastEventID)
	}
	lc.hello(5, false)
	lc.unread(2, "task-5")
	m.badge(2)
	lc.note(row(6, "task-7", 0))
	lc.unread(3, "task-5", "task-7")
	m.badge(3)

	wrong := m.box
	wrong.URL = m.box.URL + "-changed"
	if m.svc.SetSession(wrong, testSession(t, wrong, "initial")) {
		t.Fatal("credential accepted for a different address")
	}
	if !m.svc.SetSession(m.box, testSession(t, m.box, "initial")) {
		t.Fatal("credential rejected for saved target")
	}
	rc := m.remote.next(t)
	if rc.session != "initial-credential" || rc.hasCursor {
		t.Fatalf("shared stream: credential %q, cursor %q", rc.session, rc.lastEventID)
	}
	rc.hello(40, false)
	rc.note(row(41, "task-7", 0))
	rc.unread(1, "task-7")
	m.badge(4)
	m.idle()
	if got := m.svc.NeedsInput(); len(got) != 1 || got[target.LocalID] != 3 {
		t.Errorf("server-list needs-input counts %v", got)
	}

	rc.send("event: access-ended\ndata: {}\n\n")
	rc.note(row(42, "task-42", 0))
	m.badge(3)
	eventually(t, "shared OS notification closure", func() bool {
		return slices.Contains(m.sink.closed(), "s-box/task-7")
	})
	if got := m.sink.closed(); slices.Contains(got, "local/task-7") || !slices.Contains(got, "s-box/task-40") {
		t.Errorf("closed notifications %v", got)
	}
	select {
	case got := <-ended:
		if got != "s-box:initial" {
			t.Errorf("ended session %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("credential owner did not get access-ended")
	}
	if server, path := m.svc.Target("s-box/task-7"); server != "" || path != "/" {
		t.Errorf("stale click target = %q %q", server, path)
	}
	if got := m.svc.Badges(); len(got) != 1 || got[target.LocalID] != 3 {
		t.Errorf("badges after revocation %v", got)
	}
	p, err := LoadPrefs(m.prefsPath)
	if err != nil || p.Servers[m.box.ID] != nil {
		t.Fatalf("persisted shared state after revocation: %+v, %v", p.Servers[m.box.ID], err)
	}
	if n := m.remote.requestCount(); n != 2 {
		t.Errorf("shared requests after revocation = %d, want 2", n)
	}
	m.svc.SetSession(m.box, testSession(t, m.box, "replacement"))
	replacement := m.remote.next(t)
	if replacement.session != "replacement-credential" || replacement.hasCursor {
		t.Fatalf("replacement stream: credential %q, cursor %q", replacement.session, replacement.lastEventID)
	}
	replacement.hello(42, false)
	replacement.note(row(42, "task-42", 0))
	replacement.unread(2, "task-42")
	m.badge(5)
	m.idle()
	if got := m.sink.showIDs(); !slices.Equal(got, []string{"local/task-7", "s-box/task-7"}) {
		t.Errorf("shown notifications %v", got)
	}
}

func TestOfflineKeepsStateUntilSharedAccessEnds(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized},
		{name: "forbidden", status: http.StatusForbidden},
		{name: "sign-out"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, nil)
			server := target.Local(f.srv.socket)
			_ = f.srv.next(t)
			f.svc.SetSession(server, testSession(t, server, "restore"))
			c := f.srv.next(t)
			c.hello(10, false)
			f.barrier(c, 2, "task-9")
			f.svc.Disable()
			c.note(row(11, "task-11", 0))
			f.barrier(c, 3, "task-9", "task-11")
			f.srv.setStatus(http.StatusServiceUnavailable)
			c.close()
			eventually(t, "retry after outage", func() bool { return f.srv.requestCount() >= 4 })
			if got := f.svc.Badges()[target.LocalID]; got != 3 {
				t.Errorf("offline badge = %d, want 3", got)
			}
			if p := f.saved(); cursorOf(p) != 11 || p.Enabled {
				t.Errorf("offline cursor %d, enabled %v", cursorOf(p), p.Enabled)
			}

			if tt.status == 0 {
				f.svc.SetSession(server, Session{})
			} else {
				f.srv.setStatus(tt.status)
			}
			eventually(t, "badge removed after access loss", func() bool { return len(f.svc.Badges()) == 0 })
			if p := f.saved(); p.Servers[target.LocalID] != nil || p.Enabled {
				t.Errorf("disabled preferences after access loss %+v", p)
			}
			if got := f.sink.showIDs(); len(got) > 0 {
				t.Errorf("shown while disabled %v", got)
			}
		})
	}
}

type lateSink struct {
	*fakeSink
	started chan context.Context
	release chan struct{}
}

func (s lateSink) Show(ctx context.Context, n Note) error {
	if n.Body == "previous session" {
		s.started <- ctx
		<-s.release
	}
	return s.fakeSink.Show(context.Background(), n)
}

func TestReplacingSessionFencesPendingDelivery(t *testing.T) {
	sink := lateSink{
		fakeSink: &fakeSink{supported: true},
		started:  make(chan context.Context, 1),
		release:  make(chan struct{}),
	}
	f := newFixture(t, nil, func(cfg *Config) { cfg.Sink = sink })
	release := sync.OnceFunc(func() { close(sink.release) })
	t.Cleanup(release)
	server := target.Local(f.srv.socket)
	_ = f.srv.next(t)
	f.svc.SetSession(server, testSession(t, server, "previous"))
	previous := f.srv.next(t)
	previous.hello(10, false)
	n := row(11, "task-7", 0)
	n.Body = "previous session"
	previous.note(n)
	previous.note(row(12, "task-12", 0))
	previous.unread(2, "task-7", "task-12")
	var showing context.Context
	select {
	case showing = <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("previous session notification did not start showing")
	}
	f.svc.SetSession(server, testSession(t, server, "next"))
	if showing.Err() != context.Canceled {
		t.Error("previous session delivery was not canceled")
	}
	next := f.srv.next(t)
	if next.hasCursor {
		t.Fatalf("replacement received previous cursor %q", next.lastEventID)
	}
	next.hello(15, false)
	next.note(row(16, "task-7", 0))
	next.unread(1, "task-7")
	eventually(t, "current badge", func() bool { return f.svc.Badges()[target.LocalID] == 1 })
	release()
	f.idle()
	if got := sink.showIDs(); !slices.Equal(got, []string{"local/task-7", "local/task-7"}) {
		t.Errorf("shows %v", got)
	}
	if got := sink.closed(); !slices.Equal(got, []string{"local/task-7"}) {
		t.Errorf("late previous notification closures %v", got)
	}
	if p := f.saved(); cursorOf(p) != 16 || !slices.Equal(shownOf(p), []string{"local/task-7"}) {
		t.Errorf("replacement preferences: cursor %d, shown %v", cursorOf(p), shownOf(p))
	}
}

func TestRestoringExactSessionResumesCursor(t *testing.T) {
	fs := newFakeServer(t)
	server := target.Local(fs.socket)
	session := testSession(t, server, "restored")
	prefs := cursorPrefs(10)
	prefs.Servers[server.ID].SessionScope = session.scope(server)
	path := filepath.Join(t.TempDir(), "notifications.json")
	if err := SavePrefs(path, *prefs); err != nil {
		t.Fatal(err)
	}
	svc := New(Config{PrefsPath: path, Sink: &fakeSink{supported: true}})
	t.Cleanup(func() { _ = svc.Close() })
	svc.SetServers([]target.Target{server})
	svc.SetSession(server, session)
	svc.Start()
	c := fs.next(t)
	if c.session != "restored-credential" || c.lastEventID != "10" {
		t.Fatalf("restored stream: credential %q, cursor %q", c.session, c.lastEventID)
	}
}
