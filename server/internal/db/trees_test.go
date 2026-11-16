package db_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func setState(t *testing.T, s *db.Store, ctx context.Context, id int64, to db.TaskState) {
	t.Helper()
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: to}); err != nil {
		t.Fatalf("task %d to %s: %v", id, to, err)
	}
}

func getTask(t *testing.T, s *db.Store, ctx context.Context, id int64) db.Task {
	t.Helper()
	got, err := s.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return got
}

func readiness(task db.Task) string {
	if task.Readiness == nil {
		return "-"
	}
	return string(*task.Readiness)
}

func mustTree(t *testing.T, s *db.Store, ctx context.Context, title string, mode db.StartMode, members ...db.TreeMember) db.TreeDetail {
	t.Helper()
	tree, err := s.CreateTree(ctx, db.NewTree{Title: title, StartMode: mode, Tasks: members})
	if err != nil {
		t.Fatalf("create tree %q: %v", title, err)
	}
	return tree
}

func treeGone(t *testing.T, s *db.Store, ctx context.Context, id int64) bool {
	t.Helper()
	_, err := s.GetTree(ctx, id)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		t.Fatal(err)
	}
	return errors.Is(err, db.ErrNotFound)
}

func TestCreateTreeAndLevels(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	q := mustProject(t, s, ctx, "proj-2")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, q.ID, "b", nil) // trees may span projects
	c := mustTask(t, s, ctx, p.ID, "c", nil)
	d := mustTask(t, s, ctx, p.ID, "d", nil)
	e := mustTask(t, s, ctx, p.ID, "e", nil)
	// x is alone in another tree: joining moves it and the emptied tree goes.
	x := mustTask(t, s, ctx, p.ID, "x", nil)
	old := mustTree(t, s, ctx, "old", "", db.TreeMember{TaskID: x.ID})

	tree := mustTree(t, s, ctx, "Ship it", db.StartAuto,
		db.TreeMember{TaskID: e.ID, Needs: []int64{c.ID, d.ID}}, // needs may name members listed later
		db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID},
		db.TreeMember{TaskID: c.ID, Needs: []int64{a.ID, b.ID}},
		db.TreeMember{TaskID: d.ID, Needs: []int64{a.ID}},
		db.TreeMember{TaskID: x.ID},
	)
	if tree.Title != "Ship it" || tree.StartMode != db.StartAuto || len(tree.Tasks) != 6 {
		t.Fatalf("tree %+v", tree.Tree)
	}
	if got := fmt.Sprint(tree.Levels); got != fmt.Sprint([][]int64{{a.ID, b.ID, x.ID}, {c.ID, d.ID}, {e.ID}}) {
		t.Fatalf("levels %s", got)
	}
	want := []db.TreeEdge{{c.ID, a.ID}, {c.ID, b.ID}, {d.ID, a.ID}, {e.ID, c.ID}, {e.ID, d.ID}}
	if !slices.Equal(tree.Edges, want) {
		t.Fatalf("edges %v want %v", tree.Edges, want)
	}
	got := getTask(t, s, ctx, c.ID)
	if got.TreeID == nil || *got.TreeID != tree.ID || got.TreeTitle == nil || *got.TreeTitle != "Ship it" ||
		got.StartMode != nil || got.EffectiveStartMode == nil || *got.EffectiveStartMode != db.StartAuto ||
		!slices.Equal(got.Needs, []int64{a.ID, b.ID}) || !slices.Equal(got.Dependents, []int64{e.ID}) {
		t.Fatalf("member c %+v", got)
	}
	if !treeGone(t, s, ctx, old.ID) {
		t.Fatal("emptied tree must be deleted")
	}
	var invalid *db.InvalidError
	if _, err := s.CreateTree(ctx, db.NewTree{Title: "none"}); !errors.As(err, &invalid) {
		t.Fatalf("empty tree: %v", err)
	}
	if _, err := s.CreateTree(ctx, db.NewTree{Title: "dup", Tasks: []db.TreeMember{{TaskID: a.ID}, {TaskID: a.ID}}}); !errors.As(err, &invalid) {
		t.Fatalf("duplicate member: %v", err)
	}
	if _, err := s.CreateTree(ctx, db.NewTree{Title: "missing", Tasks: []db.TreeMember{{TaskID: 9999}}}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing member: %v", err)
	}
}

func TestTreeLevels(t *testing.T) {
	e := func(task, need int64) db.TreeEdge { return db.TreeEdge{TaskID: task, NeedID: need} }
	cases := []struct {
		name  string
		ids   []int64
		edges []db.TreeEdge
		want  [][]int64
	}{
		{"empty", nil, nil, [][]int64{}},
		{"diamond", []int64{4, 3, 2, 1}, []db.TreeEdge{e(2, 1), e(3, 1), e(4, 2), e(4, 3)}, [][]int64{{1}, {2, 3}, {4}}},
		// 3 needs 1 directly and through 2: longest path puts it below 2.
		{"skip level", []int64{1, 2, 3}, []db.TreeEdge{e(2, 1), e(3, 1), e(3, 2)}, [][]int64{{1}, {2}, {3}}},
		{"outside need ignored", []int64{5, 6}, []db.TreeEdge{e(5, 99), e(6, 5)}, [][]int64{{5}, {6}}},
		{"duplicates", []int64{2, 1, 2}, []db.TreeEdge{e(2, 1)}, [][]int64{{1}, {2}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := db.TreeLevels(c.ids, c.edges); fmt.Sprint(got) != fmt.Sprint(c.want) || got == nil {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestSetNeedsRules(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	var conflict *db.ConflictError

	// Create with needs and no tree: treeless needs are pulled into a new
	// auto tree named after the first need.
	a := mustTask(t, s, ctx, p.ID, "Plan", nil)
	b := mustTask(t, s, ctx, p.ID, "Build", []int64{a.ID})
	if b.TreeID == nil || b.TreeTitle == nil || *b.TreeTitle != "Plan" || *b.EffectiveStartMode != db.StartAuto || !slices.Equal(b.Needs, []int64{a.ID}) {
		t.Fatalf("created with needs %+v", b)
	}
	tree := *b.TreeID
	if got := getTask(t, s, ctx, a.ID); got.TreeID == nil || *got.TreeID != tree {
		t.Fatalf("need pulled into the tree %+v", got)
	}
	// Create with needs in a tree joins it; a treeless need is pulled in too.
	solo := mustTask(t, s, ctx, p.ID, "solo", nil)
	c := mustTask(t, s, ctx, p.ID, "Test", []int64{b.ID, solo.ID})
	if *c.TreeID != tree || *getTask(t, s, ctx, solo.ID).TreeID != tree {
		t.Fatalf("join needs' tree %+v", c)
	}
	// Needs in different trees: refused, nothing written.
	other := mustTask(t, s, ctx, p.ID, "other", nil)
	otherDep := mustTask(t, s, ctx, p.ID, "other dep", []int64{other.ID})
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "x", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, Needs: []int64{a.ID, other.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("needs in two trees: %v", err)
	}
	// Explicit tree: a need in another tree is refused; no needs just joins.
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "x", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, TreeID: &tree, Needs: []int64{other.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("need outside explicit tree: %v", err)
	}
	joined, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "joined", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, TreeID: otherDep.TreeID})
	if err != nil || joined.TreeID == nil || *joined.TreeID != *otherDep.TreeID || len(joined.Needs) != 0 {
		t.Fatalf("join explicit tree %+v %v", joined, err)
	}
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "x", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, TreeID: ptr[int64](9999)}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing tree: %v", err)
	}

	// SetNeeds: self, missing, cycle (direct and transitive).
	if _, err := s.SetNeeds(ctx, c.ID, []int64{c.ID}); !errors.As(err, &conflict) {
		t.Fatalf("self: %v", err)
	}
	if _, err := s.SetNeeds(ctx, c.ID, []int64{9999}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing need: %v", err)
	}
	if _, err := s.SetNeeds(ctx, a.ID, []int64{c.ID}); !errors.As(err, &conflict) {
		t.Fatalf("transitive cycle: %v", err)
	}
	if _, err := s.SetNeeds(ctx, b.ID, []int64{c.ID}); !errors.As(err, &conflict) {
		t.Fatalf("direct cycle: %v", err)
	}
	if got := getTask(t, s, ctx, b.ID); !slices.Equal(got.Needs, []int64{a.ID}) {
		t.Fatalf("refused change must keep needs %v", got.Needs)
	}
	// Duplicates collapse; order does not matter.
	if got := needs(t, s, ctx, c.ID, solo.ID, b.ID, b.ID); !slices.Equal(got.Needs, []int64{b.ID, solo.ID}) {
		t.Fatalf("dedupe %v", got.Needs)
	}
	// A settled need is allowed.
	setState(t, s, ctx, solo.ID, db.StateSettled)
	d := mustTask(t, s, ctx, p.ID, "d", []int64{solo.ID})
	if !slices.Equal(d.Needs, []int64{solo.ID}) {
		t.Fatalf("settled need %+v", d)
	}
	// Needs change only in backlog; the same set is a no-op in any state.
	setState(t, s, ctx, c.ID, db.StateRunning)
	if _, err := s.SetNeeds(ctx, c.ID, []int64{b.ID}); !errors.As(err, &conflict) {
		t.Fatalf("running task needs change: %v", err)
	}
	if _, err := s.SetNeeds(ctx, c.ID, []int64{b.ID, solo.ID}); err != nil {
		t.Fatalf("unchanged needs of a running task: %v", err)
	}
	// A non-backlog task joins a tree only without needs.
	r := mustTask(t, s, ctx, p.ID, "running", nil)
	setState(t, s, ctx, r.ID, db.StateRunning)
	if _, err := s.AddToTree(ctx, tree, r.ID, []int64{a.ID}); !errors.As(err, &conflict) {
		t.Fatalf("running task with needs: %v", err)
	}
	if got := getTask(t, s, ctx, r.ID); got.TreeID != nil {
		t.Fatalf("refused add must roll back %+v", got)
	}
	if _, err := s.AddToTree(ctx, tree, r.ID, nil); err != nil {
		t.Fatalf("running task without needs: %v", err)
	}
}

func TestSpliceDiamondDedupeAndBaseTask(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	c := mustTask(t, s, ctx, p.ID, "c", []int64{a.ID})
	d := mustTask(t, s, ctx, p.ID, "d", []int64{b.ID, c.ID})
	tree := *d.TreeID
	ws := db.Workspace{Type: "worktree", BaseRef: "origin/main", Base: db.BasePredecessor, BaseTaskID: &b.ID}
	if got, err := s.UpdateTask(ctx, d.ID, db.TaskPatch{Workspace: db.Optional[db.Workspace]{Set: true, V: ws}}); err != nil ||
		got.Workspace.BaseTaskID == nil || *got.Workspace.BaseTaskID != b.ID {
		t.Fatalf("base task %+v %v", got.Workspace, err)
	}
	var invalid *db.InvalidError
	notNeed := ws
	notNeed.BaseTaskID = &a.ID
	if _, err := s.UpdateTask(ctx, d.ID, db.TaskPatch{Workspace: db.Optional[db.Workspace]{Set: true, V: notNeed}}); !errors.As(err, &invalid) {
		t.Fatalf("base task not a need: %v", err)
	}

	// Splice b: d takes over b's need a (alongside c), and its base on b is stale.
	if err := s.RemoveFromTree(ctx, tree, b.ID); err != nil {
		t.Fatal(err)
	}
	got := getTask(t, s, ctx, d.ID)
	if !slices.Equal(got.Needs, []int64{a.ID, c.ID}) || got.Workspace.BaseTaskID != nil || !got.Workspace.FromPredecessor() {
		t.Fatalf("after splicing b: %+v %+v", got.Needs, got.Workspace)
	}
	if gb := getTask(t, s, ctx, b.ID); gb.TreeID != nil || len(gb.Needs) != 0 || len(gb.Dependents) != 0 {
		t.Fatalf("spliced b must be standalone %+v", gb)
	}
	// Splice c: its need a is already d's (deduplicated).
	if err := s.RemoveFromTree(ctx, tree, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := getTask(t, s, ctx, d.ID); !slices.Equal(got.Needs, []int64{a.ID}) {
		t.Fatalf("after splicing c: %v", got.Needs)
	}
	if err := s.RemoveFromTree(ctx, tree, b.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("remove non-member: %v", err)
	}
	// Delete splices too: x <- y <- z, delete y => x <- z.
	x := mustTask(t, s, ctx, p.ID, "x", nil)
	y := mustTask(t, s, ctx, p.ID, "y", []int64{x.ID})
	z := mustTask(t, s, ctx, p.ID, "z", []int64{y.ID})
	if err := s.DeleteTask(ctx, y.ID); err != nil {
		t.Fatal(err)
	}
	if got := getTask(t, s, ctx, z.ID); !slices.Equal(got.Needs, []int64{x.ID}) {
		t.Fatalf("delete splice %v", got.Needs)
	}
}

func TestMoveBetweenTrees(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	c := mustTask(t, s, ctx, p.ID, "c", []int64{b.ID})
	m := mustTask(t, s, ctx, p.ID, "m", nil)
	t2 := mustTree(t, s, ctx, "second", db.StartManual, db.TreeMember{TaskID: m.ID})

	tree, err := s.AddToTree(ctx, t2.ID, b.ID, []int64{m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Tasks) != 2 || fmt.Sprint(tree.Levels) != fmt.Sprint([][]int64{{m.ID}, {b.ID}}) {
		t.Fatalf("second tree %+v", tree)
	}
	gb := getTask(t, s, ctx, b.ID)
	if *gb.TreeID != t2.ID || !slices.Equal(gb.Needs, []int64{m.ID}) || len(gb.Dependents) != 0 || *gb.EffectiveStartMode != db.StartManual {
		t.Fatalf("moved b %+v", gb)
	}
	if gc := getTask(t, s, ctx, c.ID); !slices.Equal(gc.Needs, []int64{a.ID}) || *gc.TreeID != *c.TreeID {
		t.Fatalf("c must take over b's need in the old tree %+v", gc)
	}
	// A need from another tree is refused when adding.
	var conflict *db.ConflictError
	if _, err := s.AddToTree(ctx, t2.ID, b.ID, []int64{a.ID}); !errors.As(err, &conflict) {
		t.Fatalf("need from another tree: %v", err)
	}
	if _, err := s.AddToTree(ctx, 9999, b.ID, nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing tree: %v", err)
	}
}

func TestDissolveAndEmptyTrees(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	ws := db.Workspace{Type: "worktree", BaseRef: "origin/main", Base: db.BasePredecessor, BaseTaskID: &a.ID}
	if _, err := s.UpdateTask(ctx, b.ID, db.TaskPatch{Workspace: db.Optional[db.Workspace]{Set: true, V: ws}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTree(ctx, *b.TreeID); err != nil {
		t.Fatal(err)
	}
	if !treeGone(t, s, ctx, *b.TreeID) {
		t.Fatal("dissolved tree still exists")
	}
	for _, id := range []int64{a.ID, b.ID} {
		if got := getTask(t, s, ctx, id); got.TreeID != nil || len(got.Needs)+len(got.Dependents) != 0 || got.Workspace.BaseTaskID != nil {
			t.Fatalf("dissolved member %+v", got)
		}
	}
	if err := s.DeleteTree(ctx, *b.TreeID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("dissolve twice: %v", err)
	}

	// The last member leaving (removed or deleted) deletes the tree.
	x := mustTask(t, s, ctx, p.ID, "x", nil)
	y := mustTask(t, s, ctx, p.ID, "y", []int64{x.ID})
	tree := *y.TreeID
	if err := s.RemoveFromTree(ctx, tree, y.ID); err != nil {
		t.Fatal(err)
	}
	if treeGone(t, s, ctx, tree) {
		t.Fatal("tree with a member left must stay")
	}
	if err := s.DeleteTask(ctx, x.ID); err != nil {
		t.Fatal(err)
	}
	if !treeGone(t, s, ctx, tree) {
		t.Fatal("empty tree must be deleted")
	}
}

func TestReadinessAndLaunchable(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	launchable := func() []int64 {
		t.Helper()
		ids, err := s.LaunchableTasks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	solo := mustTask(t, s, ctx, p.ID, "solo", nil)
	if r := readiness(solo); r != "-" {
		t.Fatalf("task outside a tree has no readiness: %s", r)
	}
	// Backlog root: ready (the user starts it; auto never launches roots).
	if ra, rb := readiness(getTask(t, s, ctx, a.ID)), readiness(getTask(t, s, ctx, b.ID)); ra != "ready" || rb != "blocked" {
		t.Fatalf("initial a=%s b=%s", ra, rb)
	}
	if ids := launchable(); len(ids) != 0 {
		t.Fatalf("launchable before settle %v", ids)
	}
	setState(t, s, ctx, a.ID, db.StateRunning)
	setState(t, s, ctx, a.ID, db.StateDone)
	if ra, rb := readiness(getTask(t, s, ctx, a.ID)), readiness(getTask(t, s, ctx, b.ID)); ra != "-" || rb != "blocked" {
		t.Fatalf("done but unsettled a=%s b=%s", ra, rb)
	}
	setState(t, s, ctx, a.ID, db.StateSettled)
	// Auto with needs settled: no readiness, launchable.
	if rb := readiness(getTask(t, s, ctx, b.ID)); rb != "-" {
		t.Fatalf("auto released b=%s", rb)
	}
	if ids := launchable(); !slices.Equal(ids, []int64{b.ID}) {
		t.Fatalf("launchable %v", ids)
	}
	// Manual override: ready, not launchable; null inherits again.
	if _, err := s.UpdateTask(ctx, b.ID, db.TaskPatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	if got := getTask(t, s, ctx, b.ID); readiness(got) != "ready" || *got.StartMode != db.StartManual || *got.EffectiveStartMode != db.StartManual {
		t.Fatalf("manual b %+v", got)
	}
	if ids := launchable(); len(ids) != 0 {
		t.Fatalf("manual must not launch %v", ids)
	}
	if got, err := s.UpdateTask(ctx, b.ID, db.TaskPatch{StartMode: db.Optional[db.StartMode]{Set: true, Null: true}}); err != nil || got.StartMode != nil {
		t.Fatalf("inherit %+v %v", got, err)
	}
	// The tree's manual mode applies when inheriting.
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	if got := getTask(t, s, ctx, b.ID); readiness(got) != "ready" || got.StartMode != nil || *got.EffectiveStartMode != db.StartManual {
		t.Fatalf("tree manual b %+v", got)
	}
	// A failed, interrupted or cancelled need keeps dependents blocked.
	for _, st := range []db.TaskState{db.StateFailed, db.StateInterrupted, db.StateCancelled} {
		f := mustTask(t, s, ctx, p.ID, "f-"+string(st), nil)
		dep := mustTask(t, s, ctx, p.ID, "dep-"+string(st), []int64{f.ID})
		setState(t, s, ctx, f.ID, st)
		if r := readiness(getTask(t, s, ctx, dep.ID)); r != "blocked" {
			t.Fatalf("need %s: dependent %s", st, r)
		}
		if ids := launchable(); slices.Contains(ids, dep.ID) {
			t.Fatalf("need %s: dependent launchable", st)
		}
	}
	// A root with a start time waits on it (no readiness).
	root := mustTask(t, s, ctx, p.ID, "root", nil)
	tree := mustTree(t, s, ctx, "timed", "", db.TreeMember{TaskID: root.ID})
	at := time.Now().Add(time.Hour)
	tz := "UTC"
	if _, err := s.UpdateTask(ctx, root.ID, db.TaskPatch{StartAt: db.Optional[time.Time]{Set: true, V: at}, StartTz: db.Optional[string]{Set: true, V: tz}}); err != nil {
		t.Fatal(err)
	}
	if r := readiness(getTask(t, s, ctx, root.ID)); r != "-" {
		t.Fatalf("timed root %s (tree %d)", r, tree.ID)
	}
	startable, err := s.StartableInTree(ctx, *b.TreeID)
	if err != nil || !slices.Equal(startable, []int64{b.ID}) {
		t.Fatalf("startable %v %v", startable, err)
	}
}

func TestMarkReadyOnce(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	mark := func() []int64 {
		t.Helper()
		ids, err := s.MarkReady(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	readyEvents := func() int {
		t.Helper()
		events, err := s.TaskEvents(ctx, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range events {
			if e.Detail == "ready: needs settled" {
				n++
			}
		}
		return n
	}
	// The ready root (no needs) never notifies.
	if ids := mark(); len(ids) != 0 {
		t.Fatalf("before settle %v", ids)
	}
	setState(t, s, ctx, a.ID, db.StateSettled)
	if ids := mark(); !slices.Equal(ids, []int64{b.ID}) {
		t.Fatalf("first mark %v", ids)
	}
	if got := getTask(t, s, ctx, b.ID); got.ReadyAt == nil || readiness(got) != "ready" {
		t.Fatalf("ready b %+v", got)
	}
	if ids := mark(); len(ids) != 0 || readyEvents() != 1 {
		t.Fatalf("second mark %v, %d events", ids, readyEvents())
	}
	// Blocked again (a new unsettled need): the episode ends; the next settle
	// starts a new one.
	c := mustTask(t, s, ctx, p.ID, "c", nil)
	needs(t, s, ctx, b.ID, a.ID, c.ID)
	if ids := mark(); len(ids) != 0 {
		t.Fatalf("blocked mark %v", ids)
	}
	if got := getTask(t, s, ctx, b.ID); got.ReadyAt != nil {
		t.Fatalf("blocked b keeps readyAt %+v", got.ReadyAt)
	}
	setState(t, s, ctx, c.ID, db.StateSettled)
	if ids := mark(); !slices.Equal(ids, []int64{b.ID}) || readyEvents() != 2 {
		t.Fatalf("new episode %v, %d events", ids, readyEvents())
	}
	// A launch clears readyAt.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: b.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if got := getTask(t, s, ctx, b.ID); got.ReadyAt != nil {
		t.Fatalf("launch keeps readyAt %+v", got.ReadyAt)
	}
}

func TestListTreesRollup(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "alpha")
	q := mustProject(t, s, ctx, "beta")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, q.ID, "b", []int64{a.ID})
	c := mustTask(t, s, ctx, p.ID, "c", []int64{b.ID})
	r := mustTask(t, s, ctx, p.ID, "r", nil)
	active := *c.TreeID
	if _, err := s.AddToTree(ctx, active, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	setState(t, s, ctx, a.ID, db.StateRunning)

	x := mustTask(t, s, ctx, q.ID, "x", nil)
	y := mustTask(t, s, ctx, q.ID, "y", []int64{x.ID})
	done := *y.TreeID
	setState(t, s, ctx, x.ID, db.StateSettled)
	setState(t, s, ctx, y.ID, db.StateSettled)

	list := func(f db.TreeFilter) []db.TreeSummary {
		t.Helper()
		out, err := s.ListTrees(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ids := func(sums []db.TreeSummary) []int64 {
		out := []int64{}
		for _, s := range sums {
			out = append(out, s.ID)
		}
		return out
	}
	got := list(db.TreeFilter{})
	if !slices.Equal(ids(got), []int64{active}) {
		t.Fatalf("active trees %v", ids(got))
	}
	sum := got[0]
	want := db.TreeCounts{Total: 4, Backlog: 3, Blocked: 2, Ready: 1, Running: 1}
	if sum.Counts != want || sum.Levels != 3 || !slices.Equal(sum.Projects, []string{"alpha", "beta"}) || sum.SettledAt != nil || sum.Title != "a" {
		t.Fatalf("rollup %+v", sum)
	}
	_ = c
	if got := list(db.TreeFilter{All: true}); len(got) != 2 {
		t.Fatalf("all trees %v", ids(got))
	}
	past := time.Now().Add(-time.Hour)
	got = list(db.TreeFilter{SettledFrom: &past})
	if !slices.Contains(ids(got), done) || len(got) != 2 {
		t.Fatalf("settled from past %v", ids(got))
	}
	for _, sm := range got {
		if sm.ID == done && (sm.SettledAt == nil || sm.Counts.Settled != 2) {
			t.Fatalf("completed tree %+v", sm)
		}
	}
	future := time.Now().Add(time.Hour)
	if got := list(db.TreeFilter{SettledFrom: &future}); !slices.Equal(ids(got), []int64{active}) {
		t.Fatalf("settled from future %v", ids(got))
	}
	if got := list(db.TreeFilter{All: true, ProjectID: &q.ID}); len(got) != 2 {
		t.Fatalf("project beta %v", ids(got))
	}
	if got := list(db.TreeFilter{All: true, ProjectID: &p.ID}); !slices.Equal(ids(got), []int64{active}) {
		t.Fatalf("project alpha %v", ids(got))
	}
}

// Need writes serialize (BEGIN IMMEDIATE): racing opposite needs between two
// tasks get exactly one edge and a Conflict, never a loop, raw constraint
// error or SQLITE_BUSY.
func TestConcurrentNeedsSerialize(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "p-race").ID
	for round := range 5 {
		x := mustTask(t, s, ctx, p, fmt.Sprintf("x-%d", round), nil)
		y := mustTask(t, s, ctx, p, fmt.Sprintf("y-%d", round), nil)
		mustTree(t, s, ctx, fmt.Sprintf("race-%d", round), "", db.TreeMember{TaskID: x.ID}, db.TreeMember{TaskID: y.ID})
		errs := make(chan error, 2)
		for _, pair := range [][2]int64{{x.ID, y.ID}, {y.ID, x.ID}} {
			go func() {
				_, err := s.SetNeeds(ctx, pair[0], []int64{pair[1]})
				errs <- err
			}()
		}
		ok := 0
		for range 2 {
			err := <-errs
			var conflict *db.ConflictError
			switch {
			case err == nil:
				ok++
			case errors.As(err, &conflict):
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		gx, gy := getTask(t, s, ctx, x.ID), getTask(t, s, ctx, y.ID)
		if ok != 1 || len(gx.Needs)+len(gy.Needs) != 1 {
			t.Fatalf("round %d: %d succeeded, needs x=%v y=%v", round, ok, gx.Needs, gy.Needs)
		}
	}
}
