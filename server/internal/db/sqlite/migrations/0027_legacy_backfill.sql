-- +goose Up
-- Expand step of the #402 legacy drop (docs/storage.md "Legacy columns and
-- compatibility triggers"): fill the neutral columns from the legacy ones,
-- so 0028 can drop them. SQLite-only history below the Postgres baseline,
-- so plain SQL rather than a backfill.Steps Go migration (docs/storage.md
-- "Migrations").
--
-- Sessions. A legacy id resolves to a connection, first match wins:
--   1. the row's own connection: a task's connection_id, a notification's
--      task's connection;
--   2. an existing identity with that external id on a T3 connection, the
--      local machine's active T3 first (covers a retired connection);
--   3. the local machine's active T3, as the old compatibility triggers did.
-- Then the identity is ensured and set. A legacy id that resolves to no
-- connection, or a task session another task already holds, stays NULL
-- (the table: "a legacy id the backfill cannot resolve becomes NULL").
CREATE TEMP TABLE legacy_session (
 kind TEXT NOT NULL,
 row_id INTEGER NOT NULL,
 external_id TEXT NOT NULL,
 connection_id INTEGER
);

INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'task',id,t3_thread_id,connection_id FROM tasks
 WHERE session_id IS NULL AND COALESCE(t3_thread_id,'')<>'';
INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'task_origin',id,origin_thread_id,NULL FROM tasks
 WHERE origin_session_id IS NULL AND COALESCE(origin_thread_id,'')<>'';
INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'schedule_origin',id,origin_thread_id,NULL FROM schedules
 WHERE origin_session_id IS NULL AND COALESCE(origin_thread_id,'')<>'';
INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'notification',n.id,n.t3_thread_id,t.connection_id FROM notifications n LEFT JOIN tasks t ON t.id=n.task_id
 WHERE n.session_id IS NULL AND COALESCE(n.t3_thread_id,'')<>'';
INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'handoff',id,source_thread_id,NULL FROM task_handoffs
 WHERE source_session_id IS NULL AND COALESCE(source_thread_id,'')<>'';
INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'launch_base',id,source_thread_id,NULL FROM task_launch_bases
 WHERE source_session_id IS NULL AND COALESCE(source_thread_id,'')<>'';
INSERT INTO legacy_session(kind,row_id,external_id,connection_id)
 SELECT 'journal',id,source_thread_id,NULL FROM journal_entries
 WHERE source_session_id IS NULL AND COALESCE(source_thread_id,'')<>'';

UPDATE legacy_session SET connection_id=COALESCE(
 (SELECT si.connection_id FROM session_identities si JOIN harness_connections hc ON hc.id=si.connection_id
  WHERE hc.harness='t3' AND si.external_id=legacy_session.external_id
  ORDER BY (hc.machine_id=1 AND hc.active=1) DESC, si.id LIMIT 1),
 (SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1))
WHERE connection_id IS NULL;

INSERT INTO session_identities(connection_id,external_id)
 SELECT DISTINCT connection_id,external_id FROM legacy_session WHERE connection_id IS NOT NULL
 ON CONFLICT DO NOTHING;

CREATE TEMP VIEW legacy_resolved AS
 SELECT l.kind,l.row_id,si.id AS session_id FROM legacy_session l
 JOIN session_identities si ON si.connection_id=l.connection_id AND si.external_id=l.external_id;

UPDATE tasks SET session_id=(SELECT session_id FROM legacy_resolved WHERE kind='task' AND row_id=tasks.id)
WHERE id IN (SELECT row_id FROM legacy_resolved r WHERE kind='task'
 AND NOT EXISTS (SELECT 1 FROM tasks o WHERE o.session_id=r.session_id));
UPDATE tasks SET origin_session_id=(SELECT session_id FROM legacy_resolved WHERE kind='task_origin' AND row_id=tasks.id)
WHERE id IN (SELECT row_id FROM legacy_resolved WHERE kind='task_origin');
UPDATE schedules SET origin_session_id=(SELECT session_id FROM legacy_resolved WHERE kind='schedule_origin' AND row_id=schedules.id)
WHERE id IN (SELECT row_id FROM legacy_resolved WHERE kind='schedule_origin');
UPDATE notifications SET session_id=(SELECT session_id FROM legacy_resolved WHERE kind='notification' AND row_id=notifications.id)
WHERE id IN (SELECT row_id FROM legacy_resolved WHERE kind='notification');
UPDATE task_handoffs SET source_session_id=(SELECT session_id FROM legacy_resolved WHERE kind='handoff' AND row_id=task_handoffs.id)
WHERE id IN (SELECT row_id FROM legacy_resolved WHERE kind='handoff');
UPDATE task_launch_bases SET source_session_id=(SELECT session_id FROM legacy_resolved WHERE kind='launch_base' AND row_id=task_launch_bases.id)
WHERE id IN (SELECT row_id FROM legacy_resolved WHERE kind='launch_base');
UPDATE journal_entries SET source_session_id=(SELECT session_id FROM legacy_resolved WHERE kind='journal' AND row_id=journal_entries.id)
WHERE id IN (SELECT row_id FROM legacy_resolved WHERE kind='journal');

DROP VIEW legacy_resolved;
DROP TABLE legacy_session;

-- Execution config. A T3 task or schedule saved before executionConfig gets
-- the one its legacy model columns mean (store.LegacyT3Config): model is the
-- T3 model key ["provider","model"] when both are set, with Go's JSON string
-- escaping (<, >, &, U+2028, U+2029), else "".
UPDATE tasks SET execution_config=json_object(
 'model', CASE WHEN COALESCE(provider_instance_id,'')<>'' AND COALESCE(model,'')<>'' THEN
  replace(replace(replace(replace(replace(json_array(provider_instance_id,model),
   '<','\u003c'),'>','\u003e'),'&','\u0026'),char(8232),'\u2028'),char(8233),'\u2029') ELSE '' END,
 'options', json(model_options), 'permission', runtime_mode, 'mode', interaction_mode)
WHERE execution_config IS NULL AND connection_id IN (SELECT id FROM harness_connections WHERE harness='t3');
UPDATE schedules SET execution_config=json_object(
 'model', CASE WHEN COALESCE(provider_instance_id,'')<>'' AND COALESCE(model,'')<>'' THEN
  replace(replace(replace(replace(replace(json_array(provider_instance_id,model),
   '<','\u003c'),'>','\u003e'),'&','\u0026'),char(8232),'\u2028'),char(8233),'\u2029') ELSE '' END,
 'options', json(model_options), 'permission', runtime_mode, 'mode', interaction_mode)
WHERE execution_config IS NULL AND connection_id IN (SELECT id FROM harness_connections WHERE harness='t3');
