-- +goose Up
-- Contract step of the #402 legacy drop (docs/storage.md "Legacy columns and
-- compatibility triggers"): every Drop row goes, after 0027 backfilled the
-- neutral columns.
--
-- - The remaining compatibility triggers go. tasks and schedules are rebuilt
--   so connection_id is a real foreign key (it replaces the
--   *_connection_* triggers).
-- - tasks, schedules, projects and threads are rebuilt without their legacy
--   columns (UNIQUE, foreign key or constraint columns cannot be dropped in
--   place). Same rebuild procedure as 0015 and 0019.
-- - Plain legacy columns elsewhere are dropped in place.
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN;

DROP TRIGGER tasks_session_id_insert;
DROP TRIGGER tasks_session_id_update;
DROP TRIGGER tasks_connection_insert;
DROP TRIGGER tasks_connection_update;
DROP TRIGGER schedules_connection_insert;
DROP TRIGGER schedules_connection_update;
DROP TRIGGER threads_session_id_insert;
DROP TRIGGER threads_session_id_update;
DROP TRIGGER tasks_origin_session_id_insert;
DROP TRIGGER tasks_origin_session_id_update;
DROP TRIGGER schedules_origin_session_id_insert;
DROP TRIGGER schedules_origin_session_id_update;
DROP TRIGGER notifications_session_id_insert;
DROP TRIGGER notifications_session_id_update;
DROP TRIGGER task_launch_bases_source_session_id_insert;
DROP TRIGGER task_launch_bases_source_session_id_update;
DROP TRIGGER journal_entries_source_session_id_insert;
DROP TRIGGER journal_entries_source_session_id_update;

CREATE TABLE projects_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 title TEXT NOT NULL,
 archived_at TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 origin TEXT CHECK(origin IN ('discovered')),
 imported_connection_id INTEGER REFERENCES harness_connections(id),
 imported_at TEXT,
 archive_owner TEXT CHECK(archive_owner IN ('user','harness')),
 created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 source_machine_id INTEGER REFERENCES machines(id)
);
INSERT INTO projects_new(id,title,archived_at,created_at,updated_at,origin,imported_connection_id,imported_at,archive_owner,created_by_user_id,source_machine_id)
 SELECT id,title,archived_at,created_at,updated_at,origin,imported_connection_id,imported_at,archive_owner,created_by_user_id,source_machine_id FROM projects;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('projects','projects_new')) WHERE name='projects_new';
DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;

CREATE TABLE schedules_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects (id),
 title TEXT NOT NULL,
 prompt TEXT NOT NULL,
 workspace TEXT NOT NULL DEFAULT '{"type":"root"}' CHECK (json_type(workspace) = 'object'),
 notes TEXT NOT NULL DEFAULT '',
 cron TEXT NOT NULL,
 tz TEXT NOT NULL,
 paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
 next_run_at TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 connection_id INTEGER NOT NULL DEFAULT 1 REFERENCES harness_connections(id),
 origin_session_id INTEGER REFERENCES session_identities(id),
 execution_config TEXT,
 created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 source_machine_id INTEGER REFERENCES machines(id)
);
INSERT INTO schedules_new(id,project_id,title,prompt,workspace,notes,cron,tz,paused,next_run_at,created_at,updated_at,
 connection_id,origin_session_id,execution_config,created_by_user_id,source_machine_id)
 SELECT id,project_id,title,prompt,workspace,notes,cron,tz,paused,next_run_at,created_at,updated_at,
 connection_id,origin_session_id,execution_config,created_by_user_id,source_machine_id FROM schedules;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('schedules','schedules_new')) WHERE name='schedules_new';
DROP TABLE schedules;
ALTER TABLE schedules_new RENAME TO schedules;
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE NOT paused;
CREATE INDEX schedules_project_idx ON schedules (project_id);

CREATE TABLE tasks_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects (id),
 title TEXT NOT NULL,
 prompt TEXT NOT NULL,
 workspace TEXT NOT NULL DEFAULT '{"type":"root"}' CHECK (json_type(workspace) = 'object'),
 state TEXT NOT NULL DEFAULT 'backlog' CHECK (state IN (
  'backlog', 'launching', 'running', 'needs_input', 'done',
  'settled', 'failed', 'interrupted', 'cancelled'
 )),
 launch_attempt INTEGER NOT NULL DEFAULT 0,
 last_error TEXT,
 launched_at TEXT,
 finished_at TEXT,
 settled_at TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 launch_request_id TEXT,
 start_at TEXT,
 start_tz TEXT,
 schedule_id INTEGER REFERENCES schedules (id) ON DELETE SET NULL,
 handoff INTEGER NOT NULL DEFAULT 1 CHECK (handoff IN (0, 1)),
 tree_id INTEGER REFERENCES task_trees (id) ON DELETE SET NULL,
 start_mode TEXT CHECK (start_mode IN ('auto', 'manual')),
 adopted INTEGER NOT NULL DEFAULT 0 CHECK (adopted IN (0, 1)),
 ready_at TEXT,
 archived_at TEXT,
 recorded_work INTEGER NOT NULL DEFAULT 0 CHECK (recorded_work IN (0,1)),
 acceptance_revision INTEGER NOT NULL DEFAULT 0,
 acceptance_id INTEGER,
 scheduled_hold INTEGER NOT NULL DEFAULT 0 CHECK(scheduled_hold IN (0,1)),
 connection_id INTEGER NOT NULL DEFAULT 1 REFERENCES harness_connections(id),
 session_id INTEGER REFERENCES session_identities(id),
 origin_session_id INTEGER REFERENCES session_identities(id),
 execution_config TEXT,
 created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 source_machine_id INTEGER REFERENCES machines(id),
 CONSTRAINT tasks_start_pair CHECK ((start_at IS NULL) = (start_tz IS NULL))
);
INSERT INTO tasks_new(id,project_id,title,prompt,workspace,state,launch_attempt,last_error,launched_at,finished_at,settled_at,
 created_at,updated_at,launch_request_id,start_at,start_tz,schedule_id,handoff,tree_id,start_mode,adopted,ready_at,archived_at,
 recorded_work,acceptance_revision,acceptance_id,scheduled_hold,connection_id,session_id,origin_session_id,execution_config,
 created_by_user_id,source_machine_id)
 SELECT id,project_id,title,prompt,workspace,state,launch_attempt,last_error,launched_at,finished_at,settled_at,
 created_at,updated_at,launch_request_id,start_at,start_tz,schedule_id,handoff,tree_id,start_mode,adopted,ready_at,archived_at,
 recorded_work,acceptance_revision,acceptance_id,scheduled_hold,connection_id,session_id,origin_session_id,execution_config,
 created_by_user_id,source_machine_id FROM tasks;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('tasks','tasks_new')) WHERE name='tasks_new';
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;
CREATE INDEX tasks_state_idx ON tasks (state);
CREATE INDEX tasks_project_id_idx ON tasks (project_id);
CREATE INDEX tasks_created_at_idx ON tasks (created_at);
CREATE INDEX tasks_due_start_idx ON tasks (start_at) WHERE state = 'backlog' AND start_at IS NOT NULL;
CREATE INDEX tasks_schedule_idx ON tasks (schedule_id) WHERE schedule_id IS NOT NULL;
CREATE INDEX tasks_tree_idx ON tasks (tree_id) WHERE tree_id IS NOT NULL;
CREATE INDEX tasks_archived_idx ON tasks (archived_at) WHERE archived_at IS NOT NULL;
CREATE UNIQUE INDEX task_session_identity ON tasks(session_id);

CREATE TABLE threads_new (
 t3_thread_id TEXT PRIMARY KEY,
 t3_project_id TEXT NOT NULL,
 title TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL,
 settled INTEGER NOT NULL DEFAULT 0 CHECK (settled IN (0, 1)),
 settled_at TEXT,
 pending_request_count INTEGER NOT NULL DEFAULT 0,
 model TEXT,
 runtime_mode TEXT,
 interaction_mode TEXT,
 branch TEXT,
 worktree_path TEXT,
 parent_thread_id TEXT,
 relationship TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 synced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')),
 observed_state TEXT,
 archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
 active_run_id TEXT,
 latest_run_id TEXT,
 run_started_at TEXT,
 run_ended_at TEXT,
 last_word_text TEXT,
 last_word_preview TEXT,
 last_word_truncated INTEGER NOT NULL DEFAULT 0 CHECK (last_word_truncated IN (0, 1)),
 last_word_run_id TEXT,
 last_word_at TEXT,
 last_word_checked_at TEXT,
 session_id INTEGER REFERENCES session_identities(id),
 stage_project_id INTEGER REFERENCES projects(id),
 execution_state TEXT
);
INSERT INTO threads_new(t3_thread_id,t3_project_id,title,status,settled,settled_at,pending_request_count,model,runtime_mode,
 interaction_mode,branch,worktree_path,parent_thread_id,relationship,created_at,updated_at,synced_at,observed_state,archived,
 active_run_id,latest_run_id,run_started_at,run_ended_at,last_word_text,last_word_preview,last_word_truncated,last_word_run_id,
 last_word_at,last_word_checked_at,session_id,stage_project_id,execution_state)
 SELECT t3_thread_id,t3_project_id,title,status,settled,settled_at,pending_request_count,model,runtime_mode,
 interaction_mode,branch,worktree_path,parent_thread_id,relationship,created_at,updated_at,synced_at,observed_state,archived,
 active_run_id,latest_run_id,run_started_at,run_ended_at,last_word_text,last_word_preview,last_word_truncated,last_word_run_id,
 last_word_at,last_word_checked_at,session_id,stage_project_id,execution_state FROM threads;
DROP TABLE threads;
ALTER TABLE threads_new RENAME TO threads;
CREATE INDEX threads_created_at_idx ON threads (created_at);
CREATE UNIQUE INDEX thread_session_identity ON threads(session_id);

ALTER TABLE notifications DROP COLUMN t3_thread_id;
ALTER TABLE task_handoffs DROP COLUMN source_thread_id;
ALTER TABLE task_launch_bases DROP COLUMN source_thread_id;
ALTER TABLE journal_entries DROP COLUMN source_thread_id;
DROP TABLE question_responses;

COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
