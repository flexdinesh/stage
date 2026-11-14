-- +goose Up

-- Session batch (docs/api.md "Session batch"): tasks created by one session.
CREATE INDEX tasks_origin_thread_idx ON tasks (origin_thread_id) WHERE origin_thread_id IS NOT NULL;

-- +goose Down
DROP INDEX tasks_origin_thread_idx;
