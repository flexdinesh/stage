package engine_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/steering"
)

// Steering delivery through the real Claude Code adapter (fake claude CLI):
// an idle steerer session is resumed with the digest; a working one is
// busy, so the update stays pending until its turn ends (ADR 0013).
func TestSteeringDeliveryClaude(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, member, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		tree, err := store.CreateTree(ctx, db.NewTree{Title: "Claude tree", StartMode: db.StartManual, Tasks: []db.TreeMember{{TaskID: member.ID}}})
		if err != nil {
			t.Fatal(err)
		}
		r, err := store.SetTreeSteerer(ctx, tree.ID, db.SteererInput{
			Task:      db.NewTask{ProjectID: member.ProjectID, ConnectionID: claudeConn, Title: "Steer", Prompt: "steer the tree"},
			RequestID: "steer-1", Digest: "d",
		})
		if err != nil {
			t.Fatal(err)
		}
		steererTask, err := store.GetTask(ctx, r.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		steerer := claudeFinish(t, store, svc, f, steererTask) // idle, done
		now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
		d := steering.New(store, svc.Coordinator, slog.New(slog.DiscardHandler))
		d.Now = func() time.Time { return now }
		pass := func() {
			t.Helper()
			if err := d.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
		}
		digests := func() int {
			// A digest is the whole resumed prompt (the launch brief only names the tag).
			n := 0
			for _, c := range f.Read().Calls {
				if len(c.Args) > 0 && strings.HasPrefix(c.Args[len(c.Args)-1], `<stage-steering tree="`) {
					n++
				}
			}
			return n
		}
		note := func(text string) {
			t.Helper()
			if _, err := store.AppendNote(ctx, db.JournalOwner{TaskID: &member.ID}, db.AppendNoteInput{Text: text, Category: "blocker", RequestID: text}); err != nil {
				t.Fatal(err)
			}
			pass()
		}

		note("first")
		now = now.Add(10 * time.Second)
		pass()
		got, err := store.GetTask(ctx, steerer.ID)
		if err != nil || digests() != 1 || got.State != db.StateRunning || *got.Session != *steerer.Session {
			t.Fatalf("delivery: digests %d steerer %+v %v", digests(), got, err)
		}

		// Working: busy, kept pending without an error.
		note("second")
		now = now.Add(10 * time.Second)
		pass()
		detail, err := store.GetTree(ctx, tree.ID)
		if err != nil || digests() != 1 || detail.Steerer == nil || detail.Steerer.Pending != 1 || detail.Steerer.LastError != nil {
			t.Fatalf("busy: digests %d steerer %+v %v", digests(), detail.Steerer, err)
		}
		f.SetState(steerer.Session.ExternalSessionID, "done", "idle", "")
		claudePoll(t, store, svc, got, db.StateDone)
		pass()
		if digests() != 2 {
			t.Fatalf("after the turn: digests %d", digests())
		}
	})
}
