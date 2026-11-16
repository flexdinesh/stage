-- +goose Up
-- T3 setup (docs/api.md "T3 connection"). Forward-only.
--   t3_url: the T3 server URL saved in Settings (NULL = default); T3_URL in
--     the environment wins over it.
--   environment_id: the T3 environment this server is bound to, set by the
--     first successful pairing; the token and the mirror belong to it.
ALTER TABLE t3_connection ADD COLUMN t3_url TEXT;
ALTER TABLE t3_connection ADD COLUMN environment_id TEXT;
