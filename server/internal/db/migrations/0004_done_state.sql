-- +goose Up

-- Task state `completed` is renamed `done`: the thread's run finished, the
-- task is not settled in T3 yet.
ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'done' WHERE state = 'completed';
UPDATE task_events SET from_state = 'done' WHERE from_state = 'completed';
UPDATE task_events SET to_state = 'done' WHERE to_state = 'completed';
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN (
    'backlog', 'launching', 'running', 'needs_input', 'done',
    'settled', 'failed', 'interrupted', 'cancelled'
));

-- +goose Down
ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'completed' WHERE state = 'done';
UPDATE task_events SET from_state = 'completed' WHERE from_state = 'done';
UPDATE task_events SET to_state = 'completed' WHERE to_state = 'done';
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN (
    'backlog', 'launching', 'running', 'needs_input', 'completed',
    'settled', 'failed', 'interrupted', 'cancelled'
));
