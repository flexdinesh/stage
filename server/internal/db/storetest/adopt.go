package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func init() {
	register("AdoptSession", caseAdoptSession)
	register("CreateTaskNeedsSession", caseCreateTaskNeedsSession)
}

func caseAdoptSession(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	now := time.Now().UTC().Truncate(time.Second)
	thread := func(id string, edit func(d *t3.ThreadDetail)) {
		t.Helper()
		d := t3.ThreadDetail{Thread: t3.Thread{ThreadID: id, ProjectID: "proj-1", Title: "Session " + id, Status: t3.StatusRunning,
			ProviderInstanceID: "codex", Model: "gpt", RuntimeMode: "auto", InteractionMode: "plan", CreatedAt: now, UpdatedAt: now}}
		if edit != nil {
			edit(&d)
		}
		if err := s.PutObservation(ctx, d.Observation()); err != nil {
			t.Fatal(err)
		}
	}
	thread("planner-1", func(d *t3.ThreadDetail) { d.Title = "Planner" })

	task, created, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("planner-1")})
	if err != nil || !created {
		t.Fatalf("adopt: %+v %v %v", task, created, err)
	}
	if !task.Adopted || task.Prompt != "" || task.Title != "Planner" || task.State != db.StateRunning || task.ProjectID != p.ID ||
		task.ExternalSessionID == nil || *task.ExternalSessionID != "planner-1" || task.LaunchAttempt != 1 || task.LaunchedAt == nil ||
		task.ExecutionConfig == nil || task.ExecutionConfig.Model != t3.ModelKey("codex", "gpt") || task.ExecutionConfig.Permission != "auto" || task.ExecutionConfig.Mode != "plan" ||
		task.TreeID != nil {
		t.Fatalf("adopted task %+v", task)
	}
	events, err := s.TaskEvents(ctx, task.ID)
	if err != nil || len(events) != 1 || events[0].Detail != "adopted session" || events[0].ToState != db.StateRunning {
		t.Fatalf("events %+v %v", events, err)
	}
	if owner, err := s.TaskBySession(ctx, session("planner-1")); err != nil || owner.ID != task.ID {
		t.Fatalf("session now owned: %+v %v", owner, err)
	}
	// Adopting again returns the owner.
	again, created, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("planner-1"), Title: ptr("ignored")})
	if err != nil || created || again.ID != task.ID || again.Title != "Planner" {
		t.Fatalf("adopt again: %+v %v %v", again, created, err)
	}

	// Title and notes override; a closed session reads done with a finish timestamp.
	thread("done-1", func(d *t3.ThreadDetail) { d.Settled, d.Status = true, t3.StatusIdle })
	done, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("done-1"), Title: ptr(" Review "), InitialNote: ptr("n")})
	if err != nil || done.Title != "Review" || done.State != db.StateDone ||
		done.FinishedAt == nil {
		t.Fatalf("done adoption %+v %v", done, err)
	}
	notes := journal(t, s, ctx, db.JournalOwner{TaskID: &done.ID}, db.JournalQuery{})
	if len(notes.Entries) != 1 || notes.Entries[0].Text != "n" || notes.Entries[0].SourceSession != nil {
		t.Fatalf("adoption initial note: %+v", notes)
	}
	// A blank title falls back to "Session <id8>"; a joined tree is kept.
	thread("abcdefghij", func(d *t3.ThreadDetail) { d.Title = "  " })
	tree := mustTree(t, s, ctx, "T", "", db.TreeMember{TaskID: done.ID})
	untitled, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("abcdefghij"), TreeID: &tree.ID})
	if err != nil || untitled.Title != "Session abcdefgh" || untitled.TreeID == nil || *untitled.TreeID != tree.ID {
		t.Fatalf("untitled adopt %+v %v", untitled, err)
	}

	var conflict *db.ConflictError
	if _, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("nope")}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	thread("sub-1", func(d *t3.ThreadDetail) { d.ParentThreadID, d.RelationshipToParent = "planner-1", "subagent" })
	if _, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("sub-1")}); !errors.As(err, &conflict) {
		t.Fatalf("subagent: %v", err)
	}
	thread("arch-1", func(d *t3.ThreadDetail) { d.Archived = true })
	if _, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("arch-1")}); !errors.As(err, &conflict) {
		t.Fatalf("archived: %v", err)
	}
	thread("foreign-1", func(d *t3.ThreadDetail) { d.ProjectID = "not-in-stage" })
	if _, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("foreign-1")}); !errors.As(err, &conflict) {
		t.Fatalf("no stage project: %v", err)
	}
	if _, _, err := s.AdoptSession(ctx, db.AdoptInput{Session: session("sub-1"), TreeID: ptr[int64](9999)}); !errors.As(err, &conflict) {
		t.Fatalf("refusal comes before the tree check: %v", err)
	}

	// An adopted task cannot be duplicated, nor its prompt edited.
	if _, err := s.DuplicateTask(ctx, task.ID); !errors.As(err, &conflict) {
		t.Fatalf("duplicate adopted: %v", err)
	}
	setState(t, s, ctx, task.ID, db.StateBacklog) // not reachable in practice; isolates the prompt rule
	if _, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{Prompt: db.Optional[string]{Set: true, V: "x"}}); !errors.As(err, &conflict) {
		t.Fatalf("edit adopted prompt: %v", err)
	}
}

func caseCreateTaskNeedsSession(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	now := time.Now().UTC()
	detail := t3.ThreadDetail{Thread: t3.Thread{ThreadID: "caller", ProjectID: "proj-1", Title: "Caller",
		Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now}}
	if err := s.PutObservation(ctx, detail.Observation()); err != nil {
		t.Fatal(err)
	}
	other := mustTask(t, s, ctx, p.ID, "other", nil)
	b, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "follow-up", Prompt: "p",
		NeedsSession: ptr(session("caller")), Needs: []int64{other.ID}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.TaskBySession(ctx, session("caller"))
	if err != nil || !owner.Adopted {
		t.Fatalf("caller not adopted: %+v %v", owner, err)
	}
	if !slices.Equal(b.Needs, []int64{other.ID, owner.ID}) || b.TreeTitle == nil || *b.TreeTitle != "Caller" || readiness(b) != "blocked" {
		t.Fatalf("needs caller %+v", b)
	}
	// A second task needing the same session reuses its task.
	c, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "second", Prompt: "p",
		NeedsSession: ptr(session("caller"))})
	if err != nil || !slices.Equal(c.Needs, []int64{owner.ID}) || *c.TreeID != *b.TreeID {
		t.Fatalf("second %+v %v", c, err)
	}
	var invalid *db.InvalidError
	at := now.Add(time.Hour)
	if _, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "x", Prompt: "p",
		NeedsSession: ptr(session("caller")), StartAt: &at, StartTz: ptr("UTC")}); !errors.As(err, &invalid) {
		t.Fatalf("start time with a session need: %v", err)
	}
}
