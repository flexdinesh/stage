-- +goose Up

-- Closing work (docs/api.md "Done, archive and delete"). Forward-only.
--   task_trees.status: open | done (marked done by hand) | archived.
--     A tree that is not open releases nothing and its members leave the
--     Dashboard; activity on a member reopens it.
--   task_trees.closed_at: when it was marked done or archived (NULL open).
--   tasks.archived_at: an archived standalone task (NULL = not archived);
--     activity on it unarchives it.
ALTER TABLE task_trees ADD COLUMN status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'done', 'archived'));
ALTER TABLE task_trees ADD COLUMN closed_at TEXT;
ALTER TABLE tasks ADD COLUMN archived_at TEXT;
CREATE INDEX tasks_archived_idx ON tasks (archived_at) WHERE archived_at IS NOT NULL;
