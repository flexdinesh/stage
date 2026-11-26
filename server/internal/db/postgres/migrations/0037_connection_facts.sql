-- +goose Up
ALTER TABLE harness_connections ADD COLUMN last_known_facts TEXT;
