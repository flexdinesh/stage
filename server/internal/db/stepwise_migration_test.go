package db

import (
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
)

// migrationStep is one upgrade scenario for a migration: seed runs on a
// database at the previous version (nil: nothing to seed), check right after
// the migration, on that version's schema.
type migrationStep struct {
	name        string
	seed, check func(t *testing.T, pool *sqlite.DB)
}

// stepwiseMigrations holds every SQLite migration's upgrade scenarios. A new
// migration adds its entry (docs/storage.md "Adding a migration"); a version
// without one fails TestStepwiseMigrations.
var stepwiseMigrations = map[int64][]migrationStep{
	1:  {{"seeds singletons", nil, checkV0001Init}},
	2:  {{"drops continuations", seedV0002Continuations, checkV0002Continuations}},
	3:  {{"trees from links and sessions", seedV0003Trees, checkV0003Trees}},
	4:  {{"close columns", seedV0004Close, checkV0004Close}},
	5:  {{"journal history markers", seedV0005Journal, checkV0005Journal}},
	6:  {{"t3 settings unset", seedV0006T3Settings, checkV0006T3Settings}},
	7:  {{"historical acceptances", seedV0007Acceptance, checkV0007Acceptance}},
	8:  {{"identities and bindings", seedV0008Identities, checkV0008Identities}},
	9:  {{"legacy unknown launches held", seedV0009Execution, checkV0009Execution}},
	10: {{"opencode settings and shared paths", seedV0010Connections, checkV0010Connections}},
	11: {{"auto complete keeps acceptances", seedV0011AutoComplete, checkV0011AutoComplete}},
	12: {{"connection identity", seedV0012ConnectionIdentity, checkV0012ConnectionIdentity}},
	13: {
		{"password means basic auth", seedV0013OpenCodeAuth("pw"), checkV0013OpenCodeAuth("basic")},
		{"no password stays none", seedV0013OpenCodeAuth(""), checkV0013OpenCodeAuth("none")},
	},
	14: {{"project import columns", seedV0014ProjectImport, checkV0014ProjectImport}},
	15: {{"machines keep connections", seedV0015Machines, checkV0015Machines}},
	16: {{"drops tags", seedV0016DropTags, checkV0016DropTags}},
	17: {{"users backfill", seedV0017Users, checkV0017Users}},
	18: {{"machine settings in place", seedV0018MachineSettings, checkV0018MachineSettings}},
	19: {{"remote match", seedV0019RemoteMatch, checkV0019RemoteMatch}},
	20: {{"machine auth", seedV0020MachineAuth, checkV0020MachineAuth}},
	21: {{"source machine", seedV0021SourceMachine, checkV0021SourceMachine}},
	22: {
		{"unsets the seeded label", seedV0022LocalMachineLabel("This computer"), checkV0022LocalMachineLabel("")},
		{"keeps a chosen label", seedV0022LocalMachineLabel("desk"), checkV0022LocalMachineLabel("desk")},
	},
	23: {{"t3 browser admin", seedV0023T3BrowserAdmin, checkV0023T3BrowserAdmin}},
	24: connectionDefaultsSteps(),
	25: {{"launch observed session", seedV0025ObservedSession, checkV0025ObservedSession}},
	26: {{"journal events in go", seedV0026JournalEventsInGo, checkV0026JournalEventsInGo}},
}

// TestStepwiseMigrations is the upgrade coverage (no releases exist yet):
// for every SQLite version v, a fresh database at the previous version,
// seeded with representative rows, migrated to v and checked.
func TestStepwiseMigrations(t *testing.T) {
	versions := engineVersions(t, sqliteMigrationsDir)
	for _, v := range slices.Sorted(maps.Keys(stepwiseMigrations)) {
		if !slices.Contains(versions, v) {
			t.Errorf("step-wise entry for unknown migration %d", v)
		}
	}
	for i, v := range versions {
		steps := stepwiseMigrations[v]
		if len(steps) == 0 {
			t.Errorf("migration %d has no step-wise entry: add one to stepwiseMigrations (docs/storage.md \"Adding a migration\")", v)
			continue
		}
		var prev int64
		if i > 0 {
			prev = versions[i-1]
		}
		for _, st := range steps {
			t.Run(fmt.Sprintf("%04d/%s", v, st.name), func(t *testing.T) {
				t.Parallel()
				pool := openUnmigrated(t)
				if prev > 0 {
					migrateTo(t, pool, prev)
				}
				if st.seed != nil {
					st.seed(t, pool)
				}
				migrateTo(t, pool, v)
				st.check(t, pool)
			})
		}
	}
}

// rowsText runs q and renders each row as its columns joined by "|", NULL
// as "NULL".
func rowsText(t *testing.T, pool *sqlite.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = "NULL"
			if v.Valid {
				parts[i] = v.String
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantRows fails unless q returns exactly want, in order.
func wantRows(t *testing.T, pool *sqlite.DB, q string, want ...string) {
	t.Helper()
	if got := rowsText(t, pool, q); !slices.Equal(got, want) {
		t.Fatalf("%s:\n got  %q\n want %q", q, got, want)
	}
}

func columnExists(t *testing.T, pool *sqlite.DB, table, column string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func triggerExists(t *testing.T, pool *sqlite.DB, name string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// mustFail fails unless q is rejected (a constraint holds).
func mustFail(t *testing.T, pool *sqlite.DB, why, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(q, args...); err == nil {
		t.Fatalf("%s: %s accepted", why, q)
	}
}

// Migration 0001: the schema and its singleton rows.
func checkV0001Init(t *testing.T, pool *sqlite.DB) {
	for _, table := range []string{"projects", "threads", "schedules", "tasks", "task_events", "snippets", "preferences", "notifications", "task_handoffs", "task_launch_bases"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("%s missing", table)
		}
	}
	wantRows(t, pool, `SELECT id FROM t3_connection`, "1")
	wantRows(t, pool, `SELECT id FROM preferences`, "1")
}

// Migration 0004: trees gain a status (open by default) and closed_at, tasks
// archived_at; existing rows stay open and unarchived.
func seedV0004Close(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(1,'tree')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tree_id) VALUES(1,1,'t','p','done',1)`)
}

func checkV0004Close(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,title,status,closed_at FROM task_trees`, "1|tree|open|NULL")
	wantRows(t, pool, `SELECT id,state,tree_id,archived_at FROM tasks`, "1|done|1|NULL")
	mustFail(t, pool, "unknown tree status", `UPDATE task_trees SET status='closed' WHERE id=1`)
	if !indexExists(t, pool, "tasks_archived_idx") {
		t.Fatal("tasks_archived_idx missing")
	}
}

// Migration 0010: the OpenCode settings singleton and shared paths, keyed by
// connection.
func seedV0010Connections(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
}

func checkV0010Connections(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,endpoint,username,password,verified_endpoint FROM opencode_settings`, "1||||")
	wantRows(t, pool, `SELECT id,harness FROM harness_connections ORDER BY id`, "1|t3", "2|opencode")
	mustExec(t, pool, `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES(2,'/srv','/home')`)
	mustFail(t, pool, "one row per connection and root", `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES(2,'/srv','/other')`)
	mustFail(t, pool, "unknown connection", `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES(9,'/srv','/home')`)
	mustFail(t, pool, "second settings row", `INSERT INTO opencode_settings(id) VALUES(2)`)
}

// Migration 0013: OpenCode auth defaults to none, or basic for an existing
// password; no token issued yet.
func seedV0013OpenCodeAuth(password string) func(*testing.T, *sqlite.DB) {
	return func(t *testing.T, pool *sqlite.DB) {
		mustExec(t, pool, `UPDATE opencode_settings SET endpoint='http://oc:4096',username='u',password=$1`, password)
	}
}

func checkV0013OpenCodeAuth(auth string) func(*testing.T, *sqlite.DB) {
	return func(t *testing.T, pool *sqlite.DB) {
		wantRows(t, pool, `SELECT endpoint,username,auth,token_issued_at FROM opencode_settings`, "http://oc:4096|u|"+auth+"|NULL")
		mustFail(t, pool, "unknown auth", `UPDATE opencode_settings SET auth='token'`)
	}
}

// Migration 0014: project import columns; an archived project's archive
// decision is the user's; existing bindings are manual.
func seedV0014ProjectImport(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source,archived_at) VALUES(1,'p','P','/w','app',NULL),(2,'q','Q','/q','app','2026-11-01T00:00:00.000000Z')`)
	mustExec(t, pool, `INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace) VALUES(5,1,1,'p','/w')`)
	mustExec(t, pool, `INSERT INTO external_projects(connection_id,external_id,title,workspace,available) VALUES(1,'p','P','/w',1)`)
}

func checkV0014ProjectImport(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,origin,imported_connection_id,imported_at,archive_owner FROM projects ORDER BY id`,
		"1|NULL|NULL|NULL|NULL", "2|NULL|NULL|NULL|user")
	wantRows(t, pool, `SELECT id,project_id,origin FROM project_bindings`, "5|1|manual")
	wantRows(t, pool, `SELECT external_id,importable,archived,active_at,declined_at,repo FROM external_projects`,
		"p|0|0|NULL|NULL|")
	mustFail(t, pool, "unknown binding origin", `UPDATE project_bindings SET origin='guess'`)
	mustFail(t, pool, "unknown archive owner", `UPDATE projects SET archive_owner='robot'`)
}

// Migration 0016: tasks and schedules lose tags, keeping every row.
func seedV0016DropTags(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tags) VALUES(1,1,'t','p','backlog','["a"]')`)
	mustExec(t, pool, `INSERT INTO schedules(id,project_id,title,prompt,cron,tz,tags) VALUES(1,1,'s','p','* * * * *','UTC','["b"]')`)
}

func checkV0016DropTags(t *testing.T, pool *sqlite.DB) {
	for _, table := range []string{"tasks", "schedules"} {
		if columnExists(t, pool, table, "tags") {
			t.Fatalf("%s.tags kept", table)
		}
	}
	wantRows(t, pool, `SELECT id,title,prompt FROM tasks`, "1|t|p")
	wantRows(t, pool, `SELECT id,title,prompt FROM schedules`, "1|s|p")
}

// Migration 0020: machines gain removal and worker reports; pairing codes
// and machine tokens hold only 32-byte hashes, never for the local machine.
func seedV0020MachineAuth(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'worker')`)
}

func checkV0020MachineAuth(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,label,removed_at,last_seen_at,version FROM machines ORDER BY id`,
		"1|This computer|NULL|NULL|", "2|worker|NULL|NULL|")
	hash := strings.Repeat("a", 32)
	mustExec(t, pool, `INSERT INTO machine_tokens(machine_id,secret_hash) VALUES(2,CAST($1 AS BLOB))`, hash)
	mustFail(t, pool, "a token for the local machine", `INSERT INTO machine_tokens(machine_id,secret_hash) VALUES(1,CAST($1 AS BLOB))`, hash)
	mustFail(t, pool, "a second token per machine", `INSERT INTO machine_tokens(machine_id,secret_hash) VALUES(2,CAST($1 AS BLOB))`, hash)
	mustFail(t, pool, "a short hash", `INSERT INTO pairing_codes(secret_hash,created_by_user_id,expires_at) VALUES(x'01',1,'2026-11-01T00:00:00.000000Z')`)
	mustExec(t, pool, `INSERT INTO pairing_codes(secret_hash,created_by_user_id,expires_at) VALUES(CAST($1 AS BLOB),1,'2026-11-01T00:00:00.000000Z')`, hash)
	mustFail(t, pool, "redeemed without a machine", `UPDATE pairing_codes SET redeemed_at='2026-11-01T00:00:00.000000Z'`)
}

// Migration 0021: attributed rows gain source_machine_id, NULL for existing
// rows (not written through a gateway), referencing machines.
func seedV0021SourceMachine(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(1,'tree')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tree_id) VALUES(1,1,'t','p','backlog',1)`)
	mustExec(t, pool, `INSERT INTO schedules(id,project_id,title,prompt,cron,tz) VALUES(1,1,'s','p','* * * * *','UTC')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(901,'note',1,'old','k')`)
}

func checkV0021SourceMachine(t *testing.T, pool *sqlite.DB) {
	for _, table := range []string{"projects", "task_trees", "tasks", "schedules", "journal_entries"} {
		var n, unset int
		if err := pool.QueryRow(`SELECT count(*), count(*) FILTER (WHERE source_machine_id IS NULL) FROM `+table).Scan(&n, &unset); err != nil || n == 0 || n != unset {
			t.Fatalf("%s: %d rows, %d without a source machine, %v", table, n, unset, err)
		}
	}
	mustFail(t, pool, "unknown source machine", `UPDATE tasks SET source_machine_id=9 WHERE id=1`)
	mustExec(t, pool, `UPDATE tasks SET source_machine_id=1 WHERE id=1`)
}

// Migration 0023: T3 gains public_url and admin_token, unset; the pairing
// stays.
func seedV0023T3BrowserAdmin(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `UPDATE t3_connection SET client_id='c',token='keep',t3_url='http://t3:3773' WHERE machine_id=1`)
}

func checkV0023T3BrowserAdmin(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT machine_id,client_id,token,t3_url,public_url,admin_token FROM t3_connection`, "1|c|keep|http://t3:3773|NULL|NULL")
}

// Migration 0025: execution attempts gain observed_session (a JSON object or
// NULL); task events journal under the task's own session; the parent
// session triggers are gone.
func seedV0025ObservedSession(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'worker')`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,machine_id,harness,identity) VALUES(3,2,'t3','env-w')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,3,'worker-thread')`)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,connection_id,session_id,launch_attempt) VALUES(1,1,'t','p','launching',3,5,1)`)
	mustExec(t, pool, `INSERT INTO execution_attempts(task_id,attempt,phase) VALUES(1,1,'unconfirmed')`)
}

func checkV0025ObservedSession(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT task_id,attempt,phase,observed_session FROM execution_attempts`, "1|1|unconfirmed|NULL")
	mustFail(t, pool, "observed session not an object", `UPDATE execution_attempts SET observed_session='[]'`)
	mustExec(t, pool, `UPDATE execution_attempts SET observed_session='{"connectionId":3,"externalSessionId":"worker-thread"}'`)
	for _, name := range []string{"threads_parent_session_id_insert", "threads_parent_session_id_update"} {
		if triggerExists(t, pool, name) {
			t.Fatalf("%s kept", name)
		}
	}
	// A state change of a task on a worker's T3 journals under its own session.
	mustExec(t, pool, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES(1,'launching','running','confirmed')`)
	wantRows(t, pool, `SELECT task_id,source_session_id,event_type,text FROM journal_entries WHERE kind='event' AND task_id=1`,
		"1|5|task_state_changed|confirmed (launching -> running)")
}

// Migration 0026: business-rule triggers are dropped (Go writes journal
// events, recorded work and sessions in the same Write); journal history
// stays, and direct inserts no longer journal or derive sessions.
var v0026DroppedTriggers = []string{
	"task_event_journal", "journal_note_protection", "journal_tree_created", "journal_tree_config",
	"journal_tree_status", "journal_membership", "journal_need_added", "journal_need_removed",
	"journal_member_created", "thread_project_insert", "acceptance_session_insert",
	"task_handoffs_source_session_id_insert", "task_handoffs_source_session_id_update",
}

func seedV0026JournalEventsInGo(t *testing.T, pool *sqlite.DB) {
	for _, name := range v0026DroppedTriggers {
		if !triggerExists(t, pool, name) {
			t.Fatalf("%s missing before 0026", name)
		}
	}
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,1,'thread')`)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(1,'tree')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tree_id,session_id) VALUES(1,1,'t','p','backlog',1,5)`)
	mustExec(t, pool, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES(1,NULL,'backlog','created')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(901,'note',1,'keep','k')`)
}

func checkV0026JournalEventsInGo(t *testing.T, pool *sqlite.DB) {
	for _, name := range v0026DroppedTriggers {
		if triggerExists(t, pool, name) {
			t.Fatalf("%s kept", name)
		}
	}
	// History the triggers wrote before 0026 stays.
	history := []string{"1|event|NULL|tree_created|tree", "2|event|NULL|task_moved|t", "3|event|1|task_created|created", "901|note|1||keep"}
	const journal = `SELECT id,kind,task_id,event_type,text FROM journal_entries ORDER BY id`
	wantRows(t, pool, journal, history...)
	// A direct task event no longer journals: Go does, in the same Write.
	mustExec(t, pool, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES(1,'backlog','running','started')`)
	wantRows(t, pool, journal, history...)
	// A direct acceptance no longer takes the task's session from a trigger.
	mustExec(t, pool, `INSERT INTO task_acceptances(id,task_id,provenance,evidence) VALUES(7,1,'user','{}')`)
	wantRows(t, pool, `SELECT id,source_session_id FROM task_acceptances`, "7|NULL")
}
