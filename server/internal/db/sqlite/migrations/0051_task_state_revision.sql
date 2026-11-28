-- +goose Up
-- First answer wins (ADR 0013): state_revision counts the task's real state
-- transitions; answered_revision is the revision last answered through Stage
-- (NULL: never).
ALTER TABLE tasks ADD COLUMN state_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN answered_revision INTEGER;
