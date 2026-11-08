-- +goose Up

-- Task screenshots. task_id NULL = staged upload (not yet attached to a task,
-- or a follow-up image waiting to be sent); staged rows older than 24h are
-- swept. Uploaded to T3 on every launch.
CREATE TABLE task_attachments (
    id         bigserial PRIMARY KEY,
    task_id    bigint REFERENCES tasks (id) ON DELETE CASCADE,
    name       text NOT NULL,
    mime_type  text NOT NULL CHECK (mime_type IN ('image/png', 'image/jpeg', 'image/gif', 'image/webp')),
    size_bytes integer NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    data       bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_attachments_task_idx ON task_attachments (task_id, id);
CREATE INDEX task_attachments_staged_idx ON task_attachments (created_at) WHERE task_id IS NULL;

-- +goose Down
DROP TABLE task_attachments;
