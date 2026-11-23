-- +goose Up
-- T3 connection settings move from the environment to Settings (T3_URL,
-- T3_PUBLIC_URL and T3_ADMIN_TOKEN are no longer read). Forward-only.
--   public_url: the browser URL for Open in T3 links (NULL = the T3 URL).
--   admin_token: the local machine's T3 admin token, which lets Stage renew
--     its T3 token itself (NULL = none; pair again when it expires). A
--     worker keeps its own in its credentials file, never here.
ALTER TABLE t3_connection ADD COLUMN public_url TEXT;
ALTER TABLE t3_connection ADD COLUMN admin_token TEXT;
