-- +goose Up
ALTER TABLE task_events ADD COLUMN attribution TEXT;
ALTER TABLE journal_entries ADD COLUMN attribution TEXT;
ALTER TABLE task_acceptances ADD COLUMN attribution TEXT;
ALTER TABLE execution_attempts ADD COLUMN attribution TEXT;
ALTER TABLE schedule_runs ADD COLUMN attribution TEXT;
