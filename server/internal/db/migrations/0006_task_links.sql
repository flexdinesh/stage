-- +goose Up

-- Chains are replaced by runs-after links: a task may run after one other
-- task (after_task_id) and has at most one follower. Existing chains become
-- links in step order; chain titles and states are dropped.
ALTER TABLE tasks ADD COLUMN after_task_id bigint REFERENCES tasks (id) ON DELETE SET NULL;
CREATE UNIQUE INDEX tasks_after_task_id_key ON tasks (after_task_id) WHERE after_task_id IS NOT NULL;

UPDATE tasks t SET after_task_id = s.prev
FROM (
    SELECT id, lag(id) OVER (PARTITION BY chain_id ORDER BY chain_position, id) AS prev
    FROM tasks WHERE chain_id IS NOT NULL
) s
WHERE t.id = s.id AND s.prev IS NOT NULL;

DROP INDEX tasks_chain_idx;
ALTER TABLE tasks DROP COLUMN chain_id, DROP COLUMN chain_position;
DROP TABLE chains;

-- +goose Down
-- Schema only: links are not converted back into chains.
CREATE TABLE chains (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title      text NOT NULL,
    state      text NOT NULL DEFAULT 'idle' CHECK (state IN ('idle', 'running', 'paused', 'done')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chains_state_idx ON chains (state);
ALTER TABLE tasks ADD COLUMN chain_id bigint REFERENCES chains (id) ON DELETE SET NULL,
                  ADD COLUMN chain_position integer;
CREATE INDEX tasks_chain_idx ON tasks (chain_id, chain_position);
DROP INDEX tasks_after_task_id_key;
ALTER TABLE tasks DROP COLUMN after_task_id;
