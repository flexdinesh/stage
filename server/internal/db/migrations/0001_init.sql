-- +goose Up

-- SQLite schema. Conventions:
--   timestamps: TEXT, fixed-width UTC 'YYYY-MM-DDTHH:MM:SS.ffffffZ'
--     (microseconds), so lexical order is time order. SQL defaults use
--     strftime('%Y-%m-%dT%H:%M:%f000Z', 'now') (ms clock, 000 microseconds);
--     the app writes the same format (db.timeLayout, db.nowSQL).
--   booleans: INTEGER 0/1.
--   JSON (model options, workspace, string lists): TEXT holding JSON.
--   ids: INTEGER PRIMARY KEY AUTOINCREMENT (never reused, like identity).
-- Table and column names (and column order) match the former Postgres schema.

-- Single-row T3 connection state (OAuth mcp-client token, sync bookkeeping).
CREATE TABLE t3_connection (
    id                INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    client_id         TEXT,
    token             TEXT,
    expires_at        TEXT,
    last_error        TEXT,
    last_full_sync_at TEXT,
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);
INSERT INTO t3_connection (id) VALUES (1);

-- App projects, 1:1 with T3 projects. t3_deleted_at: T3 deletedAt of a
-- mirrored project; a deleted project stays (tasks reference it) and is
-- archived, but no longer claims its workspace root.
CREATE TABLE projects (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    t3_project_id  TEXT NOT NULL UNIQUE,
    title          TEXT NOT NULL,
    workspace_root TEXT NOT NULL,
    source         TEXT NOT NULL CHECK (source IN ('app', 't3')),
    archived_at    TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    t3_deleted_at  TEXT
);
CREATE INDEX projects_workspace_root_idx ON projects (workspace_root);

-- Mirror of T3 threads (all projects, full history).
--   observed_state: thread watcher's last mapped state (TaskState) for
--     external threads; NULL = never observed.
--   run_started_at/run_ended_at: elapsed run span from t3_thread_read
--     recentRuns; NULL until a detail read sees a run.
--   last_word_*: the thread's latest assistant reply (text is a capped
--     excerpt, preview plain text, at the reply item's updatedAt);
--     last_word_checked_at is the thread updated_at the last check saw.
--   pull_requests: [{host, repository, number, url, source, state}].
--   recent_runs/run_count: t3_thread_read recentRuns (newest first, at most
--     50) and runCount; written by detail reads only.
--   pending_question_count: pending requests that are answerable questions.
CREATE TABLE threads (
    t3_thread_id           TEXT PRIMARY KEY,
    t3_project_id          TEXT NOT NULL,
    title                  TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL,
    settled                INTEGER NOT NULL DEFAULT 0 CHECK (settled IN (0, 1)),
    settled_at             TEXT,
    pending_request_count  INTEGER NOT NULL DEFAULT 0,
    provider_instance_id   TEXT,
    model                  TEXT,
    runtime_mode           TEXT,
    interaction_mode       TEXT,
    branch                 TEXT,
    worktree_path          TEXT,
    parent_thread_id       TEXT,
    relationship           TEXT,
    creation_source        TEXT,
    link                   TEXT,
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL,
    synced_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    observed_state         TEXT,
    archived               INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    active_run_id          TEXT,
    latest_run_id          TEXT,
    latest_run_status      TEXT,
    run_started_at         TEXT,
    run_ended_at           TEXT,
    last_word_text         TEXT,
    last_word_preview      TEXT,
    last_word_truncated    INTEGER NOT NULL DEFAULT 0 CHECK (last_word_truncated IN (0, 1)),
    last_word_run_id       TEXT,
    last_word_item_id      TEXT,
    last_word_at           TEXT,
    last_word_checked_at   TEXT,
    pull_requests          TEXT NOT NULL DEFAULT '[]' CHECK (json_type(pull_requests) = 'array'),
    recent_runs            TEXT NOT NULL DEFAULT '[]' CHECK (json_type(recent_runs) = 'array'),
    run_count              INTEGER NOT NULL DEFAULT 0,
    pending_question_count INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX threads_t3_project_id_idx ON threads (t3_project_id);
CREATE INDEX threads_created_at_idx ON threads (created_at);
CREATE INDEX threads_updated_at_idx ON threads (updated_at);
CREATE INDEX threads_status_idx ON threads (status);
-- history/stats with subagents=exclude list root threads only.
CREATE INDEX threads_root_created_idx ON threads (created_at DESC) WHERE parent_thread_id IS NULL;

-- Recurring task templates: on each cron fire (evaluated in tz) a schedule
-- spawns a task with its config and images (docs/api.md "Schedule rules").
-- next_run_at: next fire (UTC); NULL while paused.
CREATE TABLE schedules (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id           INTEGER NOT NULL REFERENCES projects (id),
    title                TEXT NOT NULL,
    prompt               TEXT NOT NULL,
    provider_instance_id TEXT,
    model                TEXT,
    model_options        TEXT NOT NULL DEFAULT '[]' CHECK (json_type(model_options) = 'array'),
    runtime_mode         TEXT NOT NULL DEFAULT 'full-access'
        CHECK (runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')),
    interaction_mode     TEXT NOT NULL DEFAULT 'default' CHECK (interaction_mode IN ('default', 'plan')),
    workspace            TEXT NOT NULL DEFAULT '{"type":"root"}' CHECK (json_type(workspace) = 'object'),
    tags                 TEXT NOT NULL DEFAULT '[]' CHECK (json_type(tags) = 'array'),
    notes                TEXT NOT NULL DEFAULT '',
    cron                 TEXT NOT NULL,
    tz                   TEXT NOT NULL,
    paused               INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    next_run_at          TEXT,
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    origin_thread_id     TEXT
);
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE NOT paused;
CREATE INDEX schedules_project_idx ON schedules (project_id);

-- Tasks.
--   model_options: [{"id":"effort","value":"low"}] (T3 ProviderOptionSelection[])
--   workspace: {"type":"root"} | {"type":"worktree","baseRef":"origin/main","branch":"feat-x"|null}
--   tags: JSON array of strings.
--   launch_reuse: the current attempt's t3_thread_launch failed in transport
--     (outcome unknown, no thread recorded); the next start reuses its
--     clientRequestId instead of beginning a new attempt.
--   after_task_id: runs-after link; a task has at most one follower.
--   launch_request_id: clientRequestId of the current launch attempt,
--     "task-<id>-<uuid>"; kept while launch_reuse is set.
--   start_at/start_tz: one-shot start of a backlog task (entered in start_tz).
--   schedule_id: the schedule that spawned the task.
--   origin_thread_id: the T3 thread that created the task (agent interface).
--   after_thread_id: a backlog task waiting on a T3 thread that is not a
--     task's. A task waits on at most one of: start time, task, thread.
--   handoff: append the predecessor handoff at launch (docs/api.md "Handoff").
CREATE TABLE tasks (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id           INTEGER NOT NULL REFERENCES projects (id),
    title                TEXT NOT NULL,
    prompt               TEXT NOT NULL,
    provider_instance_id TEXT,
    model                TEXT,
    model_options        TEXT NOT NULL DEFAULT '[]' CHECK (json_type(model_options) = 'array'),
    runtime_mode         TEXT NOT NULL DEFAULT 'full-access'
        CHECK (runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')),
    interaction_mode     TEXT NOT NULL DEFAULT 'default' CHECK (interaction_mode IN ('default', 'plan')),
    workspace            TEXT NOT NULL DEFAULT '{"type":"root"}' CHECK (json_type(workspace) = 'object'),
    tags                 TEXT NOT NULL DEFAULT '[]' CHECK (json_type(tags) = 'array'),
    notes                TEXT NOT NULL DEFAULT '',
    state                TEXT NOT NULL DEFAULT 'backlog' CHECK (state IN (
        'backlog', 'launching', 'running', 'needs_input', 'done',
        'settled', 'failed', 'interrupted', 'cancelled'
    )),
    t3_thread_id         TEXT UNIQUE,
    launch_attempt       INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT,
    launched_at          TEXT,
    finished_at          TEXT,
    settled_at           TEXT,
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    launch_reuse         INTEGER NOT NULL DEFAULT 0 CHECK (launch_reuse IN (0, 1)),
    after_task_id        INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
    launch_request_id    TEXT,
    start_at             TEXT,
    start_tz             TEXT,
    schedule_id          INTEGER REFERENCES schedules (id) ON DELETE SET NULL,
    origin_thread_id     TEXT,
    after_thread_id      TEXT,
    handoff              INTEGER NOT NULL DEFAULT 1 CHECK (handoff IN (0, 1)),
    CONSTRAINT tasks_start_pair CHECK ((start_at IS NULL) = (start_tz IS NULL)),
    CONSTRAINT tasks_one_wait CHECK (
        (start_at IS NOT NULL) + (after_task_id IS NOT NULL) + (after_thread_id IS NOT NULL) <= 1)
);
CREATE INDEX tasks_state_idx ON tasks (state);
CREATE INDEX tasks_project_id_idx ON tasks (project_id);
CREATE INDEX tasks_created_at_idx ON tasks (created_at);
CREATE UNIQUE INDEX tasks_after_task_id_key ON tasks (after_task_id) WHERE after_task_id IS NOT NULL;
CREATE INDEX tasks_due_start_idx ON tasks (start_at) WHERE state = 'backlog' AND start_at IS NOT NULL;
CREATE INDEX tasks_schedule_idx ON tasks (schedule_id) WHERE schedule_id IS NOT NULL;
CREATE INDEX tasks_after_thread_idx ON tasks (after_thread_id) WHERE after_thread_id IS NOT NULL;
-- Session batch (docs/api.md "Session batch"): tasks created by one session.
CREATE INDEX tasks_origin_thread_idx ON tasks (origin_thread_id) WHERE origin_thread_id IS NOT NULL;

CREATE TABLE task_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    INTEGER NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    from_state TEXT,
    to_state   TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);
CREATE INDEX task_events_task_id_idx ON task_events (task_id, at);

-- Plain-text snippets, optionally scoped to one project.
CREATE TABLE snippets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    body       TEXT NOT NULL CONSTRAINT snippets_body_check CHECK (body <> ''),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    project_id INTEGER REFERENCES projects (id) ON DELETE CASCADE
);

-- Single-row app preferences. pinned / last_used are new-task model
-- defaults: {"providerInstanceId","model","modelOptions","runtimeMode","interactionMode"}.
-- notify_events: events (TaskState) that notify (inbox + push), a JSON array
-- of needs_input, done, failed, settled (validated by the app).
CREATE TABLE preferences (
    id            INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    pinned        TEXT CHECK (pinned IS NULL OR json_type(pinned) = 'object'),
    last_used     TEXT CHECK (last_used IS NULL OR json_type(last_used) = 'object'),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    notify_events TEXT NOT NULL DEFAULT '["needs_input","failed","done","settled"]'
        CHECK (json_type(notify_events) = 'array')
);
INSERT INTO preferences (id) VALUES (1);

-- Task and schedule images. Staged (not yet attached, or a follow-up image
-- waiting to be sent) = task_id and schedule_id both NULL; staged rows older
-- than 24h are swept.
CREATE TABLE task_attachments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     INTEGER REFERENCES tasks (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    mime_type   TEXT NOT NULL CHECK (mime_type IN ('image/png', 'image/jpeg', 'image/gif', 'image/webp')),
    size_bytes  INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    data        BLOB NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    schedule_id INTEGER REFERENCES schedules (id) ON DELETE CASCADE,
    CONSTRAINT task_attachments_one_owner CHECK (task_id IS NULL OR schedule_id IS NULL)
);
CREATE INDEX task_attachments_task_idx ON task_attachments (task_id, id);
CREATE INDEX task_attachments_staged_idx ON task_attachments (created_at) WHERE task_id IS NULL AND schedule_id IS NULL;
CREATE INDEX task_attachments_schedule_idx ON task_attachments (schedule_id, id) WHERE schedule_id IS NOT NULL;

-- Single-row VAPID key pair (base64url), generated on first start.
CREATE TABLE push_vapid (
    id          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    public_key  TEXT NOT NULL,
    private_key TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);

-- Browser Web Push subscriptions (PushSubscription.toJSON()).
CREATE TABLE push_subscriptions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint        TEXT NOT NULL UNIQUE,
    p256dh          TEXT NOT NULL,
    auth            TEXT NOT NULL,
    user_agent      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    last_success_at TEXT,
    last_error      TEXT
);

-- Inbox: every notification the notifier sends (also with no push
-- subscriptions). Pruned after 30 days. Title/project are snapshots.
CREATE TABLE notifications (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    event         TEXT NOT NULL CONSTRAINT notifications_event_check
        CHECK (event IN ('needs_input', 'done', 'failed', 'settled')),
    task_id       INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
    t3_thread_id  TEXT,
    title         TEXT NOT NULL,
    project_title TEXT,
    url           TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    read_at       TEXT
);
CREATE INDEX notifications_created_idx ON notifications (created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (id) WHERE read_at IS NULL;

-- Session continuations. order_id is a UUID (generated by the app).
-- wait_reasons: JSON array of strings.
CREATE TABLE continuations (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id                 TEXT NOT NULL,
    after_thread_id          TEXT NOT NULL REFERENCES threads (t3_thread_id),
    target_thread_id         TEXT NOT NULL REFERENCES threads (t3_thread_id),
    previous_continuation_id INTEGER UNIQUE REFERENCES continuations (id),
    prompt                   TEXT NOT NULL CHECK (length(trim(prompt)) > 0),
    state                    TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN
        ('waiting', 'paused', 'blocked', 'sending', 'running', 'awaiting_settle', 'settled', 'cancelled', 'uncertain')),
    wait_reasons             TEXT NOT NULL DEFAULT '[]' CHECK (json_type(wait_reasons) = 'array'),
    last_error               TEXT,
    request_id               TEXT UNIQUE,
    delivery_prompt          TEXT,
    claim_expires_at         TEXT,
    run_id                   TEXT,
    message_id               TEXT,
    run_completed            INTEGER NOT NULL DEFAULT 0 CHECK (run_completed IN (0, 1)),
    created_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    updated_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    sent_at                  TEXT,
    settled_at               TEXT,
    UNIQUE (order_id, after_thread_id),
    UNIQUE (order_id, target_thread_id),
    CHECK (after_thread_id <> target_thread_id)
);
CREATE TABLE continuation_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    continuation_id INTEGER NOT NULL REFERENCES continuations (id) ON DELETE CASCADE,
    state           TEXT NOT NULL,
    detail          TEXT NOT NULL DEFAULT '',
    at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);
CREATE INDEX continuation_events_order ON continuation_events (continuation_id, id);
CREATE INDEX continuation_pending ON continuations (id) WHERE state NOT IN ('settled', 'cancelled');
CREATE INDEX continuation_order ON continuations (order_id, id);

-- One row per schedule fire: started (task spawned), skipped (previous run
-- active), missed (server down across the fire time, one row per outage),
-- manual (Run now).
CREATE TABLE schedule_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    schedule_id INTEGER NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
    fire_at     TEXT NOT NULL,
    outcome     TEXT NOT NULL CHECK (outcome IN ('started', 'skipped', 'missed', 'manual')),
    task_id     INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
    detail      TEXT NOT NULL DEFAULT '',
    at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);
CREATE INDEX schedule_runs_schedule_idx ON schedule_runs (schedule_id, id DESC);

-- Run-order handoff (docs/api.md "Handoff"): what each launch attempt of a
-- linked follower sent (status sent) or why it launched without one (status
-- skipped, reason).
CREATE TABLE task_handoffs (
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
    UNIQUE (task_id, attempt)
);

-- Answers stage sent to pending user questions (docs/api.md "Pending
-- questions"), so a retried submit is idempotent. answers: JSON object.
CREATE TABLE question_responses (
    t3_thread_id TEXT NOT NULL,
    request_id   TEXT NOT NULL,
    answers      TEXT NOT NULL CHECK (json_valid(answers)),
    responded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    PRIMARY KEY (t3_thread_id, request_id)
);

-- Predecessor base (docs/api.md "Predecessor base"): the worktree base each
-- launch attempt used (source predecessor) or the fallback and why (source
-- fallback, reason).
CREATE TABLE task_launch_bases (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id           INTEGER NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    attempt           INTEGER NOT NULL,
    base_ref          TEXT NOT NULL,
    start_from_origin INTEGER NOT NULL CHECK (start_from_origin IN (0, 1)),
    source            TEXT NOT NULL CHECK (source IN ('predecessor', 'fallback')),
    source_task_id    INTEGER,
    source_thread_id  TEXT,
    source_title      TEXT NOT NULL DEFAULT '',
    reason            TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
    UNIQUE (task_id, attempt)
);

-- +goose Down
DROP TABLE task_launch_bases;
DROP TABLE question_responses;
DROP TABLE task_handoffs;
DROP TABLE schedule_runs;
DROP TABLE continuation_events;
DROP TABLE continuations;
DROP TABLE notifications;
DROP TABLE push_subscriptions;
DROP TABLE push_vapid;
DROP TABLE task_attachments;
DROP TABLE preferences;
DROP TABLE snippets;
DROP TABLE task_events;
DROP TABLE tasks;
DROP TABLE schedules;
DROP TABLE threads;
DROP TABLE projects;
DROP TABLE t3_connection;
