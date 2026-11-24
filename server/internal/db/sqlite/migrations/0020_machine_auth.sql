-- +goose Up
-- Remote machines and their credentials (docs/adr/0002-machines-and-workers.md,
-- docs/api.md "Machines").
--
-- machines: removed_at hides a removed machine while its connections stay as
-- history; last_seen_at and version are what its worker last reported. The
-- local machine (id 1) has neither and is never removed.
--
-- pairing_codes: one-time codes that add a machine (10 minute TTL, single
-- use). machine_tokens: the credential a machine's worker presents on
-- /api/worker/*. Both store only the SHA-256 of their secret; the id in the
-- presented value selects the row.

ALTER TABLE machines ADD COLUMN removed_at TEXT;
ALTER TABLE machines ADD COLUMN last_seen_at TEXT;
ALTER TABLE machines ADD COLUMN version TEXT NOT NULL DEFAULT '';

CREATE TABLE pairing_codes (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 secret_hash BLOB NOT NULL CHECK(length(secret_hash)=32),
 label TEXT NOT NULL DEFAULT '',
 created_by_user_id INTEGER NOT NULL REFERENCES users(id),
 created_at TEXT NOT NULL DEFAULT(stage_now()),
 expires_at TEXT NOT NULL,
 redeemed_at TEXT,
 machine_id INTEGER REFERENCES machines(id),
 CHECK((redeemed_at IS NULL) = (machine_id IS NULL))
);

CREATE TABLE machine_tokens (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 machine_id INTEGER NOT NULL REFERENCES machines(id) CHECK(machine_id<>1),
 secret_hash BLOB NOT NULL CHECK(length(secret_hash)=32),
 created_at TEXT NOT NULL DEFAULT(stage_now())
);
CREATE UNIQUE INDEX machine_tokens_machine ON machine_tokens(machine_id);
