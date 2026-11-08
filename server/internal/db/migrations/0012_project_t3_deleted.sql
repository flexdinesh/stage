-- +goose Up

-- t3_deleted_at: T3 deletedAt of a mirrored project. A deleted project stays
-- (tasks reference it) and is archived, but no longer claims its workspace
-- root: creating a project with that root makes a new T3 project.
ALTER TABLE projects ADD COLUMN t3_deleted_at timestamptz;

-- +goose Down
ALTER TABLE projects DROP COLUMN t3_deleted_at;
