-- +goose Up
-- A harness closure (T3 settle, OpenCode archive) of a done session may
-- complete its task. On by default; per connection.
ALTER TABLE harness_connections ADD COLUMN auto_complete INTEGER NOT NULL DEFAULT 1 CHECK(auto_complete IN (0,1));

-- task_acceptances gains harness provenances. SQLite cannot alter a CHECK,
-- so the table is rebuilt. goose runs this file in one transaction with
-- foreign_keys on: task_launch_inputs references task_acceptances, so it is
-- rebuilt first against the new table (the rename rewrites that reference).
CREATE TABLE task_acceptances_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 accepted_at TEXT NOT NULL DEFAULT (stage_now()),
 captured_at TEXT NOT NULL DEFAULT (stage_now()),
 provenance TEXT NOT NULL CHECK (provenance IN ('user','historical_settlement','harness_settled','harness_archived')),
 evidence TEXT NOT NULL CHECK (json_valid(evidence)),
 note TEXT NOT NULL DEFAULT '',
 source_session_id INTEGER REFERENCES session_identities(id)
);
INSERT INTO task_acceptances_new(id,task_id,accepted_at,captured_at,provenance,evidence,note,source_session_id)
 SELECT id,task_id,accepted_at,captured_at,provenance,evidence,note,source_session_id FROM task_acceptances;
UPDATE sqlite_sequence SET seq=max(seq,COALESCE((SELECT seq FROM sqlite_sequence WHERE name='task_acceptances'),0)) WHERE name='task_acceptances_new';
CREATE TABLE task_launch_inputs_new (
 task_id INTEGER NOT NULL,
 attempt INTEGER NOT NULL,
 source_task_id INTEGER NOT NULL,
 acceptance_id INTEGER NOT NULL REFERENCES task_acceptances_new(id),
 PRIMARY KEY(task_id, attempt, source_task_id),
 FOREIGN KEY(task_id, attempt) REFERENCES task_launch_claims(task_id, attempt)
);
INSERT INTO task_launch_inputs_new(task_id,attempt,source_task_id,acceptance_id)
 SELECT task_id,attempt,source_task_id,acceptance_id FROM task_launch_inputs;
DROP TABLE task_launch_inputs;
DROP TABLE task_acceptances;
ALTER TABLE task_acceptances_new RENAME TO task_acceptances;
ALTER TABLE task_launch_inputs_new RENAME TO task_launch_inputs;
CREATE INDEX task_acceptances_task ON task_acceptances(task_id, id);

-- +goose StatementBegin
CREATE TRIGGER acceptance_session_insert AFTER INSERT ON task_acceptances BEGIN
 UPDATE task_acceptances SET source_session_id=(SELECT session_id FROM tasks WHERE id=NEW.task_id) WHERE id=NEW.id;
END;
-- +goose StatementEnd

-- One inference decision per task execution: a completion inferred from a
-- closure, or a user reopen that suppresses inference for that execution.
CREATE TABLE completion_inferences (
 task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL,
 signal TEXT NOT NULL CHECK (signal IN ('settled','archived','reopened')),
 acceptance_id INTEGER,
 at TEXT NOT NULL DEFAULT (stage_now()),
 UNIQUE(task_id, execution_id)
);
