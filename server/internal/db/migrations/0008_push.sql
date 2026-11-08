-- +goose Up

-- Single-row VAPID key pair (base64url), generated on first start.
CREATE TABLE push_vapid (
    id          smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    public_key  text NOT NULL,
    private_key text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Browser Web Push subscriptions (PushSubscription.toJSON()).
CREATE TABLE push_subscriptions (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint        text NOT NULL UNIQUE,
    p256dh          text NOT NULL,
    auth            text NOT NULL,
    user_agent      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_success_at timestamptz,
    last_error      text
);

-- Thread watcher's last mapped state (TaskState) for external threads;
-- NULL = never observed.
ALTER TABLE threads ADD COLUMN observed_state text;

-- +goose Down
ALTER TABLE threads DROP COLUMN observed_state;
DROP TABLE push_subscriptions;
DROP TABLE push_vapid;
