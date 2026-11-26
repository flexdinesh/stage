package storetest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

func init() {
	register("MarkTreeDoneAndReopen", caseMarkTreeDoneAndReopen)
	register("ClosedTreeReleasesNothing", caseClosedTreeReleasesNothing)
	register("ActivityReopensTreeAndUnarchivesTask", caseActivityReopensTreeAndUnarchivesTask)
	register("ArchiveTaskRules", caseArchiveTaskRules)
	register("DeleteOnlyUnstarted", caseDeleteOnlyUnstarted)
}

// launch starts task id (attempt 1, a thread) and leaves it in end.
func launch(t *testing.T, s *db.Store, ctx context.Context, id int64, thread string, end db.TaskState) {
	t.Helper()
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateRunning, Session: &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: thread}}); err != nil {
		t.Fatal(err)
	}
	if end != db.StateRunning {
		setState(t, s, ctx, id, end)
	}
}

func wantConflict(t *testing.T, err error, contains string) {
	t.Helper()
	var conflict *db.ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), contains) {
		t.Fatalf("want conflict containing %q, got %v", contains, err)
	}
}

func listTasks(t *testing.T, s *db.Store, ctx context.Context, closed string) []int64 {
	t.Helper()
	ts, err := s.ListTasks(ctx, db.TaskFilter{Closed: closed})
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, x := range ts {
		ids = append(ids, x.ID)
	}
	slices.Sort(ids)
	return ids
}

func listTreeIDs(t *testing.T, s *db.Store, ctx context.Context, view db.TreeView) []int64 {
	t.Helper()
	trees, err := s.ListTrees(ctx, db.TreeFilter{View: view})
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, x := range trees {
		ids = append(ids, x.ID)
	}
	slices.Sort(ids)
	return ids
}

func lastEvent(t *testing.T, s *db.Store, ctx context.Context, id int64) string {
	t.Helper()
	evs, err := s.TaskEvents(ctx, id)
	if err != nil || len(evs) == 0 {
		t.Fatalf("events of %d: %v", id, err)
	}
	return evs[len(evs)-1].Detail
}

func caseMarkTreeDoneAndReopen(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	c := mustTask(t, s, ctx, p.ID, "c", []int64{b.ID})
	tree := *b.TreeID
	solo := mustTask(t, s, ctx, p.ID, "solo", nil)

	// A running member blocks done and archive (named in the error).
	launch(t, s, ctx, a.ID, "th-a", db.StateRunning)
	_, err := s.SetTreeStatus(ctx, tree, db.TreeDone)
	wantConflict(t, err, "#1 is running")
	_, err = s.SetTreeStatus(ctx, tree, db.TreeArchived)
	wantConflict(t, err, "#1 is running")

	// Cancelled a, unstarted b and c: done keeps member states.
	setState(t, s, ctx, a.ID, db.StateCancelled)
	got, err := s.SetTreeStatus(ctx, tree, db.TreeDone)
	if err != nil || got.Status != db.TreeDone || got.ClosedAt == nil {
		t.Fatalf("done %+v %v", got.Tree, err)
	}
	if st := getTask(t, s, ctx, b.ID); st.State != db.StateBacklog || st.TreeStatus == nil || *st.TreeStatus != db.TreeDone || readiness(st) != "-" {
		t.Fatalf("member of done tree %+v", st)
	}
	if got.Deletable {
		t.Fatal("tree with a launched member is not deletable")
	}

	// Members leave the default (open) list; closed and all list them.
	if ids := listTasks(t, s, ctx, ""); !slices.Equal(ids, []int64{solo.ID}) {
		t.Fatalf("open tasks %v", ids)
	}
	if ids := listTasks(t, s, ctx, "true"); !slices.Equal(ids, []int64{a.ID, b.ID, c.ID}) {
		t.Fatalf("closed tasks %v", ids)
	}
	if ids := listTasks(t, s, ctx, "all"); len(ids) != 4 {
		t.Fatalf("all tasks %v", ids)
	}
	if ids := listTreeIDs(t, s, ctx, db.TreeViewActive); len(ids) != 0 {
		t.Fatalf("active trees %v", ids)
	}
	if ids := listTreeIDs(t, s, ctx, db.TreeViewDone); !slices.Equal(ids, []int64{tree}) {
		t.Fatalf("done trees %v", ids)
	}

	// Structural changes need an open tree; rename does not.
	d := mustTask(t, s, ctx, p.ID, "d", nil)
	_, err = s.AddToTree(ctx, tree, d.ID, nil)
	wantConflict(t, err, "is done: reopen it first")
	_, err = s.SetNeeds(ctx, c.ID, []int64{a.ID})
	wantConflict(t, err, "is done")
	wantConflict(t, s.RemoveFromTree(ctx, tree, c.ID), "is done")
	_, err = s.StartableInTree(ctx, tree)
	wantConflict(t, err, "is done")
	_, err = s.UpdateTree(ctx, tree, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}})
	wantConflict(t, err, "is done")
	_, err = s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "e", Prompt: "p",
		Workspace: db.Workspace{Type: "root"}, TreeID: &tree})
	wantConflict(t, err, "is done")
	if _, err := s.UpdateTree(ctx, tree, db.TreePatch{Title: db.Optional[string]{Set: true, V: "renamed"}}); err != nil {
		t.Fatalf("rename done tree: %v", err)
	}

	// Archive from done, then reopen by hand.
	if got, err = s.SetTreeStatus(ctx, tree, db.TreeArchived); err != nil || got.Status != db.TreeArchived {
		t.Fatalf("archive %+v %v", got.Tree, err)
	}
	if ids := listTreeIDs(t, s, ctx, db.TreeViewArchived); !slices.Equal(ids, []int64{tree}) {
		t.Fatalf("archived trees %v", ids)
	}
	if got, err = s.SetTreeStatus(ctx, tree, db.TreeOpen); err != nil || got.Status != db.TreeOpen || got.ClosedAt != nil {
		t.Fatalf("reopen %+v %v", got.Tree, err)
	}
	if st := getTask(t, s, ctx, b.ID); readiness(st) != "blocked" {
		t.Fatalf("reopened member readiness %s", readiness(st))
	}
}

func caseClosedTreeReleasesNothing(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	launch(t, s, ctx, a.ID, "th-a", db.StateSettled)
	acceptTask(t, s, ctx, a.ID)
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartAuto}}); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.LaunchableTasks(ctx); err != nil || !slices.Equal(ids, []int64{b.ID}) {
		t.Fatalf("launchable while open %v %v", ids, err)
	}
	if _, err := s.SetTreeStatus(ctx, *b.TreeID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.LaunchableTasks(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("launchable in done tree %v %v", ids, err)
	}
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{}); err != nil {
		t.Fatal(err)
	}
	// Manual mode: no ready episode while closed.
	if _, err := s.SetTreeStatus(ctx, *b.TreeID, db.TreeOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTreeStatus(ctx, *b.TreeID, db.TreeArchived); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.MarkReady(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("ready in archived tree %v %v", ids, err)
	}
}

func caseActivityReopensTreeAndUnarchivesTask(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	launch(t, s, ctx, a.ID, "th-a", db.StateCancelled)
	if _, err := s.SetTreeStatus(ctx, *b.TreeID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	// Settling is not activity: the tree stays done.
	setState(t, s, ctx, a.ID, db.StateSettled)
	if st := getTask(t, s, ctx, b.ID); *st.TreeStatus != db.TreeDone {
		t.Fatalf("settle reopened %+v", st)
	}
	setState(t, s, ctx, a.ID, db.StateRunning)
	tree, err := s.GetTree(ctx, *b.TreeID)
	if err != nil || tree.Status != db.TreeDone || tree.ClosedAt == nil {
		t.Fatalf("activity reopened closed tree %+v %v", tree.Tree, err)
	}
	if ev := lastEvent(t, s, ctx, a.ID); ev == "tree reopened: became running" {
		t.Fatalf("event %q", ev)
	}

	solo := mustTask(t, s, ctx, p.ID, "solo", nil)
	launch(t, s, ctx, solo.ID, "th-solo", db.StateFailed)
	if got, err := s.ArchiveTask(ctx, solo.ID, true); err != nil || got.ArchivedAt == nil {
		t.Fatalf("archive %+v %v", got, err)
	}
	setState(t, s, ctx, solo.ID, db.StateNeedsInput)
	if got := getTask(t, s, ctx, solo.ID); got.ArchivedAt != nil {
		t.Fatalf("activity did not unarchive %+v", got)
	}
	if ev := lastEvent(t, s, ctx, solo.ID); ev != "unarchived: became needs input" {
		t.Fatalf("event %q", ev)
	}
}

func caseArchiveTaskRules(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	_, err := s.ArchiveTask(ctx, b.ID, true)
	wantConflict(t, err, "archive the tree, or remove it from the tree first")

	solo := mustTask(t, s, ctx, p.ID, "solo", nil)
	launch(t, s, ctx, solo.ID, "th-solo", db.StateRunning)
	_, err = s.ArchiveTask(ctx, solo.ID, true)
	wantConflict(t, err, "#3 is running")
	setState(t, s, ctx, solo.ID, db.StateDone)
	if _, err := s.ArchiveTask(ctx, solo.ID, true); err != nil {
		t.Fatal(err)
	}
	if ids := listTasks(t, s, ctx, ""); slices.Contains(ids, solo.ID) {
		t.Fatalf("archived task listed as open %v", ids)
	}
	if ids := listTasks(t, s, ctx, "true"); !slices.Equal(ids, []int64{solo.ID}) {
		t.Fatalf("closed %v", ids)
	}
	// An archived task cannot join a tree.
	_, err = s.AddToTree(ctx, *b.TreeID, solo.ID, nil)
	wantConflict(t, err, "is archived: unarchive it first")
	if got, err := s.ArchiveTask(ctx, solo.ID, false); err != nil || got.ArchivedAt != nil {
		t.Fatalf("unarchive %+v %v", got, err)
	}

	// An unstarted task can be archived too; its one-shot start waits.
	timed := mustTask(t, s, ctx, p.ID, "timed", nil)
	at := time.Now().Add(-time.Minute).UTC()
	tz := "UTC"
	if _, err := s.UpdateTask(ctx, timed.ID, db.TaskPatch{StartAt: db.Optional[time.Time]{Set: true, V: at},
		StartTz: db.Optional[string]{Set: true, V: tz}}); err != nil {
		t.Fatal(err)
	}
	due := func() []int64 {
		t.Helper()
		ds, err := s.DueOneShots(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		ids := []int64{}
		for _, d := range ds {
			ids = append(ids, d.ID)
		}
		return ids
	}
	if ids := due(); !slices.Equal(ids, []int64{timed.ID}) {
		t.Fatalf("due %v", ids)
	}
	if _, err := s.ArchiveTask(ctx, timed.ID, true); err != nil {
		t.Fatal(err)
	}
	if ids := due(); len(ids) != 0 {
		t.Fatalf("archived task due %v", ids)
	}
}

func caseDeleteOnlyUnstarted(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")

	fresh := mustTask(t, s, ctx, p.ID, "fresh", nil)
	if !fresh.Deletable {
		t.Fatal("fresh task is deletable")
	}
	failed := mustTask(t, s, ctx, p.ID, "failed", nil)
	launch(t, s, ctx, failed.ID, "th-f", db.StateFailed)
	if got := getTask(t, s, ctx, failed.ID); got.Deletable {
		t.Fatal("launched task is not deletable")
	}
	wantConflict(t, s.DeleteTask(ctx, failed.ID), "has recorded work: archive it instead")
	// Restart after a failed launch keeps it started (launch_attempt > 0, no thread).
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: failed.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	setState(t, s, ctx, failed.ID, db.StateFailed)
	wantConflict(t, s.DeleteTask(ctx, failed.ID), "has recorded work")
	if err := s.DeleteTask(ctx, fresh.ID); err != nil {
		t.Fatalf("delete fresh: %v", err)
	}

	// Trees: deletable only while every member is unstarted; delete removes the tasks.
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", []int64{a.ID})
	tree, err := s.GetTree(ctx, *b.TreeID)
	if err != nil || !tree.Deletable {
		t.Fatalf("unstarted tree deletable %+v %v", tree.Tree, err)
	}
	if err := s.DeleteTree(ctx, *b.TreeID); err != nil {
		t.Fatal(err)
	}
	if !treeGone(t, s, ctx, *b.TreeID) {
		t.Fatal("deleted tree exists")
	}
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := s.GetTask(ctx, id); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("member %d survived tree delete: %v", id, err)
		}
	}
	x := mustTask(t, s, ctx, p.ID, "x", nil)
	y := mustTask(t, s, ctx, p.ID, "y", []int64{x.ID})
	launch(t, s, ctx, x.ID, "th-x", db.StateSettled)
	wantConflict(t, s.DeleteTree(ctx, *y.TreeID), "has recorded work: archive it instead")
	if got, err := s.GetTree(ctx, *y.TreeID); err != nil || got.Deletable {
		t.Fatalf("started tree deletable %+v %v", got.Tree, err)
	}
}
