-- +goose Up
CREATE TABLE access_audit (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 action TEXT NOT NULL CHECK (action <> ''),
 actor_user_id INTEGER REFERENCES users(id),
 target_user_id INTEGER REFERENCES users(id),
 grant_id INTEGER CHECK (grant_id > 0),
 created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX auth_grants_open_reset ON auth_grants(target_user_id)
 WHERE purpose = 'password_reset' AND consumed_at IS NULL AND revoked_at IS NULL;
