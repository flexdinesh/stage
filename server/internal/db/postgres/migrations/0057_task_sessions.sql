-- +goose Up
CREATE TABLE task_sessions (
 task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 attempt INTEGER NOT NULL,
 session_id BIGINT NOT NULL REFERENCES session_identities(id),
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (task_id, attempt),
 UNIQUE (session_id)
);

INSERT INTO task_sessions(task_id, attempt, session_id, recorded_at)
SELECT id, launch_attempt, session_id, COALESCE(launched_at, created_at)
FROM tasks WHERE session_id IS NOT NULL;
