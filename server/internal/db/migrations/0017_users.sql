-- +goose Up
-- Users and their future sign-in identities (docs/architecture.md "Users").
-- No sign-in exists yet: every write attributes to the default user (id 1)
-- through db.ActingUser. identities stays empty until sign-in lands.
--
-- created_by_user_id: the user who created the row. Existing rows and rows a
-- trigger writes take the default user. SQLite refuses ADD COLUMN with a
-- REFERENCES clause and a non-null default while foreign keys are on, so the
-- migration runs outside goose's transaction with them off (as 0015).
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
BEGIN;

CREATE TABLE users (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 display_name TEXT NOT NULL CHECK(display_name<>''),
 created_at TEXT NOT NULL DEFAULT(stage_now())
);
INSERT INTO users(id,display_name) VALUES(1,'Default user');

CREATE TABLE identities (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider TEXT NOT NULL CHECK(provider<>''),
 subject TEXT NOT NULL CHECK(subject<>''),
 created_at TEXT NOT NULL DEFAULT(stage_now()),
 UNIQUE(provider,subject)
);
CREATE INDEX identities_user ON identities(user_id);

ALTER TABLE tasks ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id);
ALTER TABLE task_trees ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id);
ALTER TABLE journal_entries ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id);
ALTER TABLE schedules ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id);
ALTER TABLE projects ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id);
ALTER TABLE machines ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id);

COMMIT;
PRAGMA foreign_keys=ON;
