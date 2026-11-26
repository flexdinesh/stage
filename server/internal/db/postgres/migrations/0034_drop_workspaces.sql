-- +goose Up
-- Worktree as a harness launch option (ADR 0007 decisions 5 and 6),
-- contract. 0033 moved saved workspaces into execution_config options;
-- Stage no longer stores a workspace or resolves predecessor bases, so the
-- columns and the launch base records go. History stays readable through
-- the journal (the launching event named the base) and accepted snapshots.
ALTER TABLE tasks DROP COLUMN workspace;
ALTER TABLE schedules DROP COLUMN workspace;
DROP TABLE task_launch_bases;
