-- +goose Up
-- Worktree as a harness launch option (ADR 0007 decisions 5 and 6), expand.
-- A saved workspace becomes the connection's launch options in
-- execution_config.options, by the connection's harness kind:
--   t3: a worktree -> worktree true, plus its baseRef and branch when set
--     (a predecessor-based worktree keeps its stored fallback baseRef and
--     branch; without a baseRef T3 rejects the launch until one is set).
--   claude, opencode: a worktree (predecessor-based or not) -> worktree true.
--   codex and any other kind: nothing (Codex runs in the project checkout).
-- A config that already sets an option keeps it. Unfinished launch attempts
-- (prepared, unconfirmed) get the same options in their frozen intent, from
-- the workspace the launch resolved, so a recovered launch asks the harness
-- for the same thing. Each task that used a predecessor base gets one journal
-- event saying where its worktree now starts: T3 from its base ref (or that
-- it needs one), Claude Code and OpenCode from the harness default. 0034
-- drops the old columns and task_launch_bases.

UPDATE tasks SET execution_config = json_set(COALESCE(execution_config, '{}'), '$.options',
  json_insert(COALESCE(json_extract(execution_config, '$.options'), '[]'), '$[#]', json_object('id', 'worktree', 'value', json('true'))))
WHERE json_extract(workspace, '$.type') = 'worktree'
  AND (SELECT harness FROM harness_connections WHERE id = tasks.connection_id) IN ('t3', 'claude', 'opencode')
  AND NOT EXISTS (SELECT 1 FROM json_each(tasks.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'worktree');

UPDATE tasks SET execution_config = json_set(execution_config, '$.options',
  json_insert(json_extract(execution_config, '$.options'), '$[#]', json_object('id', 'baseRef', 'value', trim(json_extract(workspace, '$.baseRef')))))
WHERE json_extract(workspace, '$.type') = 'worktree'
  AND trim(COALESCE(json_extract(workspace, '$.baseRef'), '')) <> ''
  AND (SELECT harness FROM harness_connections WHERE id = tasks.connection_id) = 't3'
  AND NOT EXISTS (SELECT 1 FROM json_each(tasks.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'baseRef');

UPDATE tasks SET execution_config = json_set(execution_config, '$.options',
  json_insert(json_extract(execution_config, '$.options'), '$[#]', json_object('id', 'branch', 'value', trim(json_extract(workspace, '$.branch')))))
WHERE json_extract(workspace, '$.type') = 'worktree'
  AND trim(COALESCE(json_extract(workspace, '$.branch'), '')) <> ''
  AND (SELECT harness FROM harness_connections WHERE id = tasks.connection_id) = 't3'
  AND NOT EXISTS (SELECT 1 FROM json_each(tasks.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'branch');

UPDATE schedules SET execution_config = json_set(COALESCE(execution_config, '{}'), '$.options',
  json_insert(COALESCE(json_extract(execution_config, '$.options'), '[]'), '$[#]', json_object('id', 'worktree', 'value', json('true'))))
WHERE json_extract(workspace, '$.type') = 'worktree'
  AND (SELECT harness FROM harness_connections WHERE id = schedules.connection_id) IN ('t3', 'claude', 'opencode')
  AND NOT EXISTS (SELECT 1 FROM json_each(schedules.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'worktree');

UPDATE schedules SET execution_config = json_set(execution_config, '$.options',
  json_insert(json_extract(execution_config, '$.options'), '$[#]', json_object('id', 'baseRef', 'value', trim(json_extract(workspace, '$.baseRef')))))
WHERE json_extract(workspace, '$.type') = 'worktree'
  AND trim(COALESCE(json_extract(workspace, '$.baseRef'), '')) <> ''
  AND (SELECT harness FROM harness_connections WHERE id = schedules.connection_id) = 't3'
  AND NOT EXISTS (SELECT 1 FROM json_each(schedules.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'baseRef');

UPDATE schedules SET execution_config = json_set(execution_config, '$.options',
  json_insert(json_extract(execution_config, '$.options'), '$[#]', json_object('id', 'branch', 'value', trim(json_extract(workspace, '$.branch')))))
WHERE json_extract(workspace, '$.type') = 'worktree'
  AND trim(COALESCE(json_extract(workspace, '$.branch'), '')) <> ''
  AND (SELECT harness FROM harness_connections WHERE id = schedules.connection_id) = 't3'
  AND NOT EXISTS (SELECT 1 FROM json_each(schedules.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'branch');

UPDATE execution_attempts SET intent = json_set(intent, '$.config.options',
  json_insert(COALESCE(json_extract(intent, '$.config.options'), '[]'), '$[#]', json_object('id', 'worktree', 'value', json('true'))))
WHERE phase IN ('prepared', 'unconfirmed') AND json_extract(intent, '$.workspace.kind') = 'worktree'
  AND (SELECT harness FROM harness_connections WHERE id = json_extract(execution_attempts.intent, '$.connectionId')) IN ('t3', 'claude', 'opencode')
  AND NOT EXISTS (SELECT 1 FROM json_each(execution_attempts.intent, '$.config.options') WHERE json_extract(value, '$.id') = 'worktree');

UPDATE execution_attempts SET intent = json_set(intent, '$.config.options',
  json_insert(json_extract(intent, '$.config.options'), '$[#]', json_object('id', 'baseRef', 'value', trim(json_extract(intent, '$.workspace.base')))))
WHERE phase IN ('prepared', 'unconfirmed') AND json_extract(intent, '$.workspace.kind') = 'worktree'
  AND trim(COALESCE(json_extract(intent, '$.workspace.base'), '')) <> ''
  AND (SELECT harness FROM harness_connections WHERE id = json_extract(execution_attempts.intent, '$.connectionId')) = 't3'
  AND NOT EXISTS (SELECT 1 FROM json_each(execution_attempts.intent, '$.config.options') WHERE json_extract(value, '$.id') = 'baseRef');

UPDATE execution_attempts SET intent = json_set(intent, '$.config.options',
  json_insert(json_extract(intent, '$.config.options'), '$[#]', json_object('id', 'branch', 'value', trim(json_extract(intent, '$.workspace.branch')))))
WHERE phase IN ('prepared', 'unconfirmed') AND json_extract(intent, '$.workspace.kind') = 'worktree'
  AND trim(COALESCE(json_extract(intent, '$.workspace.branch'), '')) <> ''
  AND (SELECT harness FROM harness_connections WHERE id = json_extract(execution_attempts.intent, '$.connectionId')) = 't3'
  AND NOT EXISTS (SELECT 1 FROM json_each(execution_attempts.intent, '$.config.options') WHERE json_extract(value, '$.id') = 'branch');

-- A task event in its journal, as Go writes one (task_event, the task's tree,
-- session and attempt; default user, no source machine), naming where the
-- worktree now starts.
INSERT INTO journal_entries(kind, task_id, tree_id, source_session_id, attempt, event_type, text)
SELECT 'event', id, tree_id, session_id, launch_attempt, 'task_event',
  CASE
    WHEN (SELECT harness FROM harness_connections WHERE id = tasks.connection_id) = 't3' AND COALESCE((SELECT trim(json_extract(value, '$.value')) FROM json_each(tasks.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'baseRef' LIMIT 1), '') <> '' THEN
      'Predecessor bases were removed: this task now starts its worktree from ' || (SELECT trim(json_extract(value, '$.value')) FROM json_each(tasks.execution_config, '$.options') WHERE json_extract(value, '$.id') = 'baseRef' LIMIT 1) || ', not its predecessor''s branch. The handoff carries the branch the predecessor reported.'
    WHEN (SELECT harness FROM harness_connections WHERE id = tasks.connection_id) = 't3' THEN
      'Predecessor bases were removed: this task no longer starts from its predecessor''s branch. Set a base ref before it starts (T3 needs one for a new worktree). The handoff carries the branch the predecessor reported.'
    ELSE
      'Predecessor bases were removed: this task no longer starts from its predecessor''s branch. Its worktree now starts from the harness default. The handoff carries the branch the predecessor reported.'
  END
FROM tasks WHERE json_extract(workspace, '$.type') = 'worktree' AND json_extract(workspace, '$.base') = 'predecessor'
  -- only work that can still start: finished or archived history keeps its record
  AND state IN ('backlog', 'failed', 'interrupted', 'cancelled') AND archived_at IS NULL
ORDER BY id;
