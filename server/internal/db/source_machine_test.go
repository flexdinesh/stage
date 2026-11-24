package db_test

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

// remoteMachine inserts machine 2 and returns a context forwarded by it.
func remoteMachine(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'laptop')`); err != nil {
		t.Fatal(err)
	}
	return db.WithMachine(ctx, 2)
}

func TestWritesRecordSourceMachine(t *testing.T) {
	s, ctx := setup(t)
	// Not through a gateway: no source machine.
	p := mustProject(t, s, ctx, "direct")
	direct := mustTask(t, s, ctx, p.ID, "direct", nil)
	if p.SourceMachineID != nil || direct.SourceMachineID != nil {
		t.Fatalf("direct: project %v task %v", p.SourceMachineID, direct.SourceMachineID)
	}

	via := remoteMachine(t, ctx)
	p2 := mustProject(t, s, via, "via")
	a := mustTask(t, s, via, p2.ID, "a", nil)
	b := mustTask(t, s, via, p2.ID, "b", nil)
	tree, err := s.CreateTree(via, db.NewTree{Title: "tree", Tasks: []db.TreeMember{{TaskID: a.ID}, {TaskID: b.ID, Needs: []int64{a.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	sc := mustSchedule(t, s, via, p2.ID, "*/5 * * * *")
	note, err := s.AppendNote(via, db.JournalOwner{TaskID: &a.ID}, db.AppendNoteInput{Text: "n", RequestID: "k"})
	if err != nil {
		t.Fatal(err)
	}
	dup, err := s.DuplicateTask(via, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*int64{
		"project": p2.SourceMachineID, "task": a.SourceMachineID, "tree": tree.SourceMachineID,
		"schedule": sc.SourceMachineID, "note": note.SourceMachineID, "duplicate": dup.SourceMachineID,
	} {
		if got == nil || *got != 2 {
			t.Errorf("%s sourceMachineId %v, want 2", name, got)
		}
	}
	// The user stays the acting user, next to the machine.
	if a.CreatedBy != db.DefaultUser || note.CreatedBy != db.DefaultUser {
		t.Fatalf("createdBy task %d note %d", a.CreatedBy, note.CreatedBy)
	}
	page, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &dup.ID}, db.JournalQuery{Kind: "events"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Entries {
		if e.EventType == "task_duplicated" {
			found = true
			if e.SourceMachineID == nil || *e.SourceMachineID != 2 {
				t.Errorf("task_duplicated sourceMachineId %v, want 2", e.SourceMachineID)
			}
		}
	}
	if !found {
		t.Fatalf("no task_duplicated event in %+v", page.Entries)
	}
	// Reads return the stored machine whoever reads.
	if got, _ := s.GetTask(ctx, a.ID); got.SourceMachineID == nil || *got.SourceMachineID != 2 {
		t.Fatalf("GetTask sourceMachineId %v", got.SourceMachineID)
	}
	// A schedule spawns its task, not the request that ran it.
	spawned, err := s.RunScheduleNow(via, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(ctx, spawned.ID); got.SourceMachineID != nil {
		t.Fatalf("spawned task sourceMachineId %d, want nil", *got.SourceMachineID)
	}
}
