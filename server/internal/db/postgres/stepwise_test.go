//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"testing"
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
