package db

import (
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/dbtest/migrationfixture"
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
	27: {
		{"legacy sessions and model backfilled", seedV0027LegacyBackfill, checkV0027LegacyBackfill},
		{"unresolvable legacy ids stay null", seedV0027Unresolvable, checkV0027Unresolvable},
	},
	28: {{"legacy storage dropped", seedV0028DropLegacy, checkV0028DropLegacy}},
	29: {{"search index built and kept", seedV0029Search, checkV0029Search}},
	30: {{"claude settings table", seedV0030ClaudeSettings, checkV0030ClaudeSettings}},
	31: {{"codex settings per machine", seedV0031CodexSettings, checkV0031CodexSettings}},
	32: {
		{"unused seeded connection removed", seedV0032Unused, checkV0032Connection(false)},
		{"paired connection kept", seedV0032("UPDATE t3_connection SET token='tok' WHERE machine_id=1"), checkV0032Connection(true)},
		{"stamped connection kept", seedV0032("UPDATE harness_connections SET identity='env-1' WHERE id=1"), checkV0032Connection(true)},
		{"default connection kept", seedV0032("UPDATE preferences SET default_connection_id=1"), checkV0032Connection(true)},
		{"referenced connection kept", seedV0032("INSERT INTO projects(id,title) VALUES(1,'P')", "INSERT INTO tasks(id,project_id,title,prompt,connection_id) VALUES(1,1,'T','p',1)"), checkV0032Connection(true)},
		{"bound connection kept", seedV0032("INSERT INTO projects(id,title) VALUES(1,'P')", "INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace) VALUES(1,1,'x','/w')"), checkV0032Connection(true)},
	},
	33: {{"workspaces become launch options", seedV0032(launchOptionsSeed...), checkV0033LaunchOptions}},
	34: {{"workspace columns and launch bases dropped", seedV0032(launchBasesSeed...), checkV0034DropWorkspaces}},
	35: {
		{"legacy states and preference entries removed", seedV0035DropSettled, checkV0035DropSettled},
		{"empty preference filter retained", seedV0032(`UPDATE preferences SET notify_events='["settled"]'`), checkV0035EmptyPreferences},
	},
	36: {
		{"settings, secrets and status copied", seedV0036HarnessSetup, checkV0036HarnessSetup},
		{"unconfigured defaults retained", nil, checkV0036HarnessSetupDefaults},
	},
	37: {{"existing connection and task evidence retained", seedV0037ConnectionFacts, checkV0037ConnectionFacts}},
	39: {{"observation producer history", nil, checkV0039ObservationProducers}},
	38: {{"identity history and workspace bindings", seedV0038ProjectIdentity, checkV0038ProjectIdentity}},
	40: {{"legacy identity and evidence retained", seedV0040SharedAccess, checkV0040SharedAccess}},
	41: {{"shared accounts kept and audit starts empty", seedV0041AccessAudit, checkV0041AccessAudit}},
	43: {
		{"personal reads and devices retained", seedV0043NotificationRecipients, checkV0043NotificationRecipients},
		{"shared legacy devices removed", seedV0043SharedPush, checkV0043SharedPush},
	},
	44: {{"work attribution leaves old evidence untouched", seedV0044Attribution, checkV0044Attribution}},
	45: {{"existing connection has unknown workspace freshness", seedV0037ConnectionFacts, checkV0045WorkspaceRefresh}},
	46: {{"existing workspace has no historical project", seedV0046HistoricalWorkspaceProject, checkV0046HistoricalWorkspaceProject}},
	47: {{"backfill proven workspace pins and preserve attempts", seedV0047WorkspacePins, checkV0047WorkspacePins}},
	48: {{"proven group memberships and historical work retained", seedProjectGroups, checkProjectGroups}},
	49: {{"project events keep history and accept workspace_added", seedV0049ProjectEvents, checkV0049ProjectEvents}},
	50: {{"existing workspace is not blocked until refreshed", seedV0046HistoricalWorkspaceProject, checkV0050BlockedReason}},
	51: {{"existing task starts at revision 0, unanswered", seedV0040SharedAccess, checkV0051StateRevision}},
	52: {{"one active steerer per tree and per task", seedV0040SharedAccess, checkV0052TreeSteering}},
}

func checkV0051StateRevision(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,state_revision,answered_revision FROM tasks WHERE id=6`, "6|0|NULL")
}

func checkV0052TreeSteering(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(9,'Steered'),(10,'Other')`)
	mustExec(t, pool, `INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(9,6,'r1','d')`)
	for _, q := range []string{
		`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(9,NULL,'r2','d')`,
		`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(10,6,'r3','d')`,
		`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest,ended_at,end_reason) VALUES(10,6,'r4','d',stage_now(),'unknown')`,
	} {
		if _, err := pool.Exec(q); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
	mustExec(t, pool, `UPDATE tree_steerers SET ended_at=stage_now(), end_reason='replaced' WHERE request_id='r1'`)
	mustExec(t, pool, `INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(9,6,'r2','d')`)
	mustExec(t, pool, `INSERT INTO steering_events(tree_id,steerer_id,kind,task_id) VALUES(9,1,'steering_started',6)`)
	wantRows(t, pool, `SELECT id,steerer_id,kind,payload,caused_by_task_id FROM steering_events`, "1|1|steering_started|{}|NULL")
	wantRows(t, pool, `SELECT id,delivered_through FROM tree_steerers ORDER BY id`, "1|0", "2|0")
}

func seedV0049ProjectEvents(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO projects(id,title) VALUES(7,'Events')`)
	mustExec(t, pool, `INSERT INTO project_events(id,project_id,type,text,created_at) VALUES
 (1,7,'workspace_moved','Moved.','2026-11-01T00:00:00.000000000Z'),(2,7,'project_merged','Merged.','2026-11-02T00:00:00.000000000Z')`)
}

func checkV0049ProjectEvents(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	wantRows(t, pool, `SELECT id,type,text,created_by_user_id,attribution FROM project_events ORDER BY id`,
		"1|workspace_moved|Moved.|1|NULL", "2|project_merged|Merged.|1|NULL")
	mustExec(t, pool, `INSERT INTO project_events(project_id,type,text,attribution) VALUES(7,'workspace_added','Added.','{"actor":{"kind":"agent"}}')`)
	wantRows(t, pool, `SELECT id,type FROM project_events WHERE id>2`, "3|workspace_added")
	if _, err := pool.Exec(`INSERT INTO project_events(project_id,type,text) VALUES(7,'unknown','x')`); err == nil {
		t.Fatal("project event type check accepted an unknown type")
	}
}

func checkV0050BlockedReason(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT external_id,available,blocked_reason FROM external_projects WHERE connection_id=42`, "ext|1|")
}

func seedV0046HistoricalWorkspaceProject(t *testing.T, pool *sqlite.DB) {
	seedV0037ConnectionFacts(t, pool)
	mustExec(t, pool, `INSERT INTO external_projects(connection_id,external_id,title,workspace,available) VALUES(42,'ext','Workspace','/w',true)`)
}

func checkV0046HistoricalWorkspaceProject(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT external_id,workspace,historical_project_id FROM external_projects WHERE connection_id=42`, "ext|/w|NULL")
}

func seedV0047WorkspacePins(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO harness_connections(id,machine_id,harness,active,retired_at) VALUES
	 (2,1,'t3',true,NULL),(3,1,'claude',true,NULL),(4,1,'t3',false,'2026-11-27T00:00:00Z')`,
		`INSERT INTO projects(id,title) VALUES
	 (1,'Recorded primary'),(2,'Moved root'),(3,'Archived workspace'),
	 (4,'Alternate only'),(5,'Offline primary'),(6,'Retired connection')`,
		`INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,active,is_primary,status) VALUES
	 (1,1,2,'shared','/primary',true,true,'available'),
	 (2,1,2,'shared','/offline',false,false,'missing'),
	 (3,2,2,'moved','/former',true,true,'changed'),
	 (4,2,2,'alternate','/alternate',true,false,'available'),
	 (5,3,2,'archived','/archived',true,true,'available'),
	 (6,4,2,'secondary','/secondary',true,false,'available'),
	 (7,5,2,'missing','/missing',true,true,'missing'),
	 (8,6,4,'retired','/retired',true,true,'available'),
	 (9,1,3,'remote','/remote',true,true,'available'),
	 (10,1,2,'unknown','/unknown',true,false,'available'),
	 (11,1,2,'pathless','',true,false,'available')`,
		`INSERT INTO external_projects(connection_id,external_id,workspace,title,available,archived) VALUES
	 (2,'shared','/primary','Primary',true,false),
	 (2,'shared','/offline','Offline',false,false),
	 (2,'moved','/new','Moved',true,false),
	 (2,'alternate','/alternate','Alternate',true,false),
	 (3,'moved','/former','Other connection',true,false),
	 (2,'archived','/archived','Archived',true,true),
	 (2,'secondary','/secondary','Secondary',true,false),
	 (2,'missing','/missing','Missing',false,false),
	 (4,'retired','/retired','Retired',true,false),
	 (3,'remote','/remote','Remote',true,false),
	 (2,'pathless','','Pathless',true,false)`,
		`INSERT INTO tasks(id,project_id,title,prompt,connection_id,binding_id) VALUES
	 (1,1,'Primary','p',2,NULL),(2,1,'Offline explicit','p',2,2),
	 (3,2,'Moved primary','p',2,NULL),(4,3,'Archived primary','p',2,NULL),
	 (5,4,'Alternate only','p',2,NULL),(6,5,'Offline primary','p',2,NULL),
	 (7,6,'Retired connection','p',4,NULL),(8,1,'Foreign explicit','p',2,9),
	 (9,1,'Unproven explicit','p',2,10),(10,1,'Pathless','p',2,11),
	 (11,3,'Archived explicit','p',2,5),(12,2,'Explicit after split','p',2,1)`,
		`INSERT INTO schedules(id,project_id,title,prompt,cron,tz,connection_id,binding_id)
	 SELECT id,project_id,title,prompt,'@daily','UTC',connection_id,binding_id FROM tasks`,
		`INSERT INTO tasks(id,project_id,title,prompt,connection_id,launch_attempt,binding_id)
	 VALUES (13,1,'Existing attempt','p',2,1,1)`,
		`INSERT INTO task_launch_destinations(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision)
	 VALUES (13,1,2,'historic','/historic',1,7)`,
	} {
		mustExec(t, pool, stmt)
	}
}

func checkV0047WorkspacePins(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	want := []string{
		"1|shared|/primary", "2|shared|/offline", "3|NULL|NULL", "4|NULL|NULL",
		"5|NULL|NULL", "6|NULL|NULL", "7|NULL|NULL", "8|NULL|NULL",
		"9|NULL|NULL", "10|pathless|", "11|NULL|NULL", "12|shared|/primary",
	}
	const pins = `SELECT id, json_extract(workspace_pin,'$.externalId'), json_extract(workspace_pin,'$.root') FROM `
	wantRows(t, pool, pins+`schedules ORDER BY id`, want...)
	wantRows(t, pool, pins+`tasks ORDER BY id`, append(want, "13|historic|/historic")...)

	mustExec(t, pool, `INSERT INTO task_launch_destinations
		 (task_id,attempt,connection_id,external_project_id,workspace,binding_revision)
		 VALUES (13,2,2,'unbound','/unbound',0)`)
	wantRows(t, pool, `SELECT task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision
	FROM task_launch_destinations ORDER BY attempt`, "13|1|2|historic|/historic|1|7", "13|2|2|unbound|/unbound|NULL|0")
}

func seedProjectGroups(t *testing.T, pool *sqlite.DB) {
	migrationfixture.SeedProjectGroups(t, func(query string) error {
		_, err := pool.ExecContext(t.Context(), query)
		return err
	})
}

func checkProjectGroups(t *testing.T, pool *sqlite.DB) {
	migrationfixture.CheckProjectGroups(t, func(query string) (int, error) {
		var count int
		err := pool.QueryRowContext(t.Context(), query).Scan(&count)
		return count, err
	})
}

func checkV0045WorkspaceRefresh(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,identity,workspace_refreshed_at,workspace_refresh_error FROM harness_connections WHERE id=42`, "42|instance|NULL|NULL")
}

func seedV0043NotificationRecipients(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	mustExec(t, pool, `UPDATE preferences SET notify_events='["ready"]' WHERE id=1`)
	mustExec(t, pool, `INSERT INTO users(id, display_name, email, email_key) VALUES (2, 'Member', 'member@example.test', 'member@example.test')`)
	mustExec(t, pool, `INSERT INTO notifications(id, event, title, url, read_at) VALUES
	 (1, 'done', 'Read', '/', '2026-11-27T00:00:00.123456Z'), (2, 'ready', 'Unread', '/', NULL)`)
	mustExec(t, pool, `INSERT INTO push_subscriptions(endpoint, p256dh, auth) VALUES ('https://push.example/old', 'k', 'a')`)
}

func checkV0043NotificationRecipients(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	wantRows(t, pool, `SELECT notification_id, user_id, read_at FROM notification_recipients ORDER BY notification_id`,
		"1|1|2026-11-27T00:00:00.123456Z", "2|1|NULL")
	wantRows(t, pool, `SELECT user_id, notify_events FROM notification_preferences`, `1|["ready"]`)
	wantRows(t, pool, `SELECT endpoint, auth_session_id, binding FROM push_subscriptions`, "https://push.example/old|NULL|NULL")
	mustExec(t, pool, `DELETE FROM notifications WHERE id=1`)
	wantRows(t, pool, `SELECT notification_id FROM notification_recipients`, "2")
}

func seedV0043SharedPush(t *testing.T, pool *sqlite.DB) {
	seedV0043NotificationRecipients(t, pool)
	mustExec(t, pool, `UPDATE installation_access SET installation_id='shared', mode='setup_pending' WHERE id=1`)
}

func checkV0043SharedPush(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT count(*) FROM push_subscriptions`, "0")
}

func seedV0044Attribution(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	seedV0040SharedAccess(t, pool)
	mustExec(t, pool, `INSERT INTO task_events(task_id,to_state,detail) VALUES(6,'backlog','created')`)
	mustExec(t, pool, `INSERT INTO task_acceptances(task_id,provenance,evidence,note) VALUES(6,'user','{ "taskId": 6 }','old result')`)
}

func checkV0044Attribution(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for _, q := range []string{
		`SELECT count(*) FROM task_events WHERE task_id=6 AND detail='created' AND attribution IS NULL`,
		`SELECT count(*) FROM journal_entries WHERE id=8 AND text='old evidence  ✓' AND created_by_user_id=7 AND attribution IS NULL`,
		`SELECT count(*) FROM task_acceptances WHERE task_id=6 AND evidence='{ "taskId": 6 }' AND note='old result' AND attribution IS NULL`,
	} {
		var got int
		if err := pool.QueryRow(q).Scan(&got); err != nil || got != 1 {
			t.Fatalf("%s: %d, %v; want 1", q, got, err)
		}
	}
}

func seedV0041AccessAudit(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for _, q := range []string{
		`UPDATE users SET display_name = 'Owner', email = 'Owner@example.test', email_key = 'owner@example.test' WHERE id = 1`,
		`UPDATE installation_access SET installation_id = 'existing', mode = 'claimed', claimed_at = '2026-11-27T00:00:00.000000Z'`,
		`INSERT INTO identities(user_id, provider, subject) VALUES (1, 'local', 'user:1')`,
		`INSERT INTO local_credentials(identity_id, password_verifier) SELECT id, 'existing verifier' FROM identities WHERE provider = 'local'`,
	} {
		if _, err := pool.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func checkV0041AccessAudit(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for q, want := range map[string]int{
		`SELECT count(*) FROM installation_access WHERE mode = 'claimed' AND installation_id = 'existing'`: 1,
		`SELECT count(*) FROM local_credentials WHERE password_verifier = 'existing verifier'`:             1,
		`SELECT count(*) FROM access_audit`: 0,
	} {
		var got int
		if err := pool.QueryRow(q).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d, %v; want %d", q, got, err, want)
		}
	}
}

func seedV0040SharedAccess(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO users(id, display_name) VALUES (7, 'Existing user')`,
		`INSERT INTO identities(id, user_id, provider, subject) VALUES (9, 7, 'legacy', 'existing-subject')`,
		`INSERT INTO projects(id, title) VALUES (4, 'Existing project')`,
		`INSERT INTO harness_connections(id, machine_id, harness) VALUES (4, 1, 't3')`,
		`INSERT INTO tasks(id, project_id, title, prompt, connection_id) VALUES (6, 4, 'Existing task', 'prompt', 4)`,
		`INSERT INTO journal_entries(id, kind, task_id, text, created_by_user_id, request_id) VALUES (8, 'note', 6, 'old evidence  ✓', 7, 'old-note')`,
	} {
		if _, err := pool.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func checkV0040SharedAccess(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for label, q := range map[string]string{
		"implicit admin":     `SELECT count(*) FROM users WHERE id = 1 AND display_name = 'Default user' AND role = 'admin' AND status = 'active' AND email IS NULL`,
		"legacy identity":    `SELECT count(*) FROM identities i JOIN users u ON u.id = i.user_id WHERE i.id = 9 AND u.id = 7 AND i.subject = 'existing-subject' AND u.role = 'member'`,
		"historical creator": `SELECT count(*) FROM tasks WHERE id = 6 AND created_by_user_id = 1`,
		"journal evidence":   `SELECT count(*) FROM journal_entries WHERE id = 8 AND text = 'old evidence  ✓' AND created_by_user_id = 7`,
		"personal state":     `SELECT count(*) FROM installation_access WHERE mode = 'personal' AND claimed_at IS NULL`,
	} {
		var count int
		if err := pool.QueryRow(q).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s: count %d, err %v", label, count, err)
		}
	}
	q := `DELETE FROM users WHERE id = 7`
	if _, err := pool.Exec(q); err == nil {
		t.Fatal("removed referenced historical creator")
	}
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

// v0027ClearNeutral makes seeded rows look like ones written before their
// neutral columns: the legacy-id triggers filled them on insert, so clear
// them and the identities only the triggers created (keep lists the rest).
func v0027ClearNeutral(t *testing.T, pool *sqlite.DB, keep string) {
	t.Helper()
	mustExec(t, pool, `UPDATE tasks SET session_id=NULL WHERE t3_thread_id IS NOT NULL`)
	mustExec(t, pool, `UPDATE tasks SET origin_session_id=NULL`)
	mustExec(t, pool, `UPDATE schedules SET origin_session_id=NULL`)
	mustExec(t, pool, `UPDATE notifications SET session_id=NULL`)
	mustExec(t, pool, `UPDATE task_handoffs SET source_session_id=NULL`)
	mustExec(t, pool, `UPDATE task_launch_bases SET source_session_id=NULL`)
	mustExec(t, pool, `UPDATE journal_entries SET source_session_id=NULL`)
	mustExec(t, pool, `DELETE FROM session_identities WHERE id NOT IN (`+keep+`)`)
}

// Migration 0027: legacy thread ids become session identities (the row's
// own connection, else an existing T3 identity, else the local active T3),
// and T3 tasks and schedules without executionConfig get the one their
// legacy model columns mean.
func seedV0027LegacyBackfill(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,active,retired_at) VALUES(3,'t3',0,'2026-11-01T00:00:00.000000Z')`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,machine_id,harness) VALUES(4,1,'opencode')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,3,'old-thread')`)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,connection_id,t3_thread_id,provider_instance_id,model,model_options,runtime_mode,interaction_mode)
 VALUES(1,1,'live','p','done',1,'live','codex','gpt<6>','[{"id":"effort","value":"high"}]','auto','plan')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,connection_id,t3_thread_id) VALUES(2,1,'retired','p','done',3,'old-thread')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,origin_thread_id,provider_instance_id) VALUES(3,1,'origin','p',1,'old-thread','codex')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,origin_thread_id,model,execution_config) VALUES(4,1,'kept','p',1,'new-origin','m','{"model":"x","options":[]}')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,provider_instance_id,model) VALUES(5,1,'opencode','p',4,'codex','gpt')`)
	mustExec(t, pool, `INSERT INTO schedules(id,project_id,title,prompt,cron,tz,origin_thread_id,provider_instance_id,model,runtime_mode)
 VALUES(1,1,'s','p','* * * * *','UTC','old-thread','claude','haiku','approval-required')`)
	mustExec(t, pool, `INSERT INTO notifications(id,event,task_id,t3_thread_id,title,url) VALUES(1,'done',2,'old-thread','n','u'),(2,'done',NULL,'live','n','u')`)
	mustExec(t, pool, `INSERT INTO task_handoffs(task_id,attempt,source_task_id,source_thread_id,status) VALUES(3,1,1,'live','sent')`)
	mustExec(t, pool, `INSERT INTO task_launch_bases(task_id,attempt,base_ref,start_from_origin,source,source_task_id,source_thread_id) VALUES(3,1,'main',0,'predecessor',2,'old-thread')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,source_thread_id,text,request_id) VALUES(901,'note',3,'new-origin','n','k')`)
	v0027ClearNeutral(t, pool, "5")
}

func checkV0027LegacyBackfill(t *testing.T, pool *sqlite.DB) {
	const session = `(SELECT connection_id||':'||external_id FROM session_identities WHERE id=%s)`
	q := func(col, table string) string {
		return `SELECT id,COALESCE(` + fmt.Sprintf(session, col) + `,'NULL') FROM ` + table + ` ORDER BY id`
	}
	// Own connection, including a retired one.
	wantRows(t, pool, q("session_id", "tasks"), "1|1:live", "2|3:old-thread", "3|NULL", "4|NULL", "5|NULL")
	// No own connection: the existing (retired) T3 identity, else the local active T3.
	wantRows(t, pool, q("origin_session_id", "tasks"), "1|NULL", "2|NULL", "3|3:old-thread", "4|1:new-origin", "5|NULL")
	wantRows(t, pool, q("origin_session_id", "schedules"), "1|3:old-thread")
	// A notification takes its task's connection.
	wantRows(t, pool, q("session_id", "notifications"), "1|3:old-thread", "2|1:live")
	wantRows(t, pool, q("source_session_id", "task_handoffs"), "1|1:live")
	wantRows(t, pool, q("source_session_id", "task_launch_bases"), "1|3:old-thread")
	wantRows(t, pool, q("source_session_id", "journal_entries"), "901|1:new-origin")
	// T3 rows get the legacy executionConfig (Go's escaping of the model
	// key); a set config and a non-T3 row are untouched.
	wantRows(t, pool, `SELECT id,COALESCE(execution_config,'NULL') FROM tasks ORDER BY id`,
		`1|{"model":"[\"codex\",\"gpt\\u003c6\\u003e\"]","options":[{"id":"effort","value":"high"}],"permission":"auto","mode":"plan"}`,
		`2|{"model":"","options":[],"permission":"full-access","mode":"default"}`,
		`3|{"model":"","options":[],"permission":"full-access","mode":"default"}`,
		`4|{"model":"x","options":[]}`,
		`5|NULL`)
	wantRows(t, pool, `SELECT id,execution_config FROM schedules`,
		`1|{"model":"[\"claude\",\"haiku\"]","options":[],"permission":"approval-required","mode":"default"}`)
}

// Unresolvable: no own connection, no T3 identity and no active local T3;
// or a task session another task already holds. The neutral column stays
// NULL.
func seedV0027Unresolvable(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source) VALUES(1,'P','stage:p','','app')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,t3_thread_id,origin_thread_id) VALUES(1,1,'dup','p',1,'dup','ghost')`)
	mustExec(t, pool, `INSERT INTO notifications(id,event,task_id,t3_thread_id,title,url) VALUES(1,'done',NULL,'ghost','n','u')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,source_thread_id,text,request_id) VALUES(901,'note',1,'ghost','n','k')`)
	v0027ClearNeutral(t, pool, "SELECT id FROM session_identities WHERE external_id='dup'")
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,session_id) VALUES(2,1,'holder','p',1,(SELECT id FROM session_identities WHERE external_id='dup'))`)
	mustExec(t, pool, `UPDATE harness_connections SET active=0,retired_at='2026-11-01T00:00:00.000000Z' WHERE id=1`)
}

func checkV0027Unresolvable(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,COALESCE(session_id,'NULL'),COALESCE(origin_session_id,'NULL') FROM tasks WHERE id=1`, "1|NULL|NULL")
	wantRows(t, pool, `SELECT id,COALESCE(session_id,'NULL') FROM notifications`, "1|NULL")
	wantRows(t, pool, `SELECT id,COALESCE(source_session_id,'NULL') FROM journal_entries`, "901|NULL")
	wantRows(t, pool, `SELECT external_id FROM session_identities`, "dup")
}

// Migration 0028: the legacy columns, indexes, table and compatibility
// triggers are gone; rows, ids and sequences survive; tasks and schedules
// reference their connection by a real foreign key.
var v0028DroppedColumns = map[string][]string{
	"projects": {"t3_project_id", "workspace_root", "source", "t3_deleted_at"},
	"tasks": {"provider_instance_id", "model", "model_options", "runtime_mode", "interaction_mode", "t3_thread_id",
		"launch_reuse", "after_task_id", "after_thread_id", "origin_thread_id"},
	"schedules": {"provider_instance_id", "model", "model_options", "runtime_mode", "interaction_mode", "origin_thread_id"},
	"threads": {"provider_instance_id", "creation_source", "run_count", "latest_run_status", "recent_runs",
		"pending_question_count", "parent_session_id", "link", "last_word_item_id", "pull_requests"},
	"notifications":     {"t3_thread_id"},
	"task_handoffs":     {"source_thread_id"},
	"task_launch_bases": {"source_thread_id"},
	"journal_entries":   {"source_thread_id"},
}

var v0028DroppedTriggers = []string{
	"tasks_session_id_insert", "tasks_session_id_update", "tasks_connection_insert", "tasks_connection_update",
	"schedules_connection_insert", "schedules_connection_update", "threads_session_id_insert", "threads_session_id_update",
	"tasks_origin_session_id_insert", "tasks_origin_session_id_update", "schedules_origin_session_id_insert",
	"schedules_origin_session_id_update", "notifications_session_id_insert", "notifications_session_id_update",
	"task_launch_bases_source_session_id_insert", "task_launch_bases_source_session_id_update",
	"journal_entries_source_session_id_insert", "journal_entries_source_session_id_update",
}

func seedV0028DropLegacy(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,1,'thread')`)
	mustExec(t, pool, `INSERT INTO projects(id,title,t3_project_id,workspace_root,source,origin,imported_connection_id) VALUES(1,'P','stage:p','','app','discovered',1),(9,'gone','stage:g','','app',NULL,NULL)`)
	mustExec(t, pool, `DELETE FROM projects WHERE id=9`)
	mustExec(t, pool, `INSERT INTO task_trees(id,title) VALUES(1,'tree')`)
	mustExec(t, pool, `INSERT INTO schedules(id,project_id,title,prompt,cron,tz,execution_config) VALUES(1,1,'s','p','* * * * *','UTC','{"model":""}')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,tree_id,schedule_id,session_id,origin_session_id,execution_config,start_at,start_tz)
 VALUES(1,1,'t','p','backlog',1,1,5,5,'{"model":""}','2026-12-01T00:00:00.000000Z','UTC'),(7,1,'gone','p','backlog',NULL,NULL,NULL,NULL,NULL,NULL,NULL)`)
	mustExec(t, pool, `DELETE FROM tasks WHERE id=7`)
	mustExec(t, pool, `INSERT INTO threads(t3_thread_id,t3_project_id,title,status,parent_thread_id,created_at,updated_at,session_id,stage_project_id,last_word_text)
 VALUES('thread','p','th','running','parent','2026-11-01T00:00:00.000000Z','2026-11-01T00:00:00.000000Z',5,1,'hi')`)
	mustExec(t, pool, `INSERT INTO notifications(id,event,task_id,title,url,session_id) VALUES(1,'done',1,'n','u',5)`)
	mustExec(t, pool, `INSERT INTO task_handoffs(task_id,attempt,source_task_id,source_thread_id,status,source_session_id) VALUES(1,1,1,'thread','sent',5)`)
	mustExec(t, pool, `INSERT INTO task_launch_bases(task_id,attempt,base_ref,start_from_origin,source,source_session_id) VALUES(1,1,'main',0,'fallback',5)`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id,source_session_id) VALUES(901,'note',1,'n','k',5)`)
	mustExec(t, pool, `INSERT INTO question_responses(t3_thread_id,request_id,answers) VALUES('thread','r','{}')`)
}

func checkV0028DropLegacy(t *testing.T, pool *sqlite.DB) {
	for table, cols := range v0028DroppedColumns {
		for _, col := range cols {
			if columnExists(t, pool, table, col) {
				t.Errorf("%s.%s kept", table, col)
			}
		}
	}
	for _, name := range v0028DroppedTriggers {
		if triggerExists(t, pool, name) {
			t.Errorf("%s kept", name)
		}
	}
	for _, name := range []string{"tasks_origin_thread_idx", "projects_workspace_root_idx", "threads_status_idx",
		"threads_updated_at_idx", "threads_root_created_idx", "threads_t3_project_id_idx"} {
		if indexExists(t, pool, name) {
			t.Errorf("index %s kept", name)
		}
	}
	for _, name := range []string{"tasks_state_idx", "tasks_project_id_idx", "tasks_created_at_idx", "tasks_due_start_idx",
		"tasks_schedule_idx", "tasks_tree_idx", "tasks_archived_idx", "task_session_identity", "schedules_due_idx",
		"schedules_project_idx", "threads_created_at_idx", "thread_session_identity"} {
		if !indexExists(t, pool, name) {
			t.Errorf("index %s missing", name)
		}
	}
	if tableExists(t, pool, "question_responses") {
		t.Error("question_responses kept")
	}
	wantRows(t, pool, `PRAGMA foreign_key_check`)
	wantRows(t, pool, `SELECT id,title,origin,imported_connection_id FROM projects`, "1|P|discovered|1")
	wantRows(t, pool, `SELECT id,tree_id,schedule_id,session_id,origin_session_id,execution_config,connection_id FROM tasks`, `1|1|1|5|5|{"model":""}|1`)
	wantRows(t, pool, `SELECT id,connection_id,execution_config FROM schedules`, `1|1|{"model":""}`)
	wantRows(t, pool, `SELECT t3_thread_id,parent_thread_id,session_id,stage_project_id,last_word_text FROM threads`, "thread|parent|5|1|hi")
	wantRows(t, pool, `SELECT id,session_id FROM notifications`, "1|5")
	wantRows(t, pool, `SELECT task_id,source_session_id FROM task_handoffs`, "1|5")
	wantRows(t, pool, `SELECT task_id,source_session_id FROM task_launch_bases`, "1|5")
	wantRows(t, pool, `SELECT id,source_session_id FROM journal_entries WHERE kind='note'`, "901|5")
	// Sequences carry across the rebuilds: deleted ids are not reused.
	mustExec(t, pool, `INSERT INTO projects(title) VALUES('new')`)
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt) VALUES(1,'new','p')`)
	wantRows(t, pool, `SELECT max(id) FROM projects`, "10")
	wantRows(t, pool, `SELECT max(id) FROM tasks`, "8")
	// A real foreign key replaces the connection triggers; a bare thread id no
	// longer derives a session.
	mustFail(t, pool, "unknown task connection", `INSERT INTO tasks(project_id,title,prompt,connection_id) VALUES(1,'x','p',9)`)
	mustFail(t, pool, "unknown schedule connection", `UPDATE schedules SET connection_id=9`)
	mustFail(t, pool, "start without tz", `UPDATE tasks SET start_tz=NULL WHERE id=1`)
	mustExec(t, pool, `UPDATE tasks SET start_at=NULL,start_tz=NULL WHERE id=1`)
}

func seedV0029Search(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO projects(id,title) VALUES(1,'P')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt) VALUES(1,1,'Needle title','p'),(2,1,'t','Привет prompt')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(901,'note',1,'Mätch note','k')`)
}

// checkV0029Search: existing rows are indexed (case-insensitive, non-ASCII
// too) and later writes follow through the triggers.
func checkV0029Search(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH '"NEEDLE"'`, "1")
	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH '"привет"'`, "2")
	wantRows(t, pool, `SELECT rowid FROM journal_fts WHERE journal_fts MATCH '"MÄTCH"'`, "901")
	mustExec(t, pool, `UPDATE tasks SET title='renamed' WHERE id=1`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt) VALUES(3,1,'another needle','p')`)
	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH '"needle"'`, "3")
	for _, table := range []string{"tasks_fts", "journal_fts"} {
		mustExec(t, pool, `INSERT INTO `+table+`(`+table+`, rank) VALUES ('integrity-check', 1)`)
	}
}

func seedV0030ClaudeSettings(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'worker')`)
}

// checkV0030ClaudeSettings: the table starts empty (no row reads the
// defaults), defaults fill an inserted row, and machine and boolean
// constraints hold.
func checkV0030ClaudeSettings(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT count(*) FROM claude_settings`, "0")
	mustExec(t, pool, `INSERT INTO claude_settings(machine_id) VALUES(1)`)
	mustExec(t, pool, `INSERT INTO claude_settings(machine_id,binary_path,config_dir,disconnected) VALUES(2,'/bin/claude','/home/u/.claude',1)`)
	wantRows(t, pool, `SELECT machine_id,binary_path,config_dir,disconnected,updated_at<>'' FROM claude_settings ORDER BY machine_id`, "1|||0|1", "2|/bin/claude|/home/u/.claude|1|1")
	mustFail(t, pool, "unknown machine", `INSERT INTO claude_settings(machine_id) VALUES(9)`)
	mustFail(t, pool, "duplicate machine", `INSERT INTO claude_settings(machine_id) VALUES(1)`)
	mustFail(t, pool, "non-boolean disconnected", `UPDATE claude_settings SET disconnected=2 WHERE machine_id=1`)
}

func seedV0031CodexSettings(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT OR IGNORE INTO machines(id,label) VALUES(2,'worker')`)
}

// checkV0031CodexSettings: one row per existing machine, defaults never
// connected, connected is a boolean, and a missing machine is refused.
func checkV0031CodexSettings(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO codex_settings(machine_id) VALUES(1)`)
	mustExec(t, pool, `INSERT INTO codex_settings(machine_id,binary_path,connected) VALUES(2,'/opt/codex',1)`)
	wantRows(t, pool, `SELECT machine_id||':'||binary_path||':'||connected FROM codex_settings ORDER BY machine_id`, "1::0", "2:/opt/codex:1")
	mustFail(t, pool, "a second row for one machine", `INSERT INTO codex_settings(machine_id) VALUES(1)`)
	mustFail(t, pool, "connected outside 0/1", `UPDATE codex_settings SET connected=2 WHERE machine_id=1`)
	mustFail(t, pool, "an unknown machine", `INSERT INTO codex_settings(machine_id) VALUES(99)`)
}

// seedV0032Unused leaves the seeded T3 connection 1 untouched and mirrors a
// thread (the table 0032 drops).
func seedV0032Unused(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO threads(t3_thread_id,t3_project_id,status,created_at,updated_at) VALUES('th','p','idle','2026-01-01T00:00:00.000000Z','2026-01-01T00:00:00.000000Z')`)
}

// seedV0032 configures or references connection 1 with qs.
func seedV0032(qs ...string) func(*testing.T, *sqlite.DB) {
	return func(t *testing.T, pool *sqlite.DB) {
		for _, q := range qs {
			mustExec(t, pool, q)
		}
	}
}

// checkV0032Connection: connection 1 survives only when configured or
// referenced, and the threads mirror is dropped either way.
func checkV0032Connection(kept bool) func(*testing.T, *sqlite.DB) {
	return func(t *testing.T, pool *sqlite.DB) {
		want := "0"
		if kept {
			want = "1"
		}
		wantRows(t, pool, `SELECT count(*) FROM harness_connections WHERE id=1`, want)
		if tableExists(t, pool, "threads") {
			t.Fatal("threads not dropped")
		}
		if !kept {
			// A new connection takes a new id; 1 is never reissued.
			mustExec(t, pool, `INSERT INTO harness_connections(machine_id,harness) VALUES(1,'opencode')`)
			wantRows(t, pool, `SELECT id FROM harness_connections`, "2")
		}
	}
}

// launchOptionsSeed (0033): a connection per harness kind and tasks,
// schedules and unfinished attempts with each kind of saved workspace.
var launchOptionsSeed = []string{
	`INSERT INTO harness_connections(id,machine_id,harness) VALUES(2,1,'t3'),(3,1,'claude'),(4,1,'opencode'),(5,1,'codex')`,
	`INSERT INTO projects(id,title) VALUES(1,'P')`,
	`INSERT INTO task_trees(id,title) VALUES(1,'Stack')`,
	`INSERT INTO session_identities(id,connection_id,external_id) VALUES(1,2,'thread-a')`,
	`INSERT INTO tasks(id,project_id,title,prompt,connection_id,workspace,execution_config) VALUES
	 (1,1,'t3 ref','p',2,'{"type":"worktree","baseRef":"origin/main","branch":"feat-x","base":"ref"}','{"model":"m","options":[{"id":"effort","value":"high"}],"permission":"","mode":""}'),
	 (2,1,'claude worktree','p',3,'{"type":"worktree","baseRef":"","branch":null,"base":"ref"}',NULL),
	 (3,1,'opencode worktree','p',4,'{"type":"worktree"}','{"model":"","options":null,"permission":"","mode":""}'),
	 (4,1,'codex root','p',5,'{"type":"root"}','{"model":"","options":[],"permission":"","mode":""}'),
	 (5,1,'codex worktree','p',5,'{"type":"worktree"}','{"model":"","options":[],"permission":"","mode":""}'),
	 (8,1,'t3 root','p',2,'{"type":"root"}','{"model":"","options":[],"permission":"","mode":""}')`,
	`INSERT INTO tasks(id,project_id,title,prompt,connection_id,workspace,execution_config,tree_id,session_id,launch_attempt,state) VALUES
	 (6,1,'t3 predecessor','p',2,'{"type":"worktree","baseRef":"origin/main","branch":null,"base":"predecessor","baseTaskId":1}','{"model":"","options":[],"permission":"","mode":""}',1,1,2,'failed'),
	 (7,1,'claude predecessor','p',3,'{"type":"worktree","base":"predecessor"}','{"model":"","options":[],"permission":"","mode":""}',1,NULL,0,'backlog'),
	 (9,1,'t3 predecessor without ref','p',2,'{"type":"worktree","baseRef":"","branch":"stack-b","base":"predecessor"}','{"model":"","options":[],"permission":"","mode":""}',1,NULL,0,'backlog')`,
	`INSERT INTO tasks(id,project_id,title,prompt,connection_id,workspace,execution_config,state,archived_at) VALUES
	 (10,1,'claude predecessor done','p',3,'{"type":"worktree","base":"predecessor"}','{"model":"","options":[],"permission":"","mode":""}','done',NULL),
	 (11,1,'claude predecessor archived','p',3,'{"type":"worktree","base":"predecessor"}','{"model":"","options":[],"permission":"","mode":""}','backlog','2026-01-01 00:00:00')`,
	`INSERT INTO schedules(id,project_id,title,prompt,cron,tz,connection_id,workspace,execution_config) VALUES
	 (1,1,'t3 nightly','p','@daily','UTC',2,'{"type":"worktree","baseRef":"main","branch":null,"base":"ref"}','{"model":"","options":[],"permission":"","mode":""}'),
	 (2,1,'claude nightly','p','@daily','UTC',3,'{"type":"root"}','{"model":"","options":[],"permission":"","mode":""}')`,
	`INSERT INTO execution_attempts(task_id,attempt,phase,intent) VALUES
	 (6,2,'unconfirmed','{"requestId":"r6","connectionId":2,"config":{"model":"","options":[],"permission":"","mode":""},"workspace":{"kind":"worktree","base":"feat-a","fromRemote":false}}'),
	 (1,1,'confirmed','{"requestId":"r1","connectionId":2,"config":{"model":"","options":[],"permission":"","mode":""},"workspace":{"kind":"worktree","base":"origin/main","fromRemote":true}}'),
	 (2,1,'unconfirmed','{"requestId":"r2","connectionId":3,"config":{"model":"","options":null,"permission":"","mode":""},"workspace":{"kind":"worktree"}}')`,
}

// checkV0033LaunchOptions: each workspace became its harness's launch
// options (T3 keeps its base ref and branch, a predecessor's stored
// fallback included; Claude Code and OpenCode take a worktree; Codex and
// root nothing), the unfinished attempts' intents carry them, and each
// predecessor-based task that can still start has one journal event naming
// where it now starts (finished or archived history gets none).
func checkV0033LaunchOptions(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id||' '||COALESCE(json_extract(execution_config,'$.options'),'null') FROM tasks ORDER BY id`,
		`1 [{"id":"effort","value":"high"},{"id":"worktree","value":true},{"id":"baseRef","value":"origin/main"},{"id":"branch","value":"feat-x"}]`,
		`2 [{"id":"worktree","value":true}]`,
		`3 [{"id":"worktree","value":true}]`,
		`4 []`,
		`5 []`,
		`6 [{"id":"worktree","value":true},{"id":"baseRef","value":"origin/main"}]`,
		`7 [{"id":"worktree","value":true}]`,
		`8 []`,
		`9 [{"id":"worktree","value":true},{"id":"branch","value":"stack-b"}]`,
		`10 [{"id":"worktree","value":true}]`,
		`11 [{"id":"worktree","value":true}]`)
	wantRows(t, pool, `SELECT json_extract(execution_config,'$.model') FROM tasks WHERE id=1`, "m")
	wantRows(t, pool, `SELECT id||' '||json_extract(execution_config,'$.options') FROM schedules ORDER BY id`,
		`1 [{"id":"worktree","value":true},{"id":"baseRef","value":"main"}]`, `2 []`)
	wantRows(t, pool, `SELECT task_id||' '||COALESCE(json_extract(intent,'$.config.options'),'null') FROM execution_attempts ORDER BY task_id`,
		`1 []`,
		`2 [{"id":"worktree","value":true}]`,
		`6 [{"id":"worktree","value":true},{"id":"baseRef","value":"feat-a"}]`)
	wantRows(t, pool, `SELECT task_id||' '||COALESCE(tree_id,'-')||' '||COALESCE(source_session_id,'-')||' '||attempt||' '||kind||' '||event_type||' '||created_by_user_id FROM journal_entries ORDER BY id`,
		"6 1 1 2 event task_event 1", "7 1 - 0 event task_event 1", "9 1 - 0 event task_event 1")
	wantRows(t, pool, `SELECT task_id||' '||text FROM journal_entries ORDER BY id`, predecessorJournal...)
}

// predecessorJournal is 0033's journal event per predecessor-based task:
// a T3 base ref, a T3 task without one, Claude Code's harness default.
var predecessorJournal = []string{
	"6 Predecessor bases were removed: this task now starts its worktree from origin/main, not its predecessor's branch. The handoff carries the branch the predecessor reported.",
	"7 Predecessor bases were removed: this task no longer starts from its predecessor's branch. Its worktree now starts from the harness default. The handoff carries the branch the predecessor reported.",
	"9 Predecessor bases were removed: this task no longer starts from its predecessor's branch. Set a base ref before it starts (T3 needs one for a new worktree). The handoff carries the branch the predecessor reported.",
}

// launchBasesSeed (0034): a task and schedule with workspaces and a launch
// base record.
var launchBasesSeed = []string{
	`INSERT INTO harness_connections(id,machine_id,harness) VALUES(2,1,'t3')`,
	`INSERT INTO projects(id,title) VALUES(1,'P')`,
	`INSERT INTO tasks(id,project_id,title,prompt,connection_id,workspace,execution_config) VALUES(1,1,'T','p',2,'{"type":"worktree","baseRef":"main"}','{"model":"","options":[{"id":"worktree","value":true}],"permission":"","mode":""}')`,
	`INSERT INTO schedules(id,project_id,title,prompt,cron,tz,connection_id,workspace) VALUES(1,1,'S','p','@daily','UTC',2,'{"type":"root"}')`,
	`INSERT INTO task_launch_bases(task_id,attempt,base_ref,start_from_origin,source) VALUES(1,1,'main',0,'fallback')`,
}

// checkV0034DropWorkspaces: the workspace columns and launch bases are
// gone, rows and their execution configs stay.
func checkV0034DropWorkspaces(t *testing.T, pool *sqlite.DB) {
	if columnExists(t, pool, "tasks", "workspace") || columnExists(t, pool, "schedules", "workspace") || tableExists(t, pool, "task_launch_bases") {
		t.Fatal("workspace columns or task_launch_bases remain")
	}
	wantRows(t, pool, `SELECT id||' '||json_extract(execution_config,'$.options') FROM tasks`, `1 [{"id":"worktree","value":true}]`)
	wantRows(t, pool, `SELECT count(*) FROM schedules`, "1")
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id) VALUES(2,1,'New','p',2)`)
}

const v0035Evidence = `{ "threadId" : "legacy-1", "threadLink": "https://example.com/session", "executionState":"settled", "reply":"kept", "artifacts":[{"label":"old"}], "historicalSettledAt":"2026-11-03T00:00:00Z" }`
const v0035Request = `{ "requestId" : "original", "revision": 1, "note": "kept" }`

func seedV0035DropSettled(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,machine_id) VALUES(5,'t3',1)`)
	mustExec(t, pool, `INSERT INTO projects(id,title) VALUES(1,'P')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(6,5,'legacy-1')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,finished_at,settled_at,connection_id,session_id) VALUES
 (7,1,'Historical needle','p','settled','2026-11-01T00:00:00.000000Z','2026-11-03T00:00:00.000000Z',5,6),
 (8,1,'Reopened','p','settled',NULL,'2026-11-04T00:00:00.000000Z',5,NULL),
 (99,1,'Deleted','p','done',NULL,NULL,5,NULL)`)
	mustExec(t, pool, `DELETE FROM tasks WHERE id=99`)
	mustExec(t, pool, `INSERT INTO task_acceptances(id,task_id,provenance,evidence,source_session_id) VALUES
 (11,7,'historical_settlement',?,6),(12,8,'historical_settlement','{}',NULL)`, v0035Evidence)
	mustExec(t, pool, `UPDATE tasks SET acceptance_id=11,acceptance_revision=1 WHERE id=7`)
	mustExec(t, pool, `UPDATE tasks SET acceptance_revision=2 WHERE id=8`)
	mustExec(t, pool, `INSERT INTO acceptance_requests VALUES(7,'original',?,'{"revision":1,"acceptanceId":11}')`, v0035Request)
	mustExec(t, pool, `INSERT INTO task_events(id,task_id,from_state,to_state,detail) VALUES
 (1,7,'running','settled','original settled detail'),(2,8,'settled','running','original reopened detail')`)
	mustExec(t, pool, `INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(1,'note',7,'original settled note','n')`)
	mustExec(t, pool, `INSERT INTO notifications(id,event,task_id,title,url,session_id) VALUES
 (15,'settled',7,'legacy','/tasks/7',6),(98,'done',NULL,'deleted','/tasks/99',NULL)`)
	mustExec(t, pool, `DELETE FROM notifications WHERE id=98`)
	mustExec(t, pool, `UPDATE preferences SET default_connection_id=5, notify_events='["done","settled","needs_input","settled","ready","failed"]'`)
}

func checkV0035DropSettled(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	wantRows(t, pool, `SELECT id,state,finished_at,acceptance_id,acceptance_revision FROM tasks ORDER BY id`,
		"7|done|2026-11-01T00:00:00.000000Z|11|1", "8|done|2026-11-04T00:00:00.000000Z|NULL|2")
	if columnExists(t, pool, "tasks", "settled_at") {
		t.Fatal("tasks.settled_at still exists")
	}
	wantRows(t, pool, `SELECT task_id,from_state,to_state,detail FROM task_events ORDER BY id`,
		"7|running|done|original settled detail", "8|done|running|original reopened detail")
	wantRows(t, pool, `SELECT text FROM journal_entries WHERE id=1`, "original settled note")
	wantRows(t, pool, `SELECT id,event,task_id,session_id FROM notifications`, "15|done|7|6")
	wantRows(t, pool, `SELECT default_connection_id,notify_events FROM preferences`, `5|["done","needs_input","ready","failed"]`)
	wantRows(t, pool, `SELECT evidence FROM task_acceptances WHERE id=11`, v0035Evidence)
	wantRows(t, pool, `SELECT input FROM acceptance_requests WHERE request_id='original'`, v0035Request)
	for _, q := range []string{
		`INSERT INTO tasks(project_id,title,prompt,state,connection_id) VALUES(1,'T','p','settled',5)`,
		`INSERT INTO notifications(event,title,url) VALUES('settled','T','/tasks/7')`,
	} {
		if _, err := pool.Exec(q); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("%s: want CHECK violation, got %v", q, err)
		}
	}

	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH 'needle' ORDER BY rowid`, "7")
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,connection_id) VALUES(1,'New needle','p',5)`)
	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH 'needle' ORDER BY rowid`, "7", "100")
	mustExec(t, pool, `UPDATE tasks SET title='Updated' WHERE id=100`)
	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH 'updated'`, "100")
	mustExec(t, pool, `DELETE FROM tasks WHERE id=100`)
	wantRows(t, pool, `SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH 'updated'`)
	mustExec(t, pool, `INSERT INTO notifications(event,title,url) VALUES('done','New','/tasks/100')`)
	wantRows(t, pool, `SELECT max(id) FROM notifications`, "99")
	wantRows(t, pool, `PRAGMA foreign_keys`, "1")
	wantRows(t, pool, `PRAGMA foreign_key_check`)
}

func checkV0035EmptyPreferences(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT notify_events FROM preferences`, "[]")
}

func seedV0037ConnectionFacts(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `INSERT INTO harness_connections(id,machine_id,harness,identity,label) VALUES(42,1,'opencode','instance','Desk')`)
	mustExec(t, pool, `INSERT INTO projects(id,title) VALUES(1,'P')`)
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,connection_id,execution_config) VALUES(1,1,'T','p',42,'{"options":[{"id":"worktree","value":false}]}')`)
}

func checkV0037ConnectionFacts(t *testing.T, pool *sqlite.DB) {
	wantRows(t, pool, `SELECT id,identity,label,last_known_facts FROM harness_connections WHERE id=42`, "42|instance|Desk|NULL")
	wantRows(t, pool, `SELECT connection_id,json_extract(execution_config,'$.options[0].value') FROM tasks WHERE id=1`, "42|0")
}

func seedV0038ProjectIdentity(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	seedV0032(
		`INSERT INTO harness_connections(id,machine_id,harness) VALUES (42,1,'opencode'),(43,1,'t3')`,
		`INSERT INTO projects(id,title,archived_at,archive_owner) VALUES
		 (1,'User archived','2026-11-01T00:00:00.000000Z','user'),
		 (2,'Harness archived','2026-11-02T00:00:00.000000Z','harness'),
		 (3,'Open',NULL,NULL),(4,'Another',NULL,NULL)`,
		`INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,origin,active) VALUES
		 (1,1,42,'manual','/manual','manual',TRUE),(2,2,42,'imported','/imported','imported',TRUE),
		 (3,3,42,'path','/path','path',TRUE),(4,4,42,'remote','/remote','remote',TRUE),
		 (5,1,42,'manual','/old','path',FALSE)`,
		`INSERT INTO external_projects(connection_id,external_id,title,workspace,available,repo,repo_path,declined_at) VALUES
		 (42,'manual','M','/manual',TRUE,'Example.org/Org/RePo','.',NULL),
		 (42,'manual','M','/old',FALSE,'','',NULL),
		 (42,'imported','I','/imported',TRUE,'','',NULL),(42,'path','P','/path',TRUE,'','',NULL),
		 (42,'remote','R','/remote',TRUE,'','',NULL),
		 (42,'declined','D','/d',TRUE,'EXAMPLE.org/A%#B','Dir%#X','2026-11-01T00:00:00.000000Z'),
		 (42,'declined','D','/other',TRUE,'','', '2026-11-02T00:00:00.000000Z'),
		 (42,'second','D','/d2',TRUE,'EXAMPLE.org/A%#B','Dir%#X','2026-11-03T00:00:00.000000Z'),
		 (42,'pathless','D','',TRUE,'EXAMPLE.org/Pathless','.','2026-11-02T00:00:00.000000Z'),
		 (42,'unknown-subpath','D','/unknown',TRUE,'EXAMPLE.org/Unknown','','2026-11-02T00:00:00.000000Z'),
		 (43,'declined','D','/d',TRUE,'EXAMPLE.org/A%#B','Dir%#X','2026-11-01T00:00:00.000000Z')`,
		`INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES(42,'/h','/s')`,
		`INSERT INTO tasks(id,project_id,title,prompt,connection_id) VALUES(1,1,'T','p',42)`,
		`INSERT INTO schedules(id,project_id,title,prompt,cron,tz,connection_id) VALUES(1,1,'S','p','0 0 * * *','UTC',42)`,
	)(t, pool)
}

func checkV0038ProjectIdentity(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	wantRows(t, pool, `SELECT id || '|' || rule || '|' || CASE WHEN is_primary THEN 'primary' ELSE 'secondary' END
	 FROM project_bindings ORDER BY id`,
		"1|manual|primary", "2|new|primary", "3|local|primary", "4|remote|primary", "5|local|secondary")
	wantRows(t, pool, `SELECT external_id || '|' || workspace || '|' || resolution || '|' || resolution_rule
	 FROM external_projects WHERE external_id IN ('manual','imported','path','remote') ORDER BY external_id,workspace`,
		"imported|/imported|bound|existing", "manual|/manual|bound|existing", "manual|/old||", "path|/path|bound|existing", "remote|/remote|bound|existing")
	wantRows(t, pool, `SELECT repo || '|' || repo_path FROM external_projects WHERE workspace='/manual'`, "example.org/org/repo|.")
	wantRows(t, pool, `SELECT 'legacy' FROM external_projects WHERE workspace='/manual' AND file_host IS NULL
	 AND canonical_path='' AND checkout_key='' AND inode_hint='' AND evidence_at IS NULL AND evidence_source=''
	 AND resolution_reason='' AND candidates='[]'`, "legacy")
	wantRows(t, pool, `SELECT id || '' FROM projects WHERE archived_at = '2026-11-01T00:00:00.000000Z'
	 OR archived_at = '2026-11-02T00:00:00.000000Z' ORDER BY id`, "1", "2")
	wantRows(t, pool, `SELECT id || '' FROM projects WHERE archived_at IS NULL ORDER BY id`, "3", "4")
	wantRows(t, pool, `SELECT connection_id || '|' || identity_key || '|' ||
	 CASE declined_at WHEN '2026-11-01T00:00:00.000000Z' THEN '1' WHEN '2026-11-02T00:00:00.000000Z' THEN '2'
	 WHEN '2026-11-03T00:00:00.000000Z' THEN '3' ELSE '?' END
	 FROM workspace_declines ORDER BY connection_id,identity_key`,
		"42|external:42:declined|2", "42|external:42:pathless|2", "42|external:42:second|3",
		"42|external:42:unknown-subpath|2", "42|remote:example.org/a%25%23b#Dir%25%23X|3",
		"43|external:43:declined|1", "43|remote:example.org/a%25%23b#Dir%25%23X|1")

	if tableExists(t, pool, "shared_paths") || indexExists(t, pool, "project_binding_active") {
		t.Fatal("legacy shared paths or active-binding index remains")
	}
	for table, column := range map[string]string{"projects": "archive_owner", "project_bindings": "origin", "external_projects": "declined_at"} {
		if columnExists(t, pool, table, column) {
			t.Errorf("legacy column %s.%s remains", table, column)
		}
	}

	bindingsUnset := `SELECT 'task' FROM tasks WHERE id=1 AND binding_id IS NULL
	 UNION ALL SELECT 'schedule' FROM schedules WHERE id=1 AND binding_id IS NULL ORDER BY 1`
	wantRows(t, pool, bindingsUnset, "schedule", "task")
	seedV0032(
		`INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace) VALUES (6,1,42,'extra','/extra')`,
		`INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,is_primary,active)
		 VALUES (7,1,42,'inactive','/inactive',TRUE,FALSE),(8,1,43,'extra','/extra',TRUE,TRUE)`,
		`UPDATE tasks SET binding_id=6 WHERE id=1`,
		`UPDATE schedules SET binding_id=6 WHERE id=1`,
	)(t, pool)
	wantRows(t, pool, `SELECT id || '' FROM project_bindings WHERE project_id=1 AND connection_id=42 AND active ORDER BY id`, "1", "6")
	wantRows(t, pool, `SELECT rule FROM project_bindings WHERE id=6 AND NOT is_primary`, "manual")

	mustFail(t, pool, "second active primary", `UPDATE project_bindings SET is_primary=TRUE WHERE id=6`)
	mustFail(t, pool, "duplicate active workspace", `INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace) VALUES (9,2,42,'extra','/extra')`)
	mustFail(t, pool, "unknown file host", `UPDATE external_projects SET file_host=999 WHERE workspace='/manual'`)
	mustFail(t, pool, "candidates must be an array", `UPDATE external_projects SET candidates='{}' WHERE workspace='/manual'`)
	mustFail(t, pool, "invalid dismissal pair", `INSERT INTO project_merge_dismissals(project_id,other_id) VALUES (2,1)`)
	mustFail(t, pool, "invalid task binding", `UPDATE tasks SET binding_id=999 WHERE id=1`)
	mustFail(t, pool, "invalid schedule binding", `UPDATE schedules SET binding_id=999 WHERE id=1`)

	seedV0032(`DELETE FROM project_bindings WHERE id=6`)(t, pool)
	wantRows(t, pool, bindingsUnset, "schedule", "task")
}

func checkV0039ObservationProducers(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	wantRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_producers`, "0")
	wantRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_dispositions`, "0")
	for _, q := range []string{
		`INSERT INTO machines (id, label) VALUES (2, 'Worker')`,
		`INSERT INTO observation_producers (machine_id, producer, active, cursor) VALUES (2, 'old', false, 1)`,
		`INSERT INTO observation_producers (machine_id, producer) VALUES (2, 'current')`,
		`INSERT INTO observation_dispositions (machine_id, producer, cursor, code, lost) VALUES (2, 'old', 1, 'invalid_record', '0')`,
	} {
		if _, err := pool.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO observation_producers (machine_id, producer) VALUES (2, 'second-current')`,
		`INSERT INTO observation_dispositions (machine_id, producer, cursor, code, lost) VALUES (1, 'old', 1, 'invalid_record', '0')`,
	} {
		if _, err := pool.Exec(q); err == nil {
			t.Fatalf("expected constraint failure: %s", q)
		}
	}
	q := `DELETE FROM machines WHERE id=2`
	if _, err := pool.Exec(q); err != nil {
		t.Fatal(err)
	}
	wantRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_producers`, "0")
	wantRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_dispositions`, "0")
}
