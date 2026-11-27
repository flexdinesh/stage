package api_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// noRename is T3 declaring sessionRename unavailable.
type noRename struct{ *t3.Adapter }

func (a noRename) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := a.Adapter.Features(ctx)
	if err == nil {
		c.Operations[execution.OpSessionRename] = execution.Availability{Reason: "No renames here."}
	}
	return c, err
}

func renameTitle(e *env, id int64, title string) task {
	e.t.Helper()
	var out task
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", id), map[string]any{"title": title}, 200, &out)
	if out.Title != title {
		e.t.Fatalf("patched title %q, want %q", out.Title, title)
	}
	e.engine.Coordinator.WaitRenames()
	return out
}

func sessionOf(e *env, id int64) execution.SessionRef {
	e.t.Helper()
	st, err := e.store.GetTask(e.t.Context(), id)
	if err != nil || st.Session == nil {
		e.t.Fatalf("task %d session: %v", id, err)
	}
	return *st.Session
}

func notes(e *env, id int64) []db.JournalEntry {
	e.t.Helper()
	page, err := e.store.ReadJournal(e.t.Context(), db.JournalOwner{TaskID: &id}, db.JournalQuery{})
	if err != nil {
		e.t.Fatal(err)
	}
	return page.Entries
}

func TestTitleEditRenamesLaunchedSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/rename")
	a := e.task(p.ID, "Old title", map[string]any{"start": true})
	ref := sessionOf(e, a.ID)

	renameTitle(e, a.ID, "New title")
	if len(e.fake.Renamed) != 1 || e.fake.Renamed[0] != (t3.FakeRename{ThreadID: ref.ExternalSessionID, Title: "New title"}) {
		t.Fatalf("renames %+v", e.fake.Renamed)
	}
	if d, err := e.fake.ReadThread(t.Context(), ref.ExternalSessionID); err != nil || d.Title != "New title" {
		t.Fatalf("thread title %q, %v", d.Title, err)
	}
	// An unchanged title, or a task without a session, renames nothing.
	renameTitle(e, a.ID, "New title")
	backlog := e.task(p.ID, "Backlog", nil)
	renameTitle(e, backlog.ID, "Backlog renamed")
	if len(e.fake.Renamed) != 1 {
		t.Fatalf("renames without a title change or session: %+v", e.fake.Renamed)
	}
	// Edits in a row run in order; the last one names the session.
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", a.ID), map[string]any{"title": "Second"}, 200, nil)
	renameTitle(e, a.ID, "Third")
	if last := e.fake.Renamed[len(e.fake.Renamed)-1]; last.Title != "Third" {
		t.Fatalf("last rename %+v", e.fake.Renamed)
	}
	if n := notes(e, a.ID); len(n) != 0 {
		t.Fatalf("successful renames wrote notes: %+v", n)
	}
}

func TestTitleEditKeepsAdoptedSessionName(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/adopted")
	s := launchSession(t, e, p.T3ProjectID, "Planner")
	var a agentTask
	e.must("POST", "/api/connections/1/sessions/"+s+"/adopt", map[string]any{}, 201, &a)
	if !a.Adopted {
		t.Fatalf("not adopted: %+v", a)
	}
	renameTitle(e, a.ID, "Tracked in Stage")
	if len(e.fake.Renamed) != 0 {
		t.Fatalf("adopted session renamed: %+v", e.fake.Renamed)
	}
	if d, err := e.fake.ReadThread(t.Context(), s); err != nil || d.Title != "Planner" {
		t.Fatalf("adopted thread title %q, %v", d.Title, err)
	}
}

func TestTitleEditSkipsUnavailableRename(t *testing.T) {
	e := newEnvWrapped(t, func(a *t3.Adapter) execution.Adapter { return noRename{a} })
	p := e.project("/w/unavailable")
	a := e.task(p.ID, "Old title", map[string]any{"start": true})
	renameTitle(e, a.ID, "New title")
	if len(e.fake.Renamed) != 0 {
		t.Fatalf("declared-unavailable rename ran: %+v", e.fake.Renamed)
	}
	if n := notes(e, a.ID); len(n) != 0 {
		t.Fatalf("unavailable rename wrote notes: %+v", n)
	}
}

func TestTitleEditRenameFailureIsNoted(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/failure")
	a := e.task(p.ID, "Old title", map[string]any{"start": true})
	ref := sessionOf(e, a.ID)
	e.fake.RenameErr = errors.New("thread is locked")
	renameTitle(e, a.ID, "New title")
	got := notes(e, a.ID)
	if len(got) != 1 {
		t.Fatalf("notes %+v", got)
	}
	n := got[0]
	if n.Text != "Could not rename the T3 session: thread is locked" || n.Actor.Kind != "system" ||
		n.Cause == nil || n.Cause.Kind != "session_rename" || n.Cause.Session == nil || *n.Cause.Session != ref {
		t.Fatalf("failure note %+v cause %+v", n, n.Cause)
	}
	if st := e.getTask(a.ID); st.Title != "New title" {
		t.Fatalf("a failed rename undid the title: %q", st.Title)
	}

	// The harness cannot even declare its operations: also a failure.
	e.fake.RenameErr = nil
	e.t3.failCatalog.Store(true)
	renameTitle(e, a.ID, "Newer title")
	got = notes(e, a.ID)
	if len(got) != 2 || got[0].Text != "Could not rename the T3 session: model catalog unavailable" {
		t.Fatalf("features failure notes %+v", got)
	}
	if len(e.fake.Renamed) != 0 {
		t.Fatalf("renamed without a declaration: %+v", e.fake.Renamed)
	}
}
