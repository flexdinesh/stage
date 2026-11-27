package api_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

type resolved struct {
	Kind       string      `json:"kind"`
	TaskID     *int64      `json:"taskId"`
	TreeID     *int64      `json:"treeId"`
	ScheduleID *int64      `json:"scheduleId"`
	ProjectID  *int64      `json:"projectId"`
	EntryID    *int64      `json:"entryId"`
	Session    *sessionRef `json:"session"`
}

func isID(p *int64, want int64) bool { return p != nil && *p == want }

func (e *env) resolve(ref string, want int) resolved {
	e.t.Helper()
	var out resolved
	e.must("GET", "/api/refs/resolve?ref="+url.QueryEscape(ref), nil, want, &out)
	return out
}

func TestResolveRef(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")

	backlog := e.task(p.ID, "Backlog", nil)
	got := e.resolve(fmt.Sprintf("http://tailnet.example:7380/tasks/%d", backlog.ID), 200)
	if got.Kind != "task" || got.TaskID == nil || *got.TaskID != backlog.ID || got.Session != nil {
		t.Fatalf("backlog task %+v", got)
	}

	tk := e.task(p.ID, "Fix [login]", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	threadID := *started.ExternalSessionID
	md := fmt.Sprintf("[stage task #%d: Fix \\[login\\]](http://localhost:7380/tasks/%d)", tk.ID, tk.ID)
	for _, ref := range []string{md, fmt.Sprintf("#%d", tk.ID)} {
		got := e.resolve(ref, 200)
		if got.Kind != "task" || *got.TaskID != tk.ID || !e.isT3Session(got.Session, threadID) {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}
	// The task's session, by stage link and by the T3 web and app links
	// (resolved by the connection's adapter): all name the owning task.
	for _, ref := range []string{
		"http://127.0.0.1:7380/connections/1/sessions/" + url.PathEscape(threadID),
		publicURL + "/fake-env/" + url.PathEscape(threadID),
		"t3code://threads/fake-env/" + url.PathEscape(threadID),
	} {
		got := e.resolve(ref, 200)
		if got.Kind != "session" || !e.isT3Session(got.Session, threadID) || got.TaskID == nil || *got.TaskID != tk.ID {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}
	// A bare session id names no connection: without one it is refused.
	e.resolve(threadID, 400)
	e.resolve("http://127.0.0.1:7380/sessions/"+url.PathEscape(threadID), 400)
	var bare resolved
	e.must("GET", "/api/refs/resolve?connectionId=1&ref="+url.QueryEscape(threadID), nil, 200, &bare)
	if bare.TaskID == nil || *bare.TaskID != tk.ID {
		t.Fatalf("bare id with connection %+v", bare)
	}

	// A session started in T3 and not mirrored yet is read from T3.
	ext, err := e.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "outside"})
	if err != nil {
		t.Fatal(err)
	}
	got = e.resolve("http://localhost:7380/connections/1/sessions/"+url.PathEscape(ext.ThreadID), 200)
	if got.Kind != "session" || !e.isT3Session(got.Session, ext.ThreadID) || got.TaskID != nil {
		t.Fatalf("external %+v", got)
	}

	e.resolve("http://localhost:7380/tasks/9999", 404)
	e.resolve("http://localhost:7380/connections/1/sessions/nope", 404)
	e.resolve("https://github.com/o/r/pull/1", 400)
	e.resolve("", 400)

	// Offline: mirrored sessions still resolve; unknown ones are not found.
	e.t3.offline.Store(true)
	if got := e.resolve("/connections/1/sessions/"+url.PathEscape(threadID), 200); *got.TaskID != tk.ID {
		t.Fatalf("offline %+v", got)
	}
	e.resolve("/connections/1/sessions/never-seen", 404)
}

func TestResolveTreeRef(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	a := e.task(p.ID, "a", nil)
	var tr treeDetail
	e.must("POST", "/api/trees", map[string]any{"title": "Ship [auth]", "tasks": []any{map[string]any{"taskId": a.ID}}}, 201, &tr)
	for _, ref := range []string{
		fmt.Sprintf("https://phone.lan/trees/%d?task=%d", tr.ID, a.ID),
		fmt.Sprintf("[stage tree #%d: Ship \\[auth\\]](http://localhost:7380/trees/%d)", tr.ID, tr.ID),
		fmt.Sprintf("stage://trees/%d", tr.ID),
	} {
		got := e.resolve(ref, 200)
		if got.Kind != "tree" || got.TreeID == nil || *got.TreeID != tr.ID || got.TaskID != nil || got.Session != nil {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}
	// A connection selection leaves a tree link alone.
	var out resolved
	e.must("GET", fmt.Sprintf("/api/refs/resolve?connectionId=1&ref=%s", url.QueryEscape(fmt.Sprintf("http://localhost:7380/trees/%d", tr.ID))), nil, 200, &out)
	if out.Kind != "tree" || out.TreeID == nil || *out.TreeID != tr.ID {
		t.Fatalf("with connection %+v", out)
	}
	e.resolve("http://localhost:7380/trees/9999", 404)
}

func TestResolveScheduleRef(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	var sc struct {
		ID int64 `json:"id"`
	}
	e.must("POST", "/api/schedules", e.taskBody(p.ID, "nightly", map[string]any{"prompt": "go", "cron": "0 2 * * *", "tz": "UTC"}), 201, &sc)
	for _, ref := range []string{
		fmt.Sprintf("https://phone.lan/schedules/%d", sc.ID),
		fmt.Sprintf("[stage schedule #%d: nightly](http://localhost:7380/schedules/%d)", sc.ID, sc.ID),
		fmt.Sprintf("stage://schedules/%d", sc.ID),
	} {
		got := e.resolve(ref, 200)
		if got.Kind != "schedule" || !isID(got.ScheduleID, sc.ID) || got.TaskID != nil || got.Session != nil {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}
	// A connection selection leaves a schedule link alone.
	var out resolved
	e.must("GET", "/api/refs/resolve?connectionId=1&ref="+url.QueryEscape(fmt.Sprintf("/schedules/%d", sc.ID)), nil, 200, &out)
	if out.Kind != "schedule" || !isID(out.ScheduleID, sc.ID) {
		t.Fatalf("with connection %+v", out)
	}
	e.resolve("http://localhost:7380/schedules/9999", 404)
}

func TestResolveProjectRef(t *testing.T) {
	e := newEnv(t)
	into := e.project("/w/into")
	from := e.project("/w/from")
	got := e.resolve(fmt.Sprintf("[stage project #%d: into](http://localhost:7380/projects/%d)", into.ID, into.ID), 200)
	if got.Kind != "project" || !isID(got.ProjectID, into.ID) || got.TaskID != nil {
		t.Fatalf("project %+v", got)
	}
	// A merged project's link resolves to the project it was merged into.
	_, err := e.store.MergeProjects(t.Context(), into.ID, from.ID, true)
	mustValue(t, struct{}{}, err)
	for _, ref := range []string{fmt.Sprintf("https://phone.lan/projects/%d", from.ID), fmt.Sprintf("stage://projects/%d", from.ID)} {
		if got := e.resolve(ref, 200); got.Kind != "project" || !isID(got.ProjectID, into.ID) {
			t.Fatalf("alias %q -> %+v", ref, got)
		}
	}
	e.resolve("http://localhost:7380/projects/9999", 404)
}

func TestResolveJournalRef(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/app")
	a := e.task(p.ID, "a", nil)
	b := e.task(p.ID, "b", nil)
	var tr treeDetail
	e.must("POST", "/api/trees", map[string]any{"title": "Ship", "tasks": []any{map[string]any{"taskId": a.ID}}}, 201, &tr)
	taskNote := e.note(taskPath(a.ID, "/journal"), "found it", "finding", "ref-1")
	treeNote := e.note(treePath(tr.ID, "/journal"), "decided", "decision", "ref-2")

	for _, ref := range []string{
		fmt.Sprintf("https://phone.lan/tasks/%d?entry=%d#journal-%d", a.ID, taskNote.ID, taskNote.ID),
		fmt.Sprintf("[note](http://localhost:7380/tasks/%d?entry=%d)", a.ID, taskNote.ID),
		fmt.Sprintf("stage://tasks/%d?entry=%d", a.ID, taskNote.ID),
	} {
		got := e.resolve(ref, 200)
		if got.Kind != "journal" || !isID(got.EntryID, taskNote.ID) || !isID(got.TaskID, a.ID) || got.TreeID != nil || got.Session != nil {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}
	got := e.resolve(fmt.Sprintf("/trees/%d?view=notes&entry=%d", tr.ID, treeNote.ID), 200)
	if got.Kind != "journal" || !isID(got.EntryID, treeNote.ID) || !isID(got.TreeID, tr.ID) || got.TaskID != nil {
		t.Fatalf("tree entry %+v", got)
	}
	// Without entry the link stays a task link.
	if got := e.resolve(fmt.Sprintf("/tasks/%d?view=notes", a.ID), 200); got.Kind != "task" || got.EntryID != nil {
		t.Fatalf("task link %+v", got)
	}
	// The entry must belong to the task or tree the link names.
	e.resolve(fmt.Sprintf("/tasks/%d?entry=%d", b.ID, taskNote.ID), 404)
	e.resolve(fmt.Sprintf("/tasks/%d?entry=%d", a.ID, treeNote.ID), 404)
	e.resolve(fmt.Sprintf("/trees/%d?entry=%d", tr.ID, taskNote.ID), 404)
	e.resolve(fmt.Sprintf("/tasks/%d?entry=99999", a.ID), 404)
	e.resolve(fmt.Sprintf("/tasks/%d?entry=x", a.ID), 400)
}
