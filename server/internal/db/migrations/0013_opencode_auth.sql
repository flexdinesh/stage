-- +goose Up
-- How Stage authenticates to OpenCode: none, local (the password in the local
-- service's registration file, never stored), paired (a renewable session
-- token from an `opencode pair` link) or basic (entered credentials).
ALTER TABLE opencode_settings ADD COLUMN auth TEXT NOT NULL DEFAULT 'none' CHECK(auth IN ('none','local','paired','basic'));
ALTER TABLE opencode_settings ADD COLUMN token_issued_at TEXT;
UPDATE opencode_settings SET auth='basic' WHERE password<>'';
