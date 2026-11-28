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
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/datacopy"
	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/flexdinesh/stage/server/internal/db/postgres/pgtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

const legacyEvidence = `{"threadId":"thread-1", "executionState":"settled",  "a" : "x" }`

// seedSQL fills every Stage table of a SQLite database at the head with
// edge values: non-ASCII and metacharacter text, evidence JSON with odd key
// order and whitespace, queried JSON with number forms jsonb normalizes,
// BLOBs with 0x00 and 0xff, microsecond and (from schema defaults)
// millisecond timestamps, NULLs, booleans, and deleted top IDs that
// Postgres must not reissue.
var seedSQL = []string{
	`INSERT INTO users (id, display_name, created_at) VALUES (2, 'Ünïcode user ✓', '2026-11-01T10:00:00.123456Z')`,
	`INSERT INTO identities (user_id, provider, subject) VALUES (2, 'github', 'sub-1')`,
	`UPDATE users SET display_name = 'Owner', email = 'Owner@Example.test', email_key = 'owner@example.test' WHERE id = 1`,
	`UPDATE users SET email = 'Member@Example.test', email_key = 'member@example.test', status = 'disabled' WHERE id = 2`,
	`UPDATE installation_access SET installation_id = 'installation-01', mode = 'claimed', claimed_at = '2026-11-01T00:00:00.000000Z' WHERE id = 1`,
	`INSERT INTO identities (user_id, provider, subject) VALUES (1, 'local', 'user:1')`,
	`INSERT INTO local_credentials (identity_id, password_verifier) SELECT id, 'test verifier' FROM identities WHERE provider = 'local'`,
	`INSERT INTO auth_sessions (token_digest, user_id, audience, created_at, expires_at) VALUES
	  (randomblob(32), 1, 'browser', '2026-11-01T00:00:00.000000Z', '2026-12-01T00:00:00.000000Z'),
	  (randomblob(32), 1, 'agent', '2026-11-01T00:00:00.000000Z', '2026-12-01T00:00:00.000000Z')`,
	`INSERT INTO auth_grants (token_digest, purpose, issuer_user_id, email, email_key, created_at, expires_at)
	  VALUES (randomblob(32), 'invite', 1, 'invite@example.test', 'invite@example.test',
	  '2026-11-01T00:00:00.000000Z', '2026-12-01T00:00:00.000000Z')`,
	`INSERT INTO access_audit (action, actor_user_id, grant_id, created_at)
	  SELECT 'invite_issued', issuer_user_id, id, '2026-11-01T00:00:00.000000Z'
	  FROM auth_grants WHERE purpose = 'invite'`,
	`INSERT INTO machines (id, label, created_by_user_id, last_seen_at, version) VALUES (2, 'worker', 2, '2026-11-02T10:00:00.000001Z', '1.2.3')`,
	`INSERT INTO machine_tokens (machine_id, secret_hash) VALUES (2, X'00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF')`,
	`INSERT INTO pairing_codes (secret_hash, label, created_by_user_id, expires_at, redeemed_at, machine_id) VALUES (X'FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00FF00', 'pair', 1, '2026-11-03T00:00:00.000000Z', '2026-11-02T23:00:00.500000Z', 2)`,
	// A paired T3 connection 1 (migration 0032 removes the unused seed).
	`INSERT INTO harness_connections (id, machine_id, harness, identity, stable_identity) VALUES (1, 1, 't3', 'env-1', 1)`,
	`INSERT INTO harness_connections (id, machine_id, harness, auto_complete, identity, stable_identity, label, active, new_task_config, last_known_facts) VALUES (2, 1, 'opencode', 0, 'oc-1', 1, 'OpenCode', 1, '{"b": 1, "a": [1.0, 2e0, -0.5]}', '[{"id":"version","label":"Version","value":"2.3"}]')`,
	`INSERT INTO harness_connections (id, machine_id, harness, active, retired_at, forgotten_at) VALUES (3, 2, 't3', 0, '2026-11-04T01:02:03.456789Z', '2026-11-04T02:00:00.000000Z')`,
	`INSERT INTO session_identities (id, connection_id, external_id) VALUES (1, 1, 'thread-1'), (2, 2, 'oc-session')`,
	`UPDATE harness_settings SET settings = '{"url":"http://t3","publicUrl":null}' WHERE machine_id = 1 AND kind = 't3'`,
	`UPDATE harness_settings SET settings = '{"endpoint":"http://oc","browserEndpoint":"https://browser","webClient":"","auth":"basic"}' WHERE machine_id = 1 AND kind = 'opencode'`,
	`INSERT INTO harness_settings (machine_id, kind, settings, updated_at) VALUES
	  (1, 'claude', '{"binary":"/opt/claude ✓","configDir":"/home/u/.claude","disconnected":true}', '2026-11-05T00:00:00.123456Z'),
	  (2, 'codex', '{"binary":"/opt/cödex bin","connected":false}', NULL)`,
	`INSERT INTO harness_secrets (machine_id, kind, key, value) VALUES
	  (1, 't3', 'token', '{"clientId":"c","token":"s","expiresAt":"2026-12-01T00:00:00.000000Z"}'),
	  (1, 't3', 'adminToken', '"admin"'),
	  (1, 'opencode', 'credential', '{"username":"u","password":"p","tokenIssuedAt":"2026-11-05T00:00:00.123456Z"}')`,
	`INSERT INTO harness_sync_status (machine_id, kind, last_error, last_full_sync_at) VALUES
	  (1, 't3', 'once', '2026-11-05T10:00:00.123456Z'),
	  (2, 't3', NULL, NULL)`,
	`UPDATE preferences SET default_connection_id = 2, notify_events = '["done", "failed"]' WHERE id = 1`,
	`INSERT INTO projects (id, title, origin, imported_connection_id, imported_at, created_by_user_id, source_machine_id)
	  VALUES (1, 'Proj ✓', 'discovered', 2, '2026-11-06T00:00:00.000000Z', 2, 2)`,
	`INSERT INTO projects (id, title, archived_at) VALUES (2, 'Separate', '2026-11-06T01:03:00.123456Z')`,
	`INSERT INTO project_bindings (id, project_id, connection_id, external_project_id, workspace, is_primary, rule)
	  VALUES (1, 1, 1, 'ext-1', '/w', 1, 'manual'), (2, 1, 1, 'ext-1-secondary', '/old/w2', 0, 'moved')`,
	`INSERT INTO external_projects (connection_id, external_id, title, workspace, available, repo, repo_path, file_host, canonical_path, checkout_key, inode_hint, evidence_at, evidence_source, resolution, resolution_reason, resolution_rule, candidates, historical_project_id, group_project_id, group_revision, group_attribution)
	  VALUES (2, 'ext-2', 'Ext', '/w2', 1, 'example.org/Proj', 'packages/app', 1, '/work/w2', '/work/.git', '7:19', '2026-11-06T02:00:00.123456Z', 'machine', 'review', 'ambiguous', 'remote', '[1, 2]', 2, 1, 3, '{ "actor": { "kind": "human", "userId": 2 } }')`,
	`INSERT INTO workspace_declines (connection_id, identity_key, declined_at) VALUES (2, 'external:2:declined', '2026-11-06T01:30:00.000000Z')`,
	`INSERT INTO project_aliases (alias_id, project_id, title, merged_at, merged_by_user_id) VALUES (9, 1, 'Merged', '2026-11-06T01:00:00.000000Z', 2)`,
	`INSERT INTO project_events (id, project_id, type, text, created_at, created_by_user_id) VALUES
	  (4, 1, 'project_merged', 'Merged project 9', '2026-11-06T01:00:00.000000Z', 2),
	  (9, 1, 'workspace_moved', 'Moved /old/w2', '2026-11-06T02:00:00.000000Z', 1)`,
	`DELETE FROM project_events WHERE id = 9`,
	`INSERT INTO project_merge_dismissals (project_id, other_id) VALUES (1, 2)`,
	`INSERT INTO schedules (id, project_id, title, prompt, cron, tz, next_run_at, connection_id, origin_session_id, execution_config, binding_id, workspace_pin) VALUES (1, 1, 'Nightly', 'p', '0 0 * * *', 'UTC', '2026-11-07T00:00:00.000000Z', 1, 1, '{"model":"x","options":[]}', 2, '{"root":"/old/w2","externalId":"ext-1-secondary"}')`,
	`INSERT INTO task_trees (id, title, status, closed_at, recorded_work, membership_revision) VALUES (1, 'Tree', 'done', '2026-11-08T00:00:00.000000Z', 1, 2)`,
	`INSERT INTO tasks (id, project_id, title, prompt, state, launch_attempt, launched_at, finished_at, schedule_id, tree_id, start_at, start_tz, connection_id, session_id, execution_config, recorded_work, acceptance_revision, acceptance_id, binding_id) VALUES (1, 1, 'First', 'do it', 'done', 1, '2026-11-09T00:00:00.000001Z', '2026-11-09T01:00:00.999999Z', 1, 1, '2026-11-09T00:00:00.000000Z', 'UTC', 1, 1, '{"permission":"full-access","options":[{"id":"worktree","value":true},{"id":"branch","value":"b"}]}', 1, 1, 1, 1)`,
	`INSERT INTO tasks (id, project_id, title, prompt, tree_id, workspace_pin, launch_attempt) VALUES (2, 1, 'needle %_\ "quoted"', 'prompt with
newline and 日本語', 1, '{"externalId":"next","root":"/next"}', 1)`,
	`INSERT INTO task_needs (task_id, need_id) VALUES (2, 1)`,
	`INSERT INTO tasks (id, project_id, title, prompt, connection_id) VALUES (3, 1, 'Steer: Tree', 'steer it', 1)`,
	`INSERT INTO tree_steerers (id, tree_id, task_id, request_id, request_digest, created_at, ended_at, end_reason, delivered_through, last_delivered_at, last_error, attribution) VALUES
	  (1, 1, 3, 'steer-1', 'abc', '2026-11-09T00:00:00.000000Z', '2026-11-09T02:00:00.000000Z', 'stopped', 2, '2026-11-09T01:00:00.000000Z', 'busy', '{ "actor": { "kind": "agent" } }')`,
	`INSERT INTO steering_events (id, tree_id, steerer_id, kind, task_id, payload, caused_by_connection_id, caused_by_external_session_id, caused_by_task_id, created_at) VALUES
	  (1, 1, 1, 'member_done', 1, '{"reply":"done ✓","n":1.50}', 1, 'sess-1', 3, '2026-11-09T01:00:00.000000Z')`,
	`INSERT INTO task_events (task_id, from_state, to_state, detail, at) VALUES (1, NULL, 'backlog', 'created', '2026-11-09T00:00:00.000000Z'), (1, 'backlog', 'done', '', '2026-11-09T01:00:00.000000Z')`,
	`INSERT INTO task_acceptances (id, task_id, provenance, evidence, note, source_session_id) VALUES (1, 1, 'user', '` + legacyEvidence + `', 'n', 1)`,
	`INSERT INTO acceptance_requests (task_id, request_id, input, result) VALUES (1, 'r1', '{"b":2, "a":1}', '{"ok": true, "n": 10}')`,
	`INSERT INTO completion_inferences (task_id, execution_id, signal, acceptance_id) VALUES (1, 'e1', 'settled', 1)`,
	`INSERT INTO execution_attempts (task_id, attempt, intent, checkpoint, phase, revision, observed_session) VALUES (1, 1, '{"i":1}', '{"version":1,"data":{"k":"v"}}', 'confirmed', 2, '{"s":"x"}')`,
	`INSERT INTO execution_observations (session_id, observation) VALUES (1, '{"state":"done","n":1.50,"u":"é"}')`,
	`INSERT INTO journal_entries (kind, task_id, category, text, request_id, source_session_id, created_by_user_id, source_machine_id) VALUES ('note', 1, 'finding', 'searchable needle text', 'req-1', 1, 2, 2)`,
	`INSERT INTO journal_entries (kind, tree_id, text, event_type, to_tree_id) VALUES ('event', 1, 'moved', 'task_moved', 1)`,
	`UPDATE journal_entries SET attribution = '{ "actor": { "kind": "human", "userId": 2 } }' WHERE kind = 'note'`,
	`INSERT INTO journal_launch_context (task_id, attempt, instructions) VALUES (1, 1, 'context')`,
	`INSERT INTO steering_launch_context (task_id, attempt, brief, created_at) VALUES (1, 1, 'brief', '2026-01-01T00:00:00.000000Z')`,
	`INSERT INTO task_launch_claims (task_id, attempt) VALUES (1, 1)`,
	`INSERT INTO task_launch_destinations (task_id, attempt, connection_id, external_project_id, workspace, binding_id, binding_revision) VALUES
	  (1, 1, 1, 'ext-1', '/w', 1, 1), (2, 1, 1, 'unbound', '/unbound', NULL, 0)`,
	`INSERT INTO task_launch_inputs (task_id, attempt, source_task_id, acceptance_id) VALUES (1, 1, 1, 1)`,
	`INSERT INTO task_handoffs (task_id, attempt, source_task_id, source_title, status, text, source_session_id) VALUES (2, 1, 1, 'First', 'sent', 'handoff', 1)`,
	`INSERT INTO task_attachments (task_id, name, mime_type, size_bytes, data) VALUES (1, 'a.png', 'image/png', 4, X'89504E47')`,
	`INSERT INTO task_attachments (schedule_id, name, mime_type, size_bytes, data) VALUES (1, 'b.gif', 'image/gif', 3, X'00FF00')`,
	`INSERT INTO schedule_runs (schedule_id, fire_at, outcome, task_id) VALUES (1, '2026-11-07T00:00:00.000000Z', 'started', 1)`,
	`INSERT INTO notifications (event, task_id, title, project_title, url, session_id) VALUES ('done', 1, 'Done', 'Proj', '/tasks/1', 1)`,
	`INSERT INTO notification_recipients (notification_id, user_id, read_at) VALUES (1, 1, '2026-11-06T01:00:00.000000Z')`,
	`UPDATE notification_preferences SET notify_events = '["done", "failed"]' WHERE user_id = 1`,
	`INSERT INTO push_subscriptions (endpoint, p256dh, auth, auth_session_id, binding)
	  SELECT 'https://push', 'k', 'a', id, 'private-binding' FROM auth_sessions WHERE audience = 'browser'`,
	`INSERT INTO push_vapid (id, public_key, private_key) VALUES (1, 'pub', 'priv')`,
	`INSERT INTO snippets (id, name, body, project_id) VALUES (1, 's1', 'b', 1), (2, 's2', 'b', NULL), (3, 's3', 'gone', NULL)`,
	`DELETE FROM snippets WHERE id = 3`,
}

var observationGapFrom = time.Date(2026, 11, 2, 10, 0, 0, 1000, time.UTC)
var observationGapTo = time.Date(2026, 11, 2, 11, 0, 0, 456789000, time.UTC)

func seedObservationProducers(t *testing.T, s *db.Store) {
	t.Helper()
	for _, item := range []struct {
		producer    string
		disposition db.ObservationDisposition
	}{
		{"worker-old", db.ObservationDisposition{Code: "gap", Lost: ^uint64(0), From: observationGapFrom, To: observationGapTo}},
		{"worker-current", db.ObservationDisposition{Code: "invalid record with private payload"}},
	} {
		ctx := t.Context()
		if err := s.BindObservationProducer(ctx, 2, item.producer); err != nil {
			t.Fatal(err)
		}
		got, err := s.CommitObservationRecord(ctx, 2, item.producer, 1, func(*db.Store) (db.ObservationDisposition, error) {
			return item.disposition, nil
		})
		if err != nil || got != (db.ObservationCommit{Cursor: 1, Applied: true}) {
			t.Fatalf("seed observation receipt: %+v %v", got, err)
		}
	}
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
		seedObservationProducers(t, db.NewStore(d))
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
		if tr.Name == "auth_sessions" || tr.Name == "auth_grants" || tr.Name == "push_subscriptions" {
			if tr.Rows != 0 {
				t.Errorf("restored %s: got %d rows, want none", tr.Name, tr.Rows)
			}
			continue
		}
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
	if err := ps.ReadAccess(ctx, func(r store.Access) error {
		state, err := r.Installation(ctx)
		if err != nil || state.ID != "installation-01" || state.Mode != store.AccessClaimed || state.ClaimedAt == nil {
			t.Fatalf("restored access state = %+v, %v", state, err)
		}
		a, err := r.AccountByEmail(ctx, " MEMBER@example.test ")
		if err != nil || a.ID != 2 || a.Role != store.RoleMember || a.Status != store.AccountDisabled {
			t.Fatalf("restored account = %+v, %v", a, err)
		}
		v, err := r.LocalVerifier(ctx, 1)
		if err != nil || v != "test verifier" {
			t.Fatal("restored verifier missing or changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ps.BindObservationProducer(ctx, 2, "worker-old"); !errors.Is(err, db.ErrObservationProducer) {
		t.Fatalf("copied retired producer: %v", err)
	}
	replayed, err := ps.CommitObservationRecord(ctx, 2, "worker-current", 1, func(*db.Store) (db.ObservationDisposition, error) {
		return db.ObservationDisposition{}, errors.New("copied cursor did not fence replay")
	})
	if err != nil || replayed != (db.ObservationCommit{Cursor: 1}) {
		t.Fatalf("copied active cursor: %+v %v", replayed, err)
	}
	var code, lost string
	var from, to time.Time
	err = dst.Pool().QueryRow(ctx, `SELECT code,lost,observed_from,observed_to FROM observation_dispositions WHERE machine_id=2 AND producer='worker-old' AND cursor=1`).
		Scan(&code, &lost, &from, &to)
	if err != nil || code != "gap" || lost != "18446744073709551615" || !from.Equal(observationGapFrom) || !to.Equal(observationGapTo) {
		t.Fatalf("copied gap: %q %q %v %v %v", code, lost, from, to, err)
	}
	err = dst.Pool().QueryRow(ctx, `SELECT code FROM observation_dispositions WHERE machine_id=2 AND producer='worker-current' AND cursor=1`).Scan(&code)
	if err != nil || code != "invalid_disposition" {
		t.Fatalf("copied rejected record: %q %v", code, err)
	}

	one, two := int64(1), int64(2)
	reads := map[string]func(*db.Store) (any, error){
		"T3 settings":           func(s *db.Store) (any, error) { return s.HarnessSettings(ctx, one, "t3") },
		"OpenCode settings":     func(s *db.Store) (any, error) { return s.HarnessSettings(ctx, one, "opencode") },
		"Claude settings":       func(s *db.Store) (any, error) { return s.HarnessSettings(ctx, one, "claude") },
		"worker Codex settings": func(s *db.Store) (any, error) { return s.HarnessSettings(ctx, two, "codex") },
		"T3 token":              func(s *db.Store) (any, error) { return s.HarnessSecret(ctx, one, "t3", "token") },
		"T3 admin token":        func(s *db.Store) (any, error) { return s.HarnessSecret(ctx, one, "t3", "adminToken") },
		"OpenCode credential":   func(s *db.Store) (any, error) { return s.HarnessSecret(ctx, one, "opencode", "credential") },
		"T3 status":             func(s *db.Store) (any, error) { return s.HarnessSyncStatus(ctx, one, "t3") },
		"task 1":                func(s *db.Store) (any, error) { return s.GetTask(ctx, 1) },
		"task 2":                func(s *db.Store) (any, error) { return s.GetTask(ctx, 2) },
		"search":                func(s *db.Store) (any, error) { return s.ListTasks(ctx, db.TaskFilter{Q: "NEEDLE %_", Closed: "all"}) },
		"journal": func(s *db.Store) (any, error) {
			return s.ReadJournal(ctx, store.JournalOwner{TaskID: &one}, store.JournalQuery{Kind: "all"})
		},
		"tree":        func(s *db.Store) (any, error) { return s.GetTree(ctx, 1) },
		"attachment":  func(s *db.Store) (any, error) { return s.GetAttachment(ctx, 1) },
		"snippets":    func(s *db.Store) (any, error) { return s.ListSnippets(ctx) },
		"preferences": func(s *db.Store) (any, error) { return s.GetPreferences(ctx) },
		"inbox":       func(s *db.Store) (any, error) { return s.ListNotifications(ctx, 10, nil) },
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
	for _, engine := range []store.Engine{lite, dst} {
		if err := engine.Read(ctx, func(tx store.Tx) error {
			task, err := tx.Tasks().Get(ctx, 2)
			if err != nil {
				return err
			}
			pin := store.WorkspacePin{ExternalID: "next", Root: "/next"}
			frozen := store.TaskWorkspace{Path: "/unbound", Source: "pinned", ExternalID: "unbound"}
			if task.WorkspacePin == nil || *task.WorkspacePin != pin || task.Workspace == nil || *task.Workspace != frozen {
				t.Fatalf("copied task pin %+v and frozen projection %+v", task.WorkspacePin, task.Workspace)
			}
			d, err := tx.Attempts().Destination(ctx, 2, 1)
			if err != nil {
				return err
			}
			if want := (store.LaunchDestination{ConnectionID: 1, ExternalProjectID: "unbound", Workspace: "/unbound"}); d != want {
				t.Fatalf("copied unbound destination %+v, want %+v", d, want)
			}
			template, err := tx.Schedules().Template(ctx, 1)
			if err != nil {
				return err
			}
			if want := (store.WorkspacePin{ExternalID: "ext-1-secondary", Root: "/old/w2"}); template.WorkspacePin == nil || *template.WorkspacePin != want {
				t.Fatalf("copied schedule template pin %+v, want %+v", template.WorkspacePin, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	lite.Close()

	// Evidence keeps its exact bytes; IDs continue past the high-water
	// mark (snippet 3 and project event 9 were deleted before the copy).
	var evidence string
	if err := dst.Pool().QueryRow(ctx, `SELECT evidence FROM task_acceptances WHERE id = 1`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != legacyEvidence {
		t.Fatalf("evidence bytes changed: %q", evidence)
	}
	for table, want := range map[string]int64{"snippets": 4, "project_events": 10, "users": 3, "machines": 3, "harness_connections": 4, "tasks": 4, "push_subscriptions": 2} {
		var id int64
		q := `INSERT INTO ` + table + ` DEFAULT VALUES RETURNING id`
		switch table {
		case "snippets":
			q = `INSERT INTO snippets (name, body) VALUES ('new', 'b') RETURNING id`
		case "project_events":
			q = `INSERT INTO project_events (project_id, type, text) VALUES (1, 'project_split', 'split') RETURNING id`
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
	initial, err := db.NewStore(dst).ConfigureAccess(ctx, nil)
	if err != nil || initial.ID == "" || initial.Mode != store.AccessPersonal {
		t.Fatalf("initialized target = %+v, %v", initial, err)
	}
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
	got, err := db.NewStore(dst).ConfigureAccess(ctx, nil)
	if err != nil || got.ID != "installation-01" || got.Mode != store.AccessClaimed {
		t.Fatalf("copied installation = %+v, %v", got, err)
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
	src := openSource(t, sourceDB(t, sqlite.MaxVersion(), `UPDATE tasks SET launched_at = 'yesterday' WHERE id = 1`))
	dst := freshTarget(t)
	_, err := datacopy.Run(ctx, src, dst)
	if err == nil || !strings.Contains(err.Error(), "tasks.launched_at") {
		t.Fatalf("got %v, want a tasks.launched_at conversion error", err)
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
