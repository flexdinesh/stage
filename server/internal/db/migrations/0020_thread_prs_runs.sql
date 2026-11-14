-- +goose Up

-- Pull requests T3 associates with a thread (t3_thread_list/read
-- linkedPullRequest + pullRequests, deduped): [{host, repository, number,
-- url, source, state}].
ALTER TABLE threads ADD COLUMN pull_requests jsonb NOT NULL DEFAULT '[]';
-- t3_thread_read recentRuns (newest first, at most 50) and runCount; each
-- follow-up on a thread is a new run. Written by detail reads only.
ALTER TABLE threads ADD COLUMN recent_runs jsonb NOT NULL DEFAULT '[]';
ALTER TABLE threads ADD COLUMN run_count integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE threads DROP COLUMN pull_requests, DROP COLUMN recent_runs, DROP COLUMN run_count;
