-- +goose Up
-- No default harness (ADR 0007 decision 12). New installs have no
-- connection: the seeded T3 connection 1 is removed when it was
-- never configured (no identity, no T3 token, default settings, not the
-- default connection) and nothing references it. A paired or used T3
-- connection 1 stays as it is.
DELETE FROM harness_connections
WHERE id = 1 AND harness = 't3' AND identity = '' AND active = 1 AND auto_complete = 1 AND new_task_config IS NULL
  AND NOT EXISTS (SELECT 1 FROM t3_connection c WHERE c.machine_id = harness_connections.machine_id AND COALESCE(c.token, '') <> '')
  AND NOT EXISTS (SELECT 1 FROM preferences WHERE default_connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM tasks WHERE connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM schedules WHERE connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM session_identities WHERE connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM shared_paths WHERE connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM project_bindings WHERE connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM external_projects WHERE connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM projects WHERE imported_connection_id = 1)
  AND NOT EXISTS (SELECT 1 FROM task_launch_destinations WHERE connection_id = 1);

-- The T3-only session mirror: execution_observations holds every
-- harness's observed state (contract; nothing reads or writes it).
DROP TABLE threads;
