package api_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	gosync "sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

type treeDetail struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	StartMode string `json:"startMode"`
	Tasks     []task `json:"tasks"`
	Edges     []struct {
		TaskID int64 `json:"taskId"`
		NeedID int64 `json:"needId"`
	} `json:"edges"`
	Levels         [][]int64 `json:"levels"`
	StartedAt      *string   `json:"startedAt"`
	LastActivityAt string    `json:"lastActivityAt"`
	Events         []struct {
		TaskID  int64  `json:"taskId"`
		ToState string `json:"toState"`
	} `json:"events"`
}

func treePath(id int64, rest ...string) string {
	return fmt.Sprintf("/api/trees/%d", id) + strings.Join(rest, "")
}

func taskPath(id int64, rest ...string) string {
	return fmt.Sprintf("/api/tasks/%d", id) + strings.Join(rest, "")
}

func readinessOf(t task) string {
	if t.Readiness == nil {
		return ""
	}
	return *t.Readiness
}

func hasEvent(t task, detail string) int {
	n := 0
	for _, ev := range t.Events {
		if ev.Detail == detail {
			n++
		}
	}
	return n
}

func TestTreesLifecycle(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/trees")
	a := e.task(p.ID, "a", nil)
	b := e.task(p.ID, "b", nil)
	c := e.task(p.ID, "c", nil)

	e.must("POST", "/api/trees", map[string]any{"title": "T", "tasks": []any{}}, 400, nil)
	e.must("POST", "/api/trees", map[string]any{"title": " ", "tasks": []any{map[string]any{"taskId": a.ID}}}, 400, nil)
	e.must("POST", "/api/trees", map[string]any{"title": "T", "startMode": "later", "tasks": []any{map[string]any{"taskId": a.ID}}}, 400, nil)
	var tr treeDetail
	e.must("POST", "/api/trees", map[string]any{"title": " T ", "tasks": []any{
		map[string]any{"taskId": a.ID},
		map[string]any{"taskId": b.ID, "needs": []int64{a.ID}},
		map[string]any{"taskId": c.ID, "needs": []int64{a.ID, b.ID}},
	}}, 201, &tr)
	if tr.Title != "T" || tr.StartMode != "auto" || len(tr.Tasks) != 3 || len(tr.Edges) != 3 ||
		fmt.Sprint(tr.Levels) != fmt.Sprint([][]int64{{a.ID}, {b.ID}, {c.ID}}) {
		t.Fatalf("tree %+v", tr)
	}
	if tr.StartedAt != nil || tr.LastActivityAt == "" || len(tr.Events) != 3 || tr.Events[0].ToState != "backlog" {
		t.Fatalf("tree times/events %+v", tr)
	}

	var list struct {
		Trees []struct {
			ID     int64 `json:"id"`
			Counts struct {
				Total   int `json:"total"`
				Backlog int `json:"backlog"`
				Blocked int `json:"blocked"`
				Ready   int `json:"ready"`
			} `json:"counts"`
			Levels         int      `json:"levels"`
			Projects       []string `json:"projects"`
			LastActivityAt string   `json:"lastActivityAt"`
		} `json:"trees"`
	}
	e.must("GET", "/api/trees", nil, 200, &list)
	if len(list.Trees) != 1 || list.Trees[0].ID != tr.ID || list.Trees[0].Counts.Total != 3 || list.Trees[0].Counts.Backlog != 3 ||
		list.Trees[0].Counts.Ready != 1 || list.Trees[0].Counts.Blocked != 2 || list.Trees[0].Levels != 3 ||
		!slices.Equal(list.Trees[0].Projects, []string{p.Title}) || list.Trees[0].LastActivityAt != tr.LastActivityAt {
		t.Fatalf("list %+v", list)
	}
	e.must("GET", fmt.Sprintf("/api/trees?project=%d&all=1", p.ID+1), nil, 200, &list)
	if len(list.Trees) != 0 {
		t.Fatalf("other project %+v", list)
	}
	e.must("GET", "/api/trees?all=maybe", nil, 400, nil)

	// Tree start mode and per-task override (null = inherit).
	e.must("PATCH", treePath(tr.ID), map[string]any{"title": " "}, 400, nil)
	e.must("PATCH", treePath(tr.ID), map[string]any{"startMode": nil}, 400, nil)
	e.must("PATCH", treePath(tr.ID), map[string]any{"title": "T2", "startMode": "manual"}, 200, &tr)
	if tr.Title != "T2" || tr.StartMode != "manual" {
		t.Fatalf("patched %+v", tr)
	}
	got := e.getTask(c.ID)
	if got.Tree == nil || got.Tree.Title != "T2" || got.StartMode != nil || got.EffStartMode == nil || *got.EffStartMode != "manual" {
		t.Fatalf("c in manual tree %+v", got)
	}
	e.must("PATCH", taskPath(c.ID), map[string]any{"startMode": "soon"}, 400, nil)
	e.must("PATCH", taskPath(c.ID), map[string]any{"startMode": "auto"}, 200, &got)
	if got.StartMode == nil || *got.StartMode != "auto" || *got.EffStartMode != "auto" {
		t.Fatalf("override %+v", got)
	}
	e.must("PATCH", taskPath(c.ID), map[string]any{"startMode": nil}, 200, &got)
	if got.StartMode != nil || *got.EffStartMode != "manual" {
		t.Fatalf("inherit %+v", got)
	}

	// Needs: replace, cycle, missing, required.
	e.must("PUT", taskPath(c.ID, "/needs"), map[string]any{"needs": []int64{a.ID}}, 200, &got)
	if !slices.Equal(got.Needs, []int64{a.ID}) {
		t.Fatalf("set needs %+v", got)
	}
	e.must("PUT", taskPath(a.ID, "/needs"), map[string]any{"needs": []int64{c.ID}}, 409, nil)
	e.must("PUT", taskPath(c.ID, "/needs"), map[string]any{"needs": []int64{99999}}, 404, nil)
	e.must("PUT", taskPath(c.ID, "/needs"), map[string]any{}, 400, nil)

	// workspace.baseTaskId: only with base predecessor, and must be a need.
	ws := func(base string, baseTask int64) map[string]any {
		return map[string]any{"workspace": map[string]any{"type": "worktree", "baseRef": "origin/main", "base": base, "baseTaskId": baseTask}}
	}
	e.must("PATCH", taskPath(c.ID), ws("ref", a.ID), 400, nil)
	e.must("PATCH", taskPath(c.ID), ws("predecessor", b.ID), 400, nil)
	e.must("PATCH", taskPath(c.ID), ws("predecessor", a.ID), 200, &got)
	if got.Workspace["baseTaskId"] != float64(a.ID) {
		t.Fatalf("baseTaskId %+v", got.Workspace)
	}

	// Add d (needs c), remove c: d takes over c's needs; c leaves.
	d := e.task(p.ID, "d", nil)
	e.must("POST", treePath(tr.ID, "/tasks"), map[string]any{"needs": []int64{c.ID}}, 400, nil)
	e.must("POST", treePath(tr.ID, "/tasks"), map[string]any{"taskId": d.ID, "needs": []int64{c.ID}}, 200, &tr)
	if len(tr.Tasks) != 4 {
		t.Fatalf("added %+v", tr)
	}
	e.must("DELETE", treePath(tr.ID, fmt.Sprintf("/tasks/%d", c.ID)), nil, 204, nil)
	if got = e.getTask(d.ID); !slices.Equal(got.Needs, []int64{a.ID}) {
		t.Fatalf("splice %+v", got)
	}
	if got = e.getTask(c.ID); got.TreeID != nil || len(got.Needs) != 0 || got.Tree != nil || got.Workspace["baseTaskId"] != nil {
		t.Fatalf("removed %+v", got)
	}
	e.must("DELETE", treePath(tr.ID, fmt.Sprintf("/tasks/%d", c.ID)), nil, 404, nil)

	// Deleting a member splices too; dissolving the tree keeps its tasks.
	e.must("DELETE", taskPath(a.ID), nil, 204, nil)
	if got = e.getTask(d.ID); len(got.Needs) != 0 || got.TreeID == nil {
		t.Fatalf("after deleting a %+v", got)
	}
	e.must("POST", treePath(tr.ID, "/dissolve"), nil, 204, nil)
	e.must("GET", treePath(tr.ID), nil, 404, nil)
	e.must("POST", treePath(tr.ID, "/dissolve"), nil, 404, nil)
	if got = e.getTask(b.ID); got.TreeID != nil || got.Readiness != nil {
		t.Fatalf("dissolved %+v", got)
	}
}

func TestAutoFanInLaunchesWhenAllNeedsSettle(t *testing.T) {
	e := newEnv(t)
	p1 := e.project("/w/one")
	p2 := e.project("/w/two")
	a := e.task(p1.ID, "a", map[string]any{"start": true})
	b := e.task(p1.ID, "b", map[string]any{"start": true})
	c := e.task(p2.ID, "c", map[string]any{"needs": []int64{a.ID, b.ID}})
	if c.State != "backlog" || readinessOf(c) != "blocked" || c.TreeID == nil {
		t.Fatalf("c %+v", c)
	}
	if got := e.getTask(a.ID); !slices.Equal(got.Dependents, []int64{c.ID}) || got.TreeID == nil || *got.TreeID != *c.TreeID {
		t.Fatalf("a %+v", got)
	}

	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*a.T3ThreadID)
	_ = e.fake.SetStatus(*b.T3ThreadID, t3.StatusCompleted) // done, not settled
	e.poll()
	e.reconcile()
	if got := e.getTask(c.ID); got.State != "backlog" || readinessOf(got) != "blocked" {
		t.Fatalf("one need unsettled must hold: %+v", got)
	}

	_ = e.fake.Settle(*b.T3ThreadID)
	e.poll()
	e.reconcile()
	got := e.getTask(c.ID)
	if got.State != "running" || got.T3ThreadID == nil || !slices.Equal(got.Needs, []int64{a.ID, b.ID}) || got.Readiness != nil {
		t.Fatalf("after both settle c must run, keeping its needs: %+v", got)
	}
	d, _ := e.fake.ReadThread(context.Background(), *got.T3ThreadID)
	if d.ProjectID != p2.T3ProjectID {
		t.Fatalf("c launched in %s, want %s", d.ProjectID, p2.T3ProjectID)
	}
	// A root is never auto-launched; a settled member with a dependent is frozen.
	if got := e.getTask(a.ID); got.State != "settled" {
		t.Fatalf("a %s", got.State)
	}
}

func TestManualReadyNotifiesOnceAndStartTree(t *testing.T) {
	e := newEnv(t)
	var mu gosync.Mutex
	var ready []int64
	e.runner.OnReady = func(_ context.Context, id int64) {
		mu.Lock()
		defer mu.Unlock()
		ready = append(ready, id)
	}
	p := e.project("/w/manual")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}, "startMode": "manual"})
	c := e.task(p.ID, "c", map[string]any{"needs": []int64{a.ID}})
	e.must("PATCH", treePath(*b.TreeID), map[string]any{"startMode": "manual"}, 200, nil)
	if b.EffStartMode == nil || *b.EffStartMode != "manual" {
		t.Fatalf("b %+v", b)
	}

	e.reconcile()
	if len(ready) != 0 {
		t.Fatalf("blocked tasks are not ready: %v", ready)
	}
	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
	_ = e.fake.Settle(*a.T3ThreadID)
	e.poll()
	e.reconcile()
	e.reconcile()
	mu.Lock()
	gotReady := slices.Clone(ready)
	mu.Unlock()
	if !slices.Equal(gotReady, []int64{b.ID, c.ID}) {
		t.Fatalf("ready notified %v, want once each for %d %d", gotReady, b.ID, c.ID)
	}
	for _, id := range []int64{b.ID, c.ID} {
		got := e.getTask(id)
		if got.State != "backlog" || readinessOf(got) != "ready" || hasEvent(got, "ready: needs settled") != 1 {
			t.Fatalf("manual task %d: %+v", id, got)
		}
	}

	var res struct {
		Started []task `json:"started"`
		Errors  []struct {
			TaskID int64  `json:"taskId"`
			Error  string `json:"error"`
		} `json:"errors"`
	}
	e.must("POST", treePath(*b.TreeID, "/start"), nil, 200, &res)
	if len(res.Started) != 2 || res.Started[0].ID != b.ID || res.Started[1].ID != c.ID || len(res.Errors) != 0 ||
		res.Started[0].State != "running" {
		t.Fatalf("start tree %+v", res)
	}
	e.must("POST", treePath(*b.TreeID, "/start"), nil, 200, &res)
	if len(res.Started) != 0 || res.Errors == nil {
		t.Fatalf("nothing left to start %+v", res)
	}
	e.must("POST", treePath(99999, "/start"), nil, 404, nil)
}

func TestFailedNeedKeepsDependentBlocked(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/failed")
	for _, status := range []t3.ThreadStatus{t3.StatusFailed, t3.StatusInterrupted} {
		a := e.task(p.ID, "a", map[string]any{"start": true})
		b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}})
		_ = e.fake.SetStatus(*a.T3ThreadID, status)
		e.poll()
		e.reconcile()
		if got := e.getTask(b.ID); got.State != "backlog" || readinessOf(got) != "blocked" {
			t.Fatalf("dependent of %s need: %+v", status, got)
		}
	}
}

func TestStartEarlyKeepsNeeds(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/early")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", nil)
	c := e.task(p.ID, "c", map[string]any{"needs": []int64{a.ID, b.ID}})
	var got task
	e.must("POST", taskPath(c.ID, "/start"), nil, 200, &got)
	if got.State != "running" || !slices.Equal(got.Needs, []int64{a.ID, b.ID}) {
		t.Fatalf("start early %+v", got)
	}
	if got = e.getTask(c.ID); hasEvent(got, fmt.Sprintf("launch requested before #%d, #%d settled", a.ID, b.ID)) != 1 {
		t.Fatalf("events %+v", got.Events)
	}
	// Needs are frozen once it left backlog.
	e.must("PUT", taskPath(c.ID, "/needs"), map[string]any{"needs": []int64{a.ID}}, 409, nil)
}

func TestCreateWithNeedsResolvesTree(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/resolve")
	a := e.task(p.ID, "a", nil)
	b := e.task(p.ID, "b", nil)

	c := e.task(p.ID, "c", map[string]any{"needs": []int64{a.ID}})
	ta := e.getTask(c.ID).Tree
	if ta == nil || ta.Title != "a" || ta.StartMode != "auto" || len(ta.Tasks) != 2 {
		t.Fatalf("new tree after the first need %+v", ta)
	}
	d := e.task(p.ID, "d", map[string]any{"needs": []int64{b.ID}})
	if d.TreeID == nil || *d.TreeID == ta.ID {
		t.Fatalf("second tree %+v", d)
	}
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "needs": []int64{a.ID, b.ID}}, 409, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "treeId": ta.ID, "needs": []int64{b.ID}}, 409, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "treeId": 99999}, 404, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "needs": []int64{99999}}, 404, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "startMode": "soon"}, 400, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "needs": []int64{a.ID}, "start": true}, 400, nil)
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "needs": []int64{a.ID},
		"startLocal": "2999-01-01T10:00", "startTz": "UTC"}, 400, nil)

	f := e.task(p.ID, "f", map[string]any{"treeId": ta.ID, "startMode": "manual"})
	if f.TreeID == nil || *f.TreeID != ta.ID || len(f.Needs) != 0 || readinessOf(f) != "ready" ||
		f.StartMode == nil || *f.StartMode != "manual" {
		t.Fatalf("join tree as a root %+v", f)
	}
	// A failed create leaves nothing behind.
	var all struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks", nil, 200, &all)
	if len(all.Tasks) != 5 {
		t.Fatalf("tasks after rejected creates: %d", len(all.Tasks))
	}
}

func TestSchedulesRejectTreeFields(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/sched-trees")
	a := e.task(p.ID, "a", nil)
	base := map[string]any{"projectId": p.ID, "title": "n", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC"}
	for _, extra := range []map[string]any{
		{"treeId": 1}, {"needs": []int64{a.ID}}, {"needs": []int64{}}, {"startMode": "manual"}, {"needsSessionId": "s"},
		{"workspace": map[string]any{"type": "worktree", "baseRef": "origin/main", "base": "predecessor"}},
	} {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		for k, v := range extra {
			body[k] = v
		}
		if code := e.do("POST", "/api/schedules", body, nil); code != 400 {
			t.Fatalf("schedule with %v: status %d, want 400", extra, code)
		}
	}
}
