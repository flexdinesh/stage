-- +goose Up
CREATE TABLE execution_observations (
 session_id INTEGER PRIMARY KEY REFERENCES session_identities(id),
 observation TEXT NOT NULL,
 observed_at TEXT NOT NULL DEFAULT(stage_now()),
 unavailable_reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE execution_attempts (
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 attempt INTEGER NOT NULL,
 intent TEXT,
 checkpoint TEXT NOT NULL DEFAULT '{"version":0,"data":null}',
 phase TEXT NOT NULL CHECK(phase IN ('prepared','unconfirmed','confirmed','rejected')),
 revision INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 retry_at TEXT,
 retry_count INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(task_id,attempt)
);
INSERT INTO execution_attempts(task_id,attempt,phase,error)
 SELECT id,launch_attempt,'unconfirmed','Original launch identity is unavailable. Link the existing session.'
 FROM tasks WHERE launch_reuse=1 OR state='launching';
UPDATE tasks SET state='launching',finished_at=NULL WHERE id IN (SELECT task_id FROM execution_attempts);
ALTER TABLE threads ADD COLUMN execution_state TEXT;
UPDATE threads SET execution_state=CASE
 WHEN pending_request_count>0 THEN 'needs_input'
 WHEN active_run_id IS NOT NULL OR COALESCE(NULLIF(status,'idle'),latest_run_status) IN ('preparing','queued','starting','running','waiting') THEN 'running'
 WHEN COALESCE(NULLIF(status,'idle'),latest_run_status)='failed' THEN 'failed'
 WHEN COALESCE(NULLIF(status,'idle'),latest_run_status)='interrupted' THEN 'interrupted'
 WHEN COALESCE(NULLIF(status,'idle'),latest_run_status) IN ('cancelled','rolled_back') THEN 'cancelled'
 ELSE 'done' END;

ALTER TABLE tasks ADD COLUMN execution_config TEXT;
ALTER TABLE schedules ADD COLUMN execution_config TEXT;

-- Explicit scoped handoff references must not be overwritten by legacy triggers.
DROP TRIGGER task_handoffs_source_session_id_insert;
DROP TRIGGER task_handoffs_source_session_id_update;
-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_insert AFTER INSERT ON task_handoffs
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER task_handoffs_source_session_id_update AFTER UPDATE OF source_thread_id ON task_handoffs
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_handoffs SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

DROP TRIGGER task_launch_bases_source_session_id_insert;
DROP TRIGGER task_launch_bases_source_session_id_update;
-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_insert AFTER INSERT ON task_launch_bases
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER task_launch_bases_source_session_id_update AFTER UPDATE OF source_thread_id ON task_launch_bases
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES(1,NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE task_launch_bases SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=1 AND external_id=NEW.source_thread_id) WHERE task_id=NEW.task_id AND attempt=NEW.attempt;
END;
-- +goose StatementEnd
