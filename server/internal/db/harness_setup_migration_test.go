package db

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
)

func seedV0036HarnessSetup(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	seedV0032(
		`INSERT INTO machines(id,label) VALUES(2,'worker')`,
		`INSERT INTO harness_connections(id,machine_id,harness,identity,label,stable_identity) VALUES(5,1,'t3','env','Desk',true)`,
		`INSERT INTO harness_connections(id,machine_id,harness,identity,label,active,retired_at) VALUES(6,2,'opencode','http://old','Worker',false,'2026-11-12T03:00:00.000000Z')`,
		`UPDATE t3_connection SET client_id='client ✓',token='tok "x"',expires_at='2026-11-15T03:00:00.123456Z',admin_token='admin',t3_url='https://t3',last_error='stale',last_full_sync_at='2026-11-14T03:00:00.000000Z' WHERE machine_id=1`,
		`INSERT INTO t3_connection(machine_id,client_id,t3_url,public_url,last_error) VALUES(2,'partial','','https://public','offline')`,
		`UPDATE opencode_settings SET endpoint='http://oc',browser_endpoint='https://oc',web_client='https://ui',auth='paired',username='oc',password='p&',token_issued_at='2026-11-13T03:00:00.456789Z' WHERE machine_id=1`,
		`INSERT INTO opencode_settings(machine_id,auth,password) VALUES(2,'basic','p2')`,
		`INSERT INTO claude_settings(machine_id,binary_path,config_dir,disconnected,updated_at) VALUES(1,'/opt/claude ✓','/cfg',true,'2026-11-12T03:00:00.123456Z'),(2,'','',false,'2026-11-12T03:00:00.000000Z')`,
		`INSERT INTO codex_settings(machine_id,binary_path,connected) VALUES(1,'/opt/codex',true),(2,'',false)`,
	)(t, pool)
}

func checkV0036HarnessSetup(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) = 0 FROM sqlite_schema WHERE type='table' AND name IN ('t3_connection','opencode_settings','claude_settings','codex_settings')`,
		`SELECT count(*) = 8 FROM harness_settings`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=1 AND kind='t3' AND settings='{"url":"https://t3","publicUrl":null}'`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=2 AND kind='t3' AND settings='{"url":"","publicUrl":"https://public"}'`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=1 AND kind='opencode' AND settings='{"endpoint":"http://oc","browserEndpoint":"https://oc","webClient":"https://ui","auth":"paired"}'`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=2 AND kind='opencode' AND settings='{"endpoint":"","browserEndpoint":"","webClient":"","auth":"basic"}' AND updated_at IS NULL`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=1 AND kind='claude' AND settings='{"binary":"/opt/claude ✓","configDir":"/cfg","disconnected":true}' AND updated_at='2026-11-12T03:00:00.123456Z'`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=2 AND kind='claude' AND settings='{"binary":"","configDir":"","disconnected":false}'`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=1 AND kind='codex' AND settings='{"binary":"/opt/codex","connected":true}'`,
		`SELECT count(*) = 1 FROM harness_settings WHERE machine_id=2 AND kind='codex' AND settings='{"binary":"","connected":false}'`,
		`SELECT count(*) = 5 FROM harness_secrets`,
		`SELECT count(*) = 1 FROM harness_secrets WHERE machine_id=1 AND kind='t3' AND key='token' AND value='{"clientId":"client ✓","token":"tok \"x\"","expiresAt":"2026-11-15T03:00:00.123456Z"}'`,
		`SELECT count(*) = 1 FROM harness_secrets WHERE machine_id=1 AND kind='t3' AND key='adminToken' AND value='"admin"'`,
		`SELECT count(*) = 1 FROM harness_secrets WHERE machine_id=2 AND kind='t3' AND key='token' AND value='{"clientId":"partial","token":null,"expiresAt":null}'`,
		`SELECT count(*) = 1 FROM harness_secrets WHERE machine_id=1 AND kind='opencode' AND key='credential' AND value='{"username":"oc","password":"p&","tokenIssuedAt":"2026-11-13T03:00:00.456789Z"}'`,
		`SELECT count(*) = 1 FROM harness_secrets WHERE machine_id=2 AND kind='opencode' AND key='credential' AND value='{"username":"","password":"p2"}'`,
		`SELECT count(*) = 2 FROM harness_sync_status`,
		`SELECT count(*) = 1 FROM harness_sync_status WHERE machine_id=1 AND kind='t3' AND last_error='stale' AND last_full_sync_at='2026-11-14T03:00:00.000000Z'`,
		`SELECT count(*) = 1 FROM harness_sync_status WHERE machine_id=2 AND kind='t3' AND last_error='offline' AND last_full_sync_at IS NULL`,
		`SELECT count(*) = 2 FROM harness_connections`,
		`SELECT count(*) = 1 FROM harness_connections WHERE id=5 AND machine_id=1 AND harness='t3' AND active=true AND stable_identity=true AND identity='env' AND label='Desk'`,
		`SELECT count(*) = 1 FROM harness_connections WHERE id=6 AND machine_id=2 AND harness='opencode' AND active=false AND identity='http://old' AND label='Worker' AND retired_at='2026-11-12T03:00:00.000000Z'`,
	} {
		var ok bool
		if err := pool.QueryRow(query).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: got %t, %v", query, ok, err)
		}
	}
}

func checkV0036HarnessSetupDefaults(t *testing.T, pool *sqlite.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) = 2 FROM harness_settings WHERE machine_id=1 AND kind IN ('t3','opencode') AND settings='{}'`,
		`SELECT count(*) = 0 FROM harness_secrets`,
		`SELECT count(*) = 0 FROM harness_sync_status`,
		`SELECT count(*) = 0 FROM harness_connections`,
	} {
		var ok bool
		if err := pool.QueryRow(query).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: got %t, %v", query, ok, err)
		}
	}
}

func TestHarnessSetupMigrationRollsBackOnCopyFailure(t *testing.T) {
	ctx := t.Context()
	pool := openUnmigrated(t)
	migrateTo(t, pool, 35)
	seedV0036HarnessSetup(t, pool)
	mustExec(t, pool, `UPDATE t3_connection SET token=X'FF' WHERE machine_id=1`)
	if err := sqlite.MigrateTo(ctx, pool, 36); err == nil {
		t.Fatal("migration copied a blob into JSON")
	}
	if v, err := sqlite.CurrentVersion(ctx, pool); err != nil || v != 35 {
		t.Fatalf("schema version after failure %d %v", v, err)
	}
	for _, name := range []string{"harness_settings", "harness_secrets", "harness_sync_status"} {
		if tableExists(t, pool, name) {
			t.Fatalf("migration retained %s after failure", name)
		}
	}
	wantRows(t, pool, `SELECT machine_id,quote(token) FROM t3_connection ORDER BY machine_id`, "1|X'FF'", "2|NULL")
	mustExec(t, pool, `UPDATE t3_connection SET token='tok "x"' WHERE machine_id=1`)
	migrateTo(t, pool, 36)
	checkV0036HarnessSetup(t, pool)
}
