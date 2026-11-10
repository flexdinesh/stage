-- +goose Up

-- settled becomes a notification event. Default stays all: a filter with
-- every previous event on gains settled; narrower filters keep it off.
ALTER TABLE notifications DROP CONSTRAINT notifications_event_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_event_check
    CHECK (event IN ('needs_input', 'done', 'failed', 'settled'));
ALTER TABLE preferences DROP CONSTRAINT preferences_notify_events_check;
ALTER TABLE preferences ADD CONSTRAINT preferences_notify_events_check
    CHECK (notify_events <@ ARRAY['needs_input', 'done', 'failed', 'settled']::text[]);
ALTER TABLE preferences ALTER COLUMN notify_events SET DEFAULT '{needs_input,failed,done,settled}';
UPDATE preferences SET notify_events = notify_events || '{settled}'
    WHERE notify_events @> '{needs_input,failed,done}';

-- +goose Down
DELETE FROM notifications WHERE event = 'settled';
UPDATE preferences SET notify_events = array_remove(notify_events, 'settled');
ALTER TABLE preferences ALTER COLUMN notify_events SET DEFAULT '{needs_input,failed,done}';
ALTER TABLE preferences DROP CONSTRAINT preferences_notify_events_check;
ALTER TABLE preferences ADD CONSTRAINT preferences_notify_events_check
    CHECK (notify_events <@ ARRAY['needs_input', 'done', 'failed']::text[]);
ALTER TABLE notifications DROP CONSTRAINT notifications_event_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_event_check
    CHECK (event IN ('needs_input', 'done', 'failed'));
