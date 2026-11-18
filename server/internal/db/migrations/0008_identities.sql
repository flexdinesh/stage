-- +goose Up
-- Existing T3 cache columns remain historical adapter data. New identity and
-- bindings are independent of that cache and never derive identity from paths.
CREATE TABLE harness_connections (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 harness TEXT NOT NULL UNIQUE CHECK(harness IN ('t3','opencode')),
 created_at TEXT NOT NULL DEFAULT(stage_now())
);
INSERT INTO harness_connections(id,harness) VALUES(1,'t3');

CREATE TABLE project_bindings (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects(id),
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 external_project_id TEXT NOT NULL,
 workspace TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 status TEXT NOT NULL DEFAULT 'available' CHECK(status IN ('available','missing','changed')),
 observed_workspace TEXT,
 created_at TEXT NOT NULL DEFAULT(stage_now())
);
CREATE UNIQUE INDEX project_binding_active ON project_bindings(project_id,connection_id) WHERE active=1;
CREATE UNIQUE INDEX workspace_binding_active ON project_bindings(connection_id,external_project_id,workspace) WHERE active=1;
INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace,status)
 SELECT id,1,t3_project_id,workspace_root,CASE WHEN t3_deleted_at IS NULL THEN 'available' ELSE 'missing' END FROM projects;

CREATE TABLE external_projects (
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 external_id TEXT NOT NULL,
 title TEXT NOT NULL,
 workspace TEXT NOT NULL,
 available INTEGER NOT NULL CHECK(available IN (0,1)),
 PRIMARY KEY(connection_id,external_id,workspace)
);
INSERT INTO external_projects SELECT 1,t3_project_id,title,workspace_root,t3_deleted_at IS NULL FROM projects;

CREATE TABLE session_identities (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 external_id TEXT NOT NULL,
 UNIQUE(connection_id,external_id)
);
INSERT INTO session_identities(connection_id,external_id)
 SELECT 1, external_id FROM (
 SELECT t3_thread_id external_id FROM threads UNION SELECT parent_thread_id FROM threads
 UNION SELECT t3_thread_id FROM tasks UNION SELECT origin_thread_id FROM tasks
 UNION SELECT origin_thread_id FROM schedules UNION SELECT source_thread_id FROM journal_entries
 UNION SELECT t3_thread_id FROM notifications UNION SELECT source_thread_id FROM task_handoffs
 UNION SELECT source_thread_id FROM task_launch_bases
 UNION SELECT json_extract(evidence,'$.threadId') FROM task_acceptances
 UNION SELECT t3_thread_id FROM question_responses
 ) WHERE external_id IS NOT NULL AND external_id<>'';

ALTER TABLE threads ADD COLUMN session_id INTEGER REFERENCES session_identities(id);
ALTER TABLE threads ADD COLUMN parent_session_id INTEGER REFERENCES session_identities(id);
ALTER TABLE threads ADD COLUMN stage_project_id INTEGER REFERENCES projects(id);
UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=threads.t3_thread_id),
 parent_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=threads.parent_thread_id),
 stage_project_id=(SELECT id FROM projects WHERE t3_project_id=threads.t3_project_id);
CREATE UNIQUE INDEX thread_session_identity ON threads(session_id);
ALTER TABLE tasks ADD COLUMN scheduled_hold INTEGER NOT NULL DEFAULT 0 CHECK(scheduled_hold IN (0,1));
ALTER TABLE tasks ADD COLUMN connection_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE tasks ADD COLUMN session_id INTEGER REFERENCES session_identities(id);
ALTER TABLE tasks ADD COLUMN origin_session_id INTEGER REFERENCES session_identities(id);
UPDATE tasks SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=tasks.t3_thread_id),
 origin_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=tasks.origin_thread_id);
CREATE UNIQUE INDEX task_session_identity ON tasks(session_id);
ALTER TABLE schedules ADD COLUMN connection_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE schedules ADD COLUMN origin_session_id INTEGER REFERENCES session_identities(id);
UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=schedules.origin_thread_id);
ALTER TABLE journal_entries ADD COLUMN source_session_id INTEGER REFERENCES session_identities(id);
UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=journal_entries.source_thread_id);
ALTER TABLE notifications ADD COLUMN session_id INTEGER REFERENCES session_identities(id);
UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=notifications.t3_thread_id);
ALTER TABLE task_handoffs ADD COLUMN source_session_id INTEGER REFERENCES session_identities(id);
UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=task_handoffs.source_thread_id);
ALTER TABLE task_launch_bases ADD COLUMN source_session_id INTEGER REFERENCES session_identities(id);
UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=task_launch_bases.source_thread_id);
ALTER TABLE task_acceptances ADD COLUMN source_session_id INTEGER REFERENCES session_identities(id);
UPDATE task_acceptances SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=json_extract(task_acceptances.evidence,'$.threadId'));

CREATE TABLE task_launch_destinations (
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 attempt INTEGER NOT NULL,
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 external_project_id TEXT NOT NULL,
 workspace TEXT NOT NULL,
 binding_id INTEGER NOT NULL REFERENCES project_bindings(id),
 binding_revision INTEGER NOT NULL,
 PRIMARY KEY(task_id,attempt)
);
INSERT INTO task_launch_destinations
 SELECT t.id,t.launch_attempt,1,b.external_project_id,b.workspace,b.id,b.revision
 FROM tasks t JOIN project_bindings b ON b.project_id=t.project_id AND b.connection_id=1
 WHERE t.launch_attempt>0;

-- +goose StatementBegin
CREATE TRIGGER threads_session_id_insert AFTER INSERT ON threads
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.t3_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_session_id_update AFTER UPDATE OF t3_thread_id ON threads
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.t3_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_parent_session_id_insert AFTER INSERT ON threads
WHEN NEW.parent_thread_id IS NOT NULL AND NEW.parent_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.parent_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET parent_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.parent_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER threads_parent_session_id_update AFTER UPDATE OF parent_thread_id ON threads
WHEN NEW.parent_thread_id IS NOT NULL AND NEW.parent_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.parent_thread_id) ON CONFLICT DO NOTHING;
 UPDATE threads SET parent_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.parent_thread_id) WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_session_id_insert AFTER INSERT ON tasks
WHEN NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(NEW.connection_id,NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET session_id=(SELECT id FROM session_identities WHERE connection_id=NEW.connection_id AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_session_id_update AFTER UPDATE OF t3_thread_id ON tasks
WHEN NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(NEW.connection_id,NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET session_id=(SELECT id FROM session_identities WHERE connection_id=NEW.connection_id AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_origin_session_id_insert AFTER INSERT ON tasks
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_origin_session_id_update AFTER UPDATE OF origin_thread_id ON tasks
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE tasks SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_origin_session_id_insert AFTER INSERT ON schedules
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER schedules_origin_session_id_update AFTER UPDATE OF origin_thread_id ON schedules
WHEN NEW.origin_thread_id IS NOT NULL AND NEW.origin_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.origin_thread_id) ON CONFLICT DO NOTHING;
 UPDATE schedules SET origin_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.origin_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_insert AFTER INSERT ON journal_entries
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_update AFTER UPDATE OF source_thread_id ON journal_entries
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notifications_session_id_insert AFTER INSERT ON notifications
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notifications_session_id_update AFTER UPDATE OF t3_thread_id ON notifications
WHEN NEW.session_id IS NULL AND NEW.t3_thread_id IS NOT NULL AND NEW.t3_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.t3_thread_id) ON CONFLICT DO NOTHING;
 UPDATE notifications SET session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.t3_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_insert AFTER INSERT ON task_handoffs
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_update AFTER UPDATE OF source_thread_id ON task_handoffs
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_insert AFTER INSERT ON task_launch_bases
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_update AFTER UPDATE OF source_thread_id ON task_launch_bases
WHEN NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER thread_project_insert AFTER INSERT ON threads BEGIN
 UPDATE threads SET stage_project_id=COALESCE(
 (SELECT project_id FROM tasks WHERE connection_id=1 AND t3_thread_id=NEW.t3_thread_id),
 (SELECT min(project_id) FROM project_bindings WHERE connection_id=1 AND external_project_id=NEW.t3_project_id AND active=1 HAVING count(*)=1))
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

-- +goose StatementBegin
CREATE TRIGGER acceptance_session_insert AFTER INSERT ON task_acceptances BEGIN
 UPDATE task_acceptances SET source_session_id=(SELECT session_id FROM tasks WHERE id=NEW.task_id) WHERE id=NEW.id;
END;
-- +goose StatementEnd
