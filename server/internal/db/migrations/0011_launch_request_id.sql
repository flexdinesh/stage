-- +goose Up

-- launch_request_id: the clientRequestId of the current launch attempt,
-- "task-<id>-<uuid>". The random part keeps ids unique across databases
-- (dev, test, restored backups) that share one T3, which dedupes launches by
-- clientRequestId. Kept while launch_reuse is set. Earlier attempts used
-- "task-<id>-<attempt>"; backfilled so a pending reuse still finds its thread.
ALTER TABLE tasks ADD COLUMN launch_request_id text;
UPDATE tasks SET launch_request_id = 'task-' || id || '-' || launch_attempt WHERE launch_attempt > 0;

-- +goose Down
ALTER TABLE tasks DROP COLUMN launch_request_id;
