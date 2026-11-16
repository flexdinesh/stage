package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// openUnmigrated opens a fresh temp database with no migrations applied.
func openUnmigrated(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := Open(context.Background(), filepath.Join(t.TempDir(), DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// migrateTo applies the embedded migrations up to and including version.
func migrateTo(t *testing.T, pool *sql.DB, version int64) {
	t.Helper()
	provider, err := newProvider(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
}

func tableExists(t *testing.T, pool *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrationDropsContinuations(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 1)
	tables := []string{"continuations", "continuation_events"}
	for _, name := range tables {
		if !tableExists(t, pool, name) {
			t.Fatalf("%s missing after 0001", name)
		}
	}
	if _, err := pool.Exec(`INSERT INTO threads (t3_thread_id, t3_project_id, status, created_at, updated_at)
		VALUES ('a', 'p', 'idle', '', ''), ('b', 'p', 'idle', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO continuations (order_id, after_thread_id, target_thread_id, prompt) VALUES ('o', 'a', 'b', 'next')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO continuation_events (continuation_id, state) VALUES (1, 'waiting')`); err != nil {
		t.Fatal(err)
	}
	migrateTo(t, pool, 2)
	for _, name := range tables {
		if tableExists(t, pool, name) {
			t.Fatalf("%s still exists after 0002", name)
		}
	}
	if !tableExists(t, pool, "threads") {
		t.Fatal("threads dropped by 0002")
	}
}

func mustExec(t *testing.T, pool *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func indexExists(t *testing.T, pool *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrationTrees(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 2)
	const ts = "2026-11-01T10:00:00.000000Z"
	mustExec(t, pool, `INSERT INTO projects (id, t3_project_id, title, workspace_root, source) VALUES (1, 'proj-1', 'P', '/w', 'app')`)
	mustExec(t, pool, `INSERT INTO threads (t3_thread_id, t3_project_id, title, status, created_at, updated_at, provider_instance_id, model, runtime_mode)
		VALUES ('sess-1', 'proj-1', 'Planner', 'running', ?, ?, 'codex', 'gpt', 'bogus'),
		       ('sess-2', 'proj-1', '', 'idle', ?, ?, NULL, 'gpt', NULL),
		       ('foreign', 'other-proj', 'Elsewhere', 'running', ?, ?, NULL, NULL, NULL)`, ts, ts, ts, ts, ts, ts)
	// 1 <- 2 <- 3 chain; 4 waits on sess-1 and 6 runs after 4; 5 waits on an
	// unmirrored session, 7 on a session in a project stage lacks; 8 is
	// standalone; 9 ran after sess-2 (history, not backlog).
	mustExec(t, pool, `INSERT INTO tasks (id, project_id, title, prompt, state) VALUES
		(1, 1, 'Build', 'p', 'settled'), (2, 1, 'Docs', 'p', 'running'), (3, 1, 'Ship', 'p', 'backlog'),
		(4, 1, 'After planner', 'p', 'backlog'), (5, 1, 'Ghost wait', 'p', 'backlog'), (6, 1, 'After 4', 'p', 'backlog'),
		(7, 1, 'Foreign wait', 'p', 'backlog'), (8, 1, 'Solo', 'p', 'backlog'), (9, 1, 'History', 'p', 'running')`)
	mustExec(t, pool, `UPDATE tasks SET after_task_id = 1 WHERE id = 2`)
	mustExec(t, pool, `UPDATE tasks SET after_task_id = 2 WHERE id = 3`)
	mustExec(t, pool, `UPDATE tasks SET after_task_id = 4 WHERE id = 6`)
	mustExec(t, pool, `UPDATE tasks SET after_thread_id = 'sess-1' WHERE id = 4`)
	mustExec(t, pool, `UPDATE tasks SET after_thread_id = 'ghost' WHERE id = 5`)
	mustExec(t, pool, `UPDATE tasks SET after_thread_id = 'foreign' WHERE id = 7`)
	mustExec(t, pool, `UPDATE tasks SET after_thread_id = 'sess-2', t3_thread_id = 'th-9' WHERE id = 9`)
	mustExec(t, pool, `INSERT INTO task_handoffs (task_id, attempt, source_task_id, source_thread_id, source_title, status, text)
		VALUES (2, 1, 1, 'th-1', 'Build', 'sent', 'block'), (6, 1, NULL, 'sess-1', 'Planner', 'skipped', '')`)
	mustExec(t, pool, `INSERT INTO notifications (event, task_id, title, url) VALUES ('done', 1, 'Build', '/tasks/1')`)

	migrateTo(t, pool, 3)
	s := NewStore(pool)
	ctx := context.Background()

	// sess-1 became adopted task 10 (state from the session, fallback runtime mode).
	adopted, err := s.GetTask(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.Adopted || adopted.Title != "Planner" || adopted.Prompt != "" || adopted.State != StateRunning ||
		adopted.T3ThreadID == nil || *adopted.T3ThreadID != "sess-1" || adopted.RuntimeMode != "full-access" ||
		adopted.ProviderInstanceID == nil || *adopted.ProviderInstanceID != "codex" || adopted.LaunchAttempt != 1 {
		t.Fatalf("adopted %+v", adopted)
	}
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM tasks WHERE adopted = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("adopted count %d %v (only backlog waits on mirrored stage sessions)", n, err)
	}
	hasEvent := func(id int64, detail string) bool {
		events, err := s.TaskEvents(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if e.Detail == detail {
				return true
			}
		}
		return false
	}
	if !hasEvent(10, "adopted session (migration)") {
		t.Fatal("missing adoption event")
	}
	if !hasEvent(5, "session wait on ghost removed: session not in stage") || !hasEvent(7, "session wait on foreign removed: session not in stage") {
		t.Fatal("missing dropped-wait events")
	}

	// Trees: {1,2,3} titled after root 1; {4,6,10} titled after root 10.
	want := map[int64]struct {
		tree  int64
		needs []int64
	}{
		1: {1, nil}, 2: {1, []int64{1}}, 3: {1, []int64{2}},
		4: {4, []int64{10}}, 6: {4, []int64{4}}, 10: {4, nil},
	}
	for id, w := range want {
		got, err := s.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.TreeID == nil || *got.TreeID != w.tree || !slices.Equal(got.Needs, nonNilIDs(w.needs)) {
			t.Fatalf("task %d tree %v needs %v, want %d %v", id, got.TreeID, got.Needs, w.tree, w.needs)
		}
	}
	for _, id := range []int64{5, 7, 8, 9} {
		if got, _ := s.GetTask(ctx, id); got.TreeID != nil || len(got.Needs) != 0 {
			t.Fatalf("task %d must stay standalone: %+v", id, got)
		}
	}
	for id, title := range map[int64]string{1: "Build", 4: "Planner"} {
		tree, err := s.GetTree(ctx, id)
		if err != nil || tree.Title != title || tree.StartMode != StartAuto {
			t.Fatalf("tree %d %+v %v", id, tree.Tree, err)
		}
	}
	// Readiness follows: 3 waits on running 2; 4 waits on running session task 10.
	if got, _ := s.GetTask(ctx, 3); got.Readiness == nil || *got.Readiness != ReadinessBlocked {
		t.Fatalf("task 3 readiness %v", got.Readiness)
	}

	// Legacy columns NULL, their indexes dropped.
	if err := pool.QueryRow(`SELECT count(*) FROM tasks WHERE after_task_id IS NOT NULL OR after_thread_id IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy links left: %d %v", n, err)
	}
	for _, idx := range []string{"tasks_after_task_id_key", "tasks_after_thread_idx"} {
		if indexExists(t, pool, idx) {
			t.Fatalf("%s still exists", idx)
		}
	}
	if !indexExists(t, pool, "tasks_tree_idx") || !indexExists(t, pool, "task_needs_need_idx") {
		t.Fatal("tree indexes missing")
	}

	// Handoffs: session source backfilled to its adopted task; one row per
	// source per attempt now allowed.
	hs, err := s.GetHandoffs(ctx, 6, 1)
	if err != nil || len(hs) != 1 || hs[0].SourceTaskID == nil || *hs[0].SourceTaskID != 10 || hs[0].Status != HandoffSkipped {
		t.Fatalf("backfilled handoff %+v %v", hs, err)
	}
	if hs, _ := s.GetHandoffs(ctx, 2, 1); len(hs) != 1 || hs[0].Text != "block" || *hs[0].SourceTaskID != 1 {
		t.Fatalf("kept handoff %+v", hs)
	}
	if err := s.RecordHandoff(ctx, 2, Handoff{Attempt: 1, Status: HandoffSent, SourceTaskID: ptrTo(int64(10)), SourceThreadID: "sess-1", Text: "second"}); err != nil {
		t.Fatal(err)
	}
	if hs, _ := s.GetHandoffs(ctx, 2, 1); len(hs) != 2 {
		t.Fatalf("two sources %+v", hs)
	}

	// Notifications keep rows and indexes and accept ready; prefs gained it.
	if _, err := s.InsertNotification(ctx, NewNotification{Event: EventReady, TaskID: ptrTo(int64(3)), Title: "Ship", URL: "/tasks/3"}); err != nil {
		t.Fatalf("ready notification: %v", err)
	}
	if err := pool.QueryRow(`SELECT count(*) FROM notifications`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("notifications %d %v", n, err)
	}
	for _, idx := range []string{"notifications_created_idx", "notifications_unread_idx"} {
		if !indexExists(t, pool, idx) {
			t.Fatalf("%s missing", idx)
		}
	}
	if _, err := pool.Exec(`INSERT INTO notifications (event, title, url) VALUES ('bogus', 'x', '/')`); err == nil {
		t.Fatal("event check must still reject unknown events")
	}
	prefs, err := s.GetPreferences(ctx)
	if err != nil || !slices.Contains(prefs.NotifyEvents, EventReady) || len(prefs.NotifyEvents) != 5 {
		t.Fatalf("prefs %+v %v", prefs.NotifyEvents, err)
	}
}

func ptrTo[T any](v T) *T { return &v }
