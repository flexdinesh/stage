package storetest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("SteeringTreeFinished", caseSteeringTreeFinished)
	register("SteeringDeliveryRecord", caseSteeringDeliveryRecord)
	register("SteeringAutoCompleteSkips", caseSteeringAutoCompleteSkips)
	register("SteeringEndsWithSteererTask", caseSteeringEndsWithSteererTask)
}

// activeSteerer is tree's active binding as the dispatcher reads it.
func activeSteerer(t *testing.T, s *db.Store, ctx context.Context, tree int64) (db.SteererState, bool) {
	t.Helper()
	states, err := s.ActiveSteerers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.TreeID == tree {
			return st, true
		}
	}
	return db.SteererState{}, false
}

func finishedEvents(t *testing.T, s *db.Store, ctx context.Context, tree int64) []db.SteeringEvent {
	t.Helper()
	var out []db.SteeringEvent
	for _, e := range steeringFeed(t, s, ctx, tree) {
		if e.Kind == db.SteerTreeFinished {
			out = append(out, e)
		}
	}
	return out
}

func finishReason(t *testing.T, e db.SteeringEvent) string {
	t.Helper()
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Reason
}

// runSteerer moves steerer task id to running with a session.
func runSteerer(t *testing.T, s *db.Store, ctx context.Context, id int64, session string) db.SessionRef {
	t.Helper()
	ref := db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: session}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateRunning, Session: &ref}); err != nil {
		t.Fatal(err)
	}
	return ref
}

func moveTask(t *testing.T, s *db.Store, ctx context.Context, id int64, to db.TaskState) {
	t.Helper()
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: to}); err != nil {
		t.Fatalf("move #%d to %s: %v", id, to, err)
	}
}

// caseSteeringTreeFinished: tree_finished is enqueued once per binding,
// with no cause, when every member is accepted (also by removing the last
// open one), or the tree is marked done or archived.
func caseSteeringTreeFinished(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-finish")

	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	tree := mustTree(t, s, ctx, "Accepted", db.StartManual, db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID})
	mustSteer(t, s, ctx, tree.ID, p.ID, "f1", false)
	acceptTask(t, s, ctx, a.ID)
	if got := finishedEvents(t, s, ctx, tree.ID); len(got) != 0 {
		t.Fatalf("finished with an open member: %s", kinds(got))
	}
	acceptTask(t, s, ctx, b.ID)
	got := finishedEvents(t, s, ctx, tree.ID)
	if len(got) != 1 || got[0].TaskID != nil || got[0].CausedBy != nil || got[0].Self || finishReason(t, got[0]) != db.FinishMembersCompleted {
		t.Fatalf("finished %+v", got)
	}
	st, ok := activeSteerer(t, s, ctx, tree.ID)
	if !ok || st.FinishEventID == nil || *st.FinishEventID != got[0].ID {
		t.Fatalf("binding %+v %v", st, ok)
	}
	if _, err := s.SetTreeStatus(ctx, tree.ID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	if got := finishedEvents(t, s, ctx, tree.ID); len(got) != 1 {
		t.Fatalf("finished twice: %+v", got)
	}

	c := mustTask(t, s, ctx, p.ID, "c", nil)
	d := mustTask(t, s, ctx, p.ID, "d", nil)
	removed := mustTree(t, s, ctx, "Removed", db.StartManual, db.TreeMember{TaskID: c.ID}, db.TreeMember{TaskID: d.ID})
	mustSteer(t, s, ctx, removed.ID, p.ID, "f2", false)
	acceptTask(t, s, ctx, c.ID)
	if err := s.RemoveFromTree(ctx, removed.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if got := finishedEvents(t, s, ctx, removed.ID); len(got) != 1 || finishReason(t, got[0]) != db.FinishMembersCompleted {
		t.Fatalf("removal finish %+v", got)
	}

	for _, tc := range []struct {
		status db.TreeStatus
		reason string
	}{{db.TreeDone, db.FinishTreeDone}, {db.TreeArchived, db.FinishTreeArchived}} {
		m := mustTask(t, s, ctx, p.ID, "m-"+string(tc.status), nil)
		tr := mustTree(t, s, ctx, "Marked "+string(tc.status), db.StartManual, db.TreeMember{TaskID: m.ID})
		mustSteer(t, s, ctx, tr.ID, p.ID, "f-"+string(tc.status), false)
		if _, err := s.SetTreeStatus(ctx, tr.ID, tc.status); err != nil {
			t.Fatal(err)
		}
		if got := finishedEvents(t, s, ctx, tr.ID); len(got) != 1 || finishReason(t, got[0]) != tc.reason {
			t.Fatalf("%s finish %+v", tc.status, got)
		}
	}
}

// caseSteeringDeliveryRecord: pending skips the steerer's own events; a
// recorded delivery moves delivered_through, stamps it, clears the error
// and journals one steerer event; the final one ends the binding, and the
// steerer's next done auto-completes it once (system actor), never again
// after a reopen.
func caseSteeringDeliveryRecord(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-deliver")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tree := mustTree(t, s, ctx, "Delivered", db.StartManual, db.TreeMember{TaskID: a.ID})
	r := mustSteer(t, s, ctx, tree.ID, p.ID, "d1", false)
	ref := runSteerer(t, s, ctx, r.TaskID, "steerer-deliver")
	steererCtx := db.WithCallerSession(ctx, db.CallerSession{Ref: ref, TaskID: r.TaskID})
	if _, err := s.AppendNote(steererCtx, db.JournalOwner{TaskID: &a.ID}, db.AppendNoteInput{Text: "mine", Category: "decision", RequestID: "own"}); err != nil {
		t.Fatal(err)
	}
	st, _ := activeSteerer(t, s, ctx, tree.ID)
	if pending, err := s.SteeringPending(ctx, st.ID, st.DeliveredThrough); err != nil || pending.Count != 0 {
		t.Fatalf("own note pending %+v %v", pending, err)
	}
	if _, err := s.AppendNote(ctx, db.JournalOwner{TaskID: &a.ID}, db.AppendNoteInput{Text: "stuck", Category: "blocker", RequestID: "theirs"}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.SteeringPending(ctx, st.ID, st.DeliveredThrough)
	if err != nil || pending.Count != 1 || pending.FirstID != pending.LastID {
		t.Fatalf("pending %+v %v", pending, err)
	}
	msg := "boom"
	if err := s.SetSteeringError(ctx, st.ID, &msg); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RecordSteeringDelivery(ctx, st.ID, pending.LastID, 0); err != nil || !ok {
		t.Fatalf("record %v %v", ok, err)
	}
	detail, err := s.GetTree(ctx, tree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sr := detail.Steerer; sr == nil || sr.DeliveredThrough != pending.LastID || sr.LastDeliveredAt == nil || sr.LastError != nil || sr.Pending != 0 {
		t.Fatalf("delivered %+v", detail.Steerer)
	}
	if !journalHas(t, s, ctx, r.TaskID, db.SteeringSentDetail(pending.LastID)) {
		t.Fatal("no steering sent event")
	}

	// Accepting the last member finishes the tree; the final delivery ends
	// the binding and keeps the steerer's revision.
	acceptTask(t, s, ctx, a.ID)
	st, _ = activeSteerer(t, s, ctx, tree.ID)
	steerer := getTask(t, s, ctx, r.TaskID)
	if ok, err := s.RecordSteeringDelivery(ctx, st.ID, *st.FinishEventID, steerer.StateRevision); err != nil || !ok {
		t.Fatalf("final record %v %v", ok, err)
	}
	if _, active := activeSteerer(t, s, ctx, tree.ID); active {
		t.Fatal("binding still active after the final digest")
	}
	if got := getTask(t, s, ctx, r.TaskID); got.Steering == nil || got.Steering.Active || got.Steering.EndReason == nil || *got.Steering.EndReason != db.SteeringTreeFinished {
		t.Fatalf("steering %+v", got.Steering)
	}
	awaiting, err := s.SteerersAwaitingCompletion(ctx)
	if err != nil || len(awaiting) != 1 || awaiting[0].TaskID != r.TaskID || awaiting[0].FinalStateRevision != steerer.StateRevision || awaiting[0].TreeID != tree.ID {
		t.Fatalf("awaiting %+v %v", awaiting, err)
	}
	// Still running from before the final digest: nothing to decide.
	if done, err := s.AutoCompleteSteerer(ctx, awaiting[0]); err != nil || done {
		t.Fatalf("auto-complete while running %v %v", done, err)
	}
	moveTask(t, s, ctx, r.TaskID, db.StateDone)
	if done, err := s.AutoCompleteSteerer(ctx, awaiting[0]); err != nil || !done {
		t.Fatalf("auto-complete %v %v", done, err)
	}
	got := getTask(t, s, ctx, r.TaskID)
	if got.AcceptanceID == nil {
		t.Fatal("steerer not completed")
	}
	acceptances, err := s.Acceptances(ctx, r.TaskID)
	if err != nil || len(acceptances) != 1 || acceptances[0].Actor.Kind != "system" {
		t.Fatalf("acceptances %+v %v", acceptances, err)
	}
	if !journalHas(t, s, ctx, r.TaskID, db.SteererAutoCompletedDetail(tree.ID)) {
		t.Fatal("no auto-complete provenance")
	}
	if awaiting, err := s.SteerersAwaitingCompletion(ctx); err != nil || len(awaiting) != 0 {
		t.Fatalf("still awaiting %+v %v", awaiting, err)
	}
	reopenTask(t, s, ctx, r.TaskID)
	if awaiting, err := s.SteerersAwaitingCompletion(ctx); err != nil || len(awaiting) != 0 {
		t.Fatalf("awaiting after reopen %+v %v", awaiting, err)
	}
}

// caseSteeringAutoCompleteSkips: a failed run after the final digest never
// auto-completes; a steerer down at finish ends without a final digest.
func caseSteeringAutoCompleteSkips(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-skip")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tree := mustTree(t, s, ctx, "Failed run", db.StartManual, db.TreeMember{TaskID: a.ID})
	r := mustSteer(t, s, ctx, tree.ID, p.ID, "s1", false)
	runSteerer(t, s, ctx, r.TaskID, "steerer-skip")
	moveTask(t, s, ctx, r.TaskID, db.StateDone)
	acceptTask(t, s, ctx, a.ID)
	st, _ := activeSteerer(t, s, ctx, tree.ID)
	if ok, err := s.RecordSteeringDelivery(ctx, st.ID, *st.FinishEventID, getTask(t, s, ctx, r.TaskID).StateRevision); err != nil || !ok {
		t.Fatalf("final record %v %v", ok, err)
	}
	moveTask(t, s, ctx, r.TaskID, db.StateRunning)
	moveTask(t, s, ctx, r.TaskID, db.StateFailed)
	awaiting, err := s.SteerersAwaitingCompletion(ctx)
	if err != nil || len(awaiting) != 1 {
		t.Fatalf("awaiting %+v %v", awaiting, err)
	}
	if done, err := s.AutoCompleteSteerer(ctx, awaiting[0]); err != nil || done {
		t.Fatalf("failed run completed %v %v", done, err)
	}
	moveTask(t, s, ctx, r.TaskID, db.StateDone)
	if awaiting, err := s.SteerersAwaitingCompletion(ctx); err != nil || len(awaiting) != 0 {
		t.Fatalf("skip not recorded %+v %v", awaiting, err)
	}
	if got := getTask(t, s, ctx, r.TaskID); got.AcceptanceID != nil {
		t.Fatal("failed run auto-completed")
	}

	b := mustTask(t, s, ctx, p.ID, "b", nil)
	down := mustTree(t, s, ctx, "Down", db.StartManual, db.TreeMember{TaskID: b.ID})
	rd := mustSteer(t, s, ctx, down.ID, p.ID, "s2", false)
	acceptTask(t, s, ctx, b.ID)
	st, _ = activeSteerer(t, s, ctx, down.ID)
	if ok, err := s.FinishSteering(ctx, st.ID); err != nil || !ok {
		t.Fatalf("finish %v %v", ok, err)
	}
	if got := getTask(t, s, ctx, rd.TaskID); got.Steering == nil || got.Steering.Active || *got.Steering.EndReason != db.SteeringTreeFinished {
		t.Fatalf("down steering %+v", got.Steering)
	}
	if ok, err := s.RecordSteeringDelivery(ctx, st.ID, *st.FinishEventID, 0); err != nil || ok {
		t.Fatalf("record on an ended binding %v %v", ok, err)
	}
	if awaiting, err := s.SteerersAwaitingCompletion(ctx); err != nil || len(awaiting) != 0 {
		t.Fatalf("down steerer awaiting %+v %v", awaiting, err)
	}
}

// caseSteeringEndsWithSteererTask: archiving the steerer task ends its
// binding (task_removed).
func caseSteeringEndsWithSteererTask(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-archive")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tree := mustTree(t, s, ctx, "Archived steerer", db.StartManual, db.TreeMember{TaskID: a.ID})
	r := mustSteer(t, s, ctx, tree.ID, p.ID, "a1", false)
	got, err := s.ArchiveTask(ctx, r.TaskID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steering == nil || got.Steering.Active || got.Steering.EndReason == nil || *got.Steering.EndReason != db.SteeringTaskRemoved {
		t.Fatalf("steering %+v", got.Steering)
	}
	if _, active := activeSteerer(t, s, ctx, tree.ID); active {
		t.Fatal("binding still active")
	}
}

// journalHas reports whether task id's journal has an event containing text.
func journalHas(t *testing.T, s *db.Store, ctx context.Context, id int64, text string) bool {
	t.Helper()
	page, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &id}, db.JournalQuery{Kind: "events", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Entries {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}
