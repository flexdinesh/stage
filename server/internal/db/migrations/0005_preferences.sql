-- +goose Up

-- Single-row app preferences. pinned / last_used are new-task model
-- defaults: {"providerInstanceId","model","modelOptions","runtimeMode","interactionMode"}.
-- A new task prefills from pinned, else last_used (written on task create).
CREATE TABLE preferences (
    id         smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    pinned     jsonb,
    last_used  jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO preferences (id) VALUES (1);

-- +goose Down
DROP TABLE preferences;
