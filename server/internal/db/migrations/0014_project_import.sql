-- +goose Up
-- Discovery imports deliberate harness projects (docs/architecture.md
-- "Project import"). A discovered project records its importing connection.
-- archive_owner: who last decided the archive state. 'user' (any Stage
-- archive or unarchive) stops the harness from following its archive.
ALTER TABLE projects ADD COLUMN origin TEXT CHECK(origin IN ('discovered'));
ALTER TABLE projects ADD COLUMN imported_connection_id INTEGER REFERENCES harness_connections(id);
ALTER TABLE projects ADD COLUMN imported_at TEXT;
ALTER TABLE projects ADD COLUMN archive_owner TEXT CHECK(archive_owner IN ('user','harness'));
UPDATE projects SET archive_owner='user' WHERE archived_at IS NOT NULL;

-- manual: a user bound it; imported: discovery created the project for it;
-- path: discovery bound it to the project already at that path.
ALTER TABLE project_bindings ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual' CHECK(origin IN ('manual','imported','path'));

-- importable: the adapter judges it a deliberate project. declined_at: the
-- user unbound it, so discovery never binds it again until bound manually.
-- repo: normalized origin remote (host/owner/name), search metadata only.
ALTER TABLE external_projects ADD COLUMN importable INTEGER NOT NULL DEFAULT 0 CHECK(importable IN (0,1));
ALTER TABLE external_projects ADD COLUMN archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1));
ALTER TABLE external_projects ADD COLUMN active_at TEXT;
ALTER TABLE external_projects ADD COLUMN declined_at TEXT;
ALTER TABLE external_projects ADD COLUMN repo TEXT NOT NULL DEFAULT '';
