-- +goose Up

-- Inbox: every notification the notifier sends (also with no push
-- subscriptions). Pruned after 30 days. Title/project are snapshots.
CREATE TABLE notifications (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event         text NOT NULL CHECK (event IN ('needs_input', 'done', 'failed')),
    task_id       bigint REFERENCES tasks (id) ON DELETE SET NULL,
    t3_thread_id  text,
    title         text NOT NULL,
    project_title text,
    url           text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    read_at       timestamptz
);

CREATE INDEX notifications_created_idx ON notifications (created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (id) WHERE read_at IS NULL;

-- +goose Down
DROP TABLE notifications;
