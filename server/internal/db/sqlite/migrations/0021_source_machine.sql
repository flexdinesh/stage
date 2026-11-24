-- +goose Up
-- The forwarding machine of a write (docs/architecture.md "Users"): a
-- request that arrived through a worker's agent gateway records the machine
-- whose token it carried, next to created_by_user_id. NULL: not through a
-- gateway (the UI, a local agent, background work). Machines are soft
-- deleted, so the reference outlives a removed machine. A NULL default with
-- a REFERENCES clause is allowed while foreign keys are on.

ALTER TABLE tasks ADD COLUMN source_machine_id INTEGER REFERENCES machines(id);
ALTER TABLE task_trees ADD COLUMN source_machine_id INTEGER REFERENCES machines(id);
ALTER TABLE journal_entries ADD COLUMN source_machine_id INTEGER REFERENCES machines(id);
ALTER TABLE schedules ADD COLUMN source_machine_id INTEGER REFERENCES machines(id);
ALTER TABLE projects ADD COLUMN source_machine_id INTEGER REFERENCES machines(id);
