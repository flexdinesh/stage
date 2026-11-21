-- +goose Up

-- Free-form task and schedule tags are removed (never used for anything).
-- Forward-only: the dropped values are not recoverable.
ALTER TABLE tasks DROP COLUMN tags;
ALTER TABLE schedules DROP COLUMN tags;
