package api_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

type resolved struct {
	Kind     string  `json:"kind"`
	TaskID   *int64  `json:"taskId"`
	ThreadID *string `json:"threadId"`
}

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
	if got.Kind != "task" || got.TaskID == nil || *got.TaskID != backlog.ID || got.ThreadID != nil {
		t.Fatalf("backlog task %+v", got)
	}

	tk := e.task(p.ID, "Fix [login]", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	threadID := *started.T3ThreadID
	md := fmt.Sprintf("[stage task #%d: Fix \\[login\\]](http://localhost:7380/tasks/%d)", tk.ID, tk.ID)
	for _, ref := range []string{md, fmt.Sprintf("#%d", tk.ID)} {
		got := e.resolve(ref, 200)
		if got.Kind != "task" || *got.TaskID != tk.ID || got.ThreadID == nil || *got.ThreadID != threadID {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}
	// The task's session, by stage link, T3 web link, app link and bare id:
	// all name the owning task.
	for _, ref := range []string{
		"http://127.0.0.1:7380/sessions/" + url.PathEscape(threadID),
		publicURL + "/fake-env/" + url.PathEscape(threadID),
		"t3code://threads/fake-env/" + url.PathEscape(threadID),
		threadID,
	} {
		got := e.resolve(ref, 200)
		if got.Kind != "session" || *got.ThreadID != threadID || got.TaskID == nil || *got.TaskID != tk.ID {
			t.Fatalf("%q -> %+v", ref, got)
		}
	}

	// A session started in T3 and not mirrored yet is read from T3.
	ext, err := e.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "outside"})
	if err != nil {
		t.Fatal(err)
	}
	got = e.resolve("http://localhost:7380/sessions/"+url.PathEscape(ext.ThreadID), 200)
	if got.Kind != "session" || *got.ThreadID != ext.ThreadID || got.TaskID != nil {
		t.Fatalf("external %+v", got)
	}

	e.resolve("http://localhost:7380/tasks/9999", 404)
	e.resolve("http://localhost:7380/sessions/nope", 404)
	e.resolve("https://github.com/o/r/pull/1", 400)
	e.resolve("", 400)

	// Offline: mirrored sessions still resolve; unknown ones are not found.
	e.t3.offline.Store(true)
	if got := e.resolve(threadID, 200); *got.TaskID != tk.ID {
		t.Fatalf("offline %+v", got)
	}
	e.resolve("never-seen", 404)
}
