-- +goose Up

-- threads_root_created_idx: history/stats with subagents=exclude list root
-- threads only (parent_thread_id IS NULL) ordered by created_at.
CREATE INDEX threads_root_created_idx ON threads (created_at DESC) WHERE parent_thread_id IS NULL;

-- +goose Down
DROP INDEX threads_root_created_idx;
