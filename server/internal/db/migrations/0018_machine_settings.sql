-- +goose Up
-- Harness connection settings belong to a machine, not to the server: each
-- machine has its own T3 setup (token, URL, sync bookkeeping) and its own
-- OpenCode setup, for the active connection of that kind on the machine
-- (docs/architecture.md). The id=1 singleton rows become machine 1's rows; a
-- machine with no row reads defaults and gets one on its first write.
-- Connection ids are untouched.
--
-- Legacy T3 thread columns (session-less ids from before connections) keep
-- mapping to the active T3 connection, now scoped to the local machine
-- (machine 1), the only machine that wrote them.

CREATE TABLE t3_connection_new (
 machine_id INTEGER PRIMARY KEY REFERENCES machines(id),
 client_id TEXT,
 token TEXT,
 expires_at TEXT,
 last_error TEXT,
 last_full_sync_at TEXT,
 t3_url TEXT,
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);
INSERT INTO t3_connection_new(machine_id,client_id,token,expires_at,last_error,last_full_sync_at,t3_url,updated_at)
 SELECT 1,client_id,token,expires_at,last_error,last_full_sync_at,t3_url,updated_at FROM t3_connection WHERE id=1;
DROP TABLE t3_connection;
ALTER TABLE t3_connection_new RENAME TO t3_connection;

CREATE TABLE opencode_settings_new (
 machine_id INTEGER PRIMARY KEY REFERENCES machines(id),
 endpoint TEXT NOT NULL DEFAULT '',
 browser_endpoint TEXT NOT NULL DEFAULT '',
 web_client TEXT NOT NULL DEFAULT '',
 username TEXT NOT NULL DEFAULT '',
 password TEXT NOT NULL DEFAULT '',
 auth TEXT NOT NULL DEFAULT 'none' CHECK(auth IN ('none','local','paired','basic')),
 token_issued_at TEXT
);
INSERT INTO opencode_settings_new(machine_id,endpoint,browser_endpoint,web_client,username,password,auth,token_issued_at)
 SELECT 1,endpoint,browser_endpoint,web_client,username,password,auth,token_issued_at FROM opencode_settings WHERE id=1;
DROP TABLE opencode_settings;
ALTER TABLE opencode_settings_new RENAME TO opencode_settings;

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

-- +goose StatementBegin
CREATE TRIGGER threads_session_id_insert AFTER INSERT ON threads
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_session_id_update AFTER UPDATE OF t3_thread_id ON threads
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_parent_session_id_insert AFTER INSERT ON threads
WHEN NEW.parent_thread_id IS NOT NULL AND NEW.parent_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.parent_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET parent_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.parent_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_parent_session_id_update AFTER UPDATE OF parent_thread_id ON threads
WHEN NEW.parent_thread_id IS NOT NULL AND NEW.parent_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.parent_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET parent_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.parent_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_origin_session_id_insert AFTER INSERT ON tasks
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_origin_session_id_update AFTER UPDATE OF origin_thread_id ON tasks
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_origin_session_id_insert AFTER INSERT ON schedules
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_origin_session_id_update AFTER UPDATE OF origin_thread_id ON schedules
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_insert AFTER INSERT ON journal_entries
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_update AFTER UPDATE OF source_thread_id ON journal_entries
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notifications_session_id_insert AFTER INSERT ON notifications
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notifications_session_id_update AFTER UPDATE OF t3_thread_id ON notifications
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_insert AFTER INSERT ON task_handoffs
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_update AFTER UPDATE OF source_thread_id ON task_handoffs
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_insert AFTER INSERT ON task_launch_bases
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_update AFTER UPDATE OF source_thread_id ON task_launch_bases
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd

-- The mirror holds threads of the local machine's active T3 connection.
-- +goose StatementBegin
CREATE TRIGGER thread_project_insert AFTER INSERT ON threads BEGIN
 UPDATE threads SET stage_project_id=COALESCE(
 (SELECT project_id FROM tasks WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND t3_thread_id=NEW.t3_thread_id),
 (SELECT min(project_id) FROM project_bindings WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_project_id=NEW.t3_project_id AND active=1 HAVING count(*)=1))
 WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd
