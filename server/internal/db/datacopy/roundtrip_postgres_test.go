//go:build postgres

package datacopy_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/datacopy"
	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/flexdinesh/stage/server/internal/db/postgres/pgtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// seedSQL fills every Stage table of a SQLite database at the head with
// edge values: non-ASCII and metacharacter text, evidence JSON with odd key
// order and whitespace, queried JSON with number forms jsonb normalizes,
// BLOBs with 0x00 and 0xff, microsecond and (from schema defaults)
// millisecond timestamps, NULLs, booleans, and a deleted top ID (snippets
// 3) that Postgres must not reissue.
var seedSQL = []string{
	`INSERT INTO users (id, display_name, created_at) VALUES (2, 'Ünïcode user ✓', '2026-11-01T10:00:00.123456Z')`,
	`INSERT INTO identities (user_id, provider, subject) VALUES (2, 'github', 'sub-1')`,
	`INSERT INTO machines (id, label, created_by_user_id, last_seen_at, version) VALUES (2, 'worker', 2, '2026-11-02T10:00:00.000001Z', '1.2.3')`,
	`INSERT INTO machine_tokens (machine_id, secret_hash) VALUES (2, X'00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF')`,
	`INSERT INTO pairing_codes (secret_hash, label, created_by_user_id, expires_at, redeemed_at, machine_id) VALUES (X'FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00', 'pair', 1, '2026-11-03T00:00:00.000000Z', '2026-11-02T23:00:00.500000Z', 2)`,
	`INSERT INTO harness_connections (id, machine_id, harness, auto_complete, identity, stable_identity, label, active, new_task_config) VALUES (2, 1, 'opencode', 0, 'oc-1', 1, 'OpenCode', 1, '{"b": 1, "a": [1.0, 2e0, -0.5]}')`,
	`INSERT INTO harness_connections (id, machine_id, harness, active, retired_at, forgotten_at) VALUES (3, 2, 't3', 0, '2026-11-04T01:02:03.456789Z', '2026-11-04T02:00:00.000000Z')`,
	`INSERT INTO session_identities (id, connection_id, external_id) VALUES (1, 1, 'thread-1'), (2, 2, 'oc-session')`,
	`INSERT INTO shared_paths (connection_id, harness_root, stage_root) VALUES (2, '/h', '/s')`,
	`UPDATE t3_connection SET client_id = 'c', token = 'tok', expires_at = '2026-12-01T00:00:00.000000Z', t3_url = 'http://t3' WHERE machine_id = 1`,
	`INSERT INTO t3_connection (machine_id, updated_at) VALUES (2, '2026-11-05T00:00:00.000000Z')`,
	`UPDATE opencode_settings SET endpoint = 'http://oc', auth = 'basic', username = 'u', password = 'p', token_issued_at = '2026-11-05T00:00:00.000000Z' WHERE machine_id = 1`,
	`INSERT INTO opencode_settings (machine_id) VALUES (2)`,
	`INSERT INTO claude_settings (machine_id, binary_path, config_dir, disconnected, updated_at) VALUES (1, '/opt/claude ✓', '/home/u/.claude', 1, '2026-11-05T00:00:00.123456Z'), (2, '', '', 0, '2026-11-05T00:00:00.000000Z')`,
	`INSERT INTO codex_settings (machine_id, binary_path, connected) VALUES (1, '/opt/cödex bin', 1), (2, '', 0)`,
	`UPDATE preferences SET default_connection_id = 2, notify_events = '["done", "failed"]' WHERE id = 1`,
	`INSERT INTO projects (id, title, origin, imported_connection_id, imported_at, archive_owner, created_by_user_id, source_machine_id) VALUES (1, 'Proj ✓', 'discovered', 2, '2026-11-06T00:00:00.000000Z', 'user', 2, 2)`,
	`INSERT INTO project_bindings (id, project_id, connection_id, external_project_id, workspace, origin) VALUES (1, 1, 1, 'ext-1', '/w', 'manual')`,
	`INSERT INTO external_projects (connection_id, external_id, title, workspace, available, repo) VALUES (2, 'ext-2', 'Ext', '/w2', 1, 'git@x')`,
	`INSERT INTO schedules (id, project_id, title, prompt, workspace, cron, tz, next_run_at, connection_id, origin_session_id, execution_config) VALUES (1, 1, 'Nightly', 'p', '{"type":"root"}', '0 0 * * *', 'UTC', '2026-11-07T00:00:00.000000Z', 1, 1, '{"model":"x","options":[]}')`,
	`INSERT INTO task_trees (id, title, status, closed_at, recorded_work, membership_revision) VALUES (1, 'Tree', 'done', '2026-11-08T00:00:00.000000Z', 1, 2)`,
	`INSERT INTO tasks (id, project_id, title, prompt, workspace, state, launch_attempt, launched_at, finished_at, schedule_id, tree_id, start_at, start_tz, connection_id, session_id, execution_config, recorded_work, acceptance_revision, acceptance_id) VALUES (1, 1, 'First', 'do it', '{"type":"worktree","branch":"b"}', 'done', 1, '2026-11-09T00:00:00.000001Z', '2026-11-09T01:00:00.999999Z', 1, 1, '2026-11-09T00:00:00.000000Z', 'UTC', 1, 1, '{"permission":"full-access"}', 1, 1, 1)`,
	`INSERT INTO tasks (id, project_id, title, prompt, tree_id) VALUES (2, 1, 'needle %_\ "quoted"', 'prompt with
newline and 日本語', 1)`,
	`INSERT INTO task_needs (task_id, need_id) VALUES (2, 1)`,
	`INSERT INTO task_events (task_id, from_state, to_state, detail, at) VALUES (1, NULL, 'backlog', 'created', '2026-11-09T00:00:00.000000Z'), (1, 'backlog', 'done', '', '2026-11-09T01:00:00.000000Z')`,
	`INSERT INTO task_acceptances (id, task_id, provenance, evidence, note, source_session_id) VALUES (1, 1, 'user', '{"z":1,  "a" : "x" }', 'n', 1)`,
	`INSERT INTO acceptance_requests (task_id, request_id, input, result) VALUES (1, 'r1', '{"b":2, "a":1}', '{"ok": true, "n": 10}')`,
	`INSERT INTO completion_inferences (task_id, execution_id, signal, acceptance_id) VALUES (1, 'e1', 'settled', 1)`,
	`INSERT INTO execution_attempts (task_id, attempt, intent, checkpoint, phase, revision, observed_session) VALUES (1, 1, '{"i":1}', '{"version":1,"data":{"k":"v"}}', 'confirmed', 2, '{"s":"x"}')`,
	`INSERT INTO execution_observations (session_id, observation) VALUES (1, '{"state":"done","n":1.50,"u":"é"}')`,
	`INSERT INTO journal_entries (kind, task_id, category, text, request_id, source_session_id, created_by_user_id, source_machine_id) VALUES ('note', 1, 'finding', 'searchable needle text', 'req-1', 1, 2, 2)`,
	`INSERT INTO journal_entries (kind, tree_id, text, event_type, to_tree_id) VALUES ('event', 1, 'moved', 'task_moved', 1)`,
	`INSERT INTO journal_launch_context (task_id, attempt, instructions) VALUES (1, 1, 'context')`,
	`INSERT INTO task_launch_claims (task_id, attempt) VALUES (1, 1)`,
	`INSERT INTO task_launch_destinations (task_id, attempt, connection_id, external_project_id, workspace, binding_id, binding_revision) VALUES (1, 1, 1, 'ext-1', '/w', 1, 1)`,
	`INSERT INTO task_launch_inputs (task_id, attempt, source_task_id, acceptance_id) VALUES (1, 1, 1, 1)`,
	`INSERT INTO task_launch_bases (task_id, attempt, base_ref, start_from_origin, source, source_task_id, source_title, source_session_id) VALUES (2, 1, 'origin/main', 1, 'predecessor', 1, 'First', 1)`,
	`INSERT INTO task_handoffs (task_id, attempt, source_task_id, source_title, status, text, source_session_id) VALUES (2, 1, 1, 'First', 'sent', 'handoff', 1)`,
	`INSERT INTO task_attachments (task_id, name, mime_type, size_bytes, data) VALUES (1, 'a.png', 'image/png', 4, X'89504E47')`,
	`INSERT INTO task_attachments (schedule_id, name, mime_type, size_bytes, data) VALUES (1, 'b.gif', 'image/gif', 3, X'00FF00')`,
	`INSERT INTO schedule_runs (schedule_id, fire_at, outcome, task_id) VALUES (1, '2026-11-07T00:00:00.000000Z', 'started', 1)`,
	`INSERT INTO notifications (event, task_id, title, project_title, url, session_id) VALUES ('done', 1, 'Done', 'Proj', '/tasks/1', 1)`,
	`INSERT INTO threads (t3_thread_id, t3_project_id, title, status, settled, created_at, updated_at, session_id, stage_project_id) VALUES ('thread-1', 'p1', 'Thread', 'idle', 1, '2026-11-09T00:00:00.000000Z', '2026-11-09T00:00:00.000000Z', 1, 1)`,
	`INSERT INTO push_subscriptions (endpoint, p256dh, auth) VALUES ('https://push', 'k', 'a')`,
	`INSERT INTO push_vapid (id, public_key, private_key) VALUES (1, 'pub', 'priv')`,
	`INSERT INTO snippets (id, name, body, project_id) VALUES (1, 's1', 'b', 1), (2, 's2', 'b', NULL), (3, 's3', 'gone', NULL)`,
	`DELETE FROM snippets WHERE id = 3`,
}

// sourceDB writes a migrated SQLite database seeded with seedSQL (plus
// extra statements) into a temp data dir, closes it and returns its path.
func sourceDB(t *testing.T, version int64, extra ...string) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), sqlite.DBFile)
	d, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.MigrateTo(ctx, d, version); err != nil {
		t.Fatal(err)
	}
	if version == sqlite.MaxVersion() {
		for _, q := range append(seedSQL, extra...) {
			if _, err := d.ExecContext(ctx, q); err != nil {
				t.Fatalf("seed %q: %v", q, err)
			}
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func openSource(t *testing.T, path string) *sqlite.CopySource {
	t.Helper()
	src, err := sqlite.OpenCopySource(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// freshTarget is an empty Postgres database (no schema).
func freshTarget(t *testing.T) *postgres.DB {
	t.Helper()
	name := pgtest.CreateDatabase(t, pgtest.Locale)
	d, err := postgres.Open(context.Background(), pgtest.DatabaseURL(t, name), postgres.Options{ApplicationName: "stage copy test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// dirState is every file in dir with its sha256.
func dirState(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][32]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = sha256.Sum256(b)
	}
	return out
}

// canonical marshals v and decodes it back, so raw JSON fields compare by
// value (jsonb reorders keys).
func canonical(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := sourceDB(t, sqlite.MaxVersion())
	before := dirState(t, filepath.Dir(path))
	src := openSource(t, path)
	dst := freshTarget(t)

	rep, err := datacopy.Run(ctx, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Migrated || rep.Version != sqlite.MaxVersion() {
		t.Fatalf("report: migrated %v version %d", rep.Migrated, rep.Version)
	}
	schema, err := dst.Schema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tables) != len(schema) {
		t.Fatalf("report has %d tables, schema %d", len(rep.Tables), len(schema))
	}
	srcTables, err := src.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcTables) != len(schema) {
		t.Fatalf("source has %d tables, target %d", len(srcTables), len(schema))
	}
	for _, tr := range rep.Tables {
		// Every table must carry rows, so the round trip covers its
		// encodings: a new table needs a row in seedSQL.
		if tr.Rows == 0 {
			t.Errorf("table %s has no rows: add one to seedSQL", tr.Name)
		}
		if _, ok := srcTables[tr.Name]; !ok {
			t.Errorf("table %s is not in the source", tr.Name)
		}
	}

	// Reads through db.Store agree on both engines.
	lite, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lite.Close() })
	ls, ps := db.NewStore(lite), db.NewStore(dst)
	one, two := int64(1), int64(2)
	reads := map[string]func(*db.Store) (any, error){
		"task 1": func(s *db.Store) (any, error) { return s.GetTask(ctx, 1) },
		"task 2": func(s *db.Store) (any, error) { return s.GetTask(ctx, 2) },
		"search": func(s *db.Store) (any, error) { return s.ListTasks(ctx, db.TaskFilter{Q: "NEEDLE %_", Closed: "all"}) },
		"journal": func(s *db.Store) (any, error) {
			return s.ReadJournal(ctx, store.JournalOwner{TaskID: &one}, store.JournalQuery{Kind: "all"})
		},
		"tree":        func(s *db.Store) (any, error) { return s.GetTree(ctx, 1) },
		"attachment":  func(s *db.Store) (any, error) { return s.GetAttachment(ctx, 1) },
		"snippets":    func(s *db.Store) (any, error) { return s.ListSnippets(ctx) },
		"preferences": func(s *db.Store) (any, error) { return s.GetPreferences(ctx) },
		"acceptances": func(s *db.Store) (any, error) { return s.Acceptances(ctx, 1) },
		"schedule":    func(s *db.Store) (any, error) { return s.GetSchedule(ctx, 1) },
		"notes q": func(s *db.Store) (any, error) {
			return s.ReadJournal(ctx, store.JournalOwner{TaskID: &one}, store.JournalQuery{Q: "needle"})
		},
		"task 2 notes": func(s *db.Store) (any, error) {
			return s.ReadJournal(ctx, store.JournalOwner{TaskID: &two}, store.JournalQuery{Kind: "all"})
		},
	}
	for name, read := range reads {
		want, err := read(ls)
		if err != nil {
			t.Fatalf("%s on SQLite: %v", name, err)
		}
		got, err := read(ps)
		if err != nil {
			t.Fatalf("%s on Postgres: %v", name, err)
		}
		if w, g := canonical(t, want), canonical(t, got); !reflect.DeepEqual(w, g) {
			t.Errorf("%s differs:\nsqlite   %v\npostgres %v", name, w, g)
		}
	}
	lite.Close()

	// Evidence keeps its exact bytes; IDs continue past the high-water
	// mark (snippets 3 was deleted before the copy).
	var evidence string
	if err := dst.Pool().QueryRow(ctx, `SELECT evidence FROM task_acceptances WHERE id = 1`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != `{"z":1,  "a" : "x" }` {
		t.Fatalf("evidence bytes changed: %q", evidence)
	}
	for table, want := range map[string]int64{"snippets": 4, "users": 3, "machines": 3, "harness_connections": 4, "tasks": 3, "push_subscriptions": 2} {
		var id int64
		q := `INSERT INTO ` + table + ` DEFAULT VALUES RETURNING id`
		switch table {
		case "snippets":
			q = `INSERT INTO snippets (name, body) VALUES ('new', 'b') RETURNING id`
		case "users":
			q = `INSERT INTO users (display_name) VALUES ('new') RETURNING id`
		case "harness_connections":
			q = `INSERT INTO harness_connections (machine_id, harness, active, retired_at) VALUES (1, 'x', false, now()) RETURNING id`
		case "tasks":
			q = `INSERT INTO tasks (project_id, title, prompt) VALUES (1, 'new', 'p') RETURNING id`
		case "push_subscriptions":
			q = `INSERT INTO push_subscriptions (endpoint, p256dh, auth) VALUES ('https://new', 'k', 'a') RETURNING id`
		}
		if err := dst.Pool().QueryRow(ctx, q).Scan(&id); err != nil {
			t.Fatalf("insert into %s: %v", table, err)
		}
		if id != want {
			t.Errorf("new %s id %d, want %d", table, id, want)
		}
	}

	// A second copy into the now populated target is refused.
	var refused *datacopy.RefusedError
	if _, err := datacopy.Run(ctx, src, dst); !errors.As(err, &refused) || !strings.Contains(err.Error(), "holds data") {
		t.Fatalf("second copy: %v", err)
	}

	// The SQLite data dir is as it was: same files, same bytes.
	src.Close()
	if after := dirState(t, filepath.Dir(path)); !reflect.DeepEqual(before, after) {
		t.Fatalf("source dir changed:\nbefore %v\nafter  %v", keys(before), keys(after))
	}
}

func keys(m map[string][32]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A target migrated to the head with only the baseline's seeds (a server
// started on it once) is accepted; the seeds are replaced.
func TestRoundTripSeededTarget(t *testing.T) {
	ctx := context.Background()
	src := openSource(t, sourceDB(t, sqlite.MaxVersion()))
	dst := pgtest.Engine(t)
	rep, err := datacopy.Run(ctx, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Migrated {
		t.Fatal("seeded target reported migrated")
	}
	var users int
	if err := dst.Pool().QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 2 {
		t.Fatalf("users %d, want the source's 2", users)
	}
}

func TestRefusals(t *testing.T) {
	ctx := context.Background()
	head := sqlite.MaxVersion()
	cases := map[string]struct {
		source func(*testing.T) string
		target func(*testing.T) *postgres.DB
		want   string
	}{
		"source below head": {
			source: func(t *testing.T) string { return sourceDB(t, head-1) },
			target: freshTarget,
			want:   "older than this binary",
		},
		"target occupied": {
			source: func(t *testing.T) string { return sourceDB(t, head) },
			target: func(t *testing.T) *postgres.DB {
				d := pgtest.Engine(t)
				if _, err := d.Pool().Exec(ctx, `INSERT INTO users (display_name) VALUES ('x')`); err != nil {
					t.Fatal(err)
				}
				return d
			},
			want: "holds data (table users)",
		},
		"seed row changed key": {
			source: func(t *testing.T) string { return sourceDB(t, head) },
			target: func(t *testing.T) *postgres.DB {
				d := pgtest.Engine(t)
				if _, err := d.Pool().Exec(ctx, `INSERT INTO machines (id) VALUES (5)`); err != nil {
					t.Fatal(err)
				}
				return d
			},
			want: "holds data (table machines)",
		},
		"target at another version": {
			source: func(t *testing.T) string { return sourceDB(t, head) },
			target: func(t *testing.T) *postgres.DB {
				d := pgtest.Engine(t)
				if _, err := d.Pool().Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, head+1); err != nil {
					t.Fatal(err)
				}
				return d
			},
			want: "is not",
		},
		"target with foreign tables": {
			source: func(t *testing.T) string { return sourceDB(t, head) },
			target: func(t *testing.T) *postgres.DB {
				d := freshTarget(t)
				if _, err := d.Pool().Exec(ctx, `CREATE TABLE other_app (id int)`); err != nil {
					t.Fatal(err)
				}
				return d
			},
			want: "no stage schema",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := openSource(t, c.source(t))
			dst := c.target(t)
			before, err := dst.State(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = datacopy.Run(ctx, src, dst)
			var refused *datacopy.RefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want refusal %q", err, c.want)
			}
			after, err := dst.State(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("target changed: %+v -> %+v", before, after)
			}
		})
	}
}

// A value that does not convert fails the copy and rolls the target back
// to its migrated, seeds-only state.
func TestConversionErrorRollsBack(t *testing.T) {
	ctx := context.Background()
	src := openSource(t, sourceDB(t, sqlite.MaxVersion(), `UPDATE threads SET created_at = 'yesterday'`))
	dst := freshTarget(t)
	_, err := datacopy.Run(ctx, src, dst)
	if err == nil || !strings.Contains(err.Error(), "threads.created_at") {
		t.Fatalf("got %v, want a threads.created_at conversion error", err)
	}
	st, err := dst.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != sqlite.MaxVersion() || st.Occupied != "" {
		t.Fatalf("target after a failed copy: %+v", st)
	}
	// The source is fixed and the copy reruns into the same target.
	if _, err := datacopy.Run(ctx, openSource(t, sourceDB(t, sqlite.MaxVersion())), dst); err != nil {
		t.Fatalf("rerun: %v", err)
	}
}

var _ store.CopyTarget = (*postgres.DB)(nil)
