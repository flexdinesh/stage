-- +goose Up
-- New-task defaults move from the single preferences row to each connection.
-- Forward-only.
--   harness_connections.new_task_config: the model and options pinned for new
--     tasks on this connection, an execution config with empty permission and
--     mode (NULL = the harness default).
--   preferences.default_connection_id: the connection new tasks start on
--     (NULL = none: the project's only binding, as before).
-- The global pin moves to the local machine's active T3 connection (legacy
-- provider/model pins become its model key); last_used is dropped.
ALTER TABLE harness_connections ADD COLUMN new_task_config TEXT
    CHECK (new_task_config IS NULL OR json_type(new_task_config) = 'object');
ALTER TABLE preferences ADD COLUMN default_connection_id INTEGER
    REFERENCES harness_connections (id) ON DELETE SET NULL;

UPDATE harness_connections SET new_task_config = (
    SELECT CASE
        WHEN json_type(p.pinned, '$.executionConfig') = 'object' THEN json_object(
            'model', COALESCE(json_extract(p.pinned, '$.executionConfig.model'), ''),
            'options', json(COALESCE(json_extract(p.pinned, '$.executionConfig.options'), '[]')),
            'permission', '', 'mode', '')
        WHEN json_type(p.pinned, '$.providerInstanceId') = 'text' AND json_type(p.pinned, '$.model') = 'text' THEN json_object(
            'model', '' || json_array(json_extract(p.pinned, '$.providerInstanceId'), json_extract(p.pinned, '$.model')),
            'options', json(COALESCE(json_extract(p.pinned, '$.modelOptions'), '[]')),
            'permission', '', 'mode', '')
    END
    FROM preferences p WHERE p.id = 1 AND p.pinned IS NOT NULL
)
WHERE machine_id = 1 AND harness = 't3' AND active = 1;

ALTER TABLE preferences DROP COLUMN pinned;
ALTER TABLE preferences DROP COLUMN last_used;
