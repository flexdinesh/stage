//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/dbtest/migrationfixture"
)

// migrationStep is one upgrade scenario for a Postgres migration: seed runs
// on a database at the previous version (nil: nothing to seed; the baseline
// starts empty), check right after the migration.
type migrationStep struct {
	name        string
	seed, check func(t *testing.T, pool *sql.DB)
}

// stepwiseMigrations holds every Postgres migration's upgrade scenarios,
// from the baseline (0028) on. A new migration adds its entry next to the
// SQLite one (docs/storage.md "Adding a migration").
var stepwiseMigrations = map[int64][]migrationStep{
	28: {{name: "baseline", check: checkBaseline}},
	29: {{name: "search indexes", seed: seedV0029Search, check: checkV0029Search}},
	30: {{name: "claude settings table", seed: seedV0030ClaudeSettings, check: checkV0030ClaudeSettings}},
	31: {{name: "codex settings", seed: seedV0031CodexSettings, check: checkV0031CodexSettings}},
	32: {
		{name: "unused seeded connection removed", seed: seedV0032(`INSERT INTO threads (t3_thread_id, t3_project_id, status, created_at, updated_at) VALUES ('th', 'p', 'idle', now(), now())`), check: checkV0032Connection(false)},
		{name: "paired connection kept", seed: seedV0032(`UPDATE t3_connection SET token = 'tok' WHERE machine_id = 1`), check: checkV0032Connection(true)},
		{name: "stamped connection kept", seed: seedV0032(`UPDATE harness_connections SET identity = 'env-1' WHERE id = 1`), check: checkV0032Connection(true)},
		{name: "default connection kept", seed: seedV0032(`UPDATE preferences SET default_connection_id = 1`), check: checkV0032Connection(true)},
		{name: "referenced connection kept", seed: seedV0032(`INSERT INTO projects (id, title) VALUES (1, 'P')`, `INSERT INTO tasks (id, project_id, title, prompt, connection_id) VALUES (1, 1, 'T', 'p', 1)`), check: checkV0032Connection(true)},
		{name: "bound connection kept", seed: seedV0032(`INSERT INTO projects (id, title) VALUES (1, 'P')`, `INSERT INTO project_bindings (project_id, connection_id, external_project_id, workspace) VALUES (1, 1, 'x', '/w')`), check: checkV0032Connection(true)},
	},
	33: {{name: "workspaces become launch options", seed: seedV0032(launchOptionsSeed...), check: checkV0033LaunchOptions}},
	34: {{name: "workspace columns and launch bases dropped", seed: seedV0032(launchBasesSeed...), check: checkV0034DropWorkspaces}},
	35: {
		{name: "legacy states and preference entries removed", seed: seedV0035DropSettled, check: checkV0035DropSettled},
		{name: "empty preference filter retained", seed: seedV0032(`UPDATE preferences SET notify_events='["settled"]'`), check: checkV0035EmptyPreferences},
	},
	36: {
		{name: "settings, secrets and status copied", seed: seedV0036HarnessSetup, check: checkV0036HarnessSetup},
		{name: "unconfigured defaults retained", check: checkV0036HarnessSetupDefaults},
	},
	37: {{name: "existing connection and task evidence retained", seed: seedV0037ConnectionFacts, check: checkV0037ConnectionFacts}},
	39: {{name: "observation producer history", check: checkV0039ObservationProducers}},
	38: {{name: "identity history and workspace bindings", seed: seedV0038ProjectIdentity, check: checkV0038ProjectIdentity}},
	40: {{name: "legacy identity and evidence retained", seed: seedV0040SharedAccess, check: checkV0040SharedAccess}},
	41: {{name: "shared accounts kept and audit starts empty", seed: seedV0041AccessAudit, check: checkV0041AccessAudit}},
	43: {
		{name: "personal reads and devices retained", seed: seedV0043NotificationRecipients, check: checkV0043NotificationRecipients},
		{name: "shared legacy devices removed", seed: seedV0043SharedPush, check: checkV0043SharedPush},
	},
	44: {{name: "work attribution leaves old evidence untouched", seed: seedV0044Attribution, check: checkV0044Attribution}},
	45: {{name: "existing connection has unknown workspace freshness", seed: seedV0037ConnectionFacts, check: checkV0045WorkspaceRefresh}},
	46: {{name: "existing workspace has no historical project", seed: seedV0046HistoricalWorkspaceProject, check: checkV0046HistoricalWorkspaceProject}},
	47: {{name: "backfill proven workspace pins and preserve attempts", seed: seedV0047WorkspacePins, check: checkV0047WorkspacePins}},
	48: {{name: "proven group memberships and historical work retained", seed: seedProjectGroups, check: checkProjectGroups}},
	49: {{name: "project events keep history and accept workspace_added", seed: seedV0049ProjectEvents, check: checkV0049ProjectEvents}},
	50: {{name: "existing workspace is not blocked until refreshed", seed: seedV0046HistoricalWorkspaceProject, check: checkV0050BlockedReason}},
	51: {{name: "existing task starts at revision 0, unanswered", seed: seedV0040SharedAccess, check: checkV0051StateRevision}},
	52: {{name: "one active steerer per tree and per task", seed: seedV0040SharedAccess, check: checkV0052TreeSteering}},
}

func checkV0051StateRevision(t *testing.T, pool *sql.DB) {
	wantPGRows(t, pool, `SELECT id::text || '|' || state_revision::text || '|' || COALESCE(answered_revision::text,'NULL') FROM tasks WHERE id=6`, "6|0|NULL")
}

func checkV0052TreeSteering(t *testing.T, pool *sql.DB) {
	t.Helper()
	exec := func(q string) {
		t.Helper()
		if _, err := pool.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO task_trees(id,title) VALUES(9,'Steered'),(10,'Other')`)
	exec(`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(9,6,'r1','d')`)
	for _, q := range []string{
		`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(9,NULL,'r2','d')`,
		`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(10,6,'r3','d')`,
		`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest,ended_at,end_reason) VALUES(10,6,'r4','d',now(),'unknown')`,
	} {
		if _, err := pool.Exec(q); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
	exec(`UPDATE tree_steerers SET ended_at=now(), end_reason='replaced' WHERE request_id='r1'`)
	exec(`INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest) VALUES(9,6,'r2','d')`)
	exec(`INSERT INTO steering_events(tree_id,steerer_id,kind,task_id) VALUES(9,(SELECT id FROM tree_steerers WHERE request_id='r1'),'steering_started',6)`)
	wantPGRows(t, pool, `SELECT kind || '|' || payload::text || '|' || COALESCE(caused_by_task_id::text,'NULL') FROM steering_events`, "steering_started|{}|NULL")
}

func seedV0049ProjectEvents(t *testing.T, pool *sql.DB) {
	seedV0032(`INSERT INTO projects(id,title) VALUES(7,'Events')`,
		`INSERT INTO project_events(project_id,type,text) VALUES(7,'workspace_moved','Moved.'),(7,'project_merged','Merged.')`)(t, pool)
}

func checkV0049ProjectEvents(t *testing.T, pool *sql.DB) {
	t.Helper()
	wantPGRows(t, pool, `SELECT type || '|' || text || '|' || created_by_user_id || '|' || COALESCE(attribution,'NULL')
 FROM project_events ORDER BY id`, "workspace_moved|Moved.|1|NULL", "project_merged|Merged.|1|NULL")
	seedV0032(`INSERT INTO project_events(project_id,type,text,attribution) VALUES(7,'workspace_added','Added.','{"actor":{"kind":"agent"}}')`)(t, pool)
	if _, err := pool.ExecContext(context.Background(), `INSERT INTO project_events(project_id,type,text) VALUES(7,'unknown','x')`); err == nil {
		t.Fatal("project event type check accepted an unknown type")
	}
}

func checkV0050BlockedReason(t *testing.T, pool *sql.DB) {
	wantPGRows(t, pool, `SELECT external_id || '|' || available::text || '|' || blocked_reason
 FROM external_projects WHERE connection_id=42`, "ext|true|")
}

func seedV0046HistoricalWorkspaceProject(t *testing.T, pool *sql.DB) {
	seedV0037ConnectionFacts(t, pool)
	seedV0032(`INSERT INTO external_projects(connection_id,external_id,title,workspace,available) VALUES(42,'ext','Workspace','/w',true)`)(t, pool)
}

func checkV0046HistoricalWorkspaceProject(t *testing.T, pool *sql.DB) {
	wantPGRows(t, pool, `SELECT external_id || '|' || workspace || '|' || COALESCE(historical_project_id::text,'NULL')
 FROM external_projects WHERE connection_id=42`, "ext|/w|NULL")
}

func seedV0047WorkspacePins(t *testing.T, pool *sql.DB) {
	t.Helper()
	seedV0032(
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
	)(t, pool)
}

func checkV0047WorkspacePins(t *testing.T, pool *sql.DB) {
	t.Helper()
	want := []string{
		"1|shared|/primary", "2|shared|/offline", "3|NULL|NULL", "4|NULL|NULL",
		"5|NULL|NULL", "6|NULL|NULL", "7|NULL|NULL", "8|NULL|NULL",
		"9|NULL|NULL", "10|pathless|", "11|NULL|NULL", "12|shared|/primary",
	}
	const pins = `SELECT id || '|' || COALESCE(workspace_pin->>'externalId','NULL')
		|| '|' || COALESCE(workspace_pin->>'root','NULL') FROM `
	wantPGRows(t, pool, pins+`schedules ORDER BY id`, want...)
	wantPGRows(t, pool, pins+`tasks ORDER BY id`, append(want, "13|historic|/historic")...)

	seedV0032(`INSERT INTO task_launch_destinations
		 (task_id,attempt,connection_id,external_project_id,workspace,binding_revision)
		 VALUES (13,2,2,'unbound','/unbound',0)`)(t, pool)
	wantPGRows(t, pool, `SELECT task_id || '|' || attempt || '|' || connection_id || '|' || external_project_id
	 || '|' || workspace || '|' || COALESCE(binding_id::text,'NULL') || '|' || binding_revision
	FROM task_launch_destinations ORDER BY attempt`, "13|1|2|historic|/historic|1|7", "13|2|2|unbound|/unbound|NULL|0")
}

func seedProjectGroups(t *testing.T, pool *sql.DB) {
	migrationfixture.SeedProjectGroups(t, func(query string) error {
		_, err := pool.ExecContext(t.Context(), query)
		return err
	})
}

func checkProjectGroups(t *testing.T, pool *sql.DB) {
	migrationfixture.CheckProjectGroups(t, func(query string) (int, error) {
		var count int
		err := pool.QueryRowContext(t.Context(), query).Scan(&count)
		return count, err
	})
}

func checkV0045WorkspaceRefresh(t *testing.T, pool *sql.DB) {
	wantPGRows(t, pool, `SELECT id || '|' || identity || '|' || COALESCE(workspace_refreshed_at::text,'NULL')
	 || '|' || COALESCE(workspace_refresh_error,'NULL') FROM harness_connections WHERE id=42`, "42|instance|NULL|NULL")
}

func seedV0043NotificationRecipients(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`UPDATE preferences SET notify_events='["ready"]' WHERE id=1`,
		`INSERT INTO users(id, display_name, email, email_key) VALUES (2, 'Member', 'member@example.test', 'member@example.test')`,
		`INSERT INTO notifications(id, event, title, url, read_at) VALUES
		 (1, 'done', 'Read', '/', '2026-11-27T00:00:00.123456Z'), (2, 'ready', 'Unread', '/', NULL)`,
		`INSERT INTO push_subscriptions(endpoint, p256dh, auth) VALUES ('https://push.example/old', 'k', 'a')`,
	} {
		if _, err := pool.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func checkV0043NotificationRecipients(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`SELECT count(*) = 2 FROM notification_recipients`,
		`SELECT count(*) = 1 FROM notification_recipients WHERE notification_id=1 AND user_id=1 AND read_at='2026-11-27T00:00:00.123456Z'`,
		`SELECT count(*) = 1 FROM notification_recipients WHERE notification_id=2 AND user_id=1 AND read_at IS NULL`,
		`SELECT count(*) = 1 FROM notification_preferences`,
		`SELECT count(*) = 1 FROM notification_preferences WHERE user_id=1 AND notify_events='["ready"]'`,
		`SELECT count(*) = 1 FROM push_subscriptions WHERE endpoint='https://push.example/old' AND auth_session_id IS NULL AND binding IS NULL`,
	} {
		var ok bool
		if err := pool.QueryRowContext(t.Context(), q).Scan(&ok); err != nil || !ok {
			t.Fatalf("%s: %t, %v", q, ok, err)
		}
	}
	if _, err := pool.ExecContext(t.Context(), `DELETE FROM notifications WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := pool.QueryRowContext(t.Context(), `SELECT notification_id FROM notification_recipients`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("remaining receipt: %d, %v", remaining, err)
	}
}

func seedV0043SharedPush(t *testing.T, pool *sql.DB) {
	seedV0043NotificationRecipients(t, pool)
	if _, err := pool.ExecContext(t.Context(), `UPDATE installation_access SET installation_id='shared', mode='setup_pending' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
}

func checkV0043SharedPush(t *testing.T, pool *sql.DB) {
	var count int
	if err := pool.QueryRowContext(t.Context(), `SELECT count(*) FROM push_subscriptions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy device count %d, %v", count, err)
	}
}

func seedV0044Attribution(t *testing.T, pool *sql.DB) {
	t.Helper()
	seedV0040SharedAccess(t, pool)
	for _, q := range []string{
		`INSERT INTO task_events(task_id,to_state,detail) VALUES(6,'backlog','created')`,
		`INSERT INTO task_acceptances(task_id,provenance,evidence,note) VALUES(6,'user','{ "taskId": 6 }','old result')`,
	} {
		if _, err := pool.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func checkV0044Attribution(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`SELECT count(*) FROM task_events WHERE task_id=6 AND detail='created' AND attribution IS NULL`,
		`SELECT count(*) FROM journal_entries WHERE id=8 AND text='old evidence  ✓' AND created_by_user_id=7 AND attribution IS NULL`,
		`SELECT count(*) FROM task_acceptances WHERE task_id=6 AND evidence='{ "taskId": 6 }' AND note='old result' AND attribution IS NULL`,
	} {
		var got int
		if err := pool.QueryRowContext(t.Context(), q).Scan(&got); err != nil || got != 1 {
			t.Fatalf("%s: %d, %v; want 1", q, got, err)
		}
	}
}

func seedV0041AccessAudit(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`UPDATE users SET display_name = 'Owner', email = 'Owner@example.test', email_key = 'owner@example.test' WHERE id = 1`,
		`UPDATE installation_access SET installation_id = 'existing', mode = 'claimed', claimed_at = '2026-11-27T00:00:00.000000Z'`,
		`INSERT INTO identities(user_id, provider, subject) VALUES (1, 'local', 'user:1')`,
		`INSERT INTO local_credentials(identity_id, password_verifier) SELECT id, 'existing verifier' FROM identities WHERE provider = 'local'`,
	} {
		if _, err := pool.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func checkV0041AccessAudit(t *testing.T, pool *sql.DB) {
	t.Helper()
	for q, want := range map[string]int{
		`SELECT count(*) FROM installation_access WHERE mode = 'claimed' AND installation_id = 'existing'`: 1,
		`SELECT count(*) FROM local_credentials WHERE password_verifier = 'existing verifier'`:             1,
		`SELECT count(*) FROM access_audit`: 0,
	} {
		var got int
		if err := pool.QueryRowContext(t.Context(), q).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d, %v; want %d", q, got, err, want)
		}
	}
}

func seedV0040SharedAccess(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO users(id, display_name) VALUES (7, 'Existing user')`,
		`INSERT INTO identities(id, user_id, provider, subject) VALUES (9, 7, 'legacy', 'existing-subject')`,
		`INSERT INTO projects(id, title) VALUES (4, 'Existing project')`,
		`INSERT INTO harness_connections(id, machine_id, harness) VALUES (4, 1, 't3')`,
		`INSERT INTO tasks(id, project_id, title, prompt, connection_id) VALUES (6, 4, 'Existing task', 'prompt', 4)`,
		`INSERT INTO journal_entries(id, kind, task_id, text, created_by_user_id, request_id) VALUES (8, 'note', 6, 'old evidence  ✓', 7, 'old-note')`,
	} {
		if _, err := pool.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func checkV0040SharedAccess(t *testing.T, pool *sql.DB) {
	t.Helper()
	for label, q := range map[string]string{
		"implicit admin":     `SELECT count(*) FROM users WHERE id = 1 AND display_name = 'Default user' AND role = 'admin' AND status = 'active' AND email IS NULL`,
		"legacy identity":    `SELECT count(*) FROM identities i JOIN users u ON u.id = i.user_id WHERE i.id = 9 AND u.id = 7 AND i.subject = 'existing-subject' AND u.role = 'member'`,
		"historical creator": `SELECT count(*) FROM tasks WHERE id = 6 AND created_by_user_id = 1`,
		"journal evidence":   `SELECT count(*) FROM journal_entries WHERE id = 8 AND text = 'old evidence  ✓' AND created_by_user_id = 7`,
		"personal state":     `SELECT count(*) FROM installation_access WHERE mode = 'personal' AND claimed_at IS NULL`,
	} {
		var count int
		if err := pool.QueryRowContext(context.Background(), q).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s: count %d, err %v", label, count, err)
		}
	}
	q := `DELETE FROM users WHERE id = 7`
	if _, err := pool.ExecContext(context.Background(), q); err == nil {
		t.Fatal("removed referenced historical creator")
	}
}

func seedV0029Search(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO projects (id, title) VALUES (1, 'P')`,
		`INSERT INTO tasks (id, project_id, title, prompt) VALUES (1, 1, 'Needle title', 'p'), (2, 1, 't', 'Привет prompt')`,
		`INSERT INTO journal_entries (id, kind, task_id, text, request_id) VALUES (901, 'note', 1, 'Mätch note', 'k')`,
	} {
		if _, err := pool.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// checkV0029Search: pg_trgm and the GIN indexes exist, and existing rows
// match case-insensitively, non-ASCII too.
func checkV0029Search(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var ext, indexes int
	if err := pool.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'),
 (SELECT count(*) FROM pg_indexes WHERE indexname IN ('tasks_title_trgm', 'tasks_prompt_trgm', 'journal_entries_text_trgm') AND indexdef LIKE '%USING gin%gin_trgm_ops%')`).Scan(&ext, &indexes); err != nil {
		t.Fatal(err)
	}
	if ext != 1 || indexes != 3 {
		t.Fatalf("pg_trgm %d, trigram indexes %d", ext, indexes)
	}
	for q, want := range map[string]int64{
		`SELECT id FROM tasks WHERE title ILIKE '%NEEDLE%'`:         1,
		`SELECT id FROM tasks WHERE prompt ILIKE '%ПРИВЕТ%'`:        2,
		`SELECT id FROM journal_entries WHERE text ILIKE '%MÄTCH%'`: 901,
	} {
		var id int64
		if err := pool.QueryRowContext(ctx, q).Scan(&id); err != nil || id != want {
			t.Fatalf("%s: %d %v, want %d", q, id, err, want)
		}
	}
}

// checkBaseline: the baseline creates the schema with SQLite's seeded rows
// and IDs; identity sequences continue after them, and native constraints
// hold.
func checkBaseline(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var users, machines, conns, prefs, t3, opencode int
	if err := pool.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM users WHERE id = 1 AND display_name = 'Default user'),
 (SELECT count(*) FROM machines WHERE id = 1),
 (SELECT count(*) FROM harness_connections WHERE id = 1 AND machine_id = 1 AND harness = 't3' AND active AND stable_identity),
 (SELECT count(*) FROM preferences WHERE id = 1 AND notify_events = '["needs_input","failed","done","settled","ready"]'::jsonb),
 (SELECT count(*) FROM t3_connection WHERE machine_id = 1),
 (SELECT count(*) FROM opencode_settings WHERE machine_id = 1 AND auth = 'none')`).Scan(&users, &machines, &conns, &prefs, &t3, &opencode); err != nil {
		t.Fatal(err)
	}
	if users != 1 || machines != 1 || conns != 1 || prefs != 1 || t3 != 1 || opencode != 1 {
		t.Fatalf("seeded rows: users %d machines %d connections %d preferences %d t3 %d opencode %d", users, machines, conns, prefs, t3, opencode)
	}
	for _, q := range []string{
		`INSERT INTO users (display_name) VALUES ('second') RETURNING id`,
		`INSERT INTO machines (label) VALUES ('second') RETURNING id`,
		`INSERT INTO harness_connections (machine_id, harness, active, retired_at) VALUES (1, 't3', false, now()) RETURNING id`,
	} {
		var id int64
		if err := pool.QueryRowContext(ctx, q).Scan(&id); err != nil || id != 2 {
			t.Fatalf("%s: id %d %v, want 2", q, id, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO harness_connections (machine_id, harness) VALUES (1, 't3')`,  // a second active t3 on the machine
		`INSERT INTO machine_tokens (machine_id, secret_hash) VALUES (2, '\x00')`, // not 32 bytes
		`INSERT INTO tasks (project_id, title, prompt) VALUES (999, 't', 'p')`,    // unknown project
		`INSERT INTO task_acceptances (task_id, provenance, evidence) VALUES (1, 'user', 'not json')`,
	} {
		if _, err := pool.ExecContext(ctx, q); err == nil {
			t.Fatalf("%s: accepted, want a constraint violation", q)
		}
	}
}

// TestStepwiseMigrations: for every Postgres version v from the baseline, a
// fresh database at the previous version, seeded, migrated to v and checked.
func TestStepwiseMigrations(t *testing.T) {
	versions := Versions()
	for _, v := range slices.Sorted(maps.Keys(stepwiseMigrations)) {
		if !slices.Contains(versions, v) {
			t.Errorf("step-wise entry for unknown migration %d", v)
		}
	}
	if len(versions) == 0 {
		t.Fatal("no Postgres migrations embedded: the baseline is missing")
	}
	ctx := context.Background()
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
				pool := freshDB(t)
				if prev > 0 {
					if err := MigrateTo(ctx, pool, prev); err != nil {
						t.Fatal(err)
					}
				}
				if st.seed != nil {
					st.seed(t, pool)
				}
				if err := MigrateTo(ctx, pool, v); err != nil {
					t.Fatal(err)
				}
				st.check(t, pool)
			})
		}
	}
}

func seedV0030ClaudeSettings(t *testing.T, pool *sql.DB) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), `INSERT INTO machines (id, label) VALUES (2, 'worker')`); err != nil {
		t.Fatal(err)
	}
}

// checkV0030ClaudeSettings: the table starts empty (no row reads the
// defaults), defaults fill an inserted row, and the machine key holds.
func checkV0030ClaudeSettings(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var n int
	if err := pool.QueryRowContext(ctx, `SELECT count(*) FROM claude_settings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows %d %v, want 0", n, err)
	}
	for _, q := range []string{
		`INSERT INTO claude_settings (machine_id) VALUES (1)`,
		`INSERT INTO claude_settings (machine_id, binary_path, disconnected) VALUES (2, '/bin/claude', true)`,
	} {
		if _, err := pool.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var bin string
	var disconnected, stamped bool
	if err := pool.QueryRowContext(ctx, `SELECT binary_path, disconnected, updated_at IS NOT NULL FROM claude_settings WHERE machine_id = 1`).Scan(&bin, &disconnected, &stamped); err != nil {
		t.Fatal(err)
	}
	if bin != "" || disconnected || !stamped {
		t.Fatalf("defaults %q %v %v", bin, disconnected, stamped)
	}
	for why, q := range map[string]string{
		"unknown machine":   `INSERT INTO claude_settings (machine_id) VALUES (9)`,
		"duplicate machine": `INSERT INTO claude_settings (machine_id) VALUES (1)`,
	} {
		if _, err := pool.ExecContext(ctx, q); err == nil {
			t.Fatalf("%s: %s accepted", why, q)
		}
	}
}

func seedV0031CodexSettings(t *testing.T, pool *sql.DB) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), `INSERT INTO machines (id, label) VALUES (2, 'worker') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
}

// checkV0031CodexSettings: one row per existing machine, defaults never
// connected, and a duplicate or unknown machine is refused.
func checkV0031CodexSettings(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO codex_settings (machine_id) VALUES (1)`,
		`INSERT INTO codex_settings (machine_id, binary_path, connected) VALUES (2, '/opt/codex', true)`,
	} {
		if _, err := pool.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var got string
	if err := pool.QueryRowContext(ctx, `SELECT string_agg(machine_id || ':' || binary_path || ':' || connected, ',' ORDER BY machine_id) FROM codex_settings`).Scan(&got); err != nil || got != "1::false,2:/opt/codex:true" {
		t.Fatalf("codex_settings %q %v", got, err)
	}
	for _, q := range []string{
		`INSERT INTO codex_settings (machine_id) VALUES (1)`,
		`INSERT INTO codex_settings (machine_id) VALUES (99)`,
	} {
		if _, err := pool.ExecContext(ctx, q); err == nil {
			t.Fatalf("%s: accepted", q)
		}
	}
}

// seedV0032 configures, references or mirrors with qs.
func seedV0032(qs ...string) func(*testing.T, *sql.DB) {
	return func(t *testing.T, pool *sql.DB) {
		t.Helper()
		for _, q := range qs {
			if _, err := pool.ExecContext(context.Background(), q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
}

// checkV0032Connection: connection 1 survives only when configured or
// referenced, and the threads mirror is dropped either way.
func checkV0032Connection(kept bool) func(*testing.T, *sql.DB) {
	return func(t *testing.T, pool *sql.DB) {
		t.Helper()
		ctx := context.Background()
		var conns, threads int
		if err := pool.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM harness_connections WHERE id = 1),
 (SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'threads')`).Scan(&conns, &threads); err != nil {
			t.Fatal(err)
		}
		want := 0
		if kept {
			want = 1
		}
		if conns != want || threads != 0 {
			t.Fatalf("connection 1 rows %d (want %d), threads tables %d", conns, want, threads)
		}
		if !kept {
			// A new connection takes a new id; 1 is never reissued.
			var id int64
			if err := pool.QueryRowContext(ctx, `INSERT INTO harness_connections (machine_id, harness) VALUES (1, 'opencode') RETURNING id`).Scan(&id); err != nil || id != 2 {
				t.Fatalf("new connection id %d %v, want 2", id, err)
			}
		}
	}
}

// launchOptionsSeed (0033): a connection per harness kind and tasks,
// schedules and unfinished attempts with each kind of saved workspace (the
// SQLite scenario's rows).
var launchOptionsSeed = []string{
	`INSERT INTO harness_connections (id, machine_id, harness) VALUES (2, 1, 't3'), (3, 1, 'claude'), (4, 1, 'opencode'), (5, 1, 'codex')`,
	`INSERT INTO projects (id, title) VALUES (1, 'P')`,
	`INSERT INTO task_trees (id, title) VALUES (1, 'Stack')`,
	`INSERT INTO session_identities (id, connection_id, external_id) VALUES (1, 2, 'thread-a')`,
	`INSERT INTO tasks (id, project_id, title, prompt, connection_id, workspace, execution_config) VALUES
	 (1, 1, 't3 ref', 'p', 2, '{"type":"worktree","baseRef":"origin/main","branch":"feat-x","base":"ref"}', '{"model":"m","options":[{"id":"effort","value":"high"}],"permission":"","mode":""}'),
	 (2, 1, 'claude worktree', 'p', 3, '{"type":"worktree","baseRef":"","branch":null,"base":"ref"}', NULL),
	 (3, 1, 'opencode worktree', 'p', 4, '{"type":"worktree"}', '{"model":"","options":null,"permission":"","mode":""}'),
	 (4, 1, 'codex root', 'p', 5, '{"type":"root"}', '{"model":"","options":[],"permission":"","mode":""}'),
	 (5, 1, 'codex worktree', 'p', 5, '{"type":"worktree"}', '{"model":"","options":[],"permission":"","mode":""}'),
	 (8, 1, 't3 root', 'p', 2, '{"type":"root"}', '{"model":"","options":[],"permission":"","mode":""}')`,
	`INSERT INTO tasks (id, project_id, title, prompt, connection_id, workspace, execution_config, tree_id, session_id, launch_attempt, state) VALUES
	 (6, 1, 't3 predecessor', 'p', 2, '{"type":"worktree","baseRef":"origin/main","branch":null,"base":"predecessor","baseTaskId":1}', '{"model":"","options":[],"permission":"","mode":""}', 1, 1, 2, 'failed'),
	 (7, 1, 'claude predecessor', 'p', 3, '{"type":"worktree","base":"predecessor"}', '{"model":"","options":[],"permission":"","mode":""}', 1, NULL, 0, 'backlog'),
	 (9, 1, 't3 predecessor without ref', 'p', 2, '{"type":"worktree","baseRef":"","branch":"stack-b","base":"predecessor"}', '{"model":"","options":[],"permission":"","mode":""}', 1, NULL, 0, 'backlog')`,
	`INSERT INTO tasks (id, project_id, title, prompt, connection_id, workspace, execution_config, state, archived_at) VALUES
	 (10, 1, 'claude predecessor done', 'p', 3, '{"type":"worktree","base":"predecessor"}', '{"model":"","options":[],"permission":"","mode":""}', 'done', NULL),
	 (11, 1, 'claude predecessor archived', 'p', 3, '{"type":"worktree","base":"predecessor"}', '{"model":"","options":[],"permission":"","mode":""}', 'backlog', '2026-01-01 00:00:00+00')`,
	`INSERT INTO schedules (id, project_id, title, prompt, cron, tz, connection_id, workspace, execution_config) VALUES
	 (1, 1, 't3 nightly', 'p', '@daily', 'UTC', 2, '{"type":"worktree","baseRef":"main","branch":null,"base":"ref"}', '{"model":"","options":[],"permission":"","mode":""}'),
	 (2, 1, 'claude nightly', 'p', '@daily', 'UTC', 3, '{"type":"root"}', '{"model":"","options":[],"permission":"","mode":""}')`,
	`INSERT INTO execution_attempts (task_id, attempt, phase, intent) VALUES
	 (6, 2, 'unconfirmed', '{"requestId":"r6","connectionId":2,"config":{"model":"","options":[],"permission":"","mode":""},"workspace":{"kind":"worktree","base":"feat-a","fromRemote":false}}'),
	 (1, 1, 'confirmed', '{"requestId":"r1","connectionId":2,"config":{"model":"","options":[],"permission":"","mode":""},"workspace":{"kind":"worktree","base":"origin/main","fromRemote":true}}'),
	 (2, 1, 'unconfirmed', '{"requestId":"r2","connectionId":3,"config":{"model":"","options":null,"permission":"","mode":""},"workspace":{"kind":"worktree"}}')`,
}

// wantPGRows fails unless q (one text column) returns exactly want.
func wantPGRows(t *testing.T, pool *sql.DB, q string, want ...string) {
	t.Helper()
	rows, err := pool.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s:\n got  %q\n want %q", q, got, want)
	}
}

// checkV0033LaunchOptions: as on SQLite, each workspace became its
// harness's launch options, the unfinished attempts' intents carry them,
// and each predecessor-based task has one journal event.
func checkV0033LaunchOptions(t *testing.T, pool *sql.DB) {
	t.Helper()
	wantPGRows(t, pool, `SELECT id || ' ' || COALESCE(execution_config->'options', 'null')::text FROM tasks ORDER BY id`,
		`1 [{"id": "effort", "value": "high"}, {"id": "worktree", "value": true}, {"id": "baseRef", "value": "origin/main"}, {"id": "branch", "value": "feat-x"}]`,
		`2 [{"id": "worktree", "value": true}]`,
		`3 [{"id": "worktree", "value": true}]`,
		`4 []`,
		`5 []`,
		`6 [{"id": "worktree", "value": true}, {"id": "baseRef", "value": "origin/main"}]`,
		`7 [{"id": "worktree", "value": true}]`,
		`8 []`,
		`9 [{"id": "worktree", "value": true}, {"id": "branch", "value": "stack-b"}]`,
		`10 [{"id": "worktree", "value": true}]`,
		`11 [{"id": "worktree", "value": true}]`)
	wantPGRows(t, pool, `SELECT execution_config->>'model' FROM tasks WHERE id = 1`, "m")
	wantPGRows(t, pool, `SELECT id || ' ' || (execution_config->'options')::text FROM schedules ORDER BY id`,
		`1 [{"id": "worktree", "value": true}, {"id": "baseRef", "value": "main"}]`, `2 []`)
	wantPGRows(t, pool, `SELECT task_id || ' ' || COALESCE(intent->'config'->'options', 'null')::text FROM execution_attempts ORDER BY task_id`,
		`1 []`,
		`2 [{"id": "worktree", "value": true}]`,
		`6 [{"id": "worktree", "value": true}, {"id": "baseRef", "value": "feat-a"}]`)
	wantPGRows(t, pool, `SELECT task_id || ' ' || COALESCE(tree_id::text, '-') || ' ' || COALESCE(source_session_id::text, '-') || ' ' || attempt || ' ' || kind || ' ' || event_type || ' ' || created_by_user_id FROM journal_entries ORDER BY id`,
		"6 1 1 2 event task_event 1", "7 1 - 0 event task_event 1", "9 1 - 0 event task_event 1")
	wantPGRows(t, pool, `SELECT task_id || ' ' || text FROM journal_entries ORDER BY id`,
		"6 Predecessor bases were removed: this task now starts its worktree from origin/main, not its predecessor's branch. The handoff carries the branch the predecessor reported.",
		"7 Predecessor bases were removed: this task no longer starts from its predecessor's branch. Its worktree now starts from the harness default. The handoff carries the branch the predecessor reported.",
		"9 Predecessor bases were removed: this task no longer starts from its predecessor's branch. Set a base ref before it starts (T3 needs one for a new worktree). The handoff carries the branch the predecessor reported.")
}

// launchBasesSeed (0034): a task and schedule with workspaces and a launch
// base record.
var launchBasesSeed = []string{
	`INSERT INTO harness_connections (id, machine_id, harness) VALUES (2, 1, 't3')`,
	`INSERT INTO projects (id, title) VALUES (1, 'P')`,
	`INSERT INTO tasks (id, project_id, title, prompt, connection_id, workspace, execution_config) VALUES (1, 1, 'T', 'p', 2, '{"type":"worktree","baseRef":"main"}', '{"model":"","options":[{"id":"worktree","value":true}],"permission":"","mode":""}')`,
	`INSERT INTO schedules (id, project_id, title, prompt, cron, tz, connection_id, workspace) VALUES (1, 1, 'S', 'p', '@daily', 'UTC', 2, '{"type":"root"}')`,
	`INSERT INTO task_launch_bases (task_id, attempt, base_ref, start_from_origin, source) VALUES (1, 1, 'main', false, 'fallback')`,
}

// checkV0034DropWorkspaces: the workspace columns and launch bases are
// gone, rows and their execution configs stay.
func checkV0034DropWorkspaces(t *testing.T, pool *sql.DB) {
	t.Helper()
	wantPGRows(t, pool, `SELECT count(*)::text FROM information_schema.columns WHERE table_schema = current_schema() AND column_name = 'workspace' AND table_name IN ('tasks', 'schedules')`, "0")
	wantPGRows(t, pool, `SELECT count(*)::text FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'task_launch_bases'`, "0")
	wantPGRows(t, pool, `SELECT id || ' ' || (execution_config->'options')::text FROM tasks`, `1 [{"id": "worktree", "value": true}]`)
	wantPGRows(t, pool, `SELECT count(*)::text FROM schedules`, "1")
	seedV0032(`INSERT INTO tasks (id, project_id, title, prompt, connection_id) VALUES (2, 1, 'New', 'p', 2)`)(t, pool)
}

const v0035Evidence = `{ "threadId" : "legacy-1", "threadLink": "https://example.com/session", "executionState":"settled", "reply":"kept", "artifacts":[{"label":"old"}], "historicalSettledAt":"2026-11-03T00:00:00Z" }`
const v0035Request = `{ "requestId" : "original", "revision": 1, "note": "kept" }`

func seedV0035DropSettled(t *testing.T, pool *sql.DB) {
	t.Helper()
	seedV0032(
		`INSERT INTO harness_connections(id,harness,machine_id) VALUES(5,'t3',1)`,
		`INSERT INTO projects(id,title) VALUES(1,'P')`,
		`INSERT INTO session_identities(id,connection_id,external_id) VALUES(6,5,'legacy-1')`,
		`INSERT INTO tasks(id,project_id,title,prompt,state,finished_at,settled_at,connection_id,session_id) VALUES
 (7,1,'Historical needle','p','settled','2026-11-01T00:00:00Z','2026-11-03T00:00:00Z',5,6),
 (8,1,'Reopened','p','settled',NULL,'2026-11-04T00:00:00Z',5,NULL)`,
		`INSERT INTO task_acceptances(id,task_id,provenance,evidence,source_session_id) VALUES
 (11,7,'historical_settlement','`+v0035Evidence+`',6),(12,8,'historical_settlement','{}',NULL)`,
		`UPDATE tasks SET acceptance_id=11,acceptance_revision=1 WHERE id=7`,
		`UPDATE tasks SET acceptance_revision=2 WHERE id=8`,
		`INSERT INTO acceptance_requests VALUES(7,'original','`+v0035Request+`','{"revision":1,"acceptanceId":11}')`,
		`INSERT INTO task_events(id,task_id,from_state,to_state,detail) VALUES
 (1,7,'running','settled','original settled detail'),(2,8,'settled','running','original reopened detail')`,
		`INSERT INTO journal_entries(id,kind,task_id,text,request_id) VALUES(1,'note',7,'original settled note','n')`,
		`INSERT INTO notifications(id,event,task_id,title,url,session_id) VALUES(15,'settled',7,'legacy','/tasks/7',6)`,
		`UPDATE preferences SET default_connection_id=5, notify_events='["done","settled","needs_input","settled","ready","failed"]'`,
	)(t, pool)
}

func checkV0035DropSettled(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := t.Context()
	for _, q := range []string{
		`SELECT count(*) = 1 FROM tasks WHERE id=7 AND state='done' AND finished_at='2026-11-01T00:00:00Z' AND acceptance_id=11 AND acceptance_revision=1`,
		`SELECT count(*) = 1 FROM tasks WHERE id=8 AND state='done' AND finished_at='2026-11-04T00:00:00Z' AND acceptance_id IS NULL AND acceptance_revision=2`,
		`SELECT count(*) = 0 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='tasks' AND column_name='settled_at'`,
		`SELECT count(*) = 1 FROM task_events WHERE id=1 AND task_id=7 AND from_state='running' AND to_state='done' AND detail='original settled detail'`,
		`SELECT count(*) = 1 FROM task_events WHERE id=2 AND task_id=8 AND from_state='done' AND to_state='running' AND detail='original reopened detail'`,
		`SELECT count(*) = 1 FROM journal_entries WHERE id=1 AND text='original settled note'`,
		`SELECT count(*) = 1 FROM notifications WHERE id=15 AND event='done' AND task_id=7 AND session_id=6`,
		`SELECT count(*) = 1 FROM preferences WHERE id=1 AND default_connection_id=5 AND notify_events='["done","needs_input","ready","failed"]'`,
		`SELECT count(*) = 1 FROM task_acceptances WHERE id=11 AND provenance='historical_settlement' AND evidence='` + v0035Evidence + `'`,
		`SELECT count(*) = 1 FROM acceptance_requests WHERE request_id='original' AND input='` + v0035Request + `'`,
	} {
		var ok bool
		if err := pool.QueryRowContext(ctx, q).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: got %t, %v", q, ok, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO tasks(project_id,title,prompt,state,connection_id) VALUES(1,'T','p','settled',5)`,
		`INSERT INTO notifications(event,title,url) VALUES('settled','T','/tasks/7')`,
	} {
		_, err := pool.ExecContext(ctx, q)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "23514" {
			t.Fatalf("%s: want CHECK violation, got %v", q, err)
		}
	}
}

func checkV0035EmptyPreferences(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := t.Context()
	var events string
	if err := pool.QueryRowContext(ctx, `SELECT notify_events FROM preferences WHERE id=1`).Scan(&events); err != nil || events != "[]" {
		t.Fatalf("empty preferences: %s, %v", events, err)
	}
	if _, err := pool.ExecContext(ctx, `UPDATE preferences SET notify_events=DEFAULT WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var defaultMatches bool
	if err := pool.QueryRowContext(ctx, `SELECT notify_events='["needs_input","failed","done"]' FROM preferences WHERE id=1`).Scan(&defaultMatches); err != nil || !defaultMatches {
		t.Fatalf("Postgres notification default: %v, %v", defaultMatches, err)
	}
}

func seedV0037ConnectionFacts(t *testing.T, pool *sql.DB) {
	t.Helper()
	seedV0032(
		`INSERT INTO harness_connections(id,machine_id,harness,identity,label) VALUES(42,1,'opencode','instance','Desk')`,
		`INSERT INTO projects(id,title) VALUES(1,'P')`,
		`INSERT INTO tasks(id,project_id,title,prompt,connection_id,execution_config) VALUES(1,1,'T','p',42,'{"options":[{"id":"worktree","value":false}]}')`,
	)(t, pool)
}

func checkV0037ConnectionFacts(t *testing.T, pool *sql.DB) {
	wantPGRows(t, pool, `SELECT id || '|' || identity || '|' || label || '|' || COALESCE(last_known_facts,'NULL') FROM harness_connections WHERE id=42`, "42|instance|Desk|NULL")
	wantPGRows(t, pool, `SELECT connection_id || '|' || (execution_config->'options'->0->>'value') FROM tasks WHERE id=1`, "42|false")
}

func seedV0038ProjectIdentity(t *testing.T, pool *sql.DB) {
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

func checkV0038ProjectIdentity(t *testing.T, pool *sql.DB) {
	t.Helper()
	wantPGRows(t, pool, `SELECT id || '|' || rule || '|' || CASE WHEN is_primary THEN 'primary' ELSE 'secondary' END
	 FROM project_bindings ORDER BY id`,
		"1|manual|primary", "2|new|primary", "3|local|primary", "4|remote|primary", "5|local|secondary")
	wantPGRows(t, pool, `SELECT external_id || '|' || workspace || '|' || resolution || '|' || resolution_rule
	 FROM external_projects WHERE external_id IN ('manual','imported','path','remote') ORDER BY external_id,workspace`,
		"imported|/imported|bound|existing", "manual|/manual|bound|existing", "manual|/old||", "path|/path|bound|existing", "remote|/remote|bound|existing")
	wantPGRows(t, pool, `SELECT repo || '|' || repo_path FROM external_projects WHERE workspace='/manual'`, "example.org/org/repo|.")
	wantPGRows(t, pool, `SELECT 'legacy' FROM external_projects WHERE workspace='/manual' AND file_host IS NULL
	 AND canonical_path='' AND checkout_key='' AND inode_hint='' AND evidence_at IS NULL AND evidence_source=''
	 AND resolution_reason='' AND candidates='[]'`, "legacy")
	wantPGRows(t, pool, `SELECT id || '' FROM projects WHERE archived_at = '2026-11-01T00:00:00.000000Z'
	 OR archived_at = '2026-11-02T00:00:00.000000Z' ORDER BY id`, "1", "2")
	wantPGRows(t, pool, `SELECT id || '' FROM projects WHERE archived_at IS NULL ORDER BY id`, "3", "4")
	wantPGRows(t, pool, `SELECT connection_id || '|' || identity_key || '|' ||
	 CASE declined_at WHEN '2026-11-01T00:00:00.000000Z' THEN '1' WHEN '2026-11-02T00:00:00.000000Z' THEN '2'
	 WHEN '2026-11-03T00:00:00.000000Z' THEN '3' ELSE '?' END
	 FROM workspace_declines ORDER BY connection_id,identity_key`,
		"42|external:42:declined|2", "42|external:42:pathless|2", "42|external:42:second|3",
		"42|external:42:unknown-subpath|2", "42|remote:example.org/a%25%23b#Dir%25%23X|3",
		"43|external:43:declined|1", "43|remote:example.org/a%25%23b#Dir%25%23X|1")

	wantPGRows(t, pool, `SELECT 'legacy' FROM information_schema.columns
	 WHERE table_schema=current_schema() AND (table_name,column_name) IN
	 (('projects','archive_owner'),('project_bindings','origin'),('external_projects','declined_at'))`)
	wantPGRows(t, pool, `SELECT 'legacy' WHERE to_regclass('shared_paths') IS NOT NULL OR to_regclass('project_binding_active') IS NOT NULL`)

	bindingsUnset := `SELECT 'task' FROM tasks WHERE id=1 AND binding_id IS NULL
	 UNION ALL SELECT 'schedule' FROM schedules WHERE id=1 AND binding_id IS NULL ORDER BY 1`
	wantPGRows(t, pool, bindingsUnset, "schedule", "task")
	seedV0032(
		`INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace) VALUES (6,1,42,'extra','/extra')`,
		`INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,is_primary,active)
		 VALUES (7,1,42,'inactive','/inactive',TRUE,FALSE),(8,1,43,'extra','/extra',TRUE,TRUE)`,
		`UPDATE tasks SET binding_id=6 WHERE id=1`,
		`UPDATE schedules SET binding_id=6 WHERE id=1`,
	)(t, pool)
	wantPGRows(t, pool, `SELECT id || '' FROM project_bindings WHERE project_id=1 AND connection_id=42 AND active ORDER BY id`, "1", "6")
	wantPGRows(t, pool, `SELECT rule FROM project_bindings WHERE id=6 AND NOT is_primary`, "manual")

	for why, q := range map[string]string{
		"second active primary":       `UPDATE project_bindings SET is_primary=TRUE WHERE id=6`,
		"duplicate active workspace":  `INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace) VALUES (9,2,42,'extra','/extra')`,
		"unknown file host":           `UPDATE external_projects SET file_host=999 WHERE workspace='/manual'`,
		"candidates must be an array": `UPDATE external_projects SET candidates='{}' WHERE workspace='/manual'`,
		"invalid dismissal pair":      `INSERT INTO project_merge_dismissals(project_id,other_id) VALUES (2,1)`,
		"invalid task binding":        `UPDATE tasks SET binding_id=999 WHERE id=1`,
		"invalid schedule binding":    `UPDATE schedules SET binding_id=999 WHERE id=1`,
	} {
		if _, err := pool.ExecContext(t.Context(), q); err == nil {
			t.Errorf("%s: %s accepted", why, q)
		}
	}

	seedV0032(`DELETE FROM project_bindings WHERE id=6`)(t, pool)
	wantPGRows(t, pool, bindingsUnset, "schedule", "task")
}

func checkV0039ObservationProducers(t *testing.T, pool *sql.DB) {
	t.Helper()
	wantPGRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_producers`, "0")
	wantPGRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_dispositions`, "0")
	for _, q := range []string{
		`INSERT INTO machines (id, label) VALUES (2, 'Worker')`,
		`INSERT INTO observation_producers (machine_id, producer, active, cursor) VALUES (2, 'old', false, 1)`,
		`INSERT INTO observation_producers (machine_id, producer) VALUES (2, 'current')`,
		`INSERT INTO observation_dispositions (machine_id, producer, cursor, code, lost) VALUES (2, 'old', 1, 'invalid_record', '0')`,
	} {
		if _, err := pool.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO observation_producers (machine_id, producer) VALUES (2, 'second-current')`,
		`INSERT INTO observation_dispositions (machine_id, producer, cursor, code, lost) VALUES (1, 'old', 1, 'invalid_record', '0')`,
	} {
		if _, err := pool.ExecContext(context.Background(), q); err == nil {
			t.Fatalf("expected constraint failure: %s", q)
		}
	}
	q := `DELETE FROM machines WHERE id=2`
	if _, err := pool.ExecContext(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	wantPGRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_producers`, "0")
	wantPGRows(t, pool, `SELECT CAST(count(*) AS TEXT) FROM observation_dispositions`, "0")
}
