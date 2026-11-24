package db

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
)

// Migration 0019: external_projects gains repo_path (unknown for existing
// rows) and project_bindings is rebuilt to allow origin 'remote', keeping
// every row, id and the sequence.
func seedV0019RemoteMatch(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app'),(2,'Q','stage:q','','app')`)
	mustExec(t, pool, `INSERT INTO external_projects(connection_id,external_id,title,workspace,available,repo) VALUES(1,'e1','E','/w/p',1,'github.com/o/p')`)
	mustExec(t, pool, `INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,revision,active,status,observed_workspace,origin)
 VALUES(7,1,1,'e1','/w/p',3,1,'changed','/w/moved','path'),(9,2,2,'e2','/w/q',1,0,'missing',NULL,'imported')`)
	// A launch destination pins binding 7; it must still resolve after the rebuild.
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state) VALUES(1,1,'t','p','backlog')`)
	mustExec(t, pool, `INSERT INTO task_launch_destinations(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision) VALUES(1,1,1,'e1','/w/p',7,3)`)
}

func checkV0019RemoteMatch(t *testing.T, pool *sqlite.DB) {
	type row struct {
		id, project, connection, revision, active int64
		external, workspace, status, origin       string
		observed                                  *string
	}
	var got []row
	rows, err := pool.Query(`SELECT id,project_id,connection_id,revision,active,external_project_id,workspace,status,origin,observed_workspace FROM project_bindings ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.project, &r.connection, &r.revision, &r.active, &r.external, &r.workspace, &r.status, &r.origin, &r.observed); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 2 || got[0].id != 7 || got[0].revision != 3 || got[0].status != "changed" || got[0].origin != "path" || got[0].observed == nil || *got[0].observed != "/w/moved" ||
		got[1].id != 9 || got[1].active != 0 || got[1].origin != "imported" || got[1].observed != nil {
		t.Fatalf("bindings %+v", got)
	}
	var repo, repoPath string
	if err := pool.QueryRow(`SELECT repo,repo_path FROM external_projects WHERE external_id='e1'`).Scan(&repo, &repoPath); err != nil || repo != "github.com/o/p" || repoPath != "" {
		t.Fatalf("external project %q %q %v", repo, repoPath, err)
	}
	// New ids continue after the copied rows.
	var id int64
	if err := pool.QueryRow(`INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace,origin) VALUES(2,1,'e3','/w/r','remote') RETURNING id`).Scan(&id); err != nil || id != 10 {
		t.Fatalf("remote binding id %d %v", id, err)
	}
	if _, err := pool.Exec(`INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace,origin) VALUES(1,2,'e4','/w/s','guess')`); err == nil {
		t.Fatal("unknown origin accepted")
	}
	// The active-binding indexes survive the rebuild.
	if _, err := pool.Exec(`INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace) VALUES(1,1,'e5','/w/t')`); err == nil {
		t.Fatal("second active binding per project and connection accepted")
	}
	var violations, stale int
	if err := pool.QueryRow(`SELECT (SELECT count(*) FROM pragma_foreign_key_check),(SELECT count(*) FROM sqlite_master WHERE instr(sql,'project_bindings_new')>0)`).Scan(&violations, &stale); err != nil || violations != 0 || stale != 0 {
		t.Fatalf("foreign keys %d, stale references %d, %v", violations, stale, err)
	}
}
