-- +goose Up
ALTER TABLE harness_connections ADD COLUMN workspace_refreshed_at TIMESTAMPTZ;
ALTER TABLE harness_connections ADD COLUMN workspace_refresh_error TEXT;
