-- +goose Up

-- Queued session continuations are removed (never used). Forward-only:
-- the dropped rows are not recoverable. Their indexes go with the tables.
DROP TABLE continuation_events;
DROP TABLE continuations;
