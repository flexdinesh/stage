-- +goose Up
-- Legacy inventory has reported paths and remotes but no attested file evidence.
ALTER TABLE external_projects ADD COLUMN file_host INTEGER REFERENCES machines(id);
ALTER TABLE external_projects ADD COLUMN canonical_path TEXT NOT NULL DEFAULT '';
ALTER TABLE external_projects ADD COLUMN checkout_key TEXT NOT NULL DEFAULT '';
ALTER TABLE external_projects ADD COLUMN inode_hint TEXT NOT NULL DEFAULT '';
ALTER TABLE external_projects ADD COLUMN evidence_at TEXT;
ALTER TABLE external_projects ADD COLUMN evidence_source TEXT NOT NULL DEFAULT ''
 CHECK (evidence_source IN ('', 'harness', 'machine'));
ALTER TABLE external_projects ADD COLUMN resolution TEXT NOT NULL DEFAULT ''
 CHECK (resolution IN ('', 'bound', 'join', 'new', 'review'));
ALTER TABLE external_projects ADD COLUMN resolution_reason TEXT NOT NULL DEFAULT ''
 CHECK (resolution_reason IN ('', 'ambiguous', 'declined', 'moved', 'suggested'));
ALTER TABLE external_projects ADD COLUMN resolution_rule TEXT NOT NULL DEFAULT '';
ALTER TABLE external_projects ADD COLUMN candidates TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(candidates) AND json_type(candidates) = 'array');

ALTER TABLE project_bindings ADD COLUMN is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0,1));
ALTER TABLE project_bindings ADD COLUMN rule TEXT NOT NULL DEFAULT 'manual'
 CHECK (rule IN ('manual', 'new', 'remote', 'local', 'moved'));
UPDATE project_bindings
SET is_primary = active,
 rule = CASE origin WHEN 'imported' THEN 'new' WHEN 'path' THEN 'local' ELSE origin END;
DROP INDEX project_binding_active;
CREATE UNIQUE INDEX project_binding_primary ON project_bindings(project_id, connection_id)
 WHERE active = 1 AND is_primary = 1;

UPDATE external_projects SET repo = lower(repo);
UPDATE external_projects AS ep
SET resolution = 'bound', resolution_rule = 'existing'
FROM project_bindings AS pb
WHERE pb.active AND pb.connection_id = ep.connection_id
 AND pb.external_project_id = ep.external_id AND pb.workspace = ep.workspace;

CREATE TABLE workspace_declines (
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 identity_key TEXT NOT NULL,
 declined_at TEXT NOT NULL DEFAULT (stage_now()),
 PRIMARY KEY (connection_id, identity_key)
);

-- Keep the most recent decline when legacy rows share a key.
INSERT INTO workspace_declines (connection_id, identity_key, declined_at)
SELECT connection_id, 'external:' || connection_id || ':' || external_id, max(declined_at)
FROM external_projects
WHERE declined_at IS NOT NULL
GROUP BY connection_id, external_id;

-- A pathless workspace always uses its external key. Escape remote key
-- components in the same order as the resolver.
INSERT INTO workspace_declines (connection_id, identity_key, declined_at)
SELECT connection_id,
 'remote:' || replace(replace(repo, '%', '%25'), '#', '%23')
           || '#' || replace(replace(repo_path, '%', '%25'), '#', '%23') AS identity_key,
 max(declined_at)
FROM external_projects
WHERE declined_at IS NOT NULL AND workspace <> '' AND repo <> '' AND repo_path <> ''
GROUP BY connection_id, identity_key;

CREATE TABLE project_aliases (
 alias_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL REFERENCES projects(id),
 title TEXT NOT NULL,
 merged_at TEXT NOT NULL DEFAULT (stage_now()),
 merged_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 PRIMARY KEY (alias_id)
) WITHOUT ROWID;
CREATE INDEX project_aliases_project_idx ON project_aliases(project_id);

CREATE TABLE project_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects(id),
 type TEXT NOT NULL CHECK (type IN ('workspace_moved', 'project_merged', 'project_split')),
 text TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (stage_now()),
 created_by_user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id)
);
CREATE INDEX project_events_project_idx ON project_events(project_id, id DESC);

CREATE TABLE project_merge_dismissals (
 project_id INTEGER NOT NULL REFERENCES projects(id),
 other_id INTEGER NOT NULL REFERENCES projects(id),
 PRIMARY KEY (project_id, other_id),
 CHECK (project_id < other_id)
);

ALTER TABLE tasks ADD COLUMN binding_id INTEGER REFERENCES project_bindings(id) ON DELETE SET NULL;
ALTER TABLE schedules ADD COLUMN binding_id INTEGER REFERENCES project_bindings(id) ON DELETE SET NULL;

ALTER TABLE project_bindings DROP COLUMN origin;
ALTER TABLE external_projects DROP COLUMN declined_at;
ALTER TABLE projects DROP COLUMN archive_owner;
DROP TABLE shared_paths;
