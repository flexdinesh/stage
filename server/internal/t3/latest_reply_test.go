package t3

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tlItem is one visible timeline item; its position is its index.
type tlItem struct {
	typ, vis, runID, text string
}

// timeline models T3 t3_thread_read over a visible timeline (readThread +
// timelineItem + getTimelinePage): global positions, messages view filtering,
// afterPosition/limit paging, and itemId + textOffset UTF-16 text slicing.
type timeline struct {
	t     *testing.T
	mu    sync.Mutex
	items []tlItem
	calls []map[string]any
}

func user() tlItem     { return tlItem{typ: "user_message", vis: "local", text: "hi"} }
func activity() tlItem { return tlItem{typ: "command_execution", vis: "local", text: "ls"} }
func reply(run, text string) tlItem {
	return tlItem{typ: "assistant_message", vis: "local", runID: run, text: text}
}
func inherited(run string) tlItem {
	return tlItem{typ: "assistant_message", vis: "inherited", runID: run, text: "parent"}
}
func fill(n int, it tlItem) []tlItem { return slices.Repeat([]tlItem{it}, n) }

func newTimeline(t *testing.T, f *fakeT3, items ...[]tlItem) *timeline {
	tl := &timeline{t: t, items: slices.Concat(items...)}
	f.tool("t3_thread_read", tl.read)
	return tl
}

func (tl *timeline) recorded() []map[string]any {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return slices.Clone(tl.calls)
}

func intArg(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return def
}

func (tl *timeline) wireItem(pos int, maxChars, offset int) map[string]any {
	it := tl.items[pos]
	units := utf16.Encode([]rune(it.text))
	offset = min(offset, len(units))
	end := min(offset+maxChars, len(units))
	if end < len(units) && end > offset+1 && utf16.IsSurrogate(rune(units[end-1])) && units[end-1] < 0xdc00 {
		end-- // T3 textPageEnd: never split a surrogate pair
	}
	truncated := end < len(units)
	var runID, msgID, next any
	if it.runID != "" {
		runID = it.runID
	}
	if strings.HasSuffix(it.typ, "_message") {
		msgID = "msg-" + strconv.Itoa(pos)
	}
	if truncated {
		next = end
	}
	return map[string]any{
		"position": pos, "visibility": it.vis, "sourceThreadId": "t1", "itemId": "item-" + strconv.Itoa(pos),
		"runId": runID, "messageId": msgID, "type": it.typ, "status": "completed", "title": nil,
		"text": string(utf16.Decode(units[offset:end])), "textTruncated": truncated, "nextTextOffset": next,
		"updatedAt": "2026-11-14T00:00:00.000Z",
	}
}

func (tl *timeline) read(args map[string]any) (*mcp.CallToolResult, error) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.calls = append(tl.calls, args)
	if args["threadId"] != "t1" {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
			Text: `{"_tag":"OrchestratorMcpFailure","code":"thread_not_found","message":"gone"}`,
		}}}, nil
	}
	maxChars := intArg(args, "maxCharsPerItem", 2000)
	items := []map[string]any{}
	hasMore := false
	if id, ok := args["itemId"].(string); ok {
		for pos := range tl.items {
			if "item-"+strconv.Itoa(pos) == id {
				items = append(items, tl.wireItem(pos, maxChars, intArg(args, "textOffset", 0)))
			}
		}
	} else {
		after, limit := intArg(args, "afterPosition", -1), intArg(args, "limit", 50)
		messagesView := args["view"] == "messages"
		for pos, it := range tl.items {
			if pos <= after {
				continue
			}
			if messagesView && it.typ != "user_message" && it.typ != "assistant_message" && it.typ != "proposed_plan" {
				continue
			}
			if len(items) == limit {
				hasMore = true
				break
			}
			items = append(items, tl.wireItem(pos, maxChars, 0))
		}
	}
	var next any
	if len(items) > 0 {
		next = items[len(items)-1]["position"]
	}
	b, err := json.Marshal(map[string]any{
		"thread":     map[string]any{"threadId": "t1", "projectId": "p1", "status": "idle", "itemCount": len(tl.items)},
		"recentRuns": []any{}, "items": items, "nextPosition": next, "hasMore": hasMore,
	})
	if err != nil {
		return nil, err
	}
	return structured(string(b)), nil
}

// scans returns afterPosition per messages-view call (-1 = omitted), asserting scan args.
func scans(t *testing.T, calls []map[string]any) []int {
	t.Helper()
	var out []int
	for _, a := range calls {
		if a["view"] == nil {
			continue
		}
		if a["view"] != "messages" || a["maxCharsPerItem"] != float64(1) || a["limit"] != float64(100) || a["itemId"] != nil {
			t.Fatalf("scan args = %v", a)
		}
		if v, ok := a["afterPosition"].(float64); ok {
			if v < 0 {
				t.Fatalf("negative afterPosition sent: %v", a)
			}
			out = append(out, int(v))
		} else {
			out = append(out, -1)
		}
	}
	return out
}

// textReads returns (textOffset, maxCharsPerItem) per itemId call.
func textReads(calls []map[string]any) [][2]int {
	var out [][2]int
	for _, a := range calls {
		if a["itemId"] != nil {
			out = append(out, [2]int{intArg(a, "textOffset", -1), intArg(a, "maxCharsPerItem", -1)})
		}
	}
	return out
}

func TestLatestReplyNewestWindowWithInitialRead(t *testing.T) {
	f := newFakeT3(t)
	tl := newTimeline(t, f, []tlItem{user(), activity(), reply("run-1", "first"), activity(), user(), reply("run-2", "second"), activity()})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())

	r, err := c.LatestReply(ctxT(t), LatestReplyInput{ThreadID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ItemID != "item-5" || r.RunID != "run-2" || r.Text != "second" || r.Truncated || r.UpdatedAt.IsZero() {
		t.Fatalf("reply = %+v", r)
	}
	calls := tl.recorded()
	if a := calls[0]; a["limit"] != float64(1) || a["runLimit"] != float64(1) || a["maxCharsPerItem"] != float64(1) || a["view"] != nil || a["itemId"] != nil {
		t.Fatalf("initial read args = %v", a)
	}
	if got := scans(t, calls); !slices.Equal(got, []int{-1}) {
		t.Fatalf("scan afterPositions = %v", got)
	}
	if got := textReads(calls); !slices.Equal(got, [][2]int{{0, 50000}}) {
		t.Fatalf("text reads = %v", got)
	}
	if a := calls[len(calls)-1]; a["itemId"] != "item-5" || a["runLimit"] != float64(1) || a["view"] != nil {
		t.Fatalf("text read args = %v", a)
	}
}

func TestLatestReplyStepsBackAcrossWindows(t *testing.T) {
	f := newFakeT3(t)
	// 350 items: reply at 120; an inherited assistant item at 260 is skipped.
	tl := newTimeline(t, f, fill(120, activity()), []tlItem{reply("run-1", "old reply")}, fill(139, user()),
		[]tlItem{inherited("run-p")}, fill(89, activity()))
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())

	r, err := c.LatestReply(ctxT(t), LatestReplyInput{ThreadID: "t1", ItemCount: 350})
	if err != nil {
		t.Fatal(err)
	}
	if r.ItemID != "item-120" || r.Text != "old reply" || r.RunID != "run-1" {
		t.Fatalf("reply = %+v", r)
	}
	calls := tl.recorded()
	if got := scans(t, calls); !slices.Equal(got, []int{249, 149, 49}) {
		t.Fatalf("scan afterPositions = %v", got)
	}
	if calls[0]["view"] == nil {
		t.Fatal("ItemCount hint given but initial read sent")
	}
}

func TestLatestReplyRunFilterPicksOlderReply(t *testing.T) {
	f := newFakeT3(t)
	tl := newTimeline(t, f, fill(10, activity()), []tlItem{reply("run-a", "from a")}, fill(139, activity()),
		[]tlItem{reply("run-b", "b1")}, fill(49, user()), []tlItem{reply("run-b", "b2")}, fill(9, activity()))
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)

	r, err := c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1", RunID: "run-a", ItemCount: 210})
	if err != nil {
		t.Fatal(err)
	}
	if r.ItemID != "item-10" || r.Text != "from a" {
		t.Fatalf("reply = %+v", r)
	}
	if got := scans(t, tl.recorded()); !slices.Equal(got, []int{109, 9}) {
		t.Fatalf("scan afterPositions = %v", got)
	}
	r, err = c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1", ItemCount: 210})
	if err != nil || r.ItemID != "item-200" || r.Text != "b2" {
		t.Fatalf("any run: reply = %+v, err = %v", r, err)
	}
	if _, err := c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1", RunID: "run-x", ItemCount: 210}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("unknown run: err = %v, want ErrNoReply", err)
	}
}

func TestLatestReplySkipsInheritedNewer(t *testing.T) {
	f := newFakeT3(t)
	newTimeline(t, f, []tlItem{user(), reply("run-1", "mine"), activity(), inherited("run-p"), {typ: "assistant_message", vis: "synthetic", text: "s"}})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	r, err := c.LatestReply(ctxT(t), LatestReplyInput{ThreadID: "t1", ItemCount: 5})
	if err != nil || r.ItemID != "item-1" || r.Text != "mine" {
		t.Fatalf("reply = %+v, err = %v", r, err)
	}
}

func TestLatestReplyPagesForwardWhenThreadGrew(t *testing.T) {
	f := newFakeT3(t)
	// Stale hint 50; actual 260 items with the reply at 250.
	tl := newTimeline(t, f, fill(250, user()), []tlItem{reply("run-9", "newest")}, fill(9, activity()))
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	r, err := c.LatestReply(ctxT(t), LatestReplyInput{ThreadID: "t1", ItemCount: 50})
	if err != nil || r.ItemID != "item-250" || r.Text != "newest" {
		t.Fatalf("reply = %+v, err = %v", r, err)
	}
	if got := scans(t, tl.recorded()); !slices.Equal(got, []int{-1, 99, 199}) {
		t.Fatalf("scan afterPositions = %v", got)
	}
}

func TestLatestReplyAssemblesLongText(t *testing.T) {
	f := newFakeT3(t)
	// A surrogate pair straddles the 50000-unit page boundary: T3 ends the
	// page at 49999 and nextTextOffset carries on from there.
	long := strings.Repeat("a", 49999) + "🌍" + strings.Repeat("b", 70000)
	tl := newTimeline(t, f, []tlItem{user(), reply("run-1", long)})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	r, err := c.LatestReply(ctxT(t), LatestReplyInput{ThreadID: "t1", ItemCount: 2, MaxChars: 200000})
	if err != nil {
		t.Fatal(err)
	}
	if r.Truncated || r.Text != long {
		t.Fatalf("truncated=%v len=%d, want full %d", r.Truncated, len(r.Text), len(long))
	}
	if got := textReads(tl.recorded()); !slices.Equal(got, [][2]int{{0, 50000}, {49999, 50000}, {99999, 50000}}) {
		t.Fatalf("text reads = %v", got)
	}
}

func TestLatestReplyTruncatesToMaxChars(t *testing.T) {
	f := newFakeT3(t)
	tl := newTimeline(t, f, []tlItem{reply("run-1", strings.Repeat("x", 100000))}, []tlItem{reply("run-2", "hello 🌍 world")})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)

	r, err := c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1", ItemCount: 2, MaxChars: 8})
	if err != nil || !r.Truncated || r.Text != "hello 🌍" {
		t.Fatalf("reply = %+v, err = %v", r, err)
	}
	// Budget spans pages: 50000 + 20000, still more text.
	r, err = c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1", RunID: "run-1", ItemCount: 2, MaxChars: 70000})
	if err != nil || !r.Truncated || r.Text != strings.Repeat("x", 70000) {
		t.Fatalf("truncated=%v len=%d err=%v", r.Truncated, len(r.Text), err)
	}
	reads := textReads(tl.recorded())
	if !slices.Equal(reads, [][2]int{{0, 8}, {0, 50000}, {50000, 20000}}) {
		t.Fatalf("text reads = %v", reads)
	}
	// Default budget is one 50000 page.
	r, err = c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1", RunID: "run-1", ItemCount: 2})
	if err != nil || !r.Truncated || len(r.Text) != 50000 {
		t.Fatalf("default: truncated=%v len=%d err=%v", r.Truncated, len(r.Text), err)
	}
}

func TestLatestReplyNoReply(t *testing.T) {
	f := newFakeT3(t)
	tl := newTimeline(t, f, fill(30, user()), fill(30, activity()), []tlItem{inherited("run-p")})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)
	if _, err := c.LatestReply(ctx, LatestReplyInput{ThreadID: "t1"}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	if textReads(tl.recorded()) != nil {
		t.Fatal("text read without a reply")
	}
	if _, err := c.LatestReply(ctx, LatestReplyInput{ThreadID: "gone"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread: err = %v, want ErrNotFound", err)
	}
	if _, err := c.LatestReply(ctx, LatestReplyInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty thread id: err = %v, want ErrNotFound", err)
	}

	empty := newFakeT3(t)
	etl := newTimeline(t, empty)
	ec := newTestClient(t, empty, empty.adminToken, NewMemoryTokenStore())
	if _, err := ec.LatestReply(ctx, LatestReplyInput{ThreadID: "t1"}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("empty thread: err = %v, want ErrNoReply", err)
	}
	if n := len(etl.recorded()); n != 1 {
		t.Fatalf("empty thread: %d reads, want 1", n)
	}
}

func TestDecodeTimelineItems(t *testing.T) {
	var w wireThreadRead
	raw := `{"thread":{"threadId":"t1","itemCount":9},"recentRuns":[],"hasMore":true,"nextPosition":7,"items":[
		{"position":3,"visibility":"local","itemId":"i3","runId":null,"messageId":null,"type":"assistant_message","status":"completed","title":null,"text":null,"textTruncated":false,"updatedAt":"bogus"},
		{"position":7,"visibility":"local","itemId":"i7","runId":"r","messageId":"m","type":"assistant_message","text":"ab","textTruncated":true,"nextTextOffset":2,"updatedAt":"2026-11-14T00:00:00.000Z"}]}`
	if err := decodeToolResult("t3_thread_read", structuredResult(t, raw), &w); err != nil {
		t.Fatal(err)
	}
	if w.Thread.ItemCount != 9 || !w.HasMore || w.NextPosition == nil || *w.NextPosition != 7 || len(w.Items) != 2 {
		t.Fatalf("read = %+v", w)
	}
	a, b := w.Items[0], w.Items[1]
	if a.RunID != "" || a.MessageID != "" || a.Text != "" || a.NextTextOffset != nil || !a.UpdatedAt.IsZero() {
		t.Fatalf("item 0 = %+v", a)
	}
	if b.RunID != "r" || b.NextTextOffset == nil || *b.NextTextOffset != 2 || b.UpdatedAt.IsZero() {
		t.Fatalf("item 1 = %+v", b)
	}
	if d := w.toDetail(); d.ThreadID != "t1" || d.ItemCount != 9 {
		t.Fatalf("detail = %+v", d)
	}
}

func TestUTF16Len(t *testing.T) {
	for s, want := range map[string]int{"": 0, "abc": 3, "é": 1, "🌍": 2, "a🌍b": 4} {
		if got := utf16Len(s); got != want {
			t.Errorf("utf16Len(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestFakeLatestReply(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if err := f.SetReply("nope", Reply{Text: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetReply unknown: %v", err)
	}
	if _, err := f.LatestReply(ctx, LatestReplyInput{ThreadID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread: %v", err)
	}
	p := f.AddProject("P", "/p")
	res, err := f.LaunchThread(ctx, LaunchInput{ProjectID: p.ID, Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ThreadID
	if _, err := f.LatestReply(ctx, LatestReplyInput{ThreadID: id}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("no reply: %v", err)
	}
	_ = f.SetReply(id, Reply{ItemID: "i1", RunID: "run-a", Text: "héllo wörld"})
	_ = f.SetReply(id, Reply{ItemID: "i2", RunID: "run-b", Text: "second"})
	if r, _ := f.LatestReply(ctx, LatestReplyInput{ThreadID: id}); r.ItemID != "i2" || r.Truncated || r.UpdatedAt.IsZero() {
		t.Fatalf("latest = %+v", r)
	}
	if r, _ := f.LatestReply(ctx, LatestReplyInput{ThreadID: id, RunID: "run-a", MaxChars: 5}); r.ItemID != "i1" || r.Text != "héllo" || !r.Truncated {
		t.Fatalf("run-a = %+v", r)
	}
	_ = f.SetReply(id, Reply{ItemID: "i1", RunID: "run-a", Text: "replaced"})
	if r, _ := f.LatestReply(ctx, LatestReplyInput{ThreadID: id, RunID: "run-a"}); r.Text != "replaced" {
		t.Fatalf("replaced = %+v", r)
	}
	if _, err := f.LatestReply(ctx, LatestReplyInput{ThreadID: id, RunID: "run-x"}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("unknown run: %v", err)
	}
}
