-- +goose Up
ALTER TABLE external_projects ADD COLUMN historical_project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL;
