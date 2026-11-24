-- +goose Up
-- Search indexes (#404, docs/storage.md "Search"): pg_trgm GIN indexes on
-- the searched columns, used by ILIKE substring matching. pg_trgm is a
-- trusted extension: the database owner (or a role with CREATE on the
-- database) can create it.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX tasks_title_trgm ON tasks USING gin (title gin_trgm_ops);
CREATE INDEX tasks_prompt_trgm ON tasks USING gin (prompt gin_trgm_ops);
CREATE INDEX journal_entries_text_trgm ON journal_entries USING gin (text gin_trgm_ops);
