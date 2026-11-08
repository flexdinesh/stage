-- +goose Up

-- launch_reuse: the current attempt's t3_thread_launch failed in transport
-- (outcome unknown, no thread recorded); the next start reuses its
-- clientRequestId instead of beginning a new attempt.
ALTER TABLE tasks ADD COLUMN launch_reuse boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tasks DROP COLUMN launch_reuse;
