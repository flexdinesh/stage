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
	migrateTo(t, pool, 9)
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

func TestAutoCompleteMigrationKeepsAcceptances(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 10)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,1,'thread')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,session_id,launch_attempt) VALUES(1,1,'source','p','done',5,0),(2,1,'dependent','p','running',NULL,1)`)
	mustExec(t, pool, `INSERT INTO task_acceptances(id,task_id,provenance,evidence) VALUES(7,1,'user','{}')`)
	mustExec(t, pool, `UPDATE tasks SET acceptance_id=7 WHERE id=1`)
	mustExec(t, pool, `INSERT INTO task_launch_claims VALUES(2,1)`)
	mustExec(t, pool, `INSERT INTO task_launch_inputs VALUES(2,1,1,7)`)
	migrateTo(t, pool, 11)
	s := NewStore(pool)
	ctx := context.Background()
	cs, err := s.Connections(ctx)
	if err != nil || len(cs) != 2 || !cs[0].AutoComplete || !cs[1].AutoComplete {
		t.Fatalf("connections %+v %v", cs, err)
	}
	inputs, err := s.AcceptedLaunchInputs(ctx, 2, 1)
	if err != nil || len(inputs) != 1 || inputs[0].ID != 7 || inputs[0].Evidence.Session == nil || inputs[0].Evidence.Session.ExternalSessionID != "thread" {
		t.Fatalf("launch inputs %+v %v", inputs, err)
	}
	mustExec(t, pool, `INSERT INTO task_acceptances(task_id,provenance,evidence) VALUES(1,'harness_archived','{}')`)
	var id int64
	var session *int64
	if err = pool.QueryRowContext(ctx, `SELECT id,source_session_id FROM task_acceptances WHERE provenance='harness_archived'`).Scan(&id, &session); err != nil || id != 8 || session == nil || *session != 5 {
		t.Fatalf("new acceptance %d %v %v", id, session, err)
	}
	if _, err = pool.ExecContext(ctx, `INSERT INTO task_acceptances(task_id,provenance,evidence) VALUES(1,'harness','{}')`); err == nil {
		t.Fatal("unknown provenance accepted")
	}
	var violations, renamed int
	if err = pool.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM pragma_foreign_key_check),(SELECT count(*) FROM sqlite_master WHERE instr(sql,'task_acceptances_new')>0)`).Scan(&violations, &renamed); err != nil || violations != 0 || renamed != 0 {
		t.Fatalf("foreign keys %d, stale references %d, %v", violations, renamed, err)
	}
}
