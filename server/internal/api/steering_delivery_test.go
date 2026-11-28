package api_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/steering"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Steering delivery (ADR 0013) through the real coordinator and T3 adapter
// against the T3 fake, on a fake clock.

type steeringClock struct{ now time.Time }

func (c *steeringClock) Now() time.Time             { return c.now }
func (c *steeringClock) Add(d time.Duration)        { c.now = c.now.Add(d) }
func (c *steeringClock) set(d *steering.Dispatcher) { d.Now = c.Now }

type delivery struct {
	*env
	d       *steering.Dispatcher
	clock   *steeringClock
	tree    int64
	member  task
	steerer task
	project project
}

// newDelivery steers a one-member tree; the steerer runs in the T3 fake.
func newDelivery(t *testing.T) *delivery {
	e := newEnv(t)
	p := e.project("/w/deliver")
	tr, a := e.steeringTree(p.ID, "Ship it")
	var res steererResponse
	e.must("POST", fmt.Sprintf("/api/trees/%d/steerer", tr.ID), e.steerBody(p.ID, "s1", nil), 201, &res)
	if res.Task == nil || res.Task.State != "running" {
		t.Fatalf("steerer %+v", res.Task)
	}
	c := &steeringClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	d := steering.New(e.store, e.engine.Coordinator, slog.New(slog.DiscardHandler))
	c.set(d)
	return &delivery{env: e, d: d, clock: c, tree: tr.ID, member: a, steerer: res.Task.task, project: p}
}

func (x *delivery) reconcile() {
	x.t.Helper()
	ctx := principal.With(context.Background(), principal.Actor{Kind: principal.System})
	if err := x.d.Reconcile(ctx); err != nil {
		x.t.Fatal(err)
	}
}

// note adds a blocker note on the member as the user (not the steerer),
// then passes as the commit's kick would (coalescing windows start when
// the dispatcher first sees an event).
func (x *delivery) note(text string) {
	x.t.Helper()
	x.must("POST", fmt.Sprintf("/api/tasks/%d/journal", x.member.ID), map[string]any{"text": text, "category": "blocker", "requestId": "n-" + text}, 201, nil)
	x.reconcile()
}

// sends are the T3 sends into the steerer's thread.
func (x *delivery) sends() []t3.FakeSend {
	x.t.Helper()
	thread := sessionOf(x.env, x.steerer.ID).ExternalSessionID
	var out []t3.FakeSend
	for _, s := range x.fake.Sent {
		if s.ThreadID == thread {
			out = append(out, s)
		}
	}
	return out
}

func (x *delivery) treeSteerer() *struct {
	TaskID           int64   `json:"taskId"`
	DeliveredThrough int64   `json:"deliveredThrough"`
	LastDeliveredAt  *string `json:"lastDeliveredAt"`
	LastError        *string `json:"lastError"`
	Pending          int64   `json:"pending"`
} {
	x.t.Helper()
	var tree struct {
		Steerer *struct {
			TaskID           int64   `json:"taskId"`
			DeliveredThrough int64   `json:"deliveredThrough"`
			LastDeliveredAt  *string `json:"lastDeliveredAt"`
			LastError        *string `json:"lastError"`
			Pending          int64   `json:"pending"`
		} `json:"steerer"`
	}
	x.must("GET", fmt.Sprintf("/api/trees/%d", x.tree), nil, 200, &tree)
	return tree.Steerer
}

func (x *delivery) binding() db.SteererState {
	x.t.Helper()
	states, err := x.store.ActiveSteerers(context.Background())
	if err != nil {
		x.t.Fatal(err)
	}
	for _, st := range states {
		if st.TreeID == x.tree {
			return st
		}
	}
	x.t.Fatal("no active binding")
	return db.SteererState{}
}

// A digest waits 10s after the newest event, but never more than 60s after
// the oldest; it goes out once, tagged and recorded, and the tree's
// steerer fields show it.
func TestSteeringDeliveryCoalesces(t *testing.T) {
	x := newDelivery(t)
	x.reconcile()
	if n := len(x.sends()); n != 0 {
		t.Fatalf("sent %d with nothing pending", n)
	}
	x.note("first")
	for i := range 6 { // a note every 9s keeps the debounce open
		x.clock.Add(9 * time.Second)
		x.reconcile()
		if n := len(x.sends()); n != 0 {
			t.Fatalf("sent at +%ds", 9*(i+1))
		}
		x.note(fmt.Sprintf("more-%d", i))
	}
	x.clock.Add(6 * time.Second) // +60s since the first
	x.reconcile()
	sends := x.sends()
	if len(sends) != 1 {
		t.Fatalf("max wait: %d sends", len(sends))
	}
	st := x.treeSteerer()
	b := x.binding()
	msg := sends[0].Message
	if !strings.HasPrefix(msg, fmt.Sprintf(`<stage-steering tree="%d" through="%d">`, x.tree, st.DeliveredThrough)) || !strings.HasSuffix(msg, "</stage-steering>") ||
		strings.Count(msg, " note #") != 7 || !strings.Contains(msg, `"first"`) {
		t.Fatalf("digest %q", msg)
	}
	if sends[0].ClientRequestID != fmt.Sprintf("steering-%d-%d", b.ID, st.DeliveredThrough) || sends[0].Mode != t3.SendQueue {
		t.Fatalf("send %+v", sends[0])
	}
	if st.Pending != 0 || st.LastDeliveredAt == nil || st.LastError != nil {
		t.Fatalf("tree steerer %+v", st)
	}
	if !slices.ContainsFunc(notesAndEvents(x.env, x.steerer.ID), func(e db.JournalEntry) bool {
		return strings.Contains(e.Text, db.SteeringSentDetail(st.DeliveredThrough))
	}) {
		t.Fatal("no steering sent event on the steerer")
	}

	// One more event: delivered 10s later.
	x.note("late")
	x.clock.Add(9 * time.Second)
	x.reconcile()
	if n := len(x.sends()); n != 1 {
		t.Fatalf("debounce: %d sends", n)
	}
	x.clock.Add(time.Second)
	x.reconcile()
	if sends := x.sends(); len(sends) != 2 || !strings.Contains(sends[1].Message, `"late"`) || strings.Contains(sends[1].Message, `"first"`) {
		t.Fatalf("second digest %+v", sends)
	}
}

func notesAndEvents(e *env, id int64) []db.JournalEntry {
	e.t.Helper()
	page, err := e.store.ReadJournal(context.Background(), db.JournalOwner{TaskID: &id}, db.JournalQuery{Kind: "events", Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	return page.Entries
}

// Busy (a request the steerer waits on) keeps events pending without an
// error; the next pass after it clears delivers.
func TestSteeringDeliveryBusyRetries(t *testing.T) {
	x := newDelivery(t)
	thread := sessionOf(x.env, x.steerer.ID).ExternalSessionID
	req, err := x.fake.AddApproval(thread)
	if err != nil {
		t.Fatal(err)
	}
	x.note("while busy")
	x.clock.Add(10 * time.Second)
	x.reconcile()
	if st := x.treeSteerer(); len(x.sends()) != 0 || st.Pending != 1 || st.LastError != nil {
		t.Fatalf("busy: sends %d steerer %+v", len(x.sends()), st)
	}
	if err := x.fake.ResolveRequest(thread, req, "approved"); err != nil {
		t.Fatal(err)
	}
	x.reconcile()
	if st := x.treeSteerer(); len(x.sends()) != 1 || st.Pending != 0 {
		t.Fatalf("after busy: sends %d steerer %+v", len(x.sends()), st)
	}
}

// The steerer's own actions are never delivered back to it.
func TestSteeringDeliverySkipsSelf(t *testing.T) {
	x := newDelivery(t)
	ref := sessionOf(x.env, x.steerer.ID)
	asSteerer := map[string]string{buildinfo.APIVersionHeader: apiVersion, agentctx.CallerHeader: fmt.Sprintf("%d:%s", ref.ConnectionID, ref.ExternalSessionID)}
	if rec := raw(t, x.h, "POST", fmt.Sprintf("/api/tasks/%d/journal", x.member.ID), asSteerer, map[string]any{"text": "mine", "category": "decision", "requestId": "own"}, nil); rec.Code != 201 {
		t.Fatalf("steerer note: %d %s", rec.Code, rec.Body.String())
	}
	x.clock.Add(2 * time.Minute)
	x.reconcile()
	if n := len(x.sends()); n != 0 || x.treeSteerer().Pending != 0 {
		t.Fatalf("self delivered: %d sends", n)
	}
}

// A crash after the send but before its record sends the same request id
// again; the harness deduplicates it.
func TestSteeringDeliveryRedeliversAfterCrash(t *testing.T) {
	x := newDelivery(t)
	var calls atomic.Int32
	var throughs []int64
	record := x.d.Record
	x.d.Record = func(ctx context.Context, id, through, rev int64) (bool, error) {
		throughs = append(throughs, through)
		if calls.Add(1) == 1 {
			return false, errors.New("crashed before recording")
		}
		return record(ctx, id, through, rev)
	}
	x.note("once")
	x.clock.Add(10 * time.Second)
	if err := x.d.Reconcile(context.Background()); err == nil {
		t.Fatal("crash not reported")
	}
	if st := x.treeSteerer(); st.Pending != 1 || st.LastError == nil {
		t.Fatalf("after crash %+v", st)
	}
	x.clock.Add(9 * time.Second) // backoff 10s
	x.reconcile()
	if calls.Load() != 1 {
		t.Fatal("retried during backoff")
	}
	x.clock.Add(time.Second)
	x.reconcile()
	st := x.treeSteerer()
	if calls.Load() != 2 || throughs[0] != throughs[1] || st.DeliveredThrough != throughs[0] || st.LastError != nil || st.Pending != 0 {
		t.Fatalf("redelivery calls %d throughs %v steerer %+v", calls.Load(), throughs, st)
	}
	if sends := x.sends(); len(sends) != 1 {
		t.Fatalf("harness ran the digest %d times", len(sends))
	}
}

// While the steerer waits on the user, updates still go out and remind it
// that its question is open.
func TestSteeringDeliveryRemindsWaitingSteerer(t *testing.T) {
	x := newDelivery(t)
	if _, err := x.store.TransitionTask(context.Background(), db.Transition{TaskID: x.steerer.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	x.note("question")
	x.clock.Add(10 * time.Second)
	x.reconcile()
	sends := x.sends()
	if len(sends) != 1 || !strings.Contains(sends[0].Message, fmt.Sprintf("Reminder: your own question to the user is still open (task #%d", x.steerer.ID)) {
		t.Fatalf("sends %+v", sends)
	}
}

// A member needing input lists its requests (or says none are listable),
// with the revision to answer at.
func TestSteeringDeliveryListsRequests(t *testing.T) {
	x := newDelivery(t)
	b := x.task(x.project.ID, "asker", map[string]any{"start": true})
	x.must("POST", fmt.Sprintf("/api/trees/%d/tasks", x.tree), map[string]any{"taskId": b.ID}, 200, nil)
	thread := sessionOf(x.env, b.ID).ExternalSessionID
	if _, err := x.fake.AddUserInput(thread, []t3.Question{{ID: "q1", Header: "Pick", Question: "Which db?"}}); err != nil {
		t.Fatal(err)
	}
	x.poll()
	asker := x.getTask(b.ID)
	if asker.State != "needs_input" {
		t.Fatalf("asker %s", asker.State)
	}
	x.reconcile()
	x.clock.Add(10 * time.Second)
	x.reconcile()
	sends := x.sends()
	if len(sends) != 1 {
		t.Fatalf("sends %d", len(sends))
	}
	want := fmt.Sprintf("member_needs_input (stateRevision %d): requests: ", asker.StateRevision)
	if !strings.Contains(sends[0].Message, want) || !strings.Contains(sends[0].Message, fmt.Sprintf("expectedStateRevision: %d}", asker.StateRevision)) {
		t.Fatalf("digest %q lacks %q", sends[0].Message, want)
	}
}

// Overflow: the digest stays within 32 KiB and points at the feed; every
// pending event counts as delivered.
func TestSteeringDeliveryCapsDigest(t *testing.T) {
	x := newDelivery(t)
	for i := range 70 {
		x.note(fmt.Sprintf("%02d-%s", i, strings.Repeat("w", 590)))
	}
	x.clock.Add(time.Minute)
	x.reconcile()
	sends := x.sends()
	if len(sends) != 1 || len(sends[0].Message) > steering.MaxDigest {
		t.Fatalf("sends %d len %d", len(sends), len(sends[0].Message))
	}
	msg := sends[0].Message
	if !strings.Contains(msg, fmt.Sprintf("more events (#")) || !strings.Contains(msg, fmt.Sprintf("get_steering_events {treeId: %d, after: ", x.tree)) {
		t.Fatalf("no overflow pointer in %q", msg[len(msg)-300:])
	}
	if st := x.treeSteerer(); st.Pending != 0 {
		t.Fatalf("pending after overflow %+v", st)
	}
}

// Tree finish: the final digest waits for the steerer's turn to end, then
// goes out at once; the binding ends; the steerer's next done completes it
// once (system actor), and a reopen stays open.
func TestSteeringDeliveryFinishesTree(t *testing.T) {
	x := newDelivery(t)
	thread := sessionOf(x.env, x.steerer.ID).ExternalSessionID
	x.complete(x.member.ID)
	x.reconcile()
	if n := len(x.sends()); n != 0 {
		t.Fatal("final digest sent mid-turn")
	}
	_ = x.fake.SetStatus(thread, t3.StatusCompleted)
	x.poll()
	if got := x.getTask(x.steerer.ID); got.State != "done" {
		t.Fatalf("steerer %s", got.State)
	}
	x.reconcile() // no clock advance: no debounce for the final digest
	sends := x.sends()
	if len(sends) != 1 || !strings.Contains(sends[0].Message, "tree_finished (members_completed)") || !strings.Contains(sends[0].Message, "Write an outcome note on your task") {
		t.Fatalf("final digest %+v", sends)
	}
	if st := x.treeSteerer(); st != nil {
		t.Fatalf("binding still active %+v", st)
	}
	var s steeringTask
	x.must("GET", fmt.Sprintf("/api/tasks/%d", x.steerer.ID), nil, 200, &s)
	if s.Steering == nil || s.Steering.Active || s.Steering.EndReason == nil || *s.Steering.EndReason != "tree_finished" {
		t.Fatalf("steering %+v", s.Steering)
	}
	// The send resumed the steerer; its turn ends: Stage completes it.
	x.poll()
	_ = x.fake.SetStatus(thread, t3.StatusCompleted)
	x.poll()
	x.reconcile()
	got, err := x.store.GetTask(context.Background(), x.steerer.ID)
	if err != nil || got.AcceptanceID == nil {
		t.Fatalf("steerer not completed %+v %v", got.AcceptanceID, err)
	}
	acceptances, err := x.store.Acceptances(context.Background(), x.steerer.ID)
	if err != nil || len(acceptances) != 1 || acceptances[0].Actor.Kind != "system" {
		t.Fatalf("acceptances %+v %v", acceptances, err)
	}
	if !slices.ContainsFunc(notesAndEvents(x.env, x.steerer.ID), func(e db.JournalEntry) bool {
		return e.EventType == "task_completed" && e.Text == db.SteererAutoCompletedDetail(x.tree)
	}) {
		t.Fatal("no visible auto-complete provenance")
	}
	x.reopen(x.steerer.ID)
	x.reconcile()
	if got, _ := x.store.GetTask(context.Background(), x.steerer.ID); got.AcceptanceID != nil {
		t.Fatal("re-completed after reopen")
	}
}

// A steerer not running gets nothing and the tree shows why; when its tree
// finishes, the binding ends at once and nothing completes it.
func TestSteeringDeliverySteererDown(t *testing.T) {
	x := newDelivery(t)
	x.must("POST", fmt.Sprintf("/api/tasks/%d/stop", x.steerer.ID), nil, 200, nil)
	x.note("nobody listens")
	x.clock.Add(2 * time.Minute)
	x.reconcile()
	st := x.treeSteerer()
	if len(x.sends()) != 0 || st.LastError == nil || *st.LastError != steering.NotRunning || st.Pending != 1 {
		t.Fatalf("down: sends %d steerer %+v", len(x.sends()), st)
	}
	x.complete(x.member.ID)
	x.reconcile()
	if st := x.treeSteerer(); st != nil || len(x.sends()) != 0 {
		t.Fatalf("finish with steerer down: %+v sends %d", st, len(x.sends()))
	}
	awaiting, err := x.store.SteerersAwaitingCompletion(context.Background())
	if err != nil || len(awaiting) != 0 {
		t.Fatalf("awaiting %+v %v", awaiting, err)
	}
}
