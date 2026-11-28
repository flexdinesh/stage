package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("HoldConditions", caseHoldConditions)
	register("HoldCancelledByAnswerOrTransition", caseHoldCancelled)
	register("HoldRelease", caseHoldRelease)
	register("HoldEscalate", caseHoldEscalate)
	register("TreeSteeringHoldSetting", caseTreeSteeringHoldSetting)
}

var holdNow = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

// steeredTree is a manual tree of members a and b with a running steerer.
type steeredTree struct {
	tree, a, b, steerer int64
}

func setupSteered(t *testing.T, s *db.Store, ctx context.Context, prefix string) steeredTree {
	t.Helper()
	p := mustProject(t, s, ctx, prefix)
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	tree := mustTree(t, s, ctx, "Steered", db.StartManual, db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID})
	r := mustSteer(t, s, ctx, tree.ID, p.ID, prefix, false)
	return steeredTree{tree: tree.ID, a: a.ID, b: b.ID, steerer: r.TaskID}
}

func holdSession(s *db.Store, task int64) db.SessionRef {
	return db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: fmt.Sprintf("hold-%d", task)}
}

func moveTo(t *testing.T, s *db.Store, ctx context.Context, task int64, to db.TaskState) {
	t.Helper()
	ref := holdSession(s, task)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task, To: to, Session: &ref}); err != nil {
		t.Fatalf("task %d -> %s: %v", task, to, err)
	}
}

func mustHold(t *testing.T, s *db.Store, ctx context.Context, task int64, want bool) {
	t.Helper()
	held, err := s.HoldNeedsInput(ctx, task, holdNow)
	if err != nil || held != want {
		t.Fatalf("hold task %d: held=%v err=%v, want held=%v", task, held, err, want)
	}
}

func taskHold(t *testing.T, s *db.Store, ctx context.Context, task int64) *db.TaskHold {
	t.Helper()
	got, err := s.GetTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	return got.Hold
}

// recordAll inserts a plain needs_input notification for the task.
func recordAll(_ context.Context, _ *db.Store, task db.Task) (*db.NewNotification, error) {
	return &db.NewNotification{Event: db.StateNeedsInput, TaskID: &task.ID, Title: task.Title, URL: fmt.Sprintf("/tasks/%d", task.ID)}, nil
}

func recordNone(context.Context, *db.Store, db.Task) (*db.NewNotification, error) { return nil, nil }

func notificationNotes(t *testing.T, s *db.Store, ctx context.Context) []string {
	t.Helper()
	ns, err := s.ListNotifications(ctx, 50, nil)
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
	return out
}

// caseHoldConditions: a member's needs_input is held only while its tree has
// a live steerer and a hold above 0; Task JSON shows the hold.
func caseHoldConditions(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	st := setupSteered(t, s, ctx, "proj-hold-cond")
	moveTo(t, s, ctx, st.a, db.StateRunning)
	mustHold(t, s, ctx, st.a, false) // not waiting
	moveTo(t, s, ctx, st.a, db.StateNeedsInput)
	mustHold(t, s, ctx, st.a, false) // the steerer has not started
	moveTo(t, s, ctx, st.steerer, db.StateRunning)
	mustHold(t, s, ctx, st.a, true)
	mustHold(t, s, ctx, st.a, true) // the same wait again: one hold
	h := taskHold(t, s, ctx, st.a)
	if h == nil || !h.Until.Equal(holdNow.Add(300*time.Second)) || h.SteererTaskID == nil || *h.SteererTaskID != st.steerer {
		t.Fatalf("task hold %+v", h)
	}
	held, err := s.HeldHolds(ctx)
	if err != nil || len(held) != 1 || held[0].TaskID != st.a || held[0].SteererTreeID != st.tree || held[0].SteererEnded ||
		held[0].SteererState == nil || *held[0].SteererState != db.StateRunning || held[0].TaskTreeID == nil || *held[0].TaskTreeID != st.tree {
		t.Fatalf("held %+v %v", held, err)
	}

	// The steerer stopped: no new holds.
	moveTo(t, s, ctx, st.steerer, db.StateFailed)
	moveTo(t, s, ctx, st.b, db.StateNeedsInput)
	mustHold(t, s, ctx, st.b, false)
	moveTo(t, s, ctx, st.steerer, db.StateNeedsInput)
	moveTo(t, s, ctx, st.b, db.StateRunning)
	moveTo(t, s, ctx, st.b, db.StateNeedsInput)
	mustHold(t, s, ctx, st.b, true)

	// Hold 0 disables it.
	other := setupSteered(t, s, ctx, "proj-hold-off")
	if _, err := s.UpdateTree(ctx, other.tree, db.TreePatch{SteeringHoldSeconds: db.Optional[int64]{Set: true, V: 0}}); err != nil {
		t.Fatal(err)
	}
	moveTo(t, s, ctx, other.steerer, db.StateRunning)
	moveTo(t, s, ctx, other.a, db.StateNeedsInput)
	mustHold(t, s, ctx, other.a, false)

	// Standalone tasks and the steerer itself are never held.
	p := mustProject(t, s, ctx, "proj-hold-alone")
	alone := mustTask(t, s, ctx, p.ID, "alone", nil)
	moveTo(t, s, ctx, alone.ID, db.StateNeedsInput)
	mustHold(t, s, ctx, alone.ID, false)
	mustHold(t, s, ctx, st.steerer, false)
	if taskHold(t, s, ctx, alone.ID) != nil {
		t.Fatal("standalone task shows a hold")
	}
}

// caseHoldCancelled: leaving needs_input or an answer through Stage at the
// held revision cancels the hold in the same write; releasing a cancelled
// hold changes nothing.
func caseHoldCancelled(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	st := setupSteered(t, s, ctx, "proj-hold-cancel")
	moveTo(t, s, ctx, st.steerer, db.StateRunning)
	moveTo(t, s, ctx, st.a, db.StateNeedsInput)
	mustHold(t, s, ctx, st.a, true)
	held, err := s.HeldHolds(ctx)
	if err != nil || len(held) != 1 {
		t.Fatalf("held %+v %v", held, err)
	}
	moveTo(t, s, ctx, st.a, db.StateRunning)
	if h := taskHold(t, s, ctx, st.a); h != nil {
		t.Fatalf("hold after transition %+v", h)
	}
	r, err := s.ReleaseHold(ctx, held[0].ID, db.HoldEscalated, nil, ptr(db.HoldNoteTimeout), recordAll)
	if err != nil || r.State != db.HoldCancelled || r.Notification != nil {
		t.Fatalf("release cancelled: %+v %v", r, err)
	}

	moveTo(t, s, ctx, st.a, db.StateNeedsInput)
	mustHold(t, s, ctx, st.a, true)
	task, err := s.GetTask(ctx, st.a)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTaskAnswer(ctx, st.a, holdSession(s, st.a), "session_response", "Answered", ptr(task.StateRevision)); err != nil {
		t.Fatal(err)
	}
	if h := taskHold(t, s, ctx, st.a); h != nil {
		t.Fatalf("hold after answer %+v", h)
	}
	if held, err := s.HeldHolds(ctx); err != nil || len(held) != 0 {
		t.Fatalf("held after answer %+v %v", held, err)
	}
	// Answered at this revision: not held again.
	mustHold(t, s, ctx, st.a, false)
	if notes := notificationNotes(t, s, ctx); len(notes) != 0 {
		t.Fatalf("notifications %v", notes)
	}
}

// caseHoldRelease: a release inserts the notification with its note only
// at release, enqueues hold_escalated for an escalation, and records the
// filtered case without a row.
func caseHoldRelease(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	st := setupSteered(t, s, ctx, "proj-hold-release")
	moveTo(t, s, ctx, st.steerer, db.StateRunning)
	moveTo(t, s, ctx, st.a, db.StateNeedsInput)
	moveTo(t, s, ctx, st.b, db.StateNeedsInput)
	mustHold(t, s, ctx, st.a, true)
	mustHold(t, s, ctx, st.b, true)
	held, err := s.HeldHolds(ctx)
	if err != nil || len(held) != 2 {
		t.Fatalf("held %+v %v", held, err)
	}
	if notes := notificationNotes(t, s, ctx); len(notes) != 0 {
		t.Fatalf("notifications while held %v", notes)
	}
	r, err := s.ReleaseHold(ctx, held[0].ID, db.HoldEscalated, nil, ptr(db.HoldNoteTimeout), recordAll)
	if err != nil || r.State != db.HoldEscalated || r.Notification == nil || r.NotificationID == 0 {
		t.Fatalf("release: %+v %v", r, err)
	}
	if notes := strings.Join(notificationNotes(t, s, ctx), ","); notes != fmt.Sprintf("%d:%s", st.a, db.HoldNoteTimeout) {
		t.Fatalf("notifications %s", notes)
	}
	again, err := s.ReleaseHold(ctx, held[0].ID, db.HoldEscalated, nil, nil, recordAll)
	if err != nil || again.State != db.HoldEscalated || again.Notification != nil {
		t.Fatalf("release twice: %+v %v", again, err)
	}
	events := steeringFeed(t, s, ctx, st.tree)
	last := events[len(events)-1]
	if last.Kind != db.SteerHoldEscalated || last.TaskID == nil || *last.TaskID != st.a || payload(t, last)["timeout"] != true {
		t.Fatalf("last event %s %s", kinds(events), last.Payload)
	}

	// The filter mutes it: released, no row, no event.
	reason := db.HoldReasonSteererEnded
	r, err = s.ReleaseHold(ctx, held[1].ID, db.HoldReleased, &reason, nil, recordNone)
	if err != nil || r.State != db.HoldReleased || r.Notification != nil {
		t.Fatalf("filtered release: %+v %v", r, err)
	}
	if n := len(notificationNotes(t, s, ctx)); n != 1 {
		t.Fatalf("notifications after filtered release: %d", n)
	}
	if got := steeringFeed(t, s, ctx, st.tree); len(got) != len(events) {
		t.Fatalf("released enqueued %s", kinds(got[len(events):]))
	}
}

// caseHoldEscalate: only the tree's active steerer escalates; it releases
// the hold at once, or notifies at once when nothing is held.
func caseHoldEscalate(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	st := setupSteered(t, s, ctx, "proj-hold-esc")
	moveTo(t, s, ctx, st.steerer, db.StateRunning)
	moveTo(t, s, ctx, st.a, db.StateNeedsInput)
	mustHold(t, s, ctx, st.a, true)

	var notSteerer *db.NotSteererError
	for _, caller := range []int64{0, st.b} {
		if _, err := s.EscalateTask(ctx, st.a, caller, "why", recordAll); !errors.As(err, &notSteerer) {
			t.Fatalf("escalate from %d: %v", caller, err)
		}
	}
	var invalid *db.InvalidError
	if _, err := s.EscalateTask(ctx, st.a, st.steerer, strings.Repeat("x", db.MaxEscalationReason+1), recordAll); !errors.As(err, &invalid) {
		t.Fatalf("long reason: %v", err)
	}
	if _, err := s.EscalateTask(ctx, st.a, st.steerer, " ", recordAll); !errors.As(err, &invalid) {
		t.Fatalf("empty reason: %v", err)
	}
	r, err := s.EscalateTask(ctx, st.a, st.steerer, "needs a product call", recordAll)
	if err != nil || r.State != db.HoldEscalated || r.Notification == nil {
		t.Fatalf("escalate held: %+v %v", r, err)
	}
	if h := taskHold(t, s, ctx, st.a); h != nil {
		t.Fatalf("hold after escalate %+v", h)
	}
	// Nothing held, still waiting: notifies again at once.
	if _, err := s.EscalateTask(ctx, st.a, st.steerer, "still waiting", recordAll); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d:Steerer: still waiting,%d:Steerer: needs a product call", st.a, st.a)
	if notes := strings.Join(notificationNotes(t, s, ctx), ","); notes != want {
		t.Fatalf("notifications\n got %s\nwant %s", notes, want)
	}
	events := steeringFeed(t, s, ctx, st.tree)
	esc := 0
	for _, e := range events {
		if e.Kind == db.SteerHoldEscalated {
			esc++
			if p := payload(t, e); p["reason"] == nil || p["timeout"] != nil {
				t.Fatalf("escalation payload %s", e.Payload)
			}
		}
	}
	if esc != 2 {
		t.Fatalf("hold_escalated events %d in %s", esc, kinds(events))
	}
	// Not waiting.
	if _, err := s.EscalateTask(ctx, st.b, st.steerer, "why", recordAll); conflictCode(err) != "not_waiting" {
		t.Fatalf("escalate a task that does not wait: %v", err)
	}
}

// caseTreeSteeringHoldSetting: steeringHoldSeconds defaults to 300, takes 0
// to 3600 and journals a change.
func caseTreeSteeringHoldSetting(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-hold-set")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tree := mustTree(t, s, ctx, "Hold", db.StartManual, db.TreeMember{TaskID: a.ID})
	if tree.SteeringHoldSeconds != 300 {
		t.Fatalf("default hold %d", tree.SteeringHoldSeconds)
	}
	var invalid *db.InvalidError
	for _, v := range []db.Optional[int64]{{Set: true, V: -1}, {Set: true, V: 3601}, {Set: true, Null: true}} {
		if _, err := s.UpdateTree(ctx, tree.ID, db.TreePatch{SteeringHoldSeconds: v}); !errors.As(err, &invalid) {
			t.Fatalf("hold %+v: %v", v, err)
		}
	}
	got, err := s.UpdateTree(ctx, tree.ID, db.TreePatch{SteeringHoldSeconds: db.Optional[int64]{Set: true, V: 3600}})
	if err != nil || got.SteeringHoldSeconds != 3600 {
		t.Fatalf("set 3600: %d %v", got.SteeringHoldSeconds, err)
	}
	page, err := s.ReadJournal(ctx, db.JournalOwner{TreeID: &tree.ID}, db.JournalQuery{Kind: "events", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Entries {
		if e.EventType == "steering_hold_changed" {
			found = e.Text == "Steering hold: 5m0s -> 1h0m0s"
		}
	}
	if !found {
		t.Fatal("no steering_hold_changed journal event")
	}
}
