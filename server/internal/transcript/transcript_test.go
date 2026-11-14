package transcript

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

func newThread(t *testing.T) (*t3.Fake, string) {
	t.Helper()
	f := t3.NewFake()
	res, err := f.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: "p", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return f, res.ThreadID
}

func runID(t *testing.T, f *t3.Fake, id string) string {
	t.Helper()
	d, err := f.ReadThread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.ActiveRunID
}

func positions(items []Item) []int {
	out := make([]int, len(items))
	for i, it := range items {
		out[i] = it.Position
	}
	return out
}

func eq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTailPagesAcrossT3Pages(t *testing.T) {
	f, id := newThread(t)
	for i := range 250 {
		f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "m"})
		if i%2 == 0 {
			f.AddItem(id, t3.TimelineItem{Type: "command_execution", Text: "ls"}) // not a message
		}
	}
	_ = f.SetStatus(id, t3.StatusCompleted)
	r := Reader{T3: f}
	ctx := context.Background()

	page, err := r.Tail(ctx, id, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 || !page.HasEarlier || page.Cursor == nil || page.Active {
		t.Fatalf("tail = %d items, earlier=%v cursor=%v active=%v", len(page.Items), page.HasEarlier, page.Cursor, page.Active)
	}
	last := page.Items[len(page.Items)-1].Position
	if *page.Cursor != last {
		t.Fatalf("cursor = %d, want last position %d (all final)", *page.Cursor, last)
	}
	for i := 1; i < len(page.Items); i++ {
		if page.Items[i].Position <= page.Items[i-1].Position {
			t.Fatal("items not ascending")
		}
	}

	// Page backwards to the start with before cursors.
	seen := len(page.Items)
	before := page.Items[0].Position
	for page.HasEarlier {
		page, err = r.Tail(ctx, id, &before, 50)
		if err != nil {
			t.Fatal(err)
		}
		if page.Cursor != nil || page.Items[len(page.Items)-1].Position >= before {
			t.Fatalf("earlier page wrong: cursor=%v last=%d before=%d", page.Cursor, page.Items[len(page.Items)-1].Position, before)
		}
		seen += len(page.Items)
		before = page.Items[0].Position
	}
	if seen != 250 || before != 0 {
		t.Fatalf("paged %d items to %d, want 250 to 0", seen, before)
	}
}

func TestTailEmptyAndStreamingCursor(t *testing.T) {
	f, id := newThread(t)
	r := Reader{T3: f}
	page, err := r.Tail(context.Background(), id, nil, 0)
	if err != nil || len(page.Items) != 0 || *page.Cursor != -1 || !page.Active {
		t.Fatalf("empty page = %+v, %v", page, err)
	}
	run := runID(t, f, id)
	f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "q", RunID: run})
	f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "partial", RunID: run, Status: "running"})
	f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "old stale", RunID: "old-run", Status: "running"})
	page, err = r.Tail(context.Background(), id, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if *page.Cursor != 0 || !page.Items[1].Streaming || page.Items[2].Streaming || page.Items[0].Kind != "user" {
		t.Fatalf("cursor=%d items=%+v", *page.Cursor, page.Items)
	}
}

func TestPreviewAndFullText(t *testing.T) {
	f, id := newThread(t)
	long := strings.Repeat("abcdefghij", 12000) // 120k chars: three T3 text pages
	f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: long, ItemID: "big"})
	r := Reader{T3: f}
	page, err := r.Tail(context.Background(), id, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if it := page.Items[0]; !it.Truncated || len(it.Text) != PreviewChars {
		t.Fatalf("preview truncated=%v len=%d", it.Truncated, len(it.Text))
	}
	text, truncated, err := r.FullText(context.Background(), id, "big")
	if err != nil || truncated || text != long {
		t.Fatalf("full text len=%d truncated=%v err=%v", len(text), truncated, err)
	}
	if _, _, err := r.FullText(context.Background(), id, "missing"); err != ErrItemNotFound {
		t.Fatalf("missing item err = %v", err)
	}
}

// --- hub ---

type mirror struct{ on atomic.Bool }

func (m *mirror) check(context.Context, string) (bool, error) { return m.on.Load(), nil }

func newHub(t *testing.T, f *t3.Fake, m *mirror) *Hub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewHub(ctx, f, m.check, nil)
	h.PollEvery, h.IdleEvery, h.Grace, h.MaxBackoff = 10*time.Millisecond, 10*time.Millisecond, 60*time.Millisecond, 40*time.Millisecond
	return h
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// next reads the next event (fails on timeout or close).
func next(t *testing.T, s *Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-s.Events:
		if !ok {
			t.Fatal("subscription closed")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

// until reads events until match returns true.
func until(t *testing.T, s *Subscription, what string, match func(Event) bool) Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ev := next(t, s); match(ev) {
			return ev
		}
	}
	t.Fatalf("no event: %s", what)
	return Event{}
}

func TestHubSharesOnePollerAndStreamsUpserts(t *testing.T) {
	f, id := newThread(t)
	run := runID(t, f, id)
	f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "q", RunID: run})
	h := newHub(t, f, &mirror{})

	a := h.Subscribe(id, -1)
	if ev := next(t, a); ev.Item == nil || ev.Item.Position != 0 {
		t.Fatalf("backfill = %+v", ev)
	}
	if ev := next(t, a); ev.State == nil || !ev.State.Active {
		t.Fatalf("state = %+v", ev)
	}
	b := h.Subscribe(id, 0)
	if ev := next(t, b); ev.State == nil {
		t.Fatalf("second subscriber gets cached state only, got %+v", ev)
	}
	if h.Pollers() != 1 {
		t.Fatalf("pollers = %d, want 1 shared", h.Pollers())
	}

	pos := f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "Hel", RunID: run, Status: "running"})
	for _, s := range []*Subscription{a, b} {
		ev := until(t, s, "streaming item", func(ev Event) bool { return ev.Item != nil && ev.Item.Position == pos })
		if !ev.Item.Streaming || ev.Item.Text != "Hel" || ev.Cursor != 0 {
			t.Fatalf("streaming event = %+v cursor=%d", ev.Item, ev.Cursor)
		}
	}
	time.Sleep(time.Millisecond)
	_ = f.UpdateItem(id, pos, func(it *t3.TimelineItem) { it.Text = "Hello" })
	ev := until(t, a, "grown text", func(ev Event) bool { return ev.Item != nil && ev.Item.Text == "Hello" })
	if ev.Item.Position != pos {
		t.Fatalf("upsert position = %d", ev.Item.Position)
	}

	// Run ends: final text arrives, cursor advances, state goes idle.
	time.Sleep(time.Millisecond)
	_ = f.UpdateItem(id, pos, func(it *t3.TimelineItem) { it.Text = "Hello there"; it.Status = "completed" })
	_ = f.SetStatus(id, t3.StatusCompleted)
	until(t, a, "idle state", func(ev Event) bool { return ev.State != nil && !ev.State.Active })
	reads := f.ItemReads()
	time.Sleep(80 * time.Millisecond)
	if got := f.ItemReads(); got != reads {
		t.Fatalf("idle poller kept reading T3: %d -> %d", reads, got)
	}
	a.Close()
	b.Close()
	eventually(t, "poller retired after grace", func() bool { return h.Pollers() == 0 })
}

func TestHubIdleResumesFromMirror(t *testing.T) {
	f, id := newThread(t)
	_ = f.SetStatus(id, t3.StatusCompleted)
	m := &mirror{}
	h := newHub(t, f, m)
	s := h.Subscribe(id, -1)
	if ev := next(t, s); ev.State == nil || ev.State.Active {
		t.Fatalf("state = %+v", ev)
	}
	reads := f.ItemReads()
	time.Sleep(60 * time.Millisecond)
	if f.ItemReads() != reads {
		t.Fatal("idle poller read T3")
	}
	_ = f.SetStatus(id, t3.StatusRunning)
	_ = f.SetThread(id, func(d *t3.ThreadDetail) { d.ActiveRunID = "run-2" })
	pos := f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "follow-up", RunID: "run-2"})
	m.on.Store(true)
	ev := until(t, s, "resumed item", func(ev Event) bool { return ev.Item != nil })
	if ev.Item.Position != pos || ev.Item.Text != "follow-up" {
		t.Fatalf("item = %+v", ev.Item)
	}
	s.Close()
}

func TestHubResumeFromCursorSkipsOlderItems(t *testing.T) {
	f, id := newThread(t)
	run := runID(t, f, id)
	for range 3 {
		f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "x", RunID: run})
	}
	stream := f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "…", RunID: run, Status: "running"})
	h := newHub(t, f, &mirror{})
	first := h.Subscribe(id, -1)
	until(t, first, "state", func(ev Event) bool { return ev.State != nil })

	// A reconnect from cursor 1 (an older cursor than the poller's) gets
	// exactly the items after it, including the streaming one.
	re := h.Subscribe(id, 1)
	var got []int
	for {
		ev := next(t, re)
		if ev.State != nil {
			break
		}
		got = append(got, ev.Item.Position)
	}
	if !eq(got, []int{2, stream}) {
		t.Fatalf("resume from 1 got %v, want [2 %d]", got, stream)
	}
	first.Close()
	re.Close()
}

// A client that loads a fresh REST page between poller ticks subscribes with a
// cursor ahead of the poller's. The next tick must not replay items at or
// below that cursor, and event ids must not go below it.
func TestHubJoinAheadOfPollerCursor(t *testing.T) {
	f, id := newThread(t)
	run := runID(t, f, id)
	f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "q", RunID: run})
	h := newHub(t, f, &mirror{})
	h.PollEvery = 50 * time.Millisecond
	a := h.Subscribe(id, -1)
	until(t, a, "state", func(ev Event) bool { return ev.State != nil })

	ahead := f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "done", RunID: run, Status: "completed"})
	stream := f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "…", RunID: run, Status: "running"})
	b := h.Subscribe(id, ahead)
	for {
		ev := next(t, b)
		if ev.Cursor < ahead {
			t.Fatalf("event id %d below join cursor %d", ev.Cursor, ahead)
		}
		if ev.Item == nil {
			continue
		}
		if ev.Item.Position <= ahead {
			t.Fatalf("replayed item %d at or below join cursor %d", ev.Item.Position, ahead)
		}
		if ev.Item.Position == stream {
			break
		}
	}
	a.Close()
	b.Close()
}

func TestHubPositionShiftResets(t *testing.T) {
	f, id := newThread(t)
	run := runID(t, f, id)
	pos := f.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "a", RunID: run, Status: "running", ItemID: "one"})
	h := newHub(t, f, &mirror{})
	s := h.Subscribe(id, -1)
	until(t, s, "state", func(ev Event) bool { return ev.State != nil })
	_ = f.UpdateItem(id, pos, func(it *t3.TimelineItem) { it.ItemID = "other" })
	eventually(t, "reset", func() bool {
		select {
		case _, ok := <-s.Events:
			return !ok && s.Reset()
		default:
			return false
		}
	})
	eventually(t, "poller gone", func() bool { return h.Pollers() == 0 })
}

func TestHubDropsSlowSubscriber(t *testing.T) {
	f, id := newThread(t)
	run := runID(t, f, id)
	for range 10 {
		f.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "x", RunID: run})
	}
	h := newHub(t, f, &mirror{})
	h.Buffer = 3
	s := h.Subscribe(id, -1) // never read: 10 backfill items overflow 3
	eventually(t, "dropped", func() bool { return s.Reset() })
}

func TestHubStopsOnShutdown(t *testing.T) {
	f, id := newThread(t)
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHub(ctx, f, (&mirror{}).check, nil)
	h.PollEvery = 10 * time.Millisecond
	s := h.Subscribe(id, -1)
	until(t, s, "state", func(ev Event) bool { return ev.State != nil })
	cancel()
	eventually(t, "closed", func() bool {
		select {
		case _, ok := <-s.Events:
			return !ok && !s.Reset()
		default:
			return false
		}
	})
}

func TestHubReportsT3ErrorsAndRecovers(t *testing.T) {
	f, id := newThread(t)
	h := newHub(t, f, &mirror{})
	s := h.Subscribe(id, -1)
	until(t, s, "state", func(ev Event) bool { return ev.State != nil && ev.State.Error == nil })
	f.RemoveThread(id)
	until(t, s, "error state", func(ev Event) bool { return ev.State != nil && ev.State.Error != nil })
	s.Close()
}
