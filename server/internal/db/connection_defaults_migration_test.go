package db

import "testing"

// Migration 0024: the global pin moves to the local machine's active T3
// connection (legacy provider/model become its model key); last_used and the
// pinned column go; there is no default connection.
func TestConnectionDefaultsMigration(t *testing.T) {
	for _, tc := range []struct {
		name, pinned, want string
	}{
		{"legacy", `{"providerInstanceId":"claude","model":"haiku","modelOptions":[{"id":"effort","value":"high"}],"runtimeMode":"auto","interactionMode":"plan"}`,
			`{"model":"[\"claude\",\"haiku\"]","options":[{"id":"effort","value":"high"}],"permission":"","mode":""}`},
		{"execution config", `{"executionConfig":{"model":"[\"codex\",\"gpt\"]","options":[{"id":"fast","value":true}],"permission":"auto","mode":"plan"},"providerInstanceId":"codex","model":"gpt"}`,
			`{"model":"[\"codex\",\"gpt\"]","options":[{"id":"fast","value":true}],"permission":"","mode":""}`},
		{"harness default", `{"providerInstanceId":null,"model":null,"modelOptions":[]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := openUnmigrated(t)
			migrateTo(t, pool, 23)
			mustExec(t, pool, `UPDATE harness_connections SET identity='env-a' WHERE id=1`)
			mustExec(t, pool, `INSERT INTO harness_connections(machine_id,harness) VALUES(1,'opencode')`)
			mustExec(t, pool, `UPDATE preferences SET pinned=$1, last_used=$1`, tc.pinned)
			migrateTo(t, pool, 24)

			var got *string
			if err := pool.QueryRow(`SELECT new_task_config FROM harness_connections WHERE id=1`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == "") || (got != nil && *got != tc.want) {
				t.Fatalf("t3 config %v, want %q", got, tc.want)
			}
			var other *string
			if err := pool.QueryRow(`SELECT new_task_config FROM harness_connections WHERE harness='opencode'`).Scan(&other); err != nil || other != nil {
				t.Fatalf("opencode config %v, %v", other, err)
			}
			var def *int64
			if err := pool.QueryRow(`SELECT default_connection_id FROM preferences WHERE id=1`).Scan(&def); err != nil || def != nil {
				t.Fatalf("default %v, %v", def, err)
			}
			if _, err := pool.Exec(`SELECT pinned FROM preferences`); err == nil {
				t.Fatal("pinned column kept")
			}
			if _, err := pool.Exec(`SELECT last_used FROM preferences`); err == nil {
				t.Fatal("last_used column kept")
			}
			if foreignKeyViolations(t, pool) != 0 {
				t.Fatal("foreign key violations after 0024")
			}
		})
	}
}
