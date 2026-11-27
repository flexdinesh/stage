-- +goose Up
ALTER TABLE projects ADD COLUMN catalog_group BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE external_projects ADD COLUMN group_project_id BIGINT REFERENCES projects(id);
ALTER TABLE external_projects ADD COLUMN group_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE external_projects ADD COLUMN group_attribution TEXT;
CREATE INDEX external_projects_group_idx ON external_projects(group_project_id) WHERE group_project_id IS NOT NULL;

-- Exact legacy manual assignments and project origins are anchors. Carry
-- automatic joins forward only when an anchor verifies the same strong key.
-- Preserve duplicate owners and the old projects, bindings and work.
WITH bound AS (
 SELECT e.connection_id,e.external_id,e.workspace,b.project_id,b.rule,
  CASE
   WHEN e.evidence_source='' THEN NULL
   WHEN e.repo<>'' AND e.repo_path<>'' THEN
    'remote:' || replace(replace(e.repo,'%','%25'),'#','%23') || '#' || replace(replace(e.repo_path,'%','%25'),'#','%23')
   WHEN e.workspace='' THEN NULL
   WHEN e.evidence_source='machine' AND e.file_host IS NOT NULL AND e.checkout_key<>'' AND e.repo_path<>'' THEN
    'local:' || e.file_host || ':' || replace(replace(e.checkout_key,'%','%25'),'#','%23') || '#' || replace(replace(e.repo_path,'%','%25'),'#','%23')
   WHEN e.evidence_source='machine' AND e.file_host IS NOT NULL AND e.repo='' AND e.checkout_key='' AND e.repo_path='' AND e.canonical_path<>'' THEN
    'dir:' || e.file_host || ':' || e.canonical_path
  END AS strong_key
 FROM external_projects e
 JOIN project_bindings b ON b.connection_id=e.connection_id
  AND b.external_project_id=e.external_id AND b.workspace=e.workspace AND b.active
)
UPDATE external_projects AS e
SET group_project_id=b.project_id, group_revision=1
FROM bound AS b
WHERE e.connection_id=b.connection_id AND e.external_id=b.external_id AND e.workspace=b.workspace
 AND (b.rule IN ('manual','new') OR (b.rule IN ('remote','local') AND EXISTS (
  SELECT 1 FROM bound a WHERE a.project_id=b.project_id AND a.strong_key=b.strong_key AND a.rule IN ('manual','new')
 )));

UPDATE external_projects
SET group_project_id=historical_project_id, group_revision=1
WHERE group_project_id IS NULL AND historical_project_id IS NOT NULL;
