SELECT count(*) FROM (
 SELECT group_project_id FROM external_projects WHERE external_id IN ('root','clone')
  AND group_project_id=4 AND group_revision=1 AND group_attribution IS NULL
 GROUP BY group_project_id HAVING count(*)=2
 ) AS seeded;
SELECT count(*) FROM external_projects WHERE external_id='duplicate' AND group_project_id=5;
SELECT count(*) FROM external_projects WHERE external_id='unknown' AND group_project_id IS NULL;
SELECT count(*) FROM external_projects WHERE external_id='historical' AND group_project_id=5 AND historical_project_id=5;
SELECT count(*) FROM external_projects WHERE external_id='root' AND group_project_id=4 AND historical_project_id=5;
SELECT count(*) FROM projects p JOIN users u ON u.id=p.created_by_user_id
 WHERE p.id=4 AND u.id=7 AND u.status='disabled' AND p.source_machine_id=2;
SELECT count(*) FROM project_aliases WHERE alias_id=39 AND project_id=4 AND merged_by_user_id=7;
SELECT count(*) FROM tasks WHERE id=6 AND project_id=4 AND binding_id=8 AND created_by_user_id=7 AND source_machine_id=2
 AND workspace_pin='{"externalId":"root","root":"/root"}';
SELECT count(*) FROM snippets WHERE id=12 AND project_id=4 AND body='Keep scope';
SELECT count(*) FROM task_events WHERE task_id=6 AND detail='historical' AND attribution IS NULL;
SELECT count(*) FROM task_events WHERE task_id=6 AND detail='accepted' AND attribution='{{attribution}}';
SELECT count(*) FROM journal_entries WHERE task_id=6 AND text='old note ✓' AND created_by_user_id=7
 AND source_machine_id=2 AND attribution IS NULL;
SELECT count(*) FROM journal_entries WHERE task_id=6 AND text='authored note ✓' AND created_by_user_id=7
 AND source_machine_id=2 AND attribution='{{attribution}}';
SELECT count(*) FROM task_acceptances WHERE task_id=6 AND evidence='{{evidence}}' AND note='historical' AND attribution IS NULL;
SELECT count(*) FROM task_acceptances WHERE task_id=6 AND evidence='{{evidence}}' AND note='authored' AND attribution='{{attribution}}';
