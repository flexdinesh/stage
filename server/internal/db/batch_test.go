package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func ptr[T any](v T) *T { return &v }

func idsOf(ts []db.Task) []int64 {
	out := make([]int64, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func originTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title, origin string) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: projectID, Title: title, Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, OriginThreadID: &origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func link(t *testing.T, s *db.Store, ctx context.Context, id, after int64) {
	t.Helper()
	if _, err := s.UpdateTask(ctx, id, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: after}}); err != nil {
		t.Fatalf("link %d after %d: %v", id, after, err)
	}
}

func TestGroupBatch(t *testing.T) {
	task := func(id int64, after, next *int64) db.Task {
		return db.Task{ID: id, AfterTaskID: after, NextTaskID: next}
	}
	// 1→3→4 chain; 2 independent; 5 waits on outside 9 (independent);
	// 6→7 with 6 after outside 8 (chain headed by 6); 10 followed by outside 11.
	got := db.GroupBatch([]db.Task{
		task(1, nil, ptr[int64](3)), task(2, nil, nil), task(3, ptr[int64](1), ptr[int64](4)), task(4, ptr[int64](3), nil),
		task(5, ptr[int64](9), nil), task(6, ptr[int64](8), ptr[int64](7)), task(7, ptr[int64](6), nil), task(10, nil, ptr[int64](11)),
	})
	if len(got.Chains) != 2 || !slices.Equal(idsOf(got.Chains[0]), []int64{1, 3, 4}) || !slices.Equal(idsOf(got.Chains[1]), []int64{6, 7}) {
		t.Fatalf("chains %v", got.Chains)
	}
	if !slices.Equal(idsOf(got.Independent), []int64{2, 5, 10}) {
		t.Fatalf("independent %v", idsOf(got.Independent))
	}
	if empty := db.GroupBatch(nil); empty.Chains == nil || empty.Independent == nil {
		t.Fatalf("empty batch must be non-nil slices: %+v", empty)
	}
}

func TestCreatedBy(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := originTask(t, s, ctx, p.ID, "a", "plan")
	b := originTask(t, s, ctx, p.ID, "b", "plan")
	c := originTask(t, s, ctx, p.ID, "c", "plan")
	mine := mustTask(t, s, ctx, p.ID, "owner", nil)
	_ = originTask(t, s, ctx, p.ID, "other", "elsewhere")
	link(t, s, ctx, b.ID, a.ID)
	link(t, s, ctx, mine.ID, b.ID) // owner task after the batch: not in it
	got, err := s.CreatedBy(ctx, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 1 || !slices.Equal(idsOf(got.Chains[0]), []int64{a.ID, b.ID}) || !slices.Equal(idsOf(got.Independent), []int64{c.ID}) {
		t.Fatalf("batch %+v", got)
	}
	if got.Chains[0][1].NextTaskID == nil || *got.Chains[0][1].NextTaskID != mine.ID {
		t.Fatalf("outside follower must still show: %+v", got.Chains[0][1])
	}
	none, err := s.CreatedBy(ctx, "nobody")
	if err != nil || len(none.Chains)+len(none.Independent) != 0 {
		t.Fatalf("none %+v %v", none, err)
	}
}

func TestOrderTasksLinksSelected(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	now := time.Now().UTC()
	if err := s.UpsertThreads(ctx, []t3.Thread{{ThreadID: "plan", ProjectID: "proj-1", Title: "plan", Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	a := originTask(t, s, ctx, p.ID, "a", "plan")
	b := originTask(t, s, ctx, p.ID, "b", "plan")
	c := originTask(t, s, ctx, p.ID, "c", "plan")
	// Head keeps its session wait; a follower's start time is replaced.
	if _, err := s.UpdateTask(ctx, c.ID, db.TaskPatch{AfterThreadID: db.Optional[string]{Set: true, V: "plan"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTask(ctx, a.ID, db.TaskPatch{StartAt: db.Optional[time.Time]{Set: true, V: now.Add(time.Hour)}, StartTz: db.Optional[string]{Set: true, V: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	seq, err := s.OrderTasks(ctx, []int64{c.ID, a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(idsOf(seq), []int64{c.ID, a.ID, b.ID}) {
		t.Fatalf("seq %v", idsOf(seq))
	}
	if seq[0].AfterThreadID == nil || *seq[0].AfterThreadID != "plan" {
		t.Fatalf("head lost its session wait %+v", seq[0])
	}
	if seq[1].StartAt != nil || seq[1].AfterTaskID == nil || *seq[1].AfterTaskID != c.ID {
		t.Fatalf("follower a %+v", seq[1])
	}
}

func TestOrderTasksReorderMovesWaitAndTailFollower(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	outside := mustTask(t, s, ctx, p.ID, "outside", nil)
	a := originTask(t, s, ctx, p.ID, "a", "plan")
	b := originTask(t, s, ctx, p.ID, "b", "plan")
	c := originTask(t, s, ctx, p.ID, "c", "plan")
	tail := mustTask(t, s, ctx, p.ID, "tail", nil)
	link(t, s, ctx, a.ID, outside.ID)
	link(t, s, ctx, b.ID, a.ID)
	link(t, s, ctx, c.ID, b.ID)
	link(t, s, ctx, tail.ID, c.ID)

	seq, err := s.OrderTasks(ctx, []int64{c.ID, a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	// outside → c → a → b → tail: the chain's wait moved to the new head and
	// its outside follower to the new tail.
	if want := []int64{outside.ID, c.ID, a.ID, b.ID, tail.ID}; !slices.Equal(idsOf(seq), want) {
		t.Fatalf("seq %v want %v", idsOf(seq), want)
	}
	if got, _ := s.GetTask(ctx, a.ID); got.AfterTaskID == nil || *got.AfterTaskID != c.ID {
		t.Fatalf("a %+v", got)
	}
}

func TestOrderTasksRules(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	c := mustTask(t, s, ctx, p.ID, "c", nil)
	other := mustTask(t, s, ctx, p.ID, "other", nil)
	var invalid *db.InvalidError
	var conflict *db.ConflictError
	if _, err := s.OrderTasks(ctx, []int64{a.ID}); !errors.As(err, &invalid) {
		t.Fatalf("one id: %v", err)
	}
	if _, err := s.OrderTasks(ctx, []int64{a.ID, a.ID}); !errors.As(err, &invalid) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := s.OrderTasks(ctx, []int64{a.ID, 9999}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// An outside follower of a middle task would give it two followers.
	link(t, s, ctx, other.ID, b.ID)
	if _, err := s.OrderTasks(ctx, []int64{a.ID, b.ID, c.ID}); !errors.As(err, &conflict) {
		t.Fatalf("second follower: %v", err)
	}
	if got, _ := s.GetTask(ctx, b.ID); got.AfterTaskID != nil {
		t.Fatalf("failed order must roll back: %+v", got)
	}
	// Non-backlog tasks cannot be ordered.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: c.ID, To: db.StateRunning})
	if _, err := s.OrderTasks(ctx, []int64{a.ID, c.ID}); !errors.As(err, &conflict) {
		t.Fatalf("running: %v", err)
	}
}
