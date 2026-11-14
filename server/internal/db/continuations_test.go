package db_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func continuationThreads(t *testing.T, s *db.Store, ctx context.Context) {
	t.Helper()
	now := time.Now().UTC()
	if err := s.UpsertThreads(ctx, []t3.Thread{
		{ThreadID: "a", ProjectID: "p", Title: "A", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now},
		{ThreadID: "b", ProjectID: "p", Title: "B", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now},
		{ThreadID: "c", ProjectID: "p", Title: "C", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now},
		{ThreadID: "d", ProjectID: "p", Title: "D", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestContinuationLinearComposition(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			s, ctx := setup(t)
			continuationThreads(t, s, ctx)
			create := func(a, b string) db.Continuation {
				t.Helper()
				c, err := s.CreateContinuation(ctx, a, b, "  prompt\n")
				if err != nil {
					t.Fatal(err)
				}
				return c
			}
			var ab, bc db.Continuation
			if reverse {
				bc = create("b", "c")
				ab = create("a", "b")
			} else {
				ab = create("a", "b")
				bc = create("b", "c")
			}
			order, err := s.ListContinuations(ctx, "c")
			if err != nil || len(order) != 2 || order[0].ID != ab.ID || order[1].ID != bc.ID || order[1].PreviousContinuationID == nil || *order[1].PreviousContinuationID != ab.ID {
				t.Fatalf("order=%+v err=%v", order, err)
			}
			if order[0].Prompt != "  prompt\n" {
				t.Fatal("prompt changed")
			}
			for _, pair := range [][2]string{{"a", "d"}, {"d", "c"}, {"c", "a"}, {"a", "a"}} {
				_, err := s.CreateContinuation(ctx, pair[0], pair[1], "x")
				var conflict *db.ConflictError
				if !errors.As(err, &conflict) {
					t.Fatalf("%v err=%v, want conflict", pair, err)
				}
			}
			if _, err := s.MutateContinuation(ctx, ab.ID, "cancel", nil); err != nil {
				t.Fatal(err)
			}
			child, err := s.GetContinuation(ctx, bc.ID)
			if err != nil || child.State != db.ContinuationBlocked || len(child.WaitReasons) != 1 || child.WaitReasons[0] != db.WaitUpstreamCancelled || child.PreviousContinuationID == nil || *child.PreviousContinuationID != ab.ID {
				t.Fatalf("cancel descendant=%+v err=%v", child, err)
			}
			if _, err := s.CreateContinuation(ctx, "a", "d", "x"); err == nil {
				t.Fatal("cancel silently freed successor slot")
			}
		})
	}
}

func TestContinuationClaimRevisionAndMutations(t *testing.T) {
	s, ctx := setup(t)
	continuationThreads(t, s, ctx)
	c, err := s.CreateContinuation(ctx, "a", "b", "x")
	if err != nil {
		t.Fatal(err)
	}
	prompt := "updated"
	if _, err := s.MutateContinuation(ctx, c.ID, "edit", &prompt); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.ClaimContinuation(ctx, c, "old"); err != nil || claimed {
		t.Fatalf("stale claim=%v %v", claimed, err)
	}
	c, err = s.GetContinuation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := s.ClaimContinuation(ctx, c, "envelope")
	if err != nil || !ok || claimed.RequestID == nil || claimed.DeliveryPrompt == nil || *claimed.DeliveryPrompt != "envelope" {
		t.Fatalf("claim=%+v %v %v", claimed, ok, err)
	}
	for _, action := range []string{"edit", "pause", "resume", "cancel"} {
		if _, err := s.MutateContinuation(ctx, c.ID, action, &prompt); err == nil {
			t.Fatalf("claimed mutation %s accepted", action)
		}
	}
	if _, err := s.CreateContinuation(ctx, "d", "a", "x"); err == nil {
		t.Fatal("prepended prerequisite to claimed continuation")
	}
	if err := s.ObserveContinuation(ctx, c, db.ContinuationPaused, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetContinuation(ctx, c.ID)
	if current.State != db.ContinuationSending {
		t.Fatal("stale observation overwrote claim")
	}
	if err := s.RecordContinuationDelivery(ctx, claimed, "r", "m", nil); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetContinuation(ctx, c.ID)
	if current.State != db.ContinuationRunning || current.RunID == nil || current.MessageID == nil || current.SentAt == nil {
		t.Fatalf("receipt=%+v", current)
	}
}

func TestContinuationConcurrentMembership(t *testing.T) {
	s, ctx := setup(t)
	continuationThreads(t, s, ctx)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, target := range []string{"b", "c"} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			_, err := s.CreateContinuation(ctx, "a", target, "x")
			results <- err
		}(target)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			var conflict *db.ConflictError
			if !errors.As(err, &conflict) {
				t.Fatal(err)
			}
		}
	}
	if success != 1 {
		t.Fatalf("created %d successors", success)
	}
}

func TestContinuationLegacyConflictBothDirections(t *testing.T) {
	for _, legacyFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(legacyFirst), func(t *testing.T) {
			s, ctx := setup(t)
			continuationThreads(t, s, ctx)
			p := mustProject(t, s, ctx, "p")
			a := mustTask(t, s, ctx, p.ID, "a", nil)
			b := mustTask(t, s, ctx, p.ID, "b", nil)
			thread := "a"
			if _, err := s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateRunning, ThreadID: &thread}); err != nil {
				t.Fatal(err)
			}
			link := db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: a.ID}}
			if legacyFirst {
				if _, err := s.UpdateTask(ctx, b.ID, link); err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateContinuation(ctx, "a", "b", "x"); err == nil {
					t.Fatal("continuation accepted legacy participant")
				}
				unlink := db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, Null: true}}
				if _, err := s.UpdateTask(ctx, b.ID, unlink); err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateContinuation(ctx, "a", "b", "x"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.CreateContinuation(ctx, "a", "b", "x"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UpdateTask(ctx, b.ID, link); err == nil {
					t.Fatal("legacy order accepted continuation participant")
				}
				if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "new", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, AfterTaskID: &a.ID}); err == nil {
					t.Fatal("new legacy follower accepted continuation participant")
				}
			}
		})
	}
}

func TestContinuationCompletedOrdersReusableAndHistorySeparate(t *testing.T) {
	for _, final := range []db.ContinuationState{db.ContinuationSettled, db.ContinuationCancelled} {
		t.Run(string(final), func(t *testing.T) {
			s, ctx := setup(t)
			continuationThreads(t, s, ctx)
			old, err := s.CreateContinuation(ctx, "a", "b", "old")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ObserveContinuation(ctx, old, final, nil, nil, false); err != nil {
				t.Fatal(err)
			}
			next, err := s.CreateContinuation(ctx, "b", "c", "new")
			if err != nil {
				t.Fatal(err)
			}
			if next.PreviousContinuationID != nil || next.OrderID == old.OrderID {
				t.Fatal("new execution inherited historical prerequisite")
			}
			onlyA, err := s.ListContinuations(ctx, "a")
			if err != nil || len(onlyA) != 1 || onlyA[0].ID != old.ID {
				t.Fatalf("history merged via reused thread: %+v %v", onlyA, err)
			}
			onlyC, err := s.ListContinuations(ctx, "c")
			if err != nil || len(onlyC) != 1 || onlyC[0].ID != next.ID {
				t.Fatalf("unrelated historical execution leaked: %+v %v", onlyC, err)
			}
			if err := s.ObserveContinuation(ctx, next, db.ContinuationCancelled, nil, nil, false); err != nil {
				t.Fatal(err)
			}
			reused, err := s.CreateContinuation(ctx, "a", "b", "again")
			if err != nil {
				t.Fatal(err)
			}
			if reused.OrderID == old.OrderID || reused.PreviousContinuationID != nil {
				t.Fatal("same pair could not start independent execution")
			}
		})
	}
}

func TestContinuationJoinsIndependentLiveChains(t *testing.T) {
	s, ctx := setup(t)
	continuationThreads(t, s, ctx)
	ab, err := s.CreateContinuation(ctx, "a", "b", "ab")
	if err != nil {
		t.Fatal(err)
	}
	cd, err := s.CreateContinuation(ctx, "c", "d", "cd")
	if err != nil {
		t.Fatal(err)
	}
	bc, err := s.CreateContinuation(ctx, "b", "c", "bc")
	if err != nil {
		t.Fatal(err)
	}
	order, err := s.ListContinuations(ctx, "d")
	if err != nil || len(order) != 3 || order[0].ID != ab.ID || order[1].ID != bc.ID || order[2].ID != cd.ID || order[1].PreviousContinuationID == nil || *order[1].PreviousContinuationID != ab.ID || order[2].PreviousContinuationID == nil || *order[2].PreviousContinuationID != bc.ID {
		t.Fatalf("joined order=%+v err=%v", order, err)
	}
}
