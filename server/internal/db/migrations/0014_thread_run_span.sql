-- +goose Up

-- Elapsed run span of a thread from t3_thread_read recentRuns: first run's
-- start (thread created_at when older runs fall outside the read) to the
-- newest run's completion. NULL until a detail read sees a run.
ALTER TABLE threads ADD COLUMN run_started_at timestamptz;
ALTER TABLE threads ADD COLUMN run_ended_at timestamptz;

-- +goose Down
ALTER TABLE threads DROP COLUMN run_started_at, DROP COLUMN run_ended_at;
