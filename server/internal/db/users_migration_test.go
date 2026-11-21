package db

import "testing"

// Migration 0017: users seeded with the default user, empty identities, and
// existing rows of every attributed table backfilled to the default user.
func TestUsersMigration(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 16)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(1,'tree')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tree_id) VALUES(1,1,'t','p','backlog',1)`)
	mustExec(t, pool, `INSERT INTO schedules(id,project_id,title,prompt,cron,tz) VALUES(1,1,'s','p','* * * * *','UTC')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(901,'note',1,'old','k')`)
	migrateTo(t, pool, 17)

	var name string
	if err := pool.QueryRow(`SELECT display_name FROM users WHERE id=1`).Scan(&name); err != nil || name != "Default user" {
		t.Fatalf("default user %q, %v", name, err)
	}
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM identities`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("identities %d, %v", n, err)
	}
	for _, table := range []string{"projects", "task_trees", "tasks", "schedules", "journal_entries", "machines"} {
		var bad int
		if err := pool.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE created_by_user_id IS NOT 1`).Scan(&bad); err != nil || bad != 0 {
			t.Fatalf("%s: %d rows not attributed to the default user, %v", table, bad, err)
		}
		if err := pool.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n == 0 {
			t.Fatalf("%s: no rows to check, %v", table, err)
		}
	}
	rows, err := pool.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violations after 0017")
	}
	rows.Close()

	mustExec(t, pool, `INSERT INTO identities(user_id,provider,subject) VALUES(1,'github','42')`)
	if _, err := pool.Exec(`INSERT INTO identities(user_id,provider,subject) VALUES(1,'github','42')`); err == nil {
		t.Fatal("duplicate (provider, subject) accepted")
	}
	if _, err := pool.Exec(`INSERT INTO identities(user_id,provider,subject) VALUES(9,'github','43')`); err == nil {
		t.Fatal("identity for an unknown user accepted")
	}
	if _, err := pool.Exec(`INSERT INTO tasks(project_id,title,prompt,state,created_by_user_id) VALUES(1,'x','p','backlog',9)`); err == nil {
		t.Fatal("task created by an unknown user accepted")
	}
}
