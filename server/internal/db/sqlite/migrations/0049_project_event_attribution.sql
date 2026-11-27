-- +goose Up
-- ADR 0012: a workspace added through a harness is a project event, with the
-- admitted actor and cause. Rebuild to widen the type check (expand only).
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN;

CREATE TABLE project_events_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects(id),
 type TEXT NOT NULL CHECK (type IN ('workspace_moved', 'project_merged', 'project_split', 'workspace_added')),
 text TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (stage_now()),
 created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 attribution TEXT
);
INSERT INTO project_events_new(id,project_id,type,text,created_at,created_by_user_id)
 SELECT id,project_id,type,text,created_at,created_by_user_id FROM project_events;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('project_events','project_events_new')) WHERE name='project_events_new';
DROP TABLE project_events;
ALTER TABLE project_events_new RENAME TO project_events;
CREATE INDEX project_events_project_idx ON project_events(project_id, id DESC);

COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
