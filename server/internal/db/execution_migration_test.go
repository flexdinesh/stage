package db

import "testing"

func TestExecutionMigrationHoldsLegacyUnknownLaunches(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 8)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'Project','stage:test','', 'app')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,launch_attempt,launch_reuse,launch_request_id) VALUES
 (1,1,'timeout','p','backlog',3,1,'old-request'),
 (2,1,'restart','p','launching',2,0,'interrupted-request'),
 (3,1,'normal','p','backlog',0,0,NULL)`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(901,'note',1,'preserve history','note')`)
	migrateTo(t, pool, 12) // the store reads the latest schema
	store := NewStore(pool)
	for _, id := range []int64{1, 2} {
		task, err := store.GetTask(t.Context(), id)
		if err != nil || task.State != StateLaunching || task.AcceptanceID != nil {
			t.Fatal(task, err)
		}
		attempt, err := store.Attempt(t.Context(), id, task.LaunchAttempt)
		if err != nil || attempt.Phase != "unconfirmed" || attempt.Intent != nil || attempt.Checkpoint.Version != 0 {
			t.Fatal(attempt, err)
		}
	}
	normal, err := store.GetTask(t.Context(), 3)
	if err != nil || normal.State != StateBacklog {
		t.Fatal(normal, err)
	}
	note, err := store.GetJournalEntry(t.Context(), 901)
	if err != nil || note.Text != "preserve history" {
		t.Fatal(note, err)
	}
}
