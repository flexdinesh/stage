-- +goose Up
-- Cross-machine project matching (docs/architecture.md "Project import"):
-- a workspace on one machine joins the project bound on another machine to
-- the same normalized origin remote and subproject path.
--
-- external_projects.repo_path: slash path from the checkout top to the
-- workspace ("." at the top), '' when unknown. Existing rows stay unknown
-- until the next discovery refresh.
--
-- project_bindings is rebuilt so origin also allows 'remote' (discovery bound
-- it across machines by remote and subproject). Same rebuild procedure as
-- 0015; legacy_alter_table keeps triggers that name project_bindings valid
-- across the rename.
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN;

ALTER TABLE external_projects ADD COLUMN repo_path TEXT NOT NULL DEFAULT '';

CREATE TABLE project_bindings_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects(id),
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 external_project_id TEXT NOT NULL,
 workspace TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 status TEXT NOT NULL DEFAULT 'available' CHECK(status IN ('available','missing','changed')),
 observed_workspace TEXT,
 created_at TEXT NOT NULL DEFAULT(stage_now()),
 -- manual: a user bound it; imported: discovery created the project for it;
 -- path: discovery bound it to the project at that path on this machine;
 -- remote: discovery bound it to the one project with the same origin
 -- remote and subproject on another machine.
 origin TEXT NOT NULL DEFAULT 'manual' CHECK(origin IN ('manual','imported','path','remote'))
);
INSERT INTO project_bindings_new(id,project_id,connection_id,external_project_id,workspace,revision,active,status,observed_workspace,created_at,origin)
 SELECT id,project_id,connection_id,external_project_id,workspace,revision,active,status,observed_workspace,created_at,origin FROM project_bindings;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('project_bindings','project_bindings_new')) WHERE name='project_bindings_new';
DROP TABLE project_bindings;
ALTER TABLE project_bindings_new RENAME TO project_bindings;
CREATE UNIQUE INDEX project_binding_active ON project_bindings(project_id,connection_id) WHERE active=1;
CREATE UNIQUE INDEX workspace_binding_active ON project_bindings(connection_id,external_project_id,workspace) WHERE active=1;

COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
