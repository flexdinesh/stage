-- +goose Up
-- Tree steering (ADR 0013): the steering brief a steerer's launch attempt
-- appended, frozen per attempt and reused on same-attempt retries.
CREATE TABLE steering_launch_context (
 task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 attempt INTEGER NOT NULL,
 brief TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (stage_now()),
 PRIMARY KEY (task_id, attempt)
);
