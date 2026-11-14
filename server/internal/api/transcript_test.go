package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/transcript"
)

// transcriptSession launches a fake T3 thread and mirrors it.
func transcriptSession(t *testing.T, e *env) (id, run string) {
	t.Helper()
	ctx := context.Background()
	res, err := e.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: "p", Title: "talk"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.fake.ReadThread(ctx, res.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertThreads(ctx, []t3.Thread{d.Thread}); err != nil {
		t.Fatal(err)
	}
	return res.ThreadID, d.ActiveRunID
}

type transcriptPage struct {
	Items      []transcript.Item `json:"items"`
	HasEarlier bool              `json:"hasEarlier"`
	Cursor     *int              `json:"cursor"`
	Active     bool              `json:"active"`
	Status     string            `json:"status"`
}

func TestTranscriptPageAndItem(t *testing.T) {
	e := newEnv(t)
	id, run := transcriptSession(t, e)
	base := "/api/threads/" + url.PathEscape(id) + "/transcript"
	for i := range 5 {
		e.fake.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "q", RunID: run, CreatedBy: "user"})
		e.fake.AddItem(id, t3.TimelineItem{Type: "command_execution", Text: "ls"})
		text := "a"
		if i == 4 {
			text = strings.Repeat("x", transcript.PreviewChars+10)
		}
		e.fake.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: text, RunID: run, ItemID: "reply-" + string(rune('0'+i))})
	}

	var page transcriptPage
	e.must("GET", base+"?limit=4", nil, 200, &page)
	if len(page.Items) != 4 || !page.HasEarlier || page.Cursor == nil || *page.Cursor != page.Items[3].Position || !page.Active || page.Status != "running" {
		t.Fatalf("page = %+v", page)
	}
	last := page.Items[3]
	if last.Kind != "assistant" || !last.Truncated || len(last.Text) != transcript.PreviewChars {
		t.Fatalf("last = %+v", last)
	}
	var earlier transcriptPage
	e.must("GET", base+"?limit=100&before="+itoa(page.Items[0].Position), nil, 200, &earlier)
	if len(earlier.Items) != 6 || earlier.HasEarlier || earlier.Cursor != nil {
		t.Fatalf("earlier = %+v", earlier)
	}

	var full struct {
		ItemID    string `json:"itemId"`
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	}
	e.must("GET", base+"/items/"+url.PathEscape(last.ItemID), nil, 200, &full)
	if len(full.Text) != transcript.PreviewChars+10 || full.Truncated || full.ItemID != last.ItemID {
		t.Fatalf("full = %d chars, %+v", len(full.Text), full.Truncated)
	}

	e.must("GET", base+"/items/nope", nil, 404, nil)
	e.must("GET", base+"?limit=0", nil, 400, nil)
	e.must("GET", base+"?limit=201", nil, 400, nil)
	e.must("GET", base+"?before=-1", nil, 400, nil)
	e.must("GET", "/api/threads/unknown/transcript", nil, 404, nil)
	e.fake.RemoveThread(id) // mirrored but gone from T3
	e.must("GET", base, nil, 404, nil)
}

func itoa(n int) string { return strconv.Itoa(n) }

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

func openStream(t *testing.T, srv *httptest.Server, id string, after string, lastEventID string) (*http.Response, <-chan sseEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	u := srv.URL + "/api/threads/" + url.PathEscape(id) + "/transcript/stream"
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
	return resp, sseReader(t, resp), cancel
}

func itemOf(t *testing.T, ev sseEvent) transcript.Item {
	t.Helper()
	if ev.name != "item" {
		t.Fatalf("want item event, got %+v", ev)
	}
	var it transcript.Item
	if err := json.Unmarshal([]byte(ev.data), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func TestTranscriptStreamResumeAndLive(t *testing.T) {
	e := newEnv(t)
	defer api.SetStreamPing(30 * time.Millisecond)()
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	id, run := transcriptSession(t, e)
	for range 3 {
		e.fake.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "q", RunID: run})
	}

	resp, events, cancel := openStream(t, srv, id, "0", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" ||
		resp.Header.Get("Cache-Control") != "no-cache, no-transform" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("status %d headers %v", resp.StatusCode, resp.Header)
	}
	if ev := nextSSE(t, events, true); ev.name != "retry" || ev.data != "3000" {
		t.Fatalf("first = %+v", ev)
	}
	// ?after=0: backfill is positions 1 and 2 only, then state.
	if it := itemOf(t, nextSSE(t, events, true)); it.Position != 1 {
		t.Fatalf("backfill starts at %d", it.Position)
	}
	if it := itemOf(t, nextSSE(t, events, true)); it.Position != 2 {
		t.Fatalf("backfill second %d", it.Position)
	}
	st := nextSSE(t, events, true)
	if st.name != "state" || !strings.Contains(st.data, `"active":true`) || !strings.Contains(st.data, `"error":null`) {
		t.Fatalf("state = %+v", st)
	}

	// Live: a streaming reply arrives, then grows in place.
	pos := e.fake.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "Hel", RunID: run, Status: "running"})
	ev := nextSSE(t, events, true)
	it := itemOf(t, ev)
	if it.Position != pos || !it.Streaming || ev.id != "2" {
		t.Fatalf("live = %+v id=%s", it, ev.id)
	}
	time.Sleep(2 * time.Millisecond)
	_ = e.fake.UpdateItem(id, pos, func(x *t3.TimelineItem) { x.Text = "Hello" })
	if it := itemOf(t, nextSSE(t, events, true)); it.Text != "Hello" || it.Position != pos {
		t.Fatalf("upsert = %+v", it)
	}
	// Pings flow while nothing changes.
	if ev := nextSSE(t, events, false); ev.name != ":" || ev.data != "ping" {
		t.Fatalf("want ping, got %+v", ev)
	}
	cancel()

	// Reconnect: Last-Event-ID wins over ?after; resend starts after it.
	_, events2, cancel2 := openStream(t, srv, id, "-1", "2")
	nextSSE(t, events2, true) // retry
	if it := itemOf(t, nextSSE(t, events2, true)); it.Position != pos || it.Text != "Hello" {
		t.Fatalf("resume = %+v", it)
	}
	if ev := nextSSE(t, events2, true); ev.name != "state" {
		t.Fatalf("resume state = %+v", ev)
	}
	cancel2()
	deadline := time.Now().Add(3 * time.Second)
	for e.hub.Pollers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("poller kept running after streams closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTranscriptStreamResetAndErrors(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	id, run := transcriptSession(t, e)
	pos := e.fake.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "a", RunID: run, Status: "running"})

	_, events, _ := openStream(t, srv, id, "", "")
	nextSSE(t, events, true) // retry
	itemOf(t, nextSSE(t, events, true))
	nextSSE(t, events, true) // state
	_ = e.fake.UpdateItem(id, pos, func(x *t3.TimelineItem) { x.ItemID = "replaced" })
	for {
		ev := nextSSE(t, events, true)
		if ev.name == "reset" {
			break
		}
		if ev.name == "EOF" {
			t.Fatal("stream closed without reset")
		}
	}
	if ev := nextSSE(t, events, true); ev.name != "EOF" {
		t.Fatalf("stream should close after reset, got %+v", ev)
	}

	for _, tc := range []struct {
		path string
		hdr  string
		want int
	}{
		{"/api/threads/unknown/transcript/stream", "", 404},
		{"/api/threads/" + url.PathEscape(id) + "/transcript/stream?after=x", "", 400},
		{"/api/threads/" + url.PathEscape(id) + "/transcript/stream", "bad", 400},
	} {
		req, _ := http.NewRequest("GET", srv.URL+tc.path, nil)
		if tc.hdr != "" {
			req.Header.Set("Last-Event-ID", tc.hdr)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s (%q): %d, want %d", tc.path, tc.hdr, resp.StatusCode, tc.want)
		}
	}
}
