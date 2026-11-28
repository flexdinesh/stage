-- +goose Up
-- Tree steering (ADR 0013): the steering brief a steerer's launch attempt
-- appended, frozen per attempt and reused on same-attempt retries.
CREATE TABLE steering_launch_context (
 task_id bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 attempt integer NOT NULL,
 brief text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (task_id, attempt)
);
