package db_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestAdoptThread(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	now := time.Now().UTC().Truncate(time.Second)
	thread := func(id string, edit func(d *t3.ThreadDetail)) {
		t.Helper()
		d := t3.ThreadDetail{Thread: t3.Thread{ThreadID: id, ProjectID: "proj-1", Title: "Session " + id, Status: t3.StatusRunning,
			ProviderInstanceID: "codex", Model: "gpt", RuntimeMode: "auto", InteractionMode: "plan", CreatedAt: now, UpdatedAt: now}}
		if edit != nil {
			edit(&d)
		}
		if err := s.UpsertThreadDetail(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	thread("planner-1", func(d *t3.ThreadDetail) { d.Title = "Planner" })

	task, created, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "planner-1", Tags: []string{"plan"}})
	if err != nil || !created {
		t.Fatalf("adopt: %+v %v %v", task, created, err)
	}
	if !task.Adopted || task.Prompt != "" || task.Title != "Planner" || task.State != db.StateRunning || task.ProjectID != p.ID ||
		task.T3ThreadID == nil || *task.T3ThreadID != "planner-1" || task.LaunchAttempt != 1 || task.LaunchedAt == nil ||
		task.ExecutionConfig == nil || task.ExecutionConfig.Model != t3.ModelKey("codex", "gpt") || task.RuntimeMode != "auto" || task.InteractionMode != "plan" ||
		!slices.Equal(task.Tags, []string{"plan"}) || task.TreeID != nil {
		t.Fatalf("adopted task %+v", task)
	}
	events, err := s.TaskEvents(ctx, task.ID)
	if err != nil || len(events) != 1 || events[0].Detail != "adopted session" || events[0].ToState != db.StateRunning {
		t.Fatalf("events %+v %v", events, err)
	}
	if th, err := s.GetThread(ctx, "planner-1"); err != nil || th.TaskID == nil || *th.TaskID != task.ID || th.Source != db.SourceApp {
		t.Fatalf("session now owned: %+v %v", th, err)
	}
	// Adopting again returns the owner.
	again, created, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "planner-1", Title: ptr("ignored")})
	if err != nil || created || again.ID != task.ID || again.Title != "Planner" {
		t.Fatalf("adopt again: %+v %v %v", again, created, err)
	}

	// Title and notes override; a settled session reads settled with timestamps.
	thread("done-1", func(d *t3.ThreadDetail) { d.Settled, d.Status = true, t3.StatusIdle })
	settled, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "done-1", Title: ptr(" Review "), InitialNote: ptr("n")})
	if err != nil || settled.Title != "Review" || settled.State != db.StateDone ||
		settled.SettledAt != nil || settled.FinishedAt == nil {
		t.Fatalf("settled adopt %+v %v", settled, err)
	}
	notes := journal(t, s, ctx, db.JournalOwner{TaskID: &settled.ID}, db.JournalQuery{})
	if len(notes.Entries) != 1 || notes.Entries[0].Text != "n" || notes.Entries[0].SourceThreadID == nil || *notes.Entries[0].SourceThreadID != "done-1" {
		t.Fatalf("adoption initial note: %+v", notes)
	}
	// A blank title falls back to "Session <id8>"; a joined tree is kept.
	thread("abcdefghij", func(d *t3.ThreadDetail) { d.Title = "  " })
	tree := mustTree(t, s, ctx, "T", "", db.TreeMember{TaskID: settled.ID})
	untitled, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "abcdefghij", TreeID: &tree.ID})
	if err != nil || untitled.Title != "Session abcdefgh" || untitled.TreeID == nil || *untitled.TreeID != tree.ID {
		t.Fatalf("untitled adopt %+v %v", untitled, err)
	}

	var conflict *db.ConflictError
	if _, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "nope"}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	thread("sub-1", func(d *t3.ThreadDetail) { d.ParentThreadID, d.RelationshipToParent = "planner-1", "subagent" })
	if _, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "sub-1"}); !errors.As(err, &conflict) {
		t.Fatalf("subagent: %v", err)
	}
	thread("arch-1", func(d *t3.ThreadDetail) { d.Archived = true })
	if _, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "arch-1"}); !errors.As(err, &conflict) {
		t.Fatalf("archived: %v", err)
	}
	thread("foreign-1", func(d *t3.ThreadDetail) { d.ProjectID = "not-in-stage" })
	if _, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "foreign-1"}); !errors.As(err, &conflict) {
		t.Fatalf("no stage project: %v", err)
	}
	if _, _, err := s.AdoptThread(ctx, db.AdoptInput{ThreadID: "sub-1", TreeID: ptr[int64](9999)}); !errors.As(err, &conflict) {
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

func TestCreateTaskNeedsSession(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	now := time.Now().UTC()
	if err := s.UpsertThreadDetail(ctx, t3.ThreadDetail{Thread: t3.Thread{ThreadID: "caller", ProjectID: "proj-1", Title: "Caller",
		Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	other := mustTask(t, s, ctx, p.ID, "other", nil)
	b, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "follow-up", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, NeedsThreadID: ptr("caller"), Needs: []int64{other.ID}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.GetThread(ctx, "caller")
	if err != nil || owner.TaskID == nil {
		t.Fatalf("caller not adopted: %+v %v", owner, err)
	}
	if !slices.Equal(b.Needs, []int64{other.ID, *owner.TaskID}) || b.TreeTitle == nil || *b.TreeTitle != "Caller" || readiness(b) != "blocked" {
		t.Fatalf("needs caller %+v", b)
	}
	// A second task needing the same session reuses its task.
	c, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "second", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, NeedsThreadID: ptr("caller")})
	if err != nil || !slices.Equal(c.Needs, []int64{*owner.TaskID}) || *c.TreeID != *b.TreeID {
		t.Fatalf("second %+v %v", c, err)
	}
	var invalid *db.InvalidError
	at := now.Add(time.Hour)
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "x", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, NeedsThreadID: ptr("caller"), StartAt: &at, StartTz: ptr("UTC")}); !errors.As(err, &invalid) {
		t.Fatalf("start time with a session need: %v", err)
	}
}
