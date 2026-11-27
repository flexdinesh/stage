-- +goose Up
CREATE TABLE notification_recipients (
 notification_id INTEGER NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 read_at TEXT,
 PRIMARY KEY (notification_id, user_id)
);
CREATE INDEX notification_recipients_unread_idx ON notification_recipients(user_id, notification_id) WHERE read_at IS NULL;

CREATE TABLE notification_preferences (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 notify_events TEXT NOT NULL CHECK (json_type(notify_events) = 'array')
);

INSERT INTO notification_recipients(notification_id, user_id, read_at)
 SELECT id, 1, read_at FROM notifications;
INSERT INTO notification_preferences(user_id, notify_events)
 SELECT 1, notify_events FROM preferences WHERE id = 1;

DELETE FROM push_subscriptions
WHERE EXISTS (SELECT 1 FROM installation_access WHERE id = 1 AND mode <> 'personal');

ALTER TABLE push_subscriptions ADD COLUMN auth_session_id INTEGER REFERENCES auth_sessions(id) ON DELETE CASCADE;
ALTER TABLE push_subscriptions ADD COLUMN binding TEXT CHECK (
 (auth_session_id IS NULL AND binding IS NULL) OR (auth_session_id IS NOT NULL AND binding IS NOT NULL)
);
CREATE UNIQUE INDEX push_subscriptions_binding ON push_subscriptions(binding);
CREATE INDEX push_subscriptions_auth_session ON push_subscriptions(auth_session_id);
