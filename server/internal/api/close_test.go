package api_test

import (
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

type closeTask struct {
	ID         int64   `json:"id"`
	State      string  `json:"state"`
	ArchivedAt *string `json:"archivedAt"`
	TreeStatus *string `json:"treeStatus"`
	Deletable  bool    `json:"deletable"`
}

type closeTree struct {
	ID        int64   `json:"id"`
	Status    string  `json:"status"`
	ClosedAt  *string `json:"closedAt"`
	Deletable bool    `json:"deletable"`
}

func (e *env) closeTasks(query string) []int64 {
	e.t.Helper()
	var out struct {
		Tasks []closeTask `json:"tasks"`
	}
	e.must("GET", "/api/tasks"+query, nil, 200, &out)
	ids := []int64{}
	for _, x := range out.Tasks {
		ids = append(ids, x.ID)
	}
	slices.Sort(ids)
	return ids
}

func (e *env) closeTrees(query string) []int64 {
	e.t.Helper()
	var out struct {
		Trees []closeTree `json:"trees"`
	}
	e.must("GET", "/api/trees"+query, nil, 200, &out)
	ids := []int64{}
	for _, x := range out.Trees {
		ids = append(ids, x.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestTreeDoneArchiveAndReopenOnActivity(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/close")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}})
	solo := e.task(p.ID, "solo", nil)
	tree := *b.TreeID

	// Running member: done and archive are 409; bad status is 400.
	e.must("PATCH", treePath(tree), map[string]any{"status": "done"}, 409, nil)
	e.must("PATCH", treePath(tree), map[string]any{"status": "archived"}, 409, nil)
	e.must("PATCH", treePath(tree), map[string]any{"status": "closed"}, 400, nil)

	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCancelled)
	e.poll()
	var tr closeTree
	e.must("PATCH", treePath(tree), map[string]any{"status": "done"}, 200, &tr)
	if tr.Status != "done" || tr.ClosedAt == nil || tr.Deletable {
		t.Fatalf("done %+v", tr)
	}
	if ids := e.closeTasks(""); !slices.Equal(ids, []int64{solo.ID}) {
		t.Fatalf("open tasks %v", ids)
	}
	if ids := e.closeTasks("?closed=true"); !slices.Equal(ids, []int64{a.ID, b.ID}) {
		t.Fatalf("closed tasks %v", ids)
	}
	e.must("GET", "/api/tasks?closed=maybe", nil, 400, nil)
	if ids := e.closeTrees(""); len(ids) != 0 {
		t.Fatalf("active trees %v", ids)
	}
	if ids := e.closeTrees("?status=done"); !slices.Equal(ids, []int64{tree}) {
		t.Fatalf("done trees %v", ids)
	}
	if ids := e.closeTrees("?all=1"); !slices.Equal(ids, []int64{tree}) {
		t.Fatalf("all trees %v", ids)
	}
	e.must("GET", "/api/trees?status=nope", nil, 400, nil)
	e.must("POST", treePath(tree, "/start"), nil, 409, nil)
	e.must("POST", treePath(tree, "/tasks"), map[string]any{"taskId": solo.ID}, 409, nil)

	// Started members: delete is 409 (archive instead).
	e.must("DELETE", treePath(tree), nil, 409, nil)
	e.must("DELETE", taskPath(a.ID), nil, 409, nil)

	// Archive, then a T3 restart of a member reopens the tree.
	e.must("PATCH", treePath(tree), map[string]any{"status": "archived"}, 200, &tr)
	if ids := e.closeTrees("?status=archived"); !slices.Equal(ids, []int64{tree}) {
		t.Fatalf("archived trees %v", ids)
	}
	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusRunning)
	e.poll()
	e.must("GET", treePath(tree), nil, 200, &tr)
	if tr.Status != "open" || tr.ClosedAt != nil {
		t.Fatalf("activity did not reopen %+v", tr)
	}
	if got := e.getTask(a.ID); hasEvent(got, "tree reopened: became running") != 1 {
		t.Fatalf("reopen event %+v", got.Events)
	}
}

func TestTaskArchiveAndDelete(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/archive")
	fresh := e.task(p.ID, "fresh", nil)
	started := e.task(p.ID, "started", map[string]any{"start": true})
	member := e.task(p.ID, "member", map[string]any{"needs": []int64{fresh.ID}})

	var got closeTask
	e.must("GET", taskPath(fresh.ID), nil, 200, &got)
	if !got.Deletable {
		t.Fatalf("fresh %+v", got)
	}
	e.must("POST", taskPath(started.ID, "/archive"), nil, 409, nil) // running
	e.must("POST", taskPath(member.ID, "/archive"), nil, 409, nil)  // tree member
	e.must("POST", taskPath(99999, "/archive"), nil, 404, nil)

	_ = e.fake.SetStatus(*started.T3ThreadID, t3.StatusFailed)
	e.poll()
	e.must("DELETE", taskPath(started.ID), nil, 409, nil)
	e.must("POST", taskPath(started.ID, "/archive"), nil, 200, &got)
	if got.ArchivedAt == nil || got.Deletable {
		t.Fatalf("archived %+v", got)
	}
	if ids := e.closeTasks(""); slices.Contains(ids, started.ID) {
		t.Fatalf("archived task listed %v", ids)
	}
	if ids := e.closeTasks("?closed=all"); !slices.Contains(ids, started.ID) {
		t.Fatalf("closed=all %v", ids)
	}
	e.must("POST", taskPath(started.ID, "/unarchive"), nil, 200, &got)
	if got.ArchivedAt != nil {
		t.Fatalf("unarchived %+v", got)
	}

	// Unstarted tree: DELETE removes the tree and its tasks.
	x := e.task(p.ID, "x", nil)
	y := e.task(p.ID, "y", map[string]any{"needs": []int64{x.ID}})
	var tr closeTree
	e.must("GET", treePath(*y.TreeID), nil, 200, &tr)
	if !tr.Deletable {
		t.Fatalf("unstarted tree %+v", tr)
	}
	e.must("DELETE", treePath(*y.TreeID), nil, 204, nil)
	e.must("GET", treePath(*y.TreeID), nil, 404, nil)
	e.must("GET", taskPath(x.ID), nil, 404, nil)
	e.must("GET", taskPath(y.ID), nil, 404, nil)
	e.must("DELETE", taskPath(fresh.ID), nil, 204, nil)
}
