package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// heldFixture is a notifier with holds on a fake clock and a tree whose
// members a and b have a running steerer.
type heldFixture struct {
	n       *Notifier
	holds   *Holds
	store   *db.Store
	now     time.Time
	tree    int64
	a, b    int64
	steerer int64
}

func newHeldFixture(t *testing.T) *heldFixture {
	t.Helper()
	n, store := newNotifier(t)
	f := &heldFixture{n: n, store: store, now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	f.holds = NewHolds(n)
	f.holds.Now = func() time.Time { return f.now }
	n.Holds = f.holds
	n.Inbox = NewHub(t.Context())
	ctx := t.Context()
	p, err := store.InsertProject(ctx, db.Project{Title: "stage", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "proj-1", Workspace: "/w/1"}}})
	if err != nil {
		t.Fatal(err)
	}
	task := func(title string) int64 {
		t.Helper()
		got, err := store.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: title, Prompt: "p"})
		if err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	f.a, f.b = task("Member A"), task("Member B")
	tree, err := store.CreateTree(ctx, db.NewTree{Title: "Steered", StartMode: db.StartManual, Tasks: []db.TreeMember{{TaskID: f.a}, {TaskID: f.b}}})
	if err != nil {
		t.Fatal(err)
	}
	f.tree = tree.ID
	r, err := store.SetTreeSteerer(ctx, tree.ID, db.SteererInput{
		Task:      db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "Steer", Prompt: "steer"},
		RequestID: "r1", Digest: "d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.steerer = r.TaskID
	f.move(t, f.steerer, db.StateRunning)
	return f
}

func (f *heldFixture) move(t *testing.T, task int64, to db.TaskState) {
	t.Helper()
	ref := db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: fmt.Sprintf("held-%d", task)}
	if _, err := f.store.TransitionTask(t.Context(), db.Transition{TaskID: task, To: to, Session: &ref}); err != nil {
		t.Fatalf("task %d -> %s: %v", task, to, err)
	}
}

// waits moves task into needs_input and runs the hook path for it.
func (f *heldFixture) waits(t *testing.T, task int64) {
	t.Helper()
	f.move(t, task, db.StateNeedsInput)
	f.n.handle(t.Context(), event{task: &workevent.TaskChange{TaskID: task, To: db.StateNeedsInput}})
}

func (f *heldFixture) reconcile(t *testing.T) {
	t.Helper()
	if err := f.holds.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// notes lists the notifications, newest first, as task:note.
func (f *heldFixture) notes(t *testing.T) string {
	t.Helper()
	ns, err := f.store.ListNotifications(t.Context(), 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range ns {
		note := "-"
		if n.Note != nil {
			note = *n.Note
		}
		out = append(out, fmt.Sprintf("%d:%s", *n.TaskID, note))
	}
	return strings.Join(out, ",")
}

func (f *heldFixture) held(t *testing.T, task int64) bool {
	t.Helper()
	got, err := f.store.GetTask(t.Context(), task)
	if err != nil {
		t.Fatal(err)
	}
	return got.Hold != nil
}

// The hook path (OnTransition -> handle) holds a member's needs_input: no
// row, the task shows the hold; at expiry the release inserts the row with
// its note, signals the inbox and queues the push.
func TestHoldHookPathReleasesAtExpiry(t *testing.T) {
	f := newHeldFixture(t)
	wake, unsub := f.n.Inbox.Subscribe()
	defer unsub()
	f.waits(t, f.a)
	if got := f.notes(t); got != "" || !f.held(t, f.a) {
		t.Fatalf("held: notifications %q, hold %v", got, f.held(t, f.a))
	}
	f.now = f.now.Add(299 * time.Second)
	f.reconcile(t)
	if got := f.notes(t); got != "" {
		t.Fatalf("released early: %q", got)
	}
	select {
	case <-wake:
	default:
	}
	f.now = f.now.Add(time.Second)
	f.reconcile(t)
	if got, want := f.notes(t), fmt.Sprintf("%d:%s", f.a, db.HoldNoteTimeout); got != want {
		t.Fatalf("notifications %q, want %q", got, want)
	}
	if f.held(t, f.a) {
		t.Fatal("still held after release")
	}
	select {
	case <-wake:
	default:
		t.Fatal("release did not signal the inbox")
	}
	if len(f.n.events) != 1 {
		t.Fatalf("queued %d pushes, want 1", len(f.n.events))
	}
	e := <-f.n.events
	if e.saved == nil || e.saved.Note == nil || *e.saved.Note != db.HoldNoteTimeout {
		t.Fatalf("queued push %+v", e)
	}
	if p := PayloadFor(notificationFrom(*e.saved), 0); p.Title != "Needs input" || !strings.HasSuffix(p.Body, " · "+db.HoldNoteTimeout) {
		t.Fatalf("payload %+v", p)
	}
}

// The observation path holds inside the observation transaction and returns
// a kick for after commit.
func TestHoldObservationPath(t *testing.T) {
	f := newHeldFixture(t)
	f.move(t, f.a, db.StateNeedsInput)
	var after func()
	err := f.store.WriteObservation(t.Context(), func(tx *db.Store) error {
		var err error
		after, err = f.n.RecordObservation(t.Context(), tx, workevent.ObservationChange{
			Task: &workevent.TaskChange{TaskID: f.a, To: db.StateNeedsInput}, At: f.now,
		})
		return err
	})
	if err != nil || after == nil {
		t.Fatalf("record: after=%v err=%v", after != nil, err)
	}
	after()
	if got := f.notes(t); got != "" || !f.held(t, f.a) {
		t.Fatalf("observation not held: %q", got)
	}
	// failed is never held.
	f.move(t, f.b, db.StateFailed)
	err = f.store.WriteObservation(t.Context(), func(tx *db.Store) error {
		_, err := f.n.RecordObservation(t.Context(), tx, workevent.ObservationChange{
			Task: &workevent.TaskChange{TaskID: f.b, To: db.StateFailed}, At: f.now,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.notes(t); got != fmt.Sprintf("%d:-", f.b) {
		t.Fatalf("failed notification %q", got)
	}
	select {
	case <-f.holds.kick:
	default:
		t.Fatal("observation hold did not kick the release loop")
	}
}

// An answer before expiry cancels the hold: nothing is ever notified.
func TestHoldCancelledWhenAnsweredFirst(t *testing.T) {
	f := newHeldFixture(t)
	f.waits(t, f.a)
	f.waits(t, f.b)
	task, err := f.store.GetTask(t.Context(), f.a)
	if err != nil {
		t.Fatal(err)
	}
	ref := db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: fmt.Sprintf("held-%d", f.a)}
	if err := f.store.RecordTaskAnswer(t.Context(), f.a, ref, "session_response", "Answered", &task.StateRevision); err != nil {
		t.Fatal(err)
	}
	f.move(t, f.b, db.StateRunning)
	f.now = f.now.Add(time.Hour)
	f.reconcile(t)
	if got := f.notes(t); got != "" {
		t.Fatalf("answered waits notified: %q", got)
	}
}

// Holds are durable: a new release loop (a restart) releases what the
// previous one held.
func TestHoldRestartReevaluates(t *testing.T) {
	f := newHeldFixture(t)
	f.waits(t, f.a)
	restarted := NewHolds(f.n)
	restarted.Now = func() time.Time { return f.now.Add(10 * time.Minute) }
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.notes(t); got != fmt.Sprintf("%d:%s", f.a, db.HoldNoteTimeout) {
		t.Fatalf("after restart %q", got)
	}
}

// A steerer that stops running, or whose steering ends, releases its holds
// at once, without a note.
func TestHoldSteererLossReleases(t *testing.T) {
	f := newHeldFixture(t)
	f.waits(t, f.a)
	f.move(t, f.steerer, db.StateFailed)
	f.reconcile(t)
	if got := f.notes(t); got != fmt.Sprintf("%d:-", f.a) {
		t.Fatalf("after steerer failed %q", got)
	}

	f.move(t, f.steerer, db.StateRunning)
	f.waits(t, f.b)
	if !f.held(t, f.b) {
		t.Fatal("b not held")
	}
	if _, err := f.store.EndTreeSteering(t.Context(), f.tree, db.SteeringStopped); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if got := f.notes(t); got != fmt.Sprintf("%d:-,%d:-", f.b, f.a) {
		t.Fatalf("after steering ended %q", got)
	}
}

// A member connection that offers neither respond nor send releases at
// once; a failed check keeps the hold.
func TestHoldUnanswerableConnectionReleases(t *testing.T) {
	f := newHeldFixture(t)
	f.holds.CanAnswer = func(context.Context, int64) (bool, error) { return false, errors.New("harness down") }
	f.waits(t, f.a)
	f.reconcile(t)
	if got := f.notes(t); got != "" {
		t.Fatalf("failed check released: %q", got)
	}
	f.holds.CanAnswer = func(context.Context, int64) (bool, error) { return false, nil }
	f.reconcile(t)
	if got := f.notes(t); got != fmt.Sprintf("%d:-", f.a) {
		t.Fatalf("unanswerable %q", got)
	}
}

// The personal filter applies at release: muted needs_input escalates the
// hold without a row.
func TestHoldFilterAppliesAtRelease(t *testing.T) {
	f := newHeldFixture(t)
	if _, err := f.store.SetNotifyEvents(t.Context(), []db.NotificationEvent{db.EventFailed}); err != nil {
		t.Fatal(err)
	}
	f.waits(t, f.a)
	if !f.held(t, f.a) {
		t.Fatal("not held while muted")
	}
	f.now = f.now.Add(time.Hour)
	f.reconcile(t)
	if got := f.notes(t); got != "" || f.held(t, f.a) || len(f.n.events) != 0 {
		t.Fatalf("muted release: %q held=%v queued=%d", got, f.held(t, f.a), len(f.n.events))
	}
}

// escalate_to_user releases the hold now with the steerer's reason; only
// the active steerer may.
func TestHoldEscalate(t *testing.T) {
	f := newHeldFixture(t)
	f.waits(t, f.a)
	var notSteerer *db.NotSteererError
	if err := f.holds.Escalate(t.Context(), f.a, f.b, "why"); !errors.As(err, &notSteerer) {
		t.Fatalf("escalate from a member: %v", err)
	}
	if err := f.holds.Escalate(t.Context(), f.a, f.steerer, "needs a product call"); err != nil {
		t.Fatal(err)
	}
	if got := f.notes(t); got != fmt.Sprintf("%d:Steerer: needs a product call", f.a) {
		t.Fatalf("escalated %q", got)
	}
	if len(f.n.events) != 1 {
		t.Fatalf("queued %d pushes", len(f.n.events))
	}
}

// Hold 0 turns it off: the hook path notifies at once.
func TestHoldZeroDisables(t *testing.T) {
	f := newHeldFixture(t)
	if _, err := f.store.UpdateTree(t.Context(), f.tree, db.TreePatch{SteeringHoldSeconds: db.Optional[int64]{Set: true, V: 0}}); err != nil {
		t.Fatal(err)
	}
	f.waits(t, f.a)
	if got := f.notes(t); got != fmt.Sprintf("%d:-", f.a) || f.held(t, f.a) {
		t.Fatalf("hold 0: %q", got)
	}
}
