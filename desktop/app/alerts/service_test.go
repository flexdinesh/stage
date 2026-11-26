package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// --- scripted SSE server on a short temp Unix socket ---

type fakeServer struct {
	socket string
	conns  chan *fakeConn

	mu       sync.Mutex
	status   int
	requests int
	hosts    []string
}

type fakeConn struct {
	lastEventID string
	hasCursor   bool
	out         chan string
	end         chan struct{}
	once        sync.Once
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "al")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	fs := &fakeServer{socket: filepath.Join(dir, "s"), conns: make(chan *fakeConn, 16)}
	ln, err := net.Listen("unix", fs.socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(fs.serve)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return fs
}

func (fs *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != streamPath {
		http.NotFound(w, r)
		return
	}
	fs.mu.Lock()
	fs.requests++
	fs.hosts = append(fs.hosts, r.Host)
	status := fs.status
	fs.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	c := &fakeConn{out: make(chan string, 64), end: make(chan struct{})}
	c.lastEventID = r.Header.Get("Last-Event-ID")
	_, c.hasCursor = r.Header["Last-Event-Id"]
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	select {
	case fs.conns <- c:
	case <-r.Context().Done():
		return
	}
	for {
		select {
		case s := <-c.out:
			if _, err := fmt.Fprint(w, s); err != nil {
				return
			}
			_ = rc.Flush()
		case <-c.end:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (fs *fakeServer) setStatus(code int) {
	fs.mu.Lock()
	fs.status = code
	fs.mu.Unlock()
}

func (fs *fakeServer) requestCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.requests
}

func (fs *fakeServer) next(t *testing.T) *fakeConn {
	t.Helper()
	select {
	case c := <-fs.conns:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no stream connection")
		return nil
	}
}

func (c *fakeConn) send(s string) { c.out <- s }

func (c *fakeConn) hello(latest int64, reset bool) {
	c.send(fmt.Sprintf("retry: 3000\n\nevent: hello\nid: %d\ndata: {\"latestId\":%d,\"reset\":%v}\n\n", latest, latest, reset))
}

func (c *fakeConn) note(n wireNotification) {
	b, _ := json.Marshal(n)
	c.send(fmt.Sprintf("event: notification\nid: %d\ndata: %s\n\n", n.ID, b))
}

func (c *fakeConn) unread(badge int, tags ...string) {
	b, _ := json.Marshal(wireUnread{Tags: tags, Badge: badge})
	c.send(": ping\n\nevent: unread\ndata: " + string(b) + "\n\n")
}

func (c *fakeConn) close() { c.once.Do(func() { close(c.end) }) }

// --- recording sink ---

type fakeSink struct {
	mu             sync.Mutex
	supported      bool
	supportedCalls int
	showErr        error
	gate           chan struct{}
	started        int
	shows          []Note
	closes         []string
	closeAll       int
	badges         []int
}

func (s *fakeSink) Show(ctx context.Context, n Note) error {
	s.mu.Lock()
	s.started++
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shows = append(s.shows, n)
	return s.showErr
}

func (s *fakeSink) Close(id string) {
	s.mu.Lock()
	s.closes = append(s.closes, id)
	s.mu.Unlock()
}

func (s *fakeSink) CloseAll() {
	s.mu.Lock()
	s.closeAll++
	s.mu.Unlock()
}

func (s *fakeSink) SetBadge(n int) {
	s.mu.Lock()
	s.badges = append(s.badges, n)
	s.mu.Unlock()
}

func (s *fakeSink) Supported() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.supportedCalls++
	return s.supported
}

func (s *fakeSink) setErr(err error) {
	s.mu.Lock()
	s.showErr = err
	s.mu.Unlock()
}

func (s *fakeSink) showIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, n := range s.shows {
		out = append(out, n.ID)
	}
	return out
}

func (s *fakeSink) lastShow() Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.shows) == 0 {
		return Note{}
	}
	return s.shows[len(s.shows)-1]
}

func (s *fakeSink) closed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.closes)
}

func (s *fakeSink) badgeList() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.badges)
}

func (s *fakeSink) closeAllCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeAll
}

// --- fixture ---

type fixture struct {
	t         *testing.T
	srv       *fakeServer
	sink      *fakeSink
	svc       *Service
	prefsPath string

	mu        sync.Mutex
	now       time.Time
	fg        bool
	delivered []int64
	logs      []string
}

var t0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func newFixture(t *testing.T, prefs *Prefs, opts ...func(*Config)) *fixture {
	t.Helper()
	f := &fixture{t: t, srv: newFakeServer(t), sink: &fakeSink{supported: true}, now: t0}
	f.prefsPath = filepath.Join(t.TempDir(), "notifications.json")
	if prefs != nil {
		if err := SavePrefs(f.prefsPath, *prefs); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{
		PrefsPath:  f.prefsPath,
		Sink:       f.sink,
		Foreground: func(string) bool { return f.foreground() },
		Delivered:  func(_ string, id int64) { f.onDelivered(id) },
		Now:        f.clock,
		Logf:       f.logf,
		MinBackoff: 5 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond,
	}
	for _, o := range opts {
		o(&cfg)
	}
	f.svc = New(cfg)
	t.Cleanup(func() {
		if err := f.svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	f.svc.Start()
	f.svc.Connect(target.Local(f.srv.socket))
	return f
}

func (f *fixture) foreground() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fg
}

func (f *fixture) setForeground(v bool) {
	f.mu.Lock()
	f.fg = v
	f.mu.Unlock()
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) onDelivered(id int64) {
	f.mu.Lock()
	f.delivered = append(f.delivered, id)
	f.mu.Unlock()
}

func (f *fixture) deliveredIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.delivered)
}

func (f *fixture) logf(format string, args ...any) {
	f.mu.Lock()
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}

func (f *fixture) logCount(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, l := range f.logs {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// saved flushes and reads the preferences file.
func (f *fixture) saved() Prefs {
	f.t.Helper()
	if err := f.svc.Flush(); err != nil {
		f.t.Fatal(err)
	}
	p, err := LoadPrefs(f.prefsPath)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// idle waits until the delivery worker has nothing queued or in flight.
func (f *fixture) idle() {
	f.t.Helper()
	eventually(f.t, "delivery idle", func() bool {
		f.svc.mu.Lock()
		defer f.svc.mu.Unlock()
		return len(f.svc.queue) == 0 && !f.svc.busy
	})
}

// barrier waits until the stream reader handled everything sent on c
// before it (an unread snapshot with a new badge).
func (f *fixture) barrier(c *fakeConn, badge int, tags ...string) {
	f.t.Helper()
	c.unread(badge, tags...)
	eventually(f.t, fmt.Sprintf("badge %d", badge), func() bool {
		b := f.sink.badgeList()
		return len(b) > 0 && b[len(b)-1] == badge
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func cursorPrefs(c int64) *Prefs {
	p := DefaultPrefs()
	p.server(target.LocalID).Cursor = &c
	return &p
}

// local is the OS notification ID of a local tag.
func local(tags ...string) []string {
	out := []string{}
	for _, t := range tags {
		out = append(out, target.LocalID+"/"+t)
	}
	return out
}

// targetPath is the click path of a local tag's notification.
func (f *fixture) targetPath(tag string) string {
	f.t.Helper()
	server, path := f.svc.Target(target.LocalID + "/" + tag)
	if server != target.LocalID {
		f.t.Errorf("Target(%q) server %q", tag, server)
	}
	return path
}

// shownOf is the saved shown list of the local server.
func shownOf(p Prefs) []string {
	if sp := p.Servers[target.LocalID]; sp != nil {
		return sp.Shown
	}
	return nil
}

func row(id int64, tag string, age time.Duration) wireNotification {
	return wireNotification{
		ID: id, Event: "needs_input", CreatedAt: t0.Add(-age).Format("2006-01-02T15:04:05.000Z07:00"),
		Tag: tag, Title: "Needs input", Body: "Task " + tag + " · proj", URL: "/tasks/1",
	}
}

func cursorOf(p Prefs) int64 {
	sp := p.Servers[target.LocalID]
	if sp == nil || sp.Cursor == nil {
		return -1
	}
	return *sp.Cursor
}

// --- tests ---

func TestFirstRunIsSilent(t *testing.T) {
	f := newFixture(t, nil)
	c := f.srv.next(t)
	if c.hasCursor {
		t.Errorf("first run sent Last-Event-ID %q", c.lastEventID)
	}
	c.hello(5, false)
	f.barrier(c, 2, "task-1")
	if got := cursorOf(f.saved()); got != 5 {
		t.Errorf("cursor = %d, want 5", got)
	}
	f.idle()
	if ids := f.sink.showIDs(); len(ids) != 0 {
		t.Errorf("first run showed %v", ids)
	}
	if got := f.svc.State(); got != StateOn {
		t.Errorf("state = %s, want on", got)
	}
	f.srv.mu.Lock()
	host := f.srv.hosts[0]
	f.srv.mu.Unlock()
	if host != "stage.invalid" {
		t.Errorf("Host = %q", host)
	}
}

func TestLiveRowsAndReconnectCursor(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	c := f.srv.next(t)
	if c.lastEventID != "5" {
		t.Errorf("Last-Event-ID = %q, want 5", c.lastEventID)
	}
	c.hello(5, false)
	c.unread(0)
	n6 := row(6, "task-6", 0)
	n6.URL = "/tasks/6"
	c.note(n6)
	read := row(7, "task-7", 0)
	read.Read = true
	c.note(read)
	c.note(row(8, "task-8", 0))
	c.note(row(9, "", 0))
	f.barrier(c, 3, "task-6", "task-8")
	f.idle()
	if got := f.sink.showIDs(); !slices.Equal(got, []string{"local/task-6", "local/task-8", "local/notification-9"}) {
		t.Fatalf("shows = %v", got)
	}
	if got := f.sink.lastShow(); got.Title != "Needs input" || got.Body != "Task  · proj" {
		t.Errorf("note = %+v", got)
	}
	if got := f.deliveredIDs(); !slices.Equal(got, []int64{6, 8, 9}) {
		t.Errorf("delivered = %v", got)
	}
	if got := f.targetPath("task-6"); got != "/tasks/6" {
		t.Errorf("Target = %q", got)
	}

	c.close()
	c2 := f.srv.next(t)
	if c2.lastEventID != "9" {
		t.Errorf("reconnect Last-Event-ID = %q, want 9", c2.lastEventID)
	}
	c2.hello(9, false)
	c2.unread(3, "task-6", "task-8")
	c2.note(row(9, "", 0)) // already seen
	c2.note(row(10, "task-10", 0))
	f.barrier(c2, 4, "task-6", "task-8", "task-10")
	f.idle()
	if got := f.sink.showIDs(); !slices.Equal(got, []string{"local/task-6", "local/task-8", "local/notification-9", "local/task-10"}) {
		t.Fatalf("shows after reconnect = %v", got)
	}
	if got := cursorOf(f.saved()); got != 10 {
		t.Errorf("cursor = %d, want 10", got)
	}
}

func TestCursorClampedSilently(t *testing.T) {
	for _, reset := range []bool{true, false} {
		t.Run(fmt.Sprint("reset=", reset), func(t *testing.T) {
			f := newFixture(t, cursorPrefs(50))
			c := f.srv.next(t)
			c.hello(10, reset)
			c.note(row(9, "task-9", 0))
			c.note(row(10, "task-10", 0))
			f.barrier(c, 1, "task-9", "task-10")
			f.idle()
			if got := cursorOf(f.saved()); got != 10 {
				t.Errorf("cursor = %d, want 10", got)
			}
			if ids := f.sink.showIDs(); len(ids) != 0 {
				t.Errorf("clamp showed %v", ids)
			}
		})
	}
}

func TestNotFoundWaitsForConnect(t *testing.T) {
	f := newFixture(t, nil)
	first := f.srv.next(t)
	f.srv.setStatus(http.StatusNotFound)
	first.close()
	eventually(t, "404 logged", func() bool { return f.logCount("server older") == 1 })
	n := f.srv.requestCount()
	time.Sleep(150 * time.Millisecond) // many backoff periods
	if got := f.srv.requestCount(); got != n {
		t.Fatalf("retried after 404: %d requests, want %d", got, n)
	}
	if got := f.logCount("server older"); got != 1 {
		t.Errorf("logged %d times", got)
	}
	f.srv.setStatus(0)
	f.svc.Connect(target.Local(f.srv.socket))
	c := f.srv.next(t)
	c.hello(1, false)
	f.barrier(c, 1)
}

func TestReconnectBackoffAndConnect(t *testing.T) {
	f := newFixture(t, nil)
	for range 3 {
		f.srv.next(t).close() // drop before hello
	}
	c := f.srv.next(t)
	c.hello(1, false)
	f.barrier(c, 1)

	slow := newFixture(t, nil, func(c *Config) { c.MinBackoff, c.MaxBackoff = time.Hour, time.Hour })
	slow.srv.next(t).close()
	eventually(t, "retry logged", func() bool { return slow.logCount("reconnecting in") == 1 })
	slow.svc.Connect(target.Local(slow.srv.socket)) // ends the backoff wait at once
	c = slow.srv.next(t)
	c.hello(1, false)
	// A Connect mid-stream drops the stream and reconnects with the cursor.
	slow.barrier(c, 2)
	slow.svc.Connect(target.Local(slow.srv.socket))
	c = slow.srv.next(t)
	if c.lastEventID != "1" {
		t.Errorf("Last-Event-ID = %q, want 1", c.lastEventID)
	}
}

func TestJitter(t *testing.T) {
	for range 200 {
		if d := jitter(time.Second, 30*time.Second); d < time.Second || d > 1500*time.Millisecond {
			t.Fatalf("jitter(1s) = %s", d)
		}
		if d := jitter(25*time.Second, 30*time.Second); d < 25*time.Second || d > 30*time.Second {
			t.Fatalf("jitter(25s) = %s", d)
		}
	}
}

func TestCatchUp(t *testing.T) {
	f := newFixture(t, cursorPrefs(10))
	c := f.srv.next(t)
	c.hello(20, false)
	min := time.Minute
	readRow := row(13, "task-3", 9*min)
	readRow.Read = true
	for _, n := range []wireNotification{
		row(11, "task-1", 25*min), // too old
		row(12, "task-2", 10*min),
		readRow,
		row(14, "", 5*min),
		row(15, "session-a", 9*min),
		row(16, "task-6", 8*min),
		row(17, "task-2", 7*min),
		row(18, "task-8", 6*min),
		row(19, "task-9", 5*min),
		row(20, "task-10", 4*min),
	} {
		c.note(n)
	}
	all := []string{"task-1", "task-2", "session-a", "task-6", "task-8", "task-9", "task-10"}
	f.barrier(c, 6, all...)
	f.idle()
	if got := f.sink.showIDs(); !slices.Equal(got, []string{"local/task-8", "local/task-9", "local/task-10", "local/summary"}) {
		t.Fatalf("catch-up shows = %v", got)
	}
	if got := f.sink.lastShow(); got.Title != "3 more in Stage" || got.Body != "Open notifications" {
		t.Errorf("summary = %+v", got)
	}
	if got := f.deliveredIDs(); !slices.Equal(got, []int64{20}) {
		t.Errorf("delivered = %v, want [20]", got)
	}
	if got := cursorOf(f.saved()); got != 20 {
		t.Errorf("cursor = %d, want 20", got)
	}
	if got := f.targetPath("summary"); got != "/?inbox" {
		t.Errorf("summary target = %q", got)
	}

	// task-9 and task-10 read; the summary stays while task-2 is unread.
	f.barrier(c, 2, "task-2", "task-8")
	if got := f.sink.closed(); !slices.Equal(got, []string{"local/task-10", "local/task-9"}) {
		t.Errorf("closes = %v", got)
	}
	f.barrier(c, 1, "task-8")
	if got := f.sink.closed(); !slices.Equal(got, []string{"local/task-10", "local/task-9", "local/summary"}) {
		t.Errorf("closes = %v", got)
	}

	// Live rows after the catch-up show at once.
	c.note(row(21, "task-21", 0))
	f.barrier(c, 3, "task-8", "task-21")
	f.idle()
	if got := f.sink.showIDs(); got[len(got)-1] != "local/task-21" {
		t.Errorf("live after catch-up: %v", got)
	}
}

func TestReplaceAndCoalesce(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	c := f.srv.next(t)
	c.hello(5, false)
	c.unread(0)
	gate := make(chan struct{})
	f.sink.mu.Lock()
	f.sink.gate = gate
	f.sink.mu.Unlock()
	c.note(row(6, "task-1", 0))
	eventually(t, "first show started", func() bool {
		f.sink.mu.Lock()
		defer f.sink.mu.Unlock()
		return f.sink.started == 1
	})
	first := row(7, "task-2", 0)
	first.Title = "Needs input"
	second := row(8, "task-2", 0)
	second.Title, second.Event = "Done", "done"
	c.note(first)
	c.note(second)
	f.barrier(c, 2, "task-1", "task-2")
	close(gate)
	f.idle()
	if got := f.sink.showIDs(); !slices.Equal(got, []string{"local/task-1", "local/task-2"}) {
		t.Fatalf("shows = %v", got)
	}
	if got := f.sink.lastShow(); got.Title != "Done" {
		t.Errorf("coalesced note = %+v, want the newer", got)
	}
	// A later row of a shown group reuses its ID (the OS replaces it).
	c.note(row(9, "task-1", 0))
	f.barrier(c, 3, "task-1", "task-2")
	f.idle()
	if got := f.sink.showIDs(); !slices.Equal(got, []string{"local/task-1", "local/task-2", "local/task-1"}) {
		t.Fatalf("shows = %v", got)
	}
}

func TestForegroundSuppression(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	f.setForeground(true)
	c := f.srv.next(t)
	c.hello(5, false)
	c.unread(0)
	c.note(row(6, "task-6", 0))
	f.barrier(c, 1, "task-6")
	f.idle()
	if ids := f.sink.showIDs(); len(ids) != 0 {
		t.Errorf("foreground showed %v", ids)
	}
	if got := f.deliveredIDs(); !slices.Equal(got, []int64{6}) {
		t.Errorf("delivered = %v", got)
	}
	if got := cursorOf(f.saved()); got != 6 {
		t.Errorf("cursor = %d, want 6", got)
	}
	f.setForeground(false)
	c.note(row(7, "task-7", 0))
	f.barrier(c, 2, "task-6", "task-7")
	f.idle()
	if got := f.sink.showIDs(); !slices.Equal(got, []string{"local/task-7"}) {
		t.Errorf("shows = %v", got)
	}

	// Catch-up in the foreground is silent too, but still delivered.
	g := newFixture(t, cursorPrefs(10))
	g.setForeground(true)
	gc := g.srv.next(t)
	gc.hello(12, false)
	gc.note(row(11, "task-11", time.Minute))
	gc.note(row(12, "task-12", time.Minute))
	g.barrier(gc, 2, "task-11", "task-12")
	g.idle()
	if ids := g.sink.showIDs(); len(ids) != 0 {
		t.Errorf("foreground catch-up showed %v", ids)
	}
	if got := g.deliveredIDs(); !slices.Equal(got, []int64{12}) {
		t.Errorf("delivered = %v", got)
	}
	if got := cursorOf(g.saved()); got != 12 {
		t.Errorf("cursor = %d, want 12", got)
	}
}

func TestDisabledKeepsBadge(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	f.svc.Disable()
	if got := f.sink.closeAllCount(); got != 1 {
		t.Errorf("CloseAll = %d", got)
	}
	if got := f.svc.State(); got != StateOff {
		t.Errorf("state = %s, want off", got)
	}
	if f.saved().Enabled {
		t.Error("disabled not persisted")
	}
	c := f.srv.next(t)
	c.hello(5, false)
	c.unread(0)
	c.note(row(6, "task-6", 0))
	f.barrier(c, 4, "task-6")
	f.idle()
	if ids := f.sink.showIDs(); len(ids) != 0 {
		t.Errorf("disabled showed %v", ids)
	}
	if got := f.sink.badgeList(); !slices.Equal(got, []int{0, 4}) {
		t.Errorf("badges = %v", got)
	}
	if got := f.deliveredIDs(); !slices.Equal(got, []int64{6}) {
		t.Errorf("delivered = %v", got)
	}
	// An unchanged badge is not set again.
	f.barrier(c, 5, "task-6")
	c.unread(5, "task-6")
	f.barrier(c, 6, "task-6")
	if got := f.sink.badgeList(); !slices.Equal(got, []int{0, 4, 5, 6}) {
		t.Errorf("badges = %v", got)
	}
}

func TestDeniedAndRecover(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	f.sink.setErr(ErrDenied)
	c := f.srv.next(t)
	c.hello(5, false)
	c.unread(0)
	c.note(row(6, "task-6", 0))
	f.barrier(c, 1, "task-6")
	f.idle()
	if got := f.svc.State(); got != StateBlocked {
		t.Fatalf("state = %s, want blocked", got)
	}
	if !f.saved().Denied {
		t.Error("denied not persisted")
	}
	ctx := context.Background()
	if err := f.svc.Test(ctx); !errors.Is(err, ErrDenied) {
		t.Errorf("Test = %v, want ErrDenied", err)
	}
	if err := f.svc.Enable(ctx); err != nil {
		t.Errorf("Enable while denied = %v, want nil", err)
	}
	if got := f.svc.State(); got != StateBlocked {
		t.Errorf("state = %s, want blocked", got)
	}

	f.sink.setErr(nil)
	if err := f.svc.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.State(); got != StateOn {
		t.Errorf("state after try again = %s, want on", got)
	}
	if got := f.sink.lastShow(); got != (Note{ID: "test", Title: "Stage", Body: "Notifications are on"}) {
		t.Errorf("confirmation = %+v", got)
	}
	if p := f.saved(); p.Denied || !p.Enabled {
		t.Errorf("prefs = %+v", p)
	}
	if server, got := f.svc.Target("test"); server != "" || got != "/settings" {
		t.Errorf("test target = %q %q", server, got)
	}

	// Test ignores the foreground.
	f.setForeground(true)
	if err := f.svc.Test(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.sink.lastShow(); got != (Note{ID: "test", Title: "Stage", Body: "Test notification"}) {
		t.Errorf("test = %+v", got)
	}

	// A denial on a live row, then any successful Show clears it.
	f.setForeground(false)
	f.sink.setErr(ErrDenied)
	c.note(row(7, "task-7", 0))
	f.barrier(c, 2, "task-6", "task-7")
	f.idle()
	if got := f.svc.State(); got != StateBlocked {
		t.Fatalf("state = %s, want blocked", got)
	}
	f.sink.setErr(nil)
	c.note(row(8, "task-8", 0))
	f.barrier(c, 3, "task-6", "task-7", "task-8")
	f.idle()
	if got := f.svc.State(); got != StateOn {
		t.Errorf("state = %s, want on", got)
	}
}

func TestEnableReturnsOtherErrors(t *testing.T) {
	f := newFixture(t, nil)
	f.sink.setErr(errors.New("no notification daemon"))
	if err := f.svc.Enable(context.Background()); err == nil {
		t.Fatal("Enable = nil, want the Show error")
	}
	if got := f.svc.State(); got != StateOn {
		t.Errorf("state = %s, want on (only ErrDenied blocks)", got)
	}
}

func TestUnsupportedEvaluatedOnce(t *testing.T) {
	f := newFixture(t, nil)
	f.sink.mu.Lock()
	f.sink.supported = false
	f.sink.mu.Unlock()
	for range 3 {
		if got := f.svc.State(); got != StateUnsupported {
			t.Fatalf("state = %s", got)
		}
	}
	f.svc.Disable()
	if got := f.svc.State(); got != StateUnsupported {
		t.Errorf("state = %s, unsupported wins over off", got)
	}
	f.sink.mu.Lock()
	defer f.sink.mu.Unlock()
	if f.sink.supportedCalls != 1 {
		t.Errorf("Supported called %d times", f.sink.supportedCalls)
	}
}

func TestReconcile(t *testing.T) {
	p := cursorPrefs(5)
	p.server(target.LocalID).Shown = local("task-90", "session-old", "summary", "task-5")
	f := newFixture(t, p)
	c := f.srv.next(t)
	c.hello(5, false)
	// First snapshot closes earlier runs' notifications of read groups.
	f.barrier(c, 1, "task-5")
	if got := f.sink.closed(); !slices.Equal(got, []string{"local/session-old", "local/task-90"}) {
		t.Fatalf("first snapshot closes = %v", got)
	}
	c.note(row(6, "task-6", 0))
	n7 := row(7, "session-a", 0)
	n7.URL = "/sessions/a#answer"
	c.note(n7)
	f.barrier(c, 3, "task-5", "task-6", "session-a")
	f.idle()
	if got := shownOf(f.saved()); !slices.Equal(got, []string{"local/summary", "local/task-5", "local/task-6", "local/session-a"}) {
		t.Errorf("shown = %v", got)
	}
	// Read on another device: closed here.
	f.barrier(c, 1, "session-a")
	if got := f.sink.closed(); !slices.Equal(got, []string{"local/session-old", "local/task-90", "local/task-5", "local/task-6"}) {
		t.Errorf("closes = %v", got)
	}
	if got := shownOf(f.saved()); !slices.Equal(got, []string{"local/summary", "local/session-a"}) {
		t.Errorf("shown = %v", got)
	}
	f.svc.Sync("") // nothing left to close
	if got := len(f.sink.closed()); got != 4 {
		t.Errorf("Sync closed more: %v", f.sink.closed())
	}
	if got := f.targetPath("session-a"); got != "/?inbox" {
		t.Errorf("target = %q", got)
	}
	f.svc.CloseShown(target.LocalID, "session-a")
	if got := f.sink.closed(); got[len(got)-1] != "local/session-a" {
		t.Errorf("CloseShown: %v", got)
	}
	if got := f.targetPath("session-a"); got != "/?inbox" {
		t.Errorf("target after close = %q", got)
	}
	f.svc.CloseShown(target.LocalID, "")
	if got := f.sink.closed(); got[len(got)-1] != "local/summary" {
		t.Errorf("CloseShown(local, \"\") closed %v, want the earlier run's summary too", got)
	}
	if got := shownOf(f.saved()); len(got) != 0 {
		t.Errorf("shown after CloseShown(\"\") = %v", got)
	}
}

func TestReadWhileShowingCloses(t *testing.T) {
	f := newFixture(t, cursorPrefs(5))
	c := f.srv.next(t)
	c.hello(5, false)
	c.unread(0)
	gate := make(chan struct{})
	f.sink.mu.Lock()
	f.sink.gate = gate
	f.sink.mu.Unlock()
	n := row(6, "task-6", 0)
	n.URL = "/tasks/6#answer"
	c.note(n)
	eventually(t, "show started", func() bool {
		f.sink.mu.Lock()
		defer f.sink.mu.Unlock()
		return f.sink.started == 1
	})
	f.barrier(c, 1, "task-7") // task-6 read meanwhile
	close(gate)
	f.idle()
	if got := f.sink.closed(); !slices.Equal(got, []string{"local/task-6"}) {
		t.Errorf("closes = %v", got)
	}
	if got := f.targetPath("task-6"); got != "/tasks/6" {
		t.Errorf("target = %q, want derived", got)
	}
}

func TestPrefsCorruptFileLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logs []string
	s := New(Config{PrefsPath: path, Sink: &fakeSink{supported: true}, Logf: func(f string, a ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(f, a...))
		mu.Unlock()
	}})
	defer s.Close()
	if s.State() != StateOn {
		t.Errorf("state = %s", s.State())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || !strings.Contains(logs[0], "using defaults") {
		t.Errorf("logs = %v", logs)
	}
}
