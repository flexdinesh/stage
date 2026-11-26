package storetest

import (
	"context"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

func init() {
	register("GroupBatch", caseGroupBatch)
	register("CreatedBy", caseCreatedBy)
}

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
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection,
		ProjectID: projectID, Title: title, Prompt: "p",
		OriginSession: ptr(session(origin)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// needs sets id's needs (failing the test on error).
func needs(t *testing.T, s *db.Store, ctx context.Context, id int64, ids ...int64) db.Task {
	t.Helper()
	got, err := s.SetNeeds(ctx, id, ids)
	if err != nil {
		t.Fatalf("set needs of %d to %v: %v", id, ids, err)
	}
	return got
}

func caseGroupBatch(t *testing.T, open Factory) {
	inTree := func(id, tree int64, title string, mode db.StartMode) db.Task {
		return db.Task{ID: id, TreeID: &tree, TreeTitle: &title, TreeStartMode: &mode}
	}
	// Trees 7 (tasks 1, 3, 4) and 9 (tasks 5, 6); 2 and 8 independent.
	got := db.GroupBatch([]db.Task{
		inTree(1, 7, "Seven", db.StartAuto), {ID: 2}, inTree(3, 7, "Seven", db.StartAuto), inTree(4, 7, "Seven", db.StartAuto),
		inTree(5, 9, "Nine", db.StartManual), inTree(6, 9, "Nine", db.StartManual), {ID: 8},
	})
	if len(got.Trees) != 2 || got.Trees[0].Tree != (db.TreeRef{ID: 7, Title: "Seven", StartMode: db.StartAuto}) ||
		got.Trees[1].Tree != (db.TreeRef{ID: 9, Title: "Nine", StartMode: db.StartManual}) {
		t.Fatalf("trees %+v", got.Trees)
	}
	if !slices.Equal(idsOf(got.Trees[0].Tasks), []int64{1, 3, 4}) || !slices.Equal(idsOf(got.Trees[1].Tasks), []int64{5, 6}) {
		t.Fatalf("tree tasks %v %v", idsOf(got.Trees[0].Tasks), idsOf(got.Trees[1].Tasks))
	}
	if !slices.Equal(idsOf(got.Independent), []int64{2, 8}) {
		t.Fatalf("independent %v", idsOf(got.Independent))
	}
	if empty := db.GroupBatch(nil); empty.Trees == nil || empty.Independent == nil {
		t.Fatalf("empty batch must be non-nil slices: %+v", empty)
	}
}

func caseCreatedBy(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	a := originTask(t, s, ctx, p.ID, "a", "plan")
	b := originTask(t, s, ctx, p.ID, "b", "plan")
	c := originTask(t, s, ctx, p.ID, "c", "plan")
	mine := mustTask(t, s, ctx, p.ID, "owner", nil)
	_ = originTask(t, s, ctx, p.ID, "other", "elsewhere")
	needs(t, s, ctx, b.ID, a.ID)
	needs(t, s, ctx, mine.ID, b.ID) // owner task in the tree: not in the batch
	got, err := s.CreatedBySession(ctx, session("plan"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Trees) != 1 || !slices.Equal(idsOf(got.Trees[0].Tasks), []int64{a.ID, b.ID}) || !slices.Equal(idsOf(got.Independent), []int64{c.ID}) {
		t.Fatalf("batch %+v", got)
	}
	if tr := got.Trees[0].Tree; tr.Title != "a" || tr.StartMode != db.StartAuto {
		t.Fatalf("tree ref %+v", tr)
	}
	if deps := got.Trees[0].Tasks[1].Dependents; !slices.Equal(deps, []int64{mine.ID}) {
		t.Fatalf("outside dependent must still show: %v", deps)
	}
	none, err := s.CreatedBySession(ctx, session("nobody"))
	if err != nil || len(none.Trees)+len(none.Independent) != 0 {
		t.Fatalf("none %+v %v", none, err)
	}
}
