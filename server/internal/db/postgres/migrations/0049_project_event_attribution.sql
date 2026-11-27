-- +goose Up
-- ADR 0012: a workspace added through a harness is a project event, with the
-- admitted actor and cause. Widen the type check (expand only).
ALTER TABLE project_events DROP CONSTRAINT project_events_type_check;
ALTER TABLE project_events ADD CONSTRAINT project_events_type_check
 CHECK (type IN ('workspace_moved', 'project_merged', 'project_split', 'workspace_added'));
ALTER TABLE project_events ADD COLUMN attribution text;
