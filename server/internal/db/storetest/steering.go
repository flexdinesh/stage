package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

func init() {
	register("SteeringEventsOnlyWhileSteered", caseSteeringEventsOnlyWhileSteered)
	register("SteeringCausedBy", caseSteeringCausedBy)
	register("SteeringBindingRules", caseSteeringBindingRules)
	register("ConcurrentSteererBind", caseConcurrentSteererBind)
	register("ConcurrentSteeringCursor", caseConcurrentSteeringCursor)
}

func steerer(projectID int64, requestID string, replace bool) db.SteererInput {
	return db.SteererInput{
		Task:    db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: projectID, Title: "Steer", Prompt: "steer"},
		Replace: replace, RequestID: requestID, Digest: "digest-" + requestID,
	}
}

func mustSteer(t *testing.T, s *db.Store, ctx context.Context, tree, projectID int64, requestID string, replace bool) db.SteererResult {
	t.Helper()
	r, err := s.SetTreeSteerer(ctx, tree, steerer(projectID, requestID, replace))
	if err != nil {
		t.Fatalf("set steerer %s: %v", requestID, err)
	}
	return r
}

func steeringFeed(t *testing.T, s *db.Store, ctx context.Context, tree int64) []db.SteeringEvent {
	t.Helper()
	events, _, err := s.SteeringEvents(ctx, tree, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func kinds(events []db.SteeringEvent) string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
		if e.TaskID != nil {
			out[i] += fmt.Sprintf("#%d", *e.TaskID)
		}
	}
	return strings.Join(out, " ")
}

func payload(t *testing.T, e db.SteeringEvent) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("event %d payload %q: %v", e.ID, e.Payload, err)
	}
	return p
}

func conflictCode(err error) string {
	var c *db.ConflictError
	if errors.As(err, &c) {
		return c.Code
	}
	var i *db.InvalidError
	if errors.As(err, &i) {
		return i.Code
	}
	return ""
}

// caseSteeringEventsOnlyWhileSteered: member changes enqueue steering
// events only while the tree has an active steerer, in order, with their
// payloads; the binding shows on the tree and the steerer task.
func caseSteeringEventsOnlyWhileSteered(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-steer")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	tree := mustTree(t, s, ctx, "Steered", db.StartManual, db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID})
	note := func(task int64, category, requestID string) {
		t.Helper()
		if _, err := s.AppendNote(ctx, db.JournalOwner{TaskID: &task}, db.AppendNoteInput{Text: "n", Category: category, RequestID: requestID}); err != nil {
			t.Fatal(err)
		}
	}
	transition := func(task int64, to db.TaskState) {
		t.Helper()
		ref := db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: fmt.Sprintf("steer-%d", task)}
		if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task, To: to, Session: &ref}); err != nil {
			t.Fatal(err)
		}
	}
	note(a.ID, "blocker", "before")
	transition(a.ID, db.StateRunning)
	if got := steeringFeed(t, s, ctx, tree.ID); len(got) != 0 {
		t.Fatalf("events before steering: %s", kinds(got))
	}

	r := mustSteer(t, s, ctx, tree.ID, p.ID, "r1", false)
	transition(a.ID, db.StateNeedsInput)
	note(a.ID, "blocker", "blocker")
	note(a.ID, "finding", "finding") // not a steering note
	note(b.ID, "decision", "decision")
	transition(a.ID, db.StateDone)
	transition(b.ID, db.StateFailed)
	if _, err := s.SetTreeStatus(ctx, tree.ID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	events := steeringFeed(t, s, ctx, tree.ID)
	want := fmt.Sprintf("steering_started#%d member_needs_input#%d note#%d note#%d member_done#%d member_failed#%d tree_status tree_finished",
		r.TaskID, a.ID, a.ID, b.ID, a.ID, b.ID)
	if got := kinds(events); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
	for i := 1; i < len(events); i++ {
		if events[i].ID <= events[i-1].ID || events[i].SteererID != events[0].SteererID || events[i].TreeID != tree.ID {
			t.Fatalf("event %d out of order or binding: %+v", i, events[i])
		}
	}
	got, err := s.GetTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p := payload(t, events[1]); p["stateRevision"] != float64(got.StateRevision-1) || p["title"] != "a" {
		t.Fatalf("needs_input payload %s (revision now %d)", events[1].Payload, got.StateRevision)
	}
	if p := payload(t, events[6]); p["to"] != "done" || p["from"] != "open" || events[0].Self || events[0].CausedBy != nil {
		t.Fatalf("tree status %s, started %+v", events[6].Payload, events[0])
	}
	// Paging.
	page, more, err := s.SteeringEvents(ctx, tree.ID, events[1].ID, 2)
	if err != nil || !more || len(page) != 2 || page[0].ID != events[2].ID {
		t.Fatalf("page after %d: %s more=%v %v", events[1].ID, kinds(page), more, err)
	}

	detail, err := s.GetTree(ctx, tree.ID)
	if err != nil {
		t.Fatal(err)
	}
	// steering_started is delivered at bind (the launch brief covers it).
	if st := detail.Steerer; st == nil || st.TaskID != r.TaskID || st.Title != "Steer" || st.Pending != int64(len(events)-1) || st.DeliveredThrough != events[0].ID {
		t.Fatalf("tree steerer %+v", detail.Steerer)
	}
	steer, err := s.GetTask(ctx, r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if steer.Steering == nil || steer.Steering.TreeID != tree.ID || !steer.Steering.Active || steer.Steering.EndedAt != nil || steer.TreeID != nil {
		t.Fatalf("steerer task steering %+v tree %v", steer.Steering, steer.TreeID)
	}

	ended, err := s.EndTreeSteering(ctx, tree.ID, db.SteeringStopped)
	if err != nil || ended != r.TaskID {
		t.Fatalf("end: %d %v", ended, err)
	}
	if _, err := s.EndTreeSteering(ctx, tree.ID, db.SteeringStopped); conflictCode(err) != "no_steerer" {
		t.Fatalf("end again: %v", err)
	}
	note(a.ID, "blocker", "after")
	after := steeringFeed(t, s, ctx, tree.ID)
	if len(after) != len(events)+1 || after[len(events)].Kind != db.SteerEnded || payload(t, after[len(events)])["reason"] != "stopped" {
		t.Fatalf("events after end: %s", kinds(after[len(events):]))
	}
	steer, err = s.GetTask(ctx, r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if steer.Steering == nil || steer.Steering.Active || steer.Steering.EndedAt == nil || steer.Steering.EndReason == nil || *steer.Steering.EndReason != db.SteeringStopped {
		t.Fatalf("ended steering %+v", steer.Steering)
	}
	if detail, err = s.GetTree(ctx, tree.ID); err != nil || detail.Steerer != nil {
		t.Fatalf("tree steerer after end %+v %v", detail.Steerer, err)
	}
	page2, err := s.ReadJournal(ctx, db.JournalOwner{TreeID: &tree.ID}, db.JournalQuery{Kind: "events", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var journaled []string
	for _, e := range page2.Entries {
		if strings.HasPrefix(e.EventType, "steering_") {
			journaled = append(journaled, e.EventType)
		}
	}
	slices.Sort(journaled)
	if strings.Join(journaled, ",") != "steering_ended,steering_started" {
		t.Fatalf("tree journal steering events %v", journaled)
	}
}

// caseSteeringCausedBy: events record the caller session and its task
// (self for the steerer); acceptance by the steerer reads "steerer #N" and
// the readiness it releases inherits its cause.
func caseSteeringCausedBy(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-cause")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	c := mustTask(t, s, ctx, p.ID, "c", nil)
	tree := mustTree(t, s, ctx, "Caused", db.StartManual, db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID, Needs: []int64{a.ID}})
	r := mustSteer(t, s, ctx, tree.ID, p.ID, "r1", false)
	ref := db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: "steerer-session"}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: r.TaskID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: r.TaskID, To: db.StateRunning, Session: &ref}); err != nil {
		t.Fatal(err)
	}
	steererCtx := db.WithCallerSession(ctx, db.CallerSession{Ref: ref, TaskID: r.TaskID})
	other := db.SessionRef{ConnectionID: ref.ConnectionID, ExternalSessionID: "someone-else"}
	otherCtx := db.WithCallerSession(ctx, db.CallerSession{Ref: other})

	if _, err := s.AddToTree(otherCtx, tree.ID, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.CompletionPreview(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeAcceptance(steererCtx, a.ID, db.AcceptanceRequest{RequestID: "accept-a", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion, CallerSession: &ref}, false); err != nil {
		t.Fatal(err)
	}
	ready, err := s.MarkReady(ctx)
	if err != nil || !slices.Contains(ready, b.ID) {
		t.Fatalf("ready %v %v", ready, err)
	}
	events := steeringFeed(t, s, ctx, tree.ID)
	want := fmt.Sprintf("steering_started#%d member_added#%d member_accepted#%d member_ready#%d", r.TaskID, c.ID, a.ID, b.ID)
	if got := kinds(events); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
	added, accepted, readied := events[1], events[2], events[3]
	if added.Self || added.CausedBy == nil || added.CausedBy.Session == nil || *added.CausedBy.Session != other || added.CausedBy.TaskID != nil {
		t.Fatalf("added cause %+v", added.CausedBy)
	}
	for _, e := range []db.SteeringEvent{accepted, readied} {
		if !e.Self || e.CausedBy == nil || e.CausedBy.TaskID == nil || *e.CausedBy.TaskID != r.TaskID || e.CausedBy.Session == nil || *e.CausedBy.Session != ref {
			t.Fatalf("%s cause %+v self=%v", e.Kind, e.CausedBy, e.Self)
		}
	}
	page, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "events", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Entries {
		found = found || strings.Contains(e.Text, fmt.Sprintf("Completed by steerer #%d", r.TaskID))
	}
	if !found {
		t.Fatalf("no steerer acceptance in %+v", page.Entries)
	}
	detail, err := s.GetTree(ctx, tree.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Pending skips the steerer's own events and its start (the launch
	// brief covers it): member_added only.
	if detail.Steerer == nil || detail.Steerer.Pending != 1 || detail.Steerer.DeliveredThrough != events[0].ID {
		t.Fatalf("pending %+v", detail.Steerer)
	}
}

// caseSteeringBindingRules: one active steerer per tree (replace ends the
// old one), requestId replay, the steerer never joins the tree it steers,
// closed trees refuse, and deleting the steerer ends its binding.
func caseSteeringBindingRules(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-rules")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tree := mustTree(t, s, ctx, "Rules", db.StartAuto, db.TreeMember{TaskID: a.ID})
	first := mustSteer(t, s, ctx, tree.ID, p.ID, "r1", false)
	if _, err := s.SetTreeSteerer(ctx, tree.ID, steerer(p.ID, "r2", false)); conflictCode(err) != "steerer_exists" {
		t.Fatalf("second steerer: %v", err)
	}
	replay := mustSteer(t, s, ctx, tree.ID, p.ID, "r1", false)
	if !replay.Replayed || replay.TaskID != first.TaskID {
		t.Fatalf("replay %+v", replay)
	}
	changed := steerer(p.ID, "r1", false)
	changed.Digest = "other"
	if _, err := s.SetTreeSteerer(ctx, tree.ID, changed); err == nil || !strings.Contains(err.Error(), "requestId") {
		t.Fatalf("replay with other content: %v", err)
	}

	// The steerer never joins the tree it steers.
	if _, err := s.AddToTree(ctx, tree.ID, first.TaskID, nil); conflictCode(err) != "steerer_not_member" {
		t.Fatalf("add steerer: %v", err)
	}
	if _, err := s.SetNeeds(ctx, a.ID, []int64{first.TaskID}); conflictCode(err) != "steerer_not_member" {
		t.Fatalf("need steerer: %v", err)
	}
	if _, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "x", Prompt: "p", TreeID: &tree.ID, Needs: []int64{first.TaskID}}); conflictCode(err) != "steerer_not_member" {
		t.Fatalf("create after steerer: %v", err)
	}

	second := mustSteer(t, s, ctx, tree.ID, p.ID, "r3", true)
	if second.Replaced == nil || *second.Replaced != first.TaskID || second.TaskID == first.TaskID {
		t.Fatalf("replace %+v", second)
	}
	old, err := s.GetTask(ctx, first.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Steering == nil || old.Steering.Active || old.Steering.EndReason == nil || *old.Steering.EndReason != db.SteeringReplaced {
		t.Fatalf("replaced steering %+v", old.Steering)
	}
	// A former steerer may join the tree.
	if _, err := s.AddToTree(ctx, tree.ID, first.TaskID, nil); err != nil {
		t.Fatalf("add former steerer: %v", err)
	}
	if err := s.DeleteTree(ctx, tree.ID); err == nil || !strings.Contains(err.Error(), "steerer") {
		t.Fatalf("delete steered tree: %v", err)
	}
	// Deleting the (unstarted) steerer ends its binding.
	if err := s.DeleteTask(ctx, second.TaskID); err != nil {
		t.Fatal(err)
	}
	events := steeringFeed(t, s, ctx, tree.ID)
	last := events[len(events)-1]
	if last.Kind != db.SteerEnded || payload(t, last)["reason"] != "task_removed" {
		t.Fatalf("after delete: %s %s", kinds(events), last.Payload)
	}
	if detail, err := s.GetTree(ctx, tree.ID); err != nil || detail.Steerer != nil {
		t.Fatalf("steerer after delete %+v %v", detail.Steerer, err)
	}
	if _, err := s.SetTreeStatus(ctx, tree.ID, db.TreeArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTreeSteerer(ctx, tree.ID, steerer(p.ID, "r4", false)); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("steer archived tree: %v", err)
	}
	if _, err := s.SetTreeSteerer(ctx, 999999, steerer(p.ID, "r5", false)); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing tree: %v", err)
	}
}

// caseConcurrentSteererBind: racing set-steerer requests leave exactly one
// active steerer; the losers get steerer_exists.
func caseConcurrentSteererBind(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-race")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tree := mustTree(t, s, ctx, "Race", db.StartAuto, db.TreeMember{TaskID: a.ID})
	const n = 6
	errs := race(n, func(i int) error {
		_, err := s.SetTreeSteerer(ctx, tree.ID, steerer(p.ID, fmt.Sprintf("race-%d", i), false))
		return err
	})
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case conflictCode(err) != "steerer_exists":
			t.Fatalf("racer: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d steerers bound, want 1", wins)
	}
	errs = race(n, func(i int) error {
		_, err := s.SetTreeSteerer(ctx, tree.ID, steerer(p.ID, fmt.Sprintf("replace-%d", i), true))
		return err
	})
	for _, err := range errs {
		if err != nil && conflictCode(err) != "steerer_exists" {
			t.Fatalf("replacer: %v", err)
		}
	}
	events := steeringFeed(t, s, ctx, tree.ID)
	started, ended := 0, 0
	for _, e := range events {
		switch e.Kind {
		case db.SteerStarted:
			started++
		case db.SteerEnded:
			ended++
		}
	}
	if started != ended+1 {
		t.Fatalf("started %d, ended %d: %s", started, ended, kinds(events))
	}
	if detail, err := s.GetTree(ctx, tree.ID); err != nil || detail.Steerer == nil {
		t.Fatalf("no active steerer: %v", err)
	}
}

// caseConcurrentSteeringCursor: a reader following the feed with an after
// cursor while writers append concurrently sees every event (a later
// commit never carries a lower id than one the cursor passed).
func caseConcurrentSteeringCursor(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-cursor")
	const writers = 8
	var members []db.TreeMember
	for i := range writers {
		members = append(members, db.TreeMember{TaskID: mustTask(t, s, ctx, p.ID, fmt.Sprintf("m%d", i), nil).ID})
	}
	tree := mustTree(t, s, ctx, "Cursor", db.StartAuto, members...)
	mustSteer(t, s, ctx, tree.ID, p.ID, "r1", false)
	const cancels = 16
	var done atomic.Bool
	seen := map[int64]bool{}
	var cursor int64
	follow := func() {
		events, _, err := s.SteeringEvents(ctx, tree.ID, cursor, 200)
		if err != nil {
			t.Error(err)
			return
		}
		for _, e := range events {
			seen[e.ID] = true
			cursor = e.ID
		}
	}
	reader := make(chan struct{})
	go func() {
		defer close(reader)
		for !done.Load() {
			follow()
		}
	}()
	// Cancelling an unstarted member never writes its tree row (a note
	// stamps it, which would serialize the writers by itself), so only
	// the event id lock orders these commits.
	errs := race(writers, func(i int) error {
		task := members[i].TaskID
		for range cancels {
			for _, to := range []db.TaskState{db.StateCancelled, db.StateBacklog} {
				if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task, To: to}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	done.Store(true)
	<-reader
	follow()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	all := steeringFeed(t, s, ctx, tree.ID)
	if len(all) != 1+writers*cancels {
		t.Fatalf("%d events, want %d", len(all), 1+writers*cancels)
	}
	for _, e := range all {
		if !seen[e.ID] {
			t.Fatalf("cursor skipped event %d", e.ID)
		}
	}
}
