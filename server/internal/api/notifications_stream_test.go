package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/notify"
)

// inboxEnv is an env whose API wakes on hub, served over HTTP.
func inboxEnv(t *testing.T, ctx context.Context) (*env, *notify.Hub, *httptest.Server) {
	t.Helper()
	hub := notify.NewHub(ctx)
	e := newEnv(t, func(d *api.Deps) { d.Inbox = hub })
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	return e, hub, srv
}

func openInbox(t *testing.T, srv *httptest.Server, after, lastEventID string) (*http.Response, <-chan sseEvent) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	u := srv.URL + "/api/notifications/stream"
	if after != "" {
		u += "?after=" + after
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return resp, sseReader(t, resp)
}

type helloEvent struct {
	LatestID int64 `json:"latestId"`
	Reset    bool  `json:"reset"`
}

type unreadEvent struct {
	Tags  []string `json:"tags"`
	Badge int      `json:"badge"`
}

// streamRow mirrors the notification event data; unknown fields fail.
type streamRow struct {
	ID        int64     `json:"id"`
	Event     string    `json:"event"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
	Tag       string    `json:"tag"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
}

func decodeEvent[T any](t *testing.T, ev sseEvent, name string) T {
	t.Helper()
	var v T
	if ev.name != name {
		t.Fatalf("want %s event, got %+v", name, ev)
	}
	dec := json.NewDecoder(strings.NewReader(ev.data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s data %q: %v", name, ev.data, err)
	}
	return v
}

// readHello consumes retry and hello.
func readHello(t *testing.T, events <-chan sseEvent) (helloEvent, string) {
	t.Helper()
	if ev := nextSSE(t, events, true); ev.name != "retry" || ev.data != "3000" {
		t.Fatalf("first = %+v", ev)
	}
	ev := nextSSE(t, events, true)
	return decodeEvent[helloEvent](t, ev, "hello"), ev.id
}

func insertDone(t *testing.T, e *env, thread string) int64 {
	t.Helper()
	id, err := e.store.InsertNotification(context.Background(), db.NewNotification{
		Event: db.StateDone, Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: thread}, Title: thread, URL: "/sessions/" + thread,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNotificationStreamNoCursorThenLive(t *testing.T) {
	defer api.SetStreamPing(30 * time.Millisecond)()
	e, hub, srv := inboxEnv(t, t.Context())
	old := insertDone(t, e, "ext-1")

	resp, events := openInbox(t, srv, "", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" ||
		resp.Header.Get("Cache-Control") != "no-cache, no-transform" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("status %d headers %v", resp.StatusCode, resp.Header)
	}
	// No cursor: start at latest, nothing replayed.
	if h, id := readHello(t, events); h.LatestID != old || h.Reset || id != fmt.Sprint(old) {
		t.Fatalf("hello %+v id %s", h, id)
	}
	if u := decodeEvent[unreadEvent](t, nextSSE(t, events, true), "unread"); fmt.Sprint(u.Tags) != "[session-1:ext-1]" || u.Badge != 0 {
		t.Fatalf("unread %+v", u)
	}

	// Live: an insert plus a signal delivers the row, then the new snapshot.
	id := insertDone(t, e, "ext-2")
	hub.Signal()
	ev := nextSSE(t, events, true)
	row := decodeEvent[streamRow](t, ev, "notification")
	if row.ID != id || ev.id != fmt.Sprint(id) || row.Tag != "session-1:ext-2" || row.Title != "Done" {
		t.Fatalf("live row %+v id %s", row, ev.id)
	}
	if u := decodeEvent[unreadEvent](t, nextSSE(t, events, true), "unread"); fmt.Sprint(u.Tags) != "[session-1:ext-1 session-1:ext-2]" {
		t.Fatalf("live unread %+v", u)
	}

	// Reading a group (on any device) sends a snapshot without its tag.
	e.must("POST", "/api/notifications/read", map[string]any{"groups": []string{"session:1:ext-1"}}, 204, nil)
	if u := decodeEvent[unreadEvent](t, nextSSE(t, events, true), "unread"); fmt.Sprint(u.Tags) != "[session-1:ext-2]" {
		t.Fatalf("unread after read %+v", u)
	}
	// A wake with nothing changed sends nothing; the next ping is next.
	hub.Signal()
	if ev := nextSSE(t, events, false); ev.name != ":" || ev.data != "ping" {
		t.Fatalf("want ping, got %+v", ev)
	}
}

func TestNotificationStreamReplay(t *testing.T) {
	e, _, srv := inboxEnv(t, t.Context())
	ctx := context.Background()
	p := e.project("/w/1")
	tk := e.task(p.ID, "Say OK", nil)
	if _, err := e.store.TransitionTask(ctx, db.Transition{TaskID: tk.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	first := insertDone(t, e, "ext-1")
	project := tk.ProjectTitle
	thread := "provider:session/x"
	taskRow, err := e.store.InsertNotification(ctx, db.NewNotification{Event: db.StateNeedsInput, TaskID: &tk.ID,
		Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: thread}, Title: tk.Title, ProjectTitle: &project, URL: fmt.Sprintf("/tasks/%d", tk.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkNotificationsRead(ctx, first); err != nil {
		t.Fatal(err)
	}
	extRow := insertDone(t, e, "ext-2")

	// Last-Event-ID wins over ?after.
	_, events := openInbox(t, srv, "0", fmt.Sprint(first))
	if h, id := readHello(t, events); h.LatestID != extRow || h.Reset || id != fmt.Sprint(first) {
		t.Fatalf("hello %+v id %s", h, id)
	}
	ev := nextSSE(t, events, true)
	got := decodeEvent[streamRow](t, ev, "notification")
	want := streamRow{ID: taskRow, Event: "needs_input", Read: false, Tag: fmt.Sprintf("task-%d", tk.ID),
		Title: "Needs input", Body: "Say OK · " + project, URL: fmt.Sprintf("/tasks/%d", tk.ID)}
	if got.CreatedAt.IsZero() || ev.id != fmt.Sprint(taskRow) {
		t.Fatalf("createdAt/id %+v %s", got, ev.id)
	}
	got.CreatedAt = time.Time{}
	if got != want {
		t.Fatalf("task row %+v, want %+v", got, want)
	}
	if got := decodeEvent[streamRow](t, nextSSE(t, events, true), "notification"); got.ID != extRow || got.URL != "/?inbox" || got.Body != "ext-2" {
		t.Fatalf("thread row %+v", got)
	}
	u := decodeEvent[unreadEvent](t, nextSSE(t, events, true), "unread")
	if fmt.Sprint(u.Tags) != fmt.Sprintf("[session-1:ext-2 task-%d]", tk.ID) || u.Badge != 1 {
		t.Fatalf("unread %+v", u)
	}

	// ?after replays read rows too, flagged read.
	_, events = openInbox(t, srv, "0", "")
	readHello(t, events)
	if got := decodeEvent[streamRow](t, nextSSE(t, events, true), "notification"); got.ID != first || !got.Read {
		t.Fatalf("read row %+v", got)
	}
}

func TestNotificationStreamCursorRules(t *testing.T) {
	e, _, srv := inboxEnv(t, t.Context())
	var latest int64
	for i := range 105 {
		latest = insertDone(t, e, fmt.Sprintf("ext-%d", i))
	}

	// Cursor past the newest row (e.g. a reset database): reset, id = latest.
	_, events := openInbox(t, srv, fmt.Sprint(latest+10), "")
	if h, id := readHello(t, events); h.LatestID != latest || !h.Reset || id != fmt.Sprint(latest) {
		t.Fatalf("reset hello %+v id %s", h, id)
	}
	if ev := nextSSE(t, events, true); ev.name != "unread" {
		t.Fatalf("after reset want unread, got %+v", ev)
	}

	// 105 rows after the cursor: gap, then only the newest 100, ascending.
	_, events = openInbox(t, srv, "0", "")
	readHello(t, events)
	if ev := nextSSE(t, events, true); ev.name != "gap" || ev.data != "{}" || ev.id != "" {
		t.Fatalf("want gap, got %+v", ev)
	}
	for want := latest - 99; want <= latest; want++ {
		if got := decodeEvent[streamRow](t, nextSSE(t, events, true), "notification"); got.ID != want {
			t.Fatalf("replay id %d, want %d", got.ID, want)
		}
	}
	ev := nextSSE(t, events, true)
	if u := decodeEvent[unreadEvent](t, ev, "unread"); len(u.Tags) != 105 || ev.id != "" {
		t.Fatalf("unread %d tags id %q", len(u.Tags), ev.id)
	}

	for _, tc := range []struct{ after, hdr string }{
		{"x", ""}, {"-1", ""}, {"1.5", ""}, {"", "bad"}, {"1", "-2"},
	} {
		req, _ := http.NewRequest("GET", srv.URL+"/api/notifications/stream?after="+tc.after, nil)
		if tc.hdr != "" {
			req.Header.Set("Last-Event-ID", tc.hdr)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("after %q Last-Event-ID %q: %d, want 400", tc.after, tc.hdr, resp.StatusCode)
		}
	}
}

func TestNotificationStreamPingAndHubShutdown(t *testing.T) {
	defer api.SetStreamPing(30 * time.Millisecond)()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, _, srv := inboxEnv(t, ctx)
	ctxBg := context.Background()
	p := e.project("/w/1")
	tk := e.task(p.ID, "Say OK", nil)

	_, events := openInbox(t, srv, "", "")
	readHello(t, events)
	if u := decodeEvent[unreadEvent](t, nextSSE(t, events, true), "unread"); len(u.Tags) != 0 || u.Tags == nil || u.Badge != 0 {
		t.Fatalf("empty unread %+v", u)
	}
	if ev := nextSSE(t, events, false); ev.name != ":" || ev.data != "ping" {
		t.Fatalf("want ping, got %+v", ev)
	}
	// A badge change without a signal (e.g. an external thread) shows up at
	// the next ping.
	if _, err := e.store.TransitionTask(ctxBg, db.Transition{TaskID: tk.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	if u := decodeEvent[unreadEvent](t, nextSSE(t, events, true), "unread"); u.Badge != 1 {
		t.Fatalf("badge after ping %+v", u)
	}

	// The hub's ctx ending closes open streams.
	cancel()
	for {
		ev := nextSSE(t, events, true)
		if ev.name == "EOF" {
			break
		}
	}
}

type sseEvent struct{ name, id, data string }

// sseReader parses an event stream; comments come back as name ":".
func sseReader(t *testing.T, resp *http.Response) <-chan sseEvent {
	t.Helper()
	out := make(chan sseEvent, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev != (sseEvent{}) {
					out <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, ":"):
				out <- sseEvent{name: ":", data: strings.TrimSpace(line[1:])}
			case strings.HasPrefix(line, "event: "):
				ev.name = line[len("event: "):]
			case strings.HasPrefix(line, "id: "):
				ev.id = line[len("id: "):]
			case strings.HasPrefix(line, "data: "):
				ev.data = line[len("data: "):]
			case strings.HasPrefix(line, "retry: "):
				out <- sseEvent{name: "retry", data: line[len("retry: "):]}
			}
		}
	}()
	return out
}

func nextSSE(t *testing.T, ch <-chan sseEvent, skipPings bool) sseEvent {
	t.Helper()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return sseEvent{name: "EOF"}
			}
			if skipPings && ev.name == ":" {
				continue
			}
			return ev
		case <-time.After(3 * time.Second):
			t.Fatal("no event")
		}
	}
}
