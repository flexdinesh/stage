-- +goose Up
ALTER TABLE users ADD COLUMN email TEXT;
ALTER TABLE users ADD COLUMN email_key TEXT CHECK (
 (email IS NULL AND email_key IS NULL) OR
 (email IS NOT NULL AND email <> '' AND email_key IS NOT NULL AND email_key <> '')
);
ALTER TABLE users ADD COLUMN email_verified_at TEXT CHECK (email_verified_at IS NULL OR email IS NOT NULL);
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member'));
ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'));
CREATE UNIQUE INDEX users_email_key ON users(email_key);
UPDATE users SET role = 'admin' WHERE id = 1;

CREATE TABLE installation_access (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 installation_id TEXT CHECK (installation_id <> ''),
 mode TEXT NOT NULL CHECK (mode IN ('personal', 'setup_pending', 'claimed')),
 claimed_at TEXT,
 CHECK (mode = 'personal' OR installation_id IS NOT NULL),
 CHECK ((mode = 'claimed') = (claimed_at IS NOT NULL))
);
INSERT INTO installation_access(id, mode) VALUES (1, 'personal');

CREATE TABLE local_credentials (
 identity_id INTEGER PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
 password_verifier TEXT NOT NULL CHECK (password_verifier <> '')
);

CREATE TABLE auth_sessions (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 token_digest BLOB NOT NULL UNIQUE CHECK (length(token_digest) = 32),
 user_id INTEGER NOT NULL REFERENCES users(id),
 audience TEXT NOT NULL CHECK (audience IN ('browser', 'cli', 'agent')),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL CHECK (expires_at > created_at),
 idle_expires_at TEXT CHECK (idle_expires_at > created_at AND idle_expires_at <= expires_at),
 revoked_at TEXT
);
CREATE INDEX auth_sessions_user ON auth_sessions(user_id);

CREATE TABLE auth_grants (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 token_digest BLOB NOT NULL UNIQUE CHECK (length(token_digest) = 32),
 purpose TEXT NOT NULL CHECK (purpose IN ('setup', 'invite', 'password_reset')),
 issuer_user_id INTEGER REFERENCES users(id),
 email TEXT,
 email_key TEXT,
 target_user_id INTEGER REFERENCES users(id),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL CHECK (expires_at > created_at),
 consumed_at TEXT,
 revoked_at TEXT,
 CHECK (consumed_at IS NULL OR revoked_at IS NULL),
 CHECK (
  (purpose = 'setup' AND issuer_user_id IS NULL AND email IS NULL AND email_key IS NULL AND target_user_id IS NULL) OR
  (purpose = 'invite' AND issuer_user_id IS NOT NULL AND email IS NOT NULL AND email <> '' AND
   email_key IS NOT NULL AND email_key <> '' AND target_user_id IS NULL) OR
  (purpose = 'password_reset' AND email IS NULL AND email_key IS NULL AND target_user_id IS NOT NULL)
 )
);
CREATE UNIQUE INDEX auth_grants_open_setup ON auth_grants(purpose)
 WHERE purpose = 'setup' AND consumed_at IS NULL AND revoked_at IS NULL;
CREATE UNIQUE INDEX auth_grants_open_invite ON auth_grants(email_key)
 WHERE purpose = 'invite' AND consumed_at IS NULL AND revoked_at IS NULL;
