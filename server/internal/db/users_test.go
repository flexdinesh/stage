package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func TestActingUser(t *testing.T) {
	ctx := context.Background()
	if got := db.ActingUser(ctx); got != db.DefaultUser {
		t.Fatalf("no principal: %d, want default %d", got, db.DefaultUser)
	}
	if got := db.ActingUser(db.WithUser(ctx, 7)); got != 7 {
		t.Fatalf("principal: %d, want 7", got)
	}
}

func TestDefaultUserSeeded(t *testing.T) {
	s, ctx := setup(t)
	u, err := s.GetUser(ctx, db.DefaultUser)
	if err != nil || u.ID != 1 || u.DisplayName != "Default user" || u.CreatedAt.IsZero() {
		t.Fatalf("default user = %+v, %v", u, err)
	}
	if _, err := s.GetUser(ctx, 2); err == nil {
		t.Fatal("user 2 exists")
	}
}

// secondUser inserts user 2 and returns a context acting as it.
func secondUser(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO users(id,display_name) VALUES(2,'Second')`); err != nil {
		t.Fatal(err)
	}
	return db.WithUser(ctx, 2)
}

func TestWritesAttributeToActingUser(t *testing.T) {
	s, ctx := setup(t)
	// Without a principal every write is the default user's.
	p := mustProject(t, s, ctx, "default")
	task := mustTask(t, s, ctx, p.ID, "default", nil)
	if p.CreatedBy != db.DefaultUser || task.CreatedBy != db.DefaultUser {
		t.Fatalf("default: project %d task %d", p.CreatedBy, task.CreatedBy)
	}

	as2 := secondUser(t, ctx)
	p2 := mustProject(t, s, as2, "second")
	a := mustTask(t, s, as2, p2.ID, "a", nil)
	b := mustTask(t, s, as2, p2.ID, "b", nil)
	tree, err := s.CreateTree(as2, db.NewTree{Title: "tree", Tasks: []db.TreeMember{{TaskID: a.ID}, {TaskID: b.ID, Needs: []int64{a.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	sc := mustSchedule(t, s, as2, p2.ID, "*/5 * * * *")
	note, err := s.AppendNote(as2, db.JournalOwner{TaskID: &a.ID}, db.AppendNoteInput{Text: "n", RequestID: "k"})
	if err != nil {
		t.Fatal(err)
	}
	dup, err := s.DuplicateTask(as2, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]int64{
		"project": p2.CreatedBy, "task": a.CreatedBy, "tree": tree.CreatedBy,
		"schedule": sc.CreatedBy, "note": note.CreatedBy, "duplicate": dup.CreatedBy,
	} {
		if got != 2 {
			t.Errorf("%s createdBy %d, want 2", name, got)
		}
	}
	page, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &dup.ID}, db.JournalQuery{Kind: "events"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Entries {
		if e.EventType == "task_duplicated" {
			found = true
			if e.CreatedBy != 2 {
				t.Errorf("task_duplicated createdBy %d, want 2", e.CreatedBy)
			}
		}
	}
	if !found {
		t.Fatalf("no task_duplicated event in %+v", page.Entries)
	}
	// Reads return the stored creator whoever reads.
	if got, _ := s.GetTask(ctx, a.ID); got.CreatedBy != 2 {
		t.Fatalf("GetTask createdBy %d", got.CreatedBy)
	}
	if got, _ := s.GetMachine(ctx, db.LocalMachine); got.CreatedBy != db.DefaultUser {
		t.Fatalf("local machine createdBy %d", got.CreatedBy)
	}
}

func TestScheduledTaskInheritsScheduleCreator(t *testing.T) {
	s, ctx := setup(t)
	as2 := secondUser(t, ctx)
	p := mustProject(t, s, ctx, "proj")
	sc, err := s.CreateSchedule(as2, db.NewSchedule{
		ProjectID: p.ID, Title: "nightly", Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"},
		Cron: "*/5 * * * *", Tz: "UTC", Notes: "remember",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A cron fire with no principal, and Run now by the default user.
	fired := mustFire(t, s, ctx, sc.ID, sc.NextRunAt.Add(time.Second))
	if fired.TaskID == nil {
		t.Fatalf("fire = %+v", fired)
	}
	manual, err := s.RunScheduleNow(ctx, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{manual.ID, *fired.TaskID} {
		task, err := s.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if task.CreatedBy != 2 {
			t.Errorf("task %d createdBy %d, want schedule creator 2", id, task.CreatedBy)
		}
		page, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &id}, db.JournalQuery{Kind: "notes"})
		if err != nil || len(page.Entries) != 1 || page.Entries[0].CreatedBy != 2 {
			t.Errorf("task %d initial note = %+v, %v", id, page.Entries, err)
		}
	}
}
