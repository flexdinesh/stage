-- +goose Up
-- A connection is one harness instance (a T3 environment, an OpenCode
-- service), not a harness type. At most one connection per harness is
-- active; replacing the instance retires the row and keeps every relation
-- (bindings, external projects, sessions, launched tasks) on it.
--
-- harness_connections is rebuilt to drop UNIQUE(harness). Five tables
-- reference it, and SQLite rewrites those references on any rename while
-- foreign keys are on, so this follows SQLite's documented table rebuild:
-- foreign keys off, one explicit transaction, a foreign_key_check (asserted
-- by the migration test), foreign keys back on. goose runs a NO TRANSACTION
-- file on its single connection, so the pragmas hold for every statement.
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
BEGIN;

-- Triggers that name harness_connections or hardcode connection 1 are
-- recreated below against the active T3 connection.
DROP TRIGGER threads_session_id_insert;
DROP TRIGGER threads_session_id_update;
DROP TRIGGER threads_parent_session_id_insert;
DROP TRIGGER threads_parent_session_id_update;
DROP TRIGGER tasks_origin_session_id_insert;
DROP TRIGGER tasks_origin_session_id_update;
DROP TRIGGER schedules_origin_session_id_insert;
DROP TRIGGER schedules_origin_session_id_update;
DROP TRIGGER journal_entries_source_session_id_insert;
DROP TRIGGER journal_entries_source_session_id_update;
DROP TRIGGER notifications_session_id_insert;
DROP TRIGGER notifications_session_id_update;
DROP TRIGGER task_handoffs_source_session_id_insert;
DROP TRIGGER task_handoffs_source_session_id_update;
DROP TRIGGER task_launch_bases_source_session_id_insert;
DROP TRIGGER task_launch_bases_source_session_id_update;
DROP TRIGGER thread_project_insert;
DROP TRIGGER tasks_connection_insert;
DROP TRIGGER tasks_connection_update;
DROP TRIGGER schedules_connection_insert;
DROP TRIGGER schedules_connection_update;

CREATE TABLE harness_connections_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 harness TEXT NOT NULL CHECK(harness IN ('t3','opencode')),
 created_at TEXT NOT NULL DEFAULT(stage_now()),
 auto_complete INTEGER NOT NULL DEFAULT 1 CHECK(auto_complete IN (0,1)),
 -- T3: environment id. OpenCode: the verified service address.
 identity TEXT NOT NULL DEFAULT '',
 -- Display name of the instance (T3 environment label, OpenCode address).
 label TEXT NOT NULL DEFAULT '',
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 retired_at TEXT,
 forgotten_at TEXT,
 CHECK(active=1 OR retired_at IS NOT NULL),
 CHECK(forgotten_at IS NULL OR active=0)
);
INSERT INTO harness_connections_new(id,harness,created_at,auto_complete)
 SELECT id,harness,created_at,auto_complete FROM harness_connections;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('harness_connections','harness_connections_new')) WHERE name='harness_connections_new';
DROP TABLE harness_connections;
ALTER TABLE harness_connections_new RENAME TO harness_connections;
CREATE UNIQUE INDEX harness_connection_active ON harness_connections(harness) WHERE active=1;
CREATE UNIQUE INDEX harness_connection_t3_identity ON harness_connections(identity) WHERE harness='t3' AND identity<>'';

UPDATE harness_connections SET identity=COALESCE((SELECT environment_id FROM t3_connection WHERE id=1),''),
 label=COALESCE((SELECT environment_id FROM t3_connection WHERE id=1),'') WHERE harness='t3';
UPDATE harness_connections SET identity=COALESCE((SELECT verified_endpoint FROM opencode_settings WHERE id=1),''),
 label=COALESCE((SELECT verified_endpoint FROM opencode_settings WHERE id=1),'') WHERE harness='opencode';
ALTER TABLE t3_connection DROP COLUMN environment_id;
ALTER TABLE opencode_settings DROP COLUMN verified_endpoint;

-- Legacy T3 thread columns map to the active T3 connection, not id 1.

-- +goose StatementBegin
CREATE TRIGGER threads_session_id_insert AFTER INSERT ON threads
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_session_id_update AFTER UPDATE OF t3_thread_id ON threads
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_parent_session_id_insert AFTER INSERT ON threads
WHEN NEW.parent_thread_id IS NOT NULL AND NEW.parent_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.parent_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET parent_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.parent_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_parent_session_id_update AFTER UPDATE OF parent_thread_id ON threads
WHEN NEW.parent_thread_id IS NOT NULL AND NEW.parent_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.parent_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET parent_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.parent_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_origin_session_id_insert AFTER INSERT ON tasks
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_origin_session_id_update AFTER UPDATE OF origin_thread_id ON tasks
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_origin_session_id_insert AFTER INSERT ON schedules
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_origin_session_id_update AFTER UPDATE OF origin_thread_id ON schedules
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_insert AFTER INSERT ON journal_entries
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_update AFTER UPDATE OF source_thread_id ON journal_entries
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notifications_session_id_insert AFTER INSERT ON notifications
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notifications_session_id_update AFTER UPDATE OF t3_thread_id ON notifications
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_insert AFTER INSERT ON task_handoffs
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_update AFTER UPDATE OF source_thread_id ON task_handoffs
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_insert AFTER INSERT ON task_launch_bases
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_update AFTER UPDATE OF source_thread_id ON task_launch_bases
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd

-- The mirror holds threads of the active T3 connection.
-- +goose StatementBegin
CREATE TRIGGER thread_project_insert AFTER INSERT ON threads BEGIN
 UPDATE threads SET stage_project_id=COALESCE(
 (SELECT project_id FROM tasks WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND t3_thread_id=NEW.t3_thread_id),
 (SELECT min(project_id) FROM project_bindings WHERE connection_id=(SELECT id FROM harness_connections WHERE harness='t3' AND active=1) AND external_project_id=NEW.t3_project_id AND active=1 HAVING count(*)=1))
 WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_connection_insert BEFORE INSERT ON tasks
WHEN NOT EXISTS(SELECT 1 FROM harness_connections WHERE id=NEW.connection_id) BEGIN
 SELECT RAISE(ABORT,'unknown harness connection');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_connection_update BEFORE UPDATE OF connection_id ON tasks
WHEN NOT EXISTS(SELECT 1 FROM harness_connections WHERE id=NEW.connection_id) BEGIN
 SELECT RAISE(ABORT,'unknown harness connection');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_connection_insert BEFORE INSERT ON schedules
WHEN NOT EXISTS(SELECT 1 FROM harness_connections WHERE id=NEW.connection_id) BEGIN
 SELECT RAISE(ABORT,'unknown harness connection');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_connection_update BEFORE UPDATE OF connection_id ON schedules
WHEN NOT EXISTS(SELECT 1 FROM harness_connections WHERE id=NEW.connection_id) BEGIN
 SELECT RAISE(ABORT,'unknown harness connection');
END;
-- +goose StatementEnd

COMMIT;
PRAGMA foreign_keys=ON;
