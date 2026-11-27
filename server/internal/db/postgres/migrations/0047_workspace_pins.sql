-- +goose Up
ALTER TABLE tasks ADD COLUMN workspace_pin jsonb CHECK (jsonb_typeof(workspace_pin) = 'object');
ALTER TABLE schedules ADD COLUMN workspace_pin jsonb CHECK (jsonb_typeof(workspace_pin) = 'object');
ALTER TABLE task_launch_destinations ALTER COLUMN binding_id DROP NOT NULL;

-- A prior claim already proved the exact identity for a later retry.
UPDATE tasks AS owner
SET workspace_pin = jsonb_build_object('externalId', d.external_project_id, 'root', d.workspace)
FROM task_launch_destinations d
WHERE d.task_id = owner.id AND d.attempt = owner.launch_attempt
 AND d.connection_id = owner.connection_id AND d.external_project_id <> '';

-- An explicit binding can prove an exact key after a split or while
-- the workspace is offline. A legacy primary must belong to the owner's
-- project and name an eligible exact inventory row. The binding
-- and catalog keys make each match unique on the owner's connection.
UPDATE tasks AS owner
SET workspace_pin = jsonb_build_object('externalId', b.external_project_id, 'root', b.workspace)
FROM project_bindings b
JOIN harness_connections c ON c.id = b.connection_id
JOIN external_projects e ON e.connection_id = b.connection_id
 AND e.external_id = b.external_project_id AND e.workspace = b.workspace
WHERE owner.connection_id = b.connection_id AND c.active AND NOT e.archived AND e.external_id <> ''
 AND (owner.binding_id = b.id OR (owner.binding_id IS NULL AND owner.project_id = b.project_id
  AND b.active AND b.is_primary AND b.status = 'available' AND e.available))
 AND owner.launch_attempt = 0 AND owner.session_id IS NULL AND NOT owner.adopted;

UPDATE schedules AS owner
SET workspace_pin = jsonb_build_object('externalId', b.external_project_id, 'root', b.workspace)
FROM project_bindings b
JOIN harness_connections c ON c.id = b.connection_id
JOIN external_projects e ON e.connection_id = b.connection_id
 AND e.external_id = b.external_project_id AND e.workspace = b.workspace
WHERE owner.connection_id = b.connection_id AND c.active AND NOT e.archived AND e.external_id <> ''
 AND (owner.binding_id = b.id OR (owner.binding_id IS NULL AND owner.project_id = b.project_id
  AND b.active AND b.is_primary AND b.status = 'available' AND e.available));
