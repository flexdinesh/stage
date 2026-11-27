-- +goose Up
-- Why the harness will not start a session in a listed workspace ('' when
-- nothing known stops it), from the latest complete inventory.
ALTER TABLE external_projects ADD COLUMN blocked_reason TEXT NOT NULL DEFAULT '';
