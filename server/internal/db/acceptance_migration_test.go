package db

import (
	"context"
	"testing"
)

func TestAcceptanceMigrationPreservesHistoryAndPolicies(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 6)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title,start_mode) VALUES(1,'auto','auto'),(2,'manual','manual')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,settled_at,tree_id) VALUES
 (1,1,'settled','p','settled','2026-11-01T00:00:00.000000Z',1),
 (2,1,'active again','p','running','2026-11-01T00:00:00.000000Z',2),
 (3,1,'done','p','done',NULL,1)`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(900,'note',1,'keep this','original')`)
	migrateTo(t, pool, 8)
	s := NewStore(pool)
	ctx := context.Background()
	a, err := s.GetTask(ctx, 1)
	if err != nil || a.AcceptanceID == nil || a.CompletedAt == nil || a.State != StateSettled {
		t.Fatalf("migration: %+v %v", a, err)
	}
	for _, id := range []int64{2, 3} {
		task, err := s.GetTask(ctx, id)
		if err != nil || task.AcceptanceID != nil {
			t.Fatalf("accepted nonsettled task: %+v %v", task, err)
		}
	}
	snapshots, err := s.Acceptances(ctx, 1)
	if err != nil || len(snapshots) != 1 || snapshots[0].Provenance != "historical_settlement" || snapshots[0].Evidence.Reply != nil || snapshots[0].Evidence.JournalThroughID != 900 {
		t.Fatalf("snapshot: %+v %v", snapshots, err)
	}
	note, err := s.GetJournalEntry(ctx, 900)
	if err != nil || note.Text != "keep this" {
		t.Fatal("journal changed")
	}
	for id, mode := range map[int64]StartMode{1: StartAuto, 2: StartManual} {
		tree, err := s.GetTree(ctx, id)
		if err != nil || tree.StartMode != mode {
			t.Fatal("policy changed")
		}
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	snapshots, err = s.Acceptances(ctx, 1)
	if err != nil || len(snapshots) != 1 {
		t.Fatal("migration duplicated acceptance")
	}
}
