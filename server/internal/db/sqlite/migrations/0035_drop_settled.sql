-- +goose Up
-- Acceptance alone records completion. Preserve its snapshots and requests.
-- Rebuild the checked tables following 0028 and restore task search per 0029.
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN;

UPDATE tasks SET state='done', finished_at=COALESCE(finished_at, settled_at) WHERE state='settled';
UPDATE task_events SET to_state='done' WHERE to_state='settled';
UPDATE task_events SET from_state='done' WHERE from_state='settled';
UPDATE notifications SET event='done' WHERE event='settled';

-- The SQLite column default stays as-is. The singleton row is updated here.
UPDATE preferences AS p
SET notify_events=(
 SELECT json_group_array(value ORDER BY key)
 FROM json_each(p.notify_events)
 WHERE value IS NOT 'settled'
);

CREATE TABLE tasks_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects (id),
 title TEXT NOT NULL,
 prompt TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'backlog' CHECK (state IN (
  'backlog', 'launching', 'running', 'needs_input', 'done',
  'failed', 'interrupted', 'cancelled'
 )),
 launch_attempt INTEGER NOT NULL DEFAULT 0,
 last_error TEXT,
 launched_at TEXT,
 finished_at TEXT,
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
INSERT INTO tasks_new(id,project_id,title,prompt,state,launch_attempt,last_error,launched_at,finished_at,
 created_at,updated_at,launch_request_id,start_at,start_tz,schedule_id,handoff,tree_id,start_mode,adopted,ready_at,archived_at,
 recorded_work,acceptance_revision,acceptance_id,scheduled_hold,connection_id,session_id,origin_session_id,execution_config,
 created_by_user_id,source_machine_id)
 SELECT id,project_id,title,prompt,state,launch_attempt,last_error,launched_at,finished_at,
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

-- +goose StatementBegin
CREATE TRIGGER tasks_fts_insert AFTER INSERT ON tasks BEGIN
 INSERT INTO tasks_fts(rowid, title, prompt) VALUES (NEW.id, NEW.title, NEW.prompt);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_fts_delete AFTER DELETE ON tasks BEGIN
 INSERT INTO tasks_fts(tasks_fts, rowid, title, prompt) VALUES ('delete', OLD.id, OLD.title, OLD.prompt);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_fts_update AFTER UPDATE OF title, prompt ON tasks BEGIN
 INSERT INTO tasks_fts(tasks_fts, rowid, title, prompt) VALUES ('delete', OLD.id, OLD.title, OLD.prompt);
 INSERT INTO tasks_fts(rowid, title, prompt) VALUES (NEW.id, NEW.title, NEW.prompt);
END;
-- +goose StatementEnd

INSERT INTO tasks_fts(tasks_fts) VALUES ('rebuild');

CREATE TABLE notifications_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 event TEXT NOT NULL CONSTRAINT notifications_event_check CHECK (event IN ('needs_input','ready','done','failed')),
 task_id INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
 title TEXT NOT NULL,
 project_title TEXT,
 url TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now')),
 read_at TEXT,
 session_id INTEGER REFERENCES session_identities(id)
);
INSERT INTO notifications_new(id,event,task_id,title,project_title,url,created_at,read_at,session_id)
 SELECT id,event,task_id,title,project_title,url,created_at,read_at,session_id FROM notifications;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('notifications','notifications_new')) WHERE name='notifications_new';
DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;
CREATE INDEX notifications_created_idx ON notifications (created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (id) WHERE read_at IS NULL;

COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
