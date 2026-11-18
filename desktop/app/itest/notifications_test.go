package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// Native notifications (docs/desktop.md, Notifications): the real
// alerts.Service against a real `stage serve` with the fake T3, a recording
// Sink and temp preferences. Launched fake tasks ask a question ~3s after
// launch; the server's 5s poller then moves them to needs_input.

// notifWait bounds every wait on a server-side state change.
const notifWait = 40 * time.Second

// allEvents is the server's default notification filter.
var allEvents = []string{"needs_input", "failed", "done", "settled"}

type shown struct {
	note alerts.Note
	at   time.Time
}

// recSink is a Sink that records what the Service asks of the OS.
type recSink struct {
	mu       sync.Mutex
	shows    []shown
	closes   []string
	closeAll int
	badges   []int
}

func (s *recSink) Show(_ context.Context, n alerts.Note) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shows = append(s.shows, shown{note: n, at: time.Now()})
	return nil
}

func (s *recSink) Close(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes = append(s.closes, id)
}

func (s *recSink) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeAll++
}

func (s *recSink) SetBadge(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.badges = append(s.badges, n)
}

func (s *recSink) Supported() bool { return true }

func (s *recSink) notes() []alerts.Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]alerts.Note, len(s.shows))
	for i, sh := range s.shows {
		out[i] = sh.note
	}
	return out
}

func (s *recSink) closed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.closes)
}

// badge is the last badge set; ok false before the first.
func (s *recSink) badge() (n int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.badges) == 0 {
		return 0, false
	}
	return s.badges[len(s.badges)-1], true
}

// delivered records Config.Delivered calls.
type delivered struct {
	mu  sync.Mutex
	ids []int64
}

func (d *delivered) add(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ids = append(d.ids, id)
}

func (d *delivered) all() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.ids)
}

// harness is one desktop process: a Service with its sink and callbacks.
type harness struct {
	svc       *alerts.Service
	sink      *recSink
	delivered *delivered
}

// open starts a Service on prefs and connects it to sock. now shifts its
// clock; fg is the window-in-front flag (nil = never).
func open(t *testing.T, prefs, sock string, now time.Duration, fg *atomic.Bool) *harness {
	t.Helper()
	h := &harness{sink: &recSink{}, delivered: &delivered{}}
	cfg := alerts.Config{
		PrefsPath:  prefs,
		Sink:       h.sink,
		Delivered:  func(_ string, id int64) { h.delivered.add(id) },
		Logf:       t.Logf,
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
	}
	if fg != nil {
		cfg.Foreground = func(string) bool { return fg.Load() }
	}
	if now != 0 {
		cfg.Now = func() time.Time { return time.Now().Add(now) }
	}
	h.svc = alerts.New(cfg)
	t.Cleanup(func() { _ = h.svc.Close() })
	h.svc.Start()
	h.svc.Connect(target.Local(sock))
	return h
}

// waitSnapshot waits for the first unread snapshot (always sets the
// badge), by which the catch-up has been planned and queued.
func (h *harness) waitSnapshot(t *testing.T) {
	t.Helper()
	waitUntil(t, "first unread snapshot", notifWait, func() bool { _, ok := h.sink.badge(); return ok })
}

// api talks to the server directly over its socket.
type api struct {
	t    *testing.T
	sock string
}

func (a api) do(method, path string, in any, want int, out any) {
	a.t.Helper()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			a.t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := newRequest(method, path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	res, err := socketClient(a.sock).Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		a.t.Fatalf("%s %s: %d %s, want %d", method, path, res.StatusCode, b, want)
	}
	if out != nil {
		decodeJSON(a.t, string(b), out)
	}
}

func newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, "http://stage.invalid"+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

type inboxRow struct {
	ID         int64   `json:"id"`
	Event      string  `json:"event"`
	TaskID     *int64  `json:"taskId"`
	T3ThreadID *string `json:"externalSessionId"`
	Title      string  `json:"title"`
	ReadAt     *string `json:"readAt"`
}

// rows is the inbox, oldest first.
func (a api) rows() []inboxRow {
	a.t.Helper()
	var res struct {
		Notifications []inboxRow `json:"notifications"`
	}
	a.do("GET", "/api/notifications?limit=200", nil, 200, &res)
	sort.Slice(res.Notifications, func(i, j int) bool { return res.Notifications[i].ID < res.Notifications[j].ID })
	return res.Notifications
}

func (a api) latestID() int64 {
	a.t.Helper()
	var res struct {
		LatestID int64 `json:"latestId"`
	}
	a.do("GET", "/api/notifications/groups", nil, 200, &res)
	return res.LatestID
}

func (a api) badge() int {
	a.t.Helper()
	var res struct {
		NeedsInput int `json:"needsInput"`
	}
	a.do("GET", "/api/badge", nil, 200, &res)
	return res.NeedsInput
}

func (a api) task(id int64) task {
	a.t.Helper()
	var tk task
	a.do("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &tk)
	return tk
}

func (a api) setEvents(events []string) {
	a.t.Helper()
	a.do("PUT", "/api/preferences/notifications", map[string]any{"events": events}, 200, nil)
}

func (a api) read(groups ...string) {
	a.t.Helper()
	a.do("POST", "/api/notifications/read", map[string]any{"groups": groups}, 204, nil)
}

// project explicitly imports and binds the isolated fake's demo workspace.
func (a api) project() (int64, string) { return bindDemoProject(a.t, a.do) }

// Exercise the same explicit create/bind workflow over both bridge and socket.
func bindDemoProject(t *testing.T, request func(string, string, any, int, any)) (int64, string) {
	t.Helper()
	var discovered struct {
		Projects []struct {
			ExternalID string `json:"externalId"`
			Workspace  string `json:"workspace"`
			Title      string `json:"title"`
		} `json:"projects"`
	}
	request("GET", "/api/connections/t3/projects", nil, 200, &discovered)
	if len(discovered.Projects) == 0 {
		t.Fatal("fake has no workspace")
	}
	workspace := discovered.Projects[0]
	var project struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	request("POST", "/api/projects", map[string]any{"title": workspace.Title}, 201, &project)
	request("PUT", fmt.Sprintf("/api/projects/%d/binding", project.ID), map[string]any{"connectionId": 1, "externalProjectId": workspace.ExternalID, "workspace": workspace.Workspace}, 200, nil)
	return project.ID, project.Title
}

// launch creates and starts a task; the fake T3 makes it ask a question.
func (a api) launch(project int64, title string) task {
	a.t.Helper()
	var tk task
	a.do("POST", "/api/tasks", map[string]any{"projectId": project, "title": title, "prompt": "reply OK"}, 201, &tk)
	a.do("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), map[string]any{}, 200, &tk)
	return tk
}

// needsInput waits for the needs_input inbox row of task id and returns it
// with the task (now carrying its thread).
func (a api) needsInput(id int64) (inboxRow, task) {
	a.t.Helper()
	var row inboxRow
	waitUntil(a.t, fmt.Sprintf("needs_input row of task %d", id), notifWait, func() bool {
		for _, r := range a.rows() {
			if r.TaskID != nil && *r.TaskID == id && r.Event == "needs_input" {
				row = r
				return true
			}
		}
		return false
	})
	tk := a.task(id)
	if tk.T3ThreadID == "" {
		a.t.Fatalf("task %d has no thread: %+v", id, tk)
	}
	return row, tk
}

// cursor is the persisted cursor (-1 when none).
func cursor(t *testing.T, prefs string) int64 {
	t.Helper()
	p, err := alerts.LoadPrefs(prefs)
	if err != nil {
		t.Fatal(err)
	}
	sp := p.Servers[target.LocalID]
	if sp == nil || sp.Cursor == nil {
		return -1
	}
	return *sp.Cursor
}

func taskTag(id int64) string { return fmt.Sprintf("task-%d", id) }

// noteID is the OS notification ID of a local tag.
func noteID(tag string) string { return target.LocalID + "/" + tag }

// clickPath is the path a click on a local notification opens.
func (h *harness) clickPath(tag string) string {
	server, path := h.svc.Target(noteID(tag))
	if server != target.LocalID {
		return "server " + server
	}
	return path
}

func sessionAnswer(thread string) string { return "/sessions/" + url.PathEscape(thread) + "#answer" }

// TestNotificationsAgainstRealServer walks one desktop profile through
// first run, live rows, foreground, cross-device read, the global filter
// and catch-up after the app was closed.
func TestNotificationsAgainstRealServer(t *testing.T) {
	t.Parallel()
	bin := stage(t)
	e := newEnv(t)
	e.serve(t, bin)
	a := api{t: t, sock: e.sock}
	projectID, projectTitle := a.project()
	prefs := filepath.Join(t.TempDir(), "userdata", "notifications.json")
	var fg atomic.Bool
	outer := t // the first Service outlives its subtest

	var h *harness
	t.Run("1 first run is silent", func(t *testing.T) {
		latest := a.latestID()
		h = open(outer, prefs, e.sock, 0, &fg)
		h.waitSnapshot(t)
		waitUntil(t, "cursor persisted at latestId", notifWait, func() bool { return cursor(t, prefs) == latest })
		// The badge follows the server (external threads converge on the
		// stream's 15s ping).
		waitUntil(t, "badge equals GET /api/badge", notifWait, func() bool {
			b, _ := h.sink.badge()
			return b == a.badge()
		})
		b, _ := h.sink.badge()
		t.Logf("first run: latestId %d, badge %d", latest, b)
		if n := h.sink.notes(); len(n) != 0 {
			t.Fatalf("first run showed %+v", n)
		}
		if d := h.delivered.all(); len(d) != 0 {
			t.Fatalf("first run delivered %v", d)
		}
	})
	if t.Failed() {
		return
	}

	var live task
	t.Run("2 live needs_input shows", func(t *testing.T) {
		before, _ := h.sink.badge()
		tk := a.launch(projectID, "Notify live")
		row, tk := a.needsInput(tk.ID)
		live = tk
		want := alerts.Note{ID: noteID(taskTag(tk.ID)), Title: "Needs input", Body: "Notify live · " + projectTitle}
		waitUntil(t, "show "+want.ID, notifWait, func() bool { return slices.Contains(h.sink.notes(), want) })
		if n := h.sink.notes(); len(n) != 1 {
			t.Fatalf("shows %+v, want only %+v", n, want)
		}
		path := fmt.Sprintf("/tasks/%d", tk.ID)
		waitUntil(t, "Target "+path, 5*time.Second, func() bool { return h.clickPath(taskTag(tk.ID)) == path })
		waitUntil(t, "Delivered", 5*time.Second, func() bool { return slices.Contains(h.delivered.all(), row.ID) })
		waitUntil(t, "badge up", notifWait, func() bool {
			b, _ := h.sink.badge()
			return b > before && b == a.badge()
		})
		waitUntil(t, "cursor at the live row", 10*time.Second, func() bool { return cursor(t, prefs) == row.ID })
	})
	if t.Failed() {
		return
	}

	t.Run("4 read elsewhere closes it", func(t *testing.T) {
		tag := taskTag(live.ID)
		start := time.Now()
		a.read(fmt.Sprintf("task:%d", live.ID))
		waitUntil(t, "Close "+tag, 5*time.Second, func() bool { return slices.Contains(h.sink.closed(), noteID(tag)) })
		t.Logf("closed %s %s after the read", tag, time.Since(start).Round(time.Millisecond))
		if got := h.clickPath(tag); got != "/tasks/"+fmt.Sprint(live.ID) {
			t.Fatalf("Target after close %q, want the derived /tasks/%d", got, live.ID)
		}
	})

	t.Run("3 foreground suppresses the show", func(t *testing.T) {
		fg.Store(true)
		defer fg.Store(false)
		shows := len(h.sink.notes())
		tk := a.launch(projectID, "Notify foreground")
		row, _ := a.needsInput(tk.ID)
		waitUntil(t, "Delivered", notifWait, func() bool { return slices.Contains(h.delivered.all(), row.ID) })
		waitUntil(t, "cursor advanced", 10*time.Second, func() bool { return cursor(t, prefs) == row.ID })
		if n := h.sink.notes(); len(n) != shows {
			t.Fatalf("foreground showed %+v", n[shows:])
		}
	})
	if t.Failed() {
		return
	}

	t.Run("5 global filter creates nothing", func(t *testing.T) {
		a.setEvents([]string{"done"})
		defer a.setEvents(allEvents)
		latest, shows := a.latestID(), len(h.sink.notes())
		tk := a.launch(projectID, "Notify filtered")
		waitUntil(t, "task needs_input", notifWait, func() bool { return a.task(tk.ID).State == "needs_input" })
		// The notifier handles the transition at once; allow it time.
		time.Sleep(2 * time.Second)
		if got := a.latestID(); got != latest {
			t.Fatalf("filtered event recorded rows %d..%d: %+v", latest+1, got, a.rows())
		}
		if n := h.sink.notes(); len(n) != shows {
			t.Fatalf("filtered event showed %+v", n[shows:])
		}
	})
	if t.Failed() {
		return
	}

	t.Run("6 catch-up after the app was closed", func(t *testing.T) {
		if err := h.svc.Close(); err != nil {
			t.Fatal(err)
		}
		before := cursor(t, prefs)
		if latest := a.latestID(); before != latest {
			t.Fatalf("cursor %d after close, latestId %d", before, latest)
		}
		tasks := make([]task, 5)
		for i := range tasks {
			tasks[i] = a.launch(projectID, fmt.Sprintf("Notify offline %d", i+1))
		}
		var batch []inboxRow // oldest first
		for _, tk := range tasks {
			row, _ := a.needsInput(tk.ID)
			batch = append(batch, row)
		}
		sort.Slice(batch, func(i, j int) bool { return batch[i].ID < batch[j].ID })
		// Read on another device: the newest group, the first to show
		// otherwise.
		read := batch[len(batch)-1]
		a.read(fmt.Sprintf("task:%d", *read.TaskID))
		unread := batch[:len(batch)-1]
		latest := a.latestID()

		h2 := open(t, prefs, e.sock, 0, &fg)
		var want []alerts.Note
		for _, r := range unread[len(unread)-3:] {
			want = append(want, alerts.Note{ID: noteID(taskTag(*r.TaskID)), Title: "Needs input", Body: r.Title + " · " + projectTitle})
		}
		want = append(want, alerts.Note{ID: noteID(alerts.SummaryID), Title: fmt.Sprintf("%d more in Stage", len(unread)-3), Body: "Open notifications"})
		h2.waitSnapshot(t)
		waitUntil(t, "catch-up shows", notifWait, func() bool { return len(h2.sink.notes()) >= len(want) })
		time.Sleep(500 * time.Millisecond) // nothing more may follow
		if got := h2.sink.notes(); !slices.Equal(got, want) {
			t.Fatalf("catch-up shows\n got %+v\nwant %+v", got, want)
		}
		if got := h2.clickPath(alerts.SummaryID); got != "/?inbox" {
			t.Fatalf("summary Target %q", got)
		}
		if d := h2.delivered.all(); len(d) != 1 {
			t.Fatalf("Delivered %v, want once for the batch", d)
		}
		waitUntil(t, "cursor at latestId", 10*time.Second, func() bool { return cursor(t, prefs) == latest })
		if err := h2.svc.Close(); err != nil {
			t.Fatal(err)
		}

		// Reopened 20 minutes later: nothing new, nothing repeated.
		h3 := open(t, prefs, e.sock, 20*time.Minute, &fg)
		h3.waitSnapshot(t)
		if n := h3.sink.notes(); len(n) != 0 {
			t.Fatalf("reopen showed %+v", n)
		}
		if err := h3.svc.Close(); err != nil {
			t.Fatal(err)
		}

		// Same batch replayed (cursor from before it) 20 minutes later: past
		// the 15 min window, it stays silent (inbox and badge only).
		p, err := alerts.LoadPrefs(prefs)
		if err != nil {
			t.Fatal(err)
		}
		p.Servers[target.LocalID].Cursor = &before
		if err := alerts.SavePrefs(prefs, p); err != nil {
			t.Fatal(err)
		}
		h4 := open(t, prefs, e.sock, 20*time.Minute, &fg)
		h4.waitSnapshot(t)
		if n := h4.sink.notes(); len(n) != 0 {
			t.Fatalf("stale catch-up showed %+v", n)
		}
		if d := h4.delivered.all(); len(d) != 1 {
			t.Fatalf("stale catch-up Delivered %v, want once", d)
		}
		waitUntil(t, "cursor at latestId", 10*time.Second, func() bool { return cursor(t, prefs) == latest })
	})
}

// TestNotificationsServerRestart: a server restart on the same data dir and
// socket shows nothing twice, and rows after it still arrive.
func TestNotificationsServerRestart(t *testing.T) {
	t.Parallel()
	bin := stage(t)
	e := newEnv(t)
	srv := e.serve(t, bin)
	a := api{t: t, sock: e.sock}
	projectID, projectTitle := a.project()
	prefs := filepath.Join(t.TempDir(), "notifications.json")

	h := open(t, prefs, e.sock, 0, nil)
	h.waitSnapshot(t)
	tk := a.launch(projectID, "Notify restart")
	row, _ := a.needsInput(tk.ID)
	first := alerts.Note{ID: noteID(taskTag(tk.ID)), Title: "Needs input", Body: "Notify restart · " + projectTitle}
	waitUntil(t, "show before restart", notifWait, func() bool { return slices.Contains(h.sink.notes(), first) })
	if err := h.svc.Flush(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "cursor at the row", 10*time.Second, func() bool { return cursor(t, prefs) == row.ID })
	shows := len(h.sink.notes())

	srv.kill(syscall.SIGTERM)
	e.serve(t, bin)
	h.svc.Connect(target.Local(e.sock)) // the launcher's OnServer on every ready server

	// A missing harness session preserves execution state. A new Stage-only
	// readiness event proves the notification stream reconnects without replay.
	var source, dependent task
	a.do("POST", "/api/tasks", map[string]any{"projectId": projectID, "title": "Review prerequisite", "prompt": "review"}, 201, &source)
	a.do("POST", "/api/tasks", map[string]any{"projectId": projectID, "title": "Ready after restart", "prompt": "continue", "needs": []int64{source.ID}, "startMode": "manual"}, 201, &dependent)
	var preview struct {
		Revision        int64  `json:"revision"`
		EvidenceVersion string `json:"evidenceVersion"`
	}
	a.do("GET", fmt.Sprintf("/api/tasks/%d/completion-preview", source.ID), nil, 200, &preview)
	a.do("POST", fmt.Sprintf("/api/tasks/%d/complete", source.ID), map[string]any{"requestId": "restart-ready", "revision": preview.Revision, "evidenceVersion": preview.EvidenceVersion}, 200, nil)
	want := alerts.Note{ID: noteID(taskTag(dependent.ID)), Title: "Ready", Body: "Ready after restart · " + projectTitle}
	waitUntil(t, "ready after restart", notifWait, func() bool { return slices.Contains(h.sink.notes(), want) })
	time.Sleep(500 * time.Millisecond)
	if got := a.task(tk.ID).State; got != "needs_input" {
		t.Fatalf("missing session changed execution state: %s", got)
	}
	if got := h.sink.notes()[shows:]; !slices.Equal(got, []alerts.Note{want}) {
		t.Fatalf("notifications after restart: got %+v, want %+v", got, want)
	}
}

// TestNotificationsDatabaseReset: preferences from another database (a
// cursor above its newest id) are clamped silently.
func TestNotificationsDatabaseReset(t *testing.T) {
	t.Parallel()
	bin := stage(t)
	e := newEnv(t)
	e.serve(t, bin)
	a := api{t: t, sock: e.sock}
	prefs := filepath.Join(t.TempDir(), "notifications.json")
	old := int64(1000)
	p := alerts.DefaultPrefs()
	p.Servers[target.LocalID] = &alerts.ServerPrefs{Cursor: &old, Shown: []string{}}
	if err := alerts.SavePrefs(prefs, p); err != nil {
		t.Fatal(err)
	}
	latest := a.latestID()
	if latest >= old {
		t.Fatalf("fresh database latestId %d not below the old cursor %d", latest, old)
	}
	h := open(t, prefs, e.sock, 0, nil)
	h.waitSnapshot(t)
	waitUntil(t, "cursor clamped to latestId", 10*time.Second, func() bool { return cursor(t, prefs) == latest })
	if n := h.sink.notes(); len(n) != 0 {
		t.Fatalf("reset showed %+v", n)
	}
	if d := h.delivered.all(); len(d) != 0 {
		t.Fatalf("reset delivered %v", d)
	}
}
