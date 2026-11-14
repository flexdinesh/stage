-- +goose Up

-- Agent interface (docs/api.md "Agents"): the T3 thread that created a task
-- or schedule (provenance), and a backlog task waiting on a T3 thread that is
-- not a task's ("run after me" from an external session). Many tasks may wait
-- on one thread. A task waits on at most one of: start time, task, thread.
ALTER TABLE tasks
    ADD COLUMN origin_thread_id text,
    ADD COLUMN after_thread_id text,
    DROP CONSTRAINT tasks_start_or_after,
    ADD CONSTRAINT tasks_one_wait CHECK (num_nonnulls(start_at, after_task_id, after_thread_id) <= 1);
CREATE INDEX tasks_after_thread_idx ON tasks (after_thread_id) WHERE after_thread_id IS NOT NULL;
ALTER TABLE schedules ADD COLUMN origin_thread_id text;

-- +goose Down
ALTER TABLE schedules DROP COLUMN origin_thread_id;
DROP INDEX tasks_after_thread_idx;
ALTER TABLE tasks
    DROP CONSTRAINT tasks_one_wait,
    DROP COLUMN after_thread_id,
    DROP COLUMN origin_thread_id,
    ADD CONSTRAINT tasks_start_or_after CHECK (start_at IS NULL OR after_task_id IS NULL);
