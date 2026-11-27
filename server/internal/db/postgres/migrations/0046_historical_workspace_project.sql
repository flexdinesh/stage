-- +goose Up
ALTER TABLE external_projects ADD COLUMN historical_project_id BIGINT REFERENCES projects(id) ON DELETE SET NULL;
