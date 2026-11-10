-- +goose Up

-- Global notification filter: events (TaskState) that notify (inbox + push).
-- An event not listed is dropped entirely. Default: all.
ALTER TABLE preferences ADD COLUMN notify_events text[] NOT NULL
    DEFAULT '{needs_input,failed,done}'
    CHECK (notify_events <@ ARRAY['needs_input', 'done', 'failed']::text[]);

-- +goose Down
ALTER TABLE preferences DROP COLUMN notify_events;
