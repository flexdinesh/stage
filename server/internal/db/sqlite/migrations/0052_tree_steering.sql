-- +goose Up
-- Tree steering (ADR 0013): a steerer binding per tree and its durable
-- event outbox. One active binding per tree and per task (ended_at NULL).
-- request_id/request_digest make POST /api/trees/{id}/steerer replayable.
CREATE TABLE tree_steerers (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 tree_id INTEGER NOT NULL REFERENCES task_trees(id) ON DELETE CASCADE,
 task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
 request_id TEXT NOT NULL,
 request_digest TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (stage_now()),
 ended_at TEXT,
 end_reason TEXT CHECK (end_reason IN ('replaced', 'stopped', 'tree_finished', 'task_removed')),
 delivered_through INTEGER NOT NULL DEFAULT 0,
 last_delivered_at TEXT,
 last_error TEXT,
 attribution TEXT
);
CREATE UNIQUE INDEX tree_steerers_request_idx ON tree_steerers(tree_id, request_id);
CREATE UNIQUE INDEX tree_steerers_active_tree_idx ON tree_steerers(tree_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX tree_steerers_active_task_idx ON tree_steerers(task_id) WHERE ended_at IS NULL;
CREATE INDEX tree_steerers_task_idx ON tree_steerers(task_id, id);

CREATE TABLE steering_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 tree_id INTEGER NOT NULL REFERENCES task_trees(id) ON DELETE CASCADE,
 steerer_id INTEGER NOT NULL REFERENCES tree_steerers(id) ON DELETE CASCADE,
 kind TEXT NOT NULL,
 task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
 payload TEXT NOT NULL DEFAULT '{}',
 caused_by_connection_id INTEGER,
 caused_by_external_session_id TEXT,
 caused_by_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
 created_at TEXT NOT NULL DEFAULT (stage_now())
);
CREATE INDEX steering_events_tree_idx ON steering_events(tree_id, id);
CREATE INDEX steering_events_steerer_idx ON steering_events(steerer_id, id);
