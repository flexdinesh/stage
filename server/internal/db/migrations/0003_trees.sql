-- +goose Up

-- Task trees (docs/api.md "Trees"): a tree is a DAG of tasks; a task has
-- 0..n needs (task_needs) in its own tree and launches (auto) or reads ready
-- (manual) once every need is settled. Trees replace runs-after links,
-- session waits and session batch ordering. Forward-only.
--
-- tasks is NOT rebuilt: goose runs each file in one transaction with
-- foreign_keys on, so DROP TABLE tasks would cascade into task_events,
-- task_attachments, task_handoffs and task_launch_bases. Instead columns are
-- added, the link indexes dropped and after_task_id / after_thread_id set to
-- NULL. Both columns and the tasks_one_wait check stay as dead schema (always
-- NULL from here on). Only notifications (event check gains 'ready') and
-- task_handoffs (one row per need: unique per task, attempt and source) are
-- rebuilt; nothing references them.

--   start_mode: auto (a member launches once its needs settle) | manual (it
--     reads ready and waits for a start).
CREATE TABLE task_trees (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT NOT NULL CHECK (length(trim(title)) > 0),
    start_mode TEXT NOT NULL DEFAULT 'auto' CHECK (start_mode IN ('auto', 'manual')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);

--   tree_id: the task's tree (one tree per task).
--   start_mode: per-task override of the tree's start_mode; NULL = inherit.
--   adopted: the task tracks an existing T3 session ("Track as task"):
--     prompt is '', its state follows the session; never started by stage.
--   ready_at: when a manual task's needs all settled (one ready notification
--     per episode); cleared when it is no longer ready and on launch.
ALTER TABLE tasks ADD COLUMN tree_id    INTEGER REFERENCES task_trees (id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN start_mode TEXT CHECK (start_mode IN ('auto', 'manual'));
ALTER TABLE tasks ADD COLUMN adopted    INTEGER NOT NULL DEFAULT 0 CHECK (adopted IN (0, 1));
ALTER TABLE tasks ADD COLUMN ready_at   TEXT;
CREATE INDEX tasks_tree_idx ON tasks (tree_id) WHERE tree_id IS NOT NULL;

-- task_id needs need_id (same tree; no cycles; enforced by the app).
CREATE TABLE task_needs (
    task_id INTEGER NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    need_id INTEGER NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, need_id),
    CHECK (task_id <> need_id)
) WITHOUT ROWID;
CREATE INDEX task_needs_need_idx ON task_needs (need_id);

-- 1. Sessions that backlog tasks waited on become adopted tasks. State is a
-- copy of db.adoptStateSQL (sync.MapThreadState). Unmirrored waits (or
-- sessions in a project stage does not have) are dropped with an event.
INSERT INTO tasks (project_id, title, prompt, provider_instance_id, model, runtime_mode, interaction_mode,
  state, t3_thread_id, launch_attempt, launched_at, finished_at, settled_at, adopted)
SELECT pid, title, '', prov, model, rmode, imode, st, tid, 1, created_at,
  CASE WHEN st IN ('done', 'failed', 'interrupted', 'cancelled', 'settled') THEN COALESCE(run_ended_at, updated_at) END,
  CASE WHEN st = 'settled' THEN COALESCE(settled_at, updated_at) END, 1
FROM (
  SELECT p.id AS pid, th.t3_thread_id AS tid, th.created_at, th.updated_at, th.run_ended_at, th.settled_at,
    COALESCE(NULLIF(trim(th.title), ''), 'Session ' || substr(th.t3_thread_id, 1, 8)) AS title,
    CASE WHEN th.provider_instance_id IS NOT NULL AND th.model IS NOT NULL THEN th.provider_instance_id END AS prov,
    CASE WHEN th.provider_instance_id IS NOT NULL AND th.model IS NOT NULL THEN th.model END AS model,
    CASE WHEN th.runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')
         THEN th.runtime_mode ELSE 'full-access' END AS rmode,
    CASE WHEN th.interaction_mode IN ('default', 'plan') THEN th.interaction_mode ELSE 'default' END AS imode,
    CASE WHEN th.settled THEN 'settled'
         WHEN th.pending_request_count > 0 THEN 'needs_input'
         WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) IN ('preparing', 'queued', 'starting', 'running', 'waiting') THEN 'running'
         WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) = 'failed' THEN 'failed'
         WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) = 'interrupted' THEN 'interrupted'
         WHEN COALESCE(NULLIF(th.status, 'idle'), th.latest_run_status) IN ('cancelled', 'rolled_back') THEN 'cancelled'
         ELSE 'done' END AS st
  FROM threads th JOIN projects p ON p.t3_project_id = th.t3_project_id
  WHERE th.t3_thread_id IN (SELECT after_thread_id FROM tasks WHERE state = 'backlog' AND after_thread_id IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM tasks o WHERE o.t3_thread_id = th.t3_thread_id)
);
INSERT INTO task_events (task_id, from_state, to_state, detail)
  SELECT id, NULL, state, 'adopted session (migration)' FROM tasks WHERE adopted = 1;
INSERT INTO task_events (task_id, from_state, to_state, detail)
  SELECT w.id, 'backlog', 'backlog', 'session wait on ' || w.after_thread_id || ' removed: session not in stage'
  FROM tasks w WHERE w.state = 'backlog' AND w.after_thread_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM tasks o WHERE o.t3_thread_id = w.after_thread_id);

-- 2. Edges: every runs-after link (history included) and backlog session waits.
INSERT OR IGNORE INTO task_needs (task_id, need_id)
  SELECT id, after_task_id FROM tasks WHERE after_task_id IS NOT NULL;
INSERT OR IGNORE INTO task_needs (task_id, need_id)
  SELECT w.id, o.id FROM tasks w JOIN tasks o ON o.t3_thread_id = w.after_thread_id
  WHERE w.state = 'backlog' AND w.after_thread_id IS NOT NULL AND o.id <> w.id;

-- 3. One auto tree per connected component: tree id = the component's lowest
-- task id, title = its earliest root's title.
CREATE TEMP TABLE mig_tree (task_id INTEGER PRIMARY KEY, tree INTEGER NOT NULL);
INSERT INTO mig_tree (task_id, tree)
  WITH RECURSIVE
    und(a, b) AS (SELECT task_id, need_id FROM task_needs UNION SELECT need_id, task_id FROM task_needs),
    reach(root, id) AS (SELECT a, a FROM und UNION SELECT reach.root, und.b FROM reach JOIN und ON und.a = reach.id)
  SELECT id, min(root) FROM reach GROUP BY id;
INSERT INTO task_trees (id, title, start_mode, created_at)
SELECT m.tree,
  COALESCE((SELECT NULLIF(trim(h.title), '') FROM tasks h JOIN mig_tree hm ON hm.task_id = h.id
            WHERE hm.tree = m.tree AND NOT EXISTS (SELECT 1 FROM task_needs n WHERE n.task_id = h.id)
            ORDER BY h.id LIMIT 1), 'Tree ' || m.tree),
  'auto', min(t.created_at)
FROM mig_tree m JOIN tasks t ON t.id = m.task_id GROUP BY m.tree;
UPDATE tasks SET tree_id = (SELECT tree FROM mig_tree WHERE task_id = tasks.id)
WHERE id IN (SELECT task_id FROM mig_tree);
DROP TABLE mig_tree;

-- 4. Legacy links are gone (columns stay, always NULL).
DROP INDEX tasks_after_task_id_key;
DROP INDEX tasks_after_thread_idx;
UPDATE tasks SET after_task_id = NULL, after_thread_id = NULL
WHERE after_task_id IS NOT NULL OR after_thread_id IS NOT NULL;

-- 5. Handoffs: one row per need a launch attempt read (sent) or skipped.
-- Session sources map to their adopted task.
CREATE TABLE task_handoffs_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id          INTEGER NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    attempt          INTEGER NOT NULL,
    source_task_id   INTEGER,
    source_thread_id TEXT NOT NULL,
    source_title     TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL CHECK (status IN ('sent', 'skipped')),
    text             TEXT NOT NULL DEFAULT '',
    reason           TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    UNIQUE (task_id, attempt, source_task_id)
);
INSERT INTO task_handoffs_new (id, task_id, attempt, source_task_id, source_thread_id, source_title, status, text, reason, created_at)
SELECT h.id, h.task_id, h.attempt,
  COALESCE(h.source_task_id, (SELECT t.id FROM tasks t WHERE t.t3_thread_id = h.source_thread_id)),
  h.source_thread_id, h.source_title, h.status, h.text, h.reason, h.created_at
FROM task_handoffs h;
DROP TABLE task_handoffs;
ALTER TABLE task_handoffs_new RENAME TO task_handoffs;

-- 6. 'ready' notification event (a manual task whose needs settled).
CREATE TABLE notifications_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    event         TEXT NOT NULL CONSTRAINT notifications_event_check
        CHECK (event IN ('needs_input', 'ready', 'done', 'failed', 'settled')),
    task_id       INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
    t3_thread_id  TEXT,
    title         TEXT NOT NULL,
    project_title TEXT,
    url           TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    read_at       TEXT
);
INSERT INTO notifications_new (id, event, task_id, t3_thread_id, title, project_title, url, created_at, read_at)
SELECT id, event, task_id, t3_thread_id, title, project_title, url, created_at, read_at FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;
CREATE INDEX notifications_created_idx ON notifications (created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (id) WHERE read_at IS NULL;

-- 7. The notification filter gains 'ready' (on by default).
UPDATE preferences SET notify_events = json_insert(notify_events, '$[#]', 'ready')
WHERE NOT EXISTS (SELECT 1 FROM json_each(preferences.notify_events) WHERE value = 'ready');
