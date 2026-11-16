package alerts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// newTCPFakeServer is the scripted SSE server over TCP (a remote).
func newTCPFakeServer(t *testing.T) (*fakeServer, string) {
	t.Helper()
	fs := &fakeServer{conns: make(chan *fakeConn, 16)}
	srv := httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(srv.Close)
	u, err := target.NormalizeURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return fs, u
}

type multi struct {
	t         *testing.T
	sink      *fakeSink
	svc       *Service
	prefsPath string
	local     *fakeServer
	remote    *fakeServer
	box       target.Target

	mu        sync.Mutex
	front     string // the server shown in a focused window, "" none
	delivered []string
	changes   atomic.Int32
}

func newMulti(t *testing.T, prefs string) *multi {
	t.Helper()
	m := &multi{t: t, sink: &fakeSink{supported: true}, local: newFakeServer(t)}
	var u string
	m.remote, u = newTCPFakeServer(t)
	m.box = target.Target{ID: "s-box", Name: "Box", Kind: target.KindRemote, URL: u}
	m.prefsPath = filepath.Join(t.TempDir(), "notifications.json")
	if prefs != "" {
		if err := os.WriteFile(m.prefsPath, []byte(prefs), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.svc = New(Config{
		PrefsPath: m.prefsPath,
		Sink:      m.sink,
		Foreground: func(id string) bool {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.front == id
		},
		Delivered: func(id string, n int64) {
			m.mu.Lock()
			m.delivered = append(m.delivered, id)
			m.mu.Unlock()
		},
		Changed:    func() { m.changes.Add(1) },
		Now:        func() time.Time { return t0 },
		Logf:       t.Logf,
		MinBackoff: 5 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond,
	})
	t.Cleanup(func() { _ = m.svc.Close() })
	m.svc.SetServers([]target.Target{target.Local(m.local.socket), m.box})
	m.svc.Start()
	return m
}

func (m *multi) badge(want int) {
	m.t.Helper()
	eventually(m.t, "badge", func() bool {
		b := m.sink.badgeList()
		return len(b) > 0 && b[len(b)-1] == want
	})
}

func (m *multi) idle() {
	m.t.Helper()
	eventually(m.t, "delivery idle", func() bool {
		m.svc.mu.Lock()
		defer m.svc.mu.Unlock()
		return len(m.svc.queue) == 0 && !m.svc.busy
	})
}

// TestTwoServers: every saved server streams at once; the badge is the
// sum; note IDs carry the server, so the same task number on two servers
// never collides; titles name the server; clicks know their server; the
// focused window only silences the server it shows.
func TestTwoServers(t *testing.T) {
	m := newMulti(t, `{"version":2,"enabled":true,"denied":false,"servers":{"local":{"cursor":5},"s-box":{"cursor":40}}}`)
	lc, rc := m.local.next(t), m.remote.next(t)
	if lc.lastEventID != "5" || rc.lastEventID != "40" {
		t.Fatalf("cursors local %q remote %q", lc.lastEventID, rc.lastEventID)
	}
	m.remote.mu.Lock()
	host := m.remote.hosts[0]
	m.remote.mu.Unlock()
	if host != m.box.Host() {
		t.Errorf("remote Host %q, want %q", host, m.box.Host())
	}
	lc.hello(5, false)
	rc.hello(40, false)
	lc.unread(2, "task-1")
	m.badge(2)
	rc.unread(3, "task-9")
	m.badge(5)
	if got := m.svc.Badges(); got["local"] != 2 || got["s-box"] != 3 || len(got) != 2 {
		t.Fatalf("Badges %v", got)
	}
	if m.changes.Load() < 2 {
		t.Errorf("Changed ran %d times", m.changes.Load())
	}

	lc.note(row(6, "task-7", 0))
	r := row(41, "task-7", 0)
	r.URL = "/tasks/7#answer"
	rc.note(r)
	lc.unread(3, "task-1", "task-7")
	rc.unread(4, "task-9", "task-7")
	m.badge(7)
	m.idle()
	ids := m.sink.showIDs()
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"local/task-7", "s-box/task-7"}) {
		t.Fatalf("shows %v", ids)
	}
	titles := map[string]string{}
	m.sink.mu.Lock()
	for _, n := range m.sink.shows {
		titles[n.ID] = n.Title
	}
	m.sink.mu.Unlock()
	if titles["local/task-7"] != "This machine · Needs input" || titles["s-box/task-7"] != "Box · Needs input" {
		t.Errorf("titles %v", titles)
	}
	if server, path := m.svc.Target("s-box/task-7"); server != "s-box" || path != "/tasks/7#answer" {
		t.Errorf("Target = %q %q", server, path)
	}
	if server, path := m.svc.Target("task-3"); server != target.LocalID || path != "/tasks/3" {
		t.Errorf("v1 Target = %q %q", server, path)
	}

	// The remote's task-7 read: only its notification closes.
	rc.unread(3, "task-9")
	m.badge(6)
	if got := m.sink.closed(); !slices.Equal(got, []string{"s-box/task-7"}) {
		t.Fatalf("closes %v", got)
	}

	// The window shows this machine: its rows are toasts, the remote's
	// still show.
	m.mu.Lock()
	m.front = target.LocalID
	m.mu.Unlock()
	lc.note(row(7, "task-8", 0))
	rc.note(row(42, "task-8", 0))
	lc.unread(4, "task-1", "task-7", "task-8")
	rc.unread(4, "task-9", "task-8")
	m.badge(8)
	m.idle()
	if got := m.sink.showIDs(); got[len(got)-1] != "s-box/task-8" || slices.Contains(got, "local/task-8") {
		t.Fatalf("foreground shows %v", got)
	}
	m.mu.Lock()
	delivered := slices.Clone(m.delivered)
	m.mu.Unlock()
	slices.Sort(delivered)
	if strings.Join(delivered, ",") != "local,local,s-box,s-box" {
		t.Errorf("delivered %v", delivered)
	}

	// Page actions name their server.
	m.svc.CloseShown("s-box", "task-8")
	if got := m.sink.closed(); got[len(got)-1] != "s-box/task-8" {
		t.Errorf("CloseShown %v", got)
	}

	// Removing the remote closes its notifications, drops its cursor and
	// its share of the badge.
	m.svc.SetServers([]target.Target{target.Local("")})
	m.badge(4)
	if err := m.svc.Flush(); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPrefs(m.prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	if p.Servers["s-box"] != nil || p.Servers["local"] == nil || *p.Servers["local"].Cursor != 7 {
		t.Fatalf("prefs after remove %+v", p.Servers)
	}
	if got := m.svc.Badges(); len(got) != 1 {
		t.Errorf("Badges after remove %v", got)
	}
	// The local stream kept its socket (SetServers without one) and lives.
	lc.note(row(8, "task-10", 0))
	lc.unread(5, "task-1", "task-7", "task-8", "task-10")
	m.badge(5)
}

// TestOneServerNoPrefix: with a single server, titles are unprefixed.
func TestOneServerNoPrefix(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	c := f.srv.next(t)
	c.hello(5, false)
	c.note(row(6, "task-6", 0))
	f.barrier(c, 1, "task-6")
	f.idle()
	if got := f.sink.lastShow(); got.Title != "Needs input" || got.ID != "local/task-6" {
		t.Fatalf("note %+v", got)
	}
}

// TestMigrationV1: a version 1 file keeps its cursor for this machine,
// closes an earlier run's notification by its old ID, and is saved as
// version 2.
func TestMigrationV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"enabled":true,"cursor":5,"denied":false,"shown":["task-90","task-5"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newFakeServer(t)
	sink := &fakeSink{supported: true}
	svc := New(Config{PrefsPath: path, Sink: sink, Logf: t.Logf, MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond})
	t.Cleanup(func() { _ = svc.Close() })
	svc.Start()
	svc.Connect(target.Local(srv.socket))
	c := srv.next(t)
	if c.lastEventID != "5" {
		t.Fatalf("Last-Event-ID %q", c.lastEventID)
	}
	c.hello(5, false)
	c.unread(1, "task-5")
	eventually(t, "old notification closed", func() bool { return slices.Equal(sink.closed(), []string{"task-90"}) })
	if err := svc.Flush(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"version":2,"enabled":true,"denied":false,"servers":{"local":{"cursor":5,"shown":["task-5"]}}}` {
		t.Fatalf("saved %s", b)
	}
	// A click on the earlier run's notification goes to this machine.
	if server, p := svc.Target("task-5"); server != target.LocalID || p != "/tasks/5" {
		t.Errorf("Target %q %q", server, p)
	}
}
