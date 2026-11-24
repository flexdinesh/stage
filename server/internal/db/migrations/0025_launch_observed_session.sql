-- +goose Up
-- Launch confirmation no longer depends on the local machine's T3. Forward-only.
--   threads_parent_session_id_insert/update: dropped. They filed a thread's
--     parent under the local machine's active T3 connection; once that is
--     retired (T3 moved to a worker) the identity's connection_id was NULL and
--     every observation of a thread with a parent aborted. PutObservation now
--     writes parent_session_id on the thread's own connection.
--   thread_project_insert: resolves the project through the thread's own
--     session (its task, or its connection's single binding), not the local
--     machine's T3.
--   task_event_journal: event rows carry the task's own session_id. Before,
--     only source_thread_id was set and the journal trigger below filed it under
--     the local machine's T3, so every state change of a task on another
--     connection (a T3 on a worker) aborted, launch confirmation included.
--   journal_entries_source_session_id_insert/update: skip rows that already
--     carry source_session_id (the task_handoffs pattern).
--   execution_attempts.observed_session: the session a harness returned for an
--     unconfirmed attempt that Stage could not record yet (NULL = none seen):
--     {connectionId, externalSessionId, title, state}.
DROP TRIGGER threads_parent_session_id_insert;
DROP TRIGGER threads_parent_session_id_update;
DROP TRIGGER thread_project_insert;
DROP TRIGGER task_event_journal;
DROP TRIGGER journal_entries_source_session_id_insert;
DROP TRIGGER journal_entries_source_session_id_update;

-- +goose StatementBegin
CREATE TRIGGER task_event_journal AFTER INSERT ON task_events BEGIN
 INSERT INTO journal_entries(kind, task_id, tree_id, source_thread_id, source_session_id, attempt, event_type, text, created_at)
 SELECT 'event', t.id, t.tree_id, t.t3_thread_id, t.session_id, t.launch_attempt,
 CASE WHEN NEW.from_state IS NULL THEN 'task_created' WHEN NEW.from_state <> NEW.to_state THEN 'task_state_changed' ELSE 'task_event' END,
 NEW.detail || CASE WHEN NEW.from_state IS NOT NULL AND NEW.from_state <> NEW.to_state THEN ' (' || NEW.from_state || ' -> ' || NEW.to_state || ')' ELSE '' END, NEW.at
 FROM tasks t WHERE t.id = NEW.task_id;
 UPDATE tasks SET recorded_work = 1 WHERE id = NEW.task_id AND (launch_attempt > 0 OR t3_thread_id IS NOT NULL OR adopted = 1 OR NEW.to_state IN ('launching','running','needs_input','done','settled','failed','interrupted'));
 UPDATE task_trees SET recorded_work = 1 WHERE id = (SELECT tree_id FROM tasks WHERE id = NEW.task_id AND recorded_work = 1);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_insert AFTER INSERT ON journal_entries
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_entries_source_session_id_update AFTER UPDATE OF source_thread_id ON journal_entries
WHEN NEW.source_session_id IS NULL AND NEW.source_thread_id IS NOT NULL AND NEW.source_thread_id<>'' BEGIN
 INSERT INTO session_identities(connection_id,external_id) VALUES((SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1),NEW.source_thread_id) ON CONFLICT DO NOTHING;
 UPDATE journal_entries SET source_session_id=(SELECT id FROM session_identities WHERE connection_id=(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1) AND external_id=NEW.source_thread_id) WHERE rowid=NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER thread_project_insert AFTER INSERT ON threads BEGIN
 UPDATE threads SET stage_project_id=COALESCE(
 (SELECT project_id FROM tasks WHERE session_id=NEW.session_id),
 (SELECT min(project_id) FROM project_bindings WHERE connection_id=(SELECT connection_id FROM session_identities WHERE id=NEW.session_id) AND external_project_id=NEW.t3_project_id AND active=1 HAVING count(*)=1))
 WHERE t3_thread_id=NEW.t3_thread_id;
END;
-- +goose StatementEnd

ALTER TABLE execution_attempts ADD COLUMN observed_session TEXT
    CHECK (observed_session IS NULL OR json_type(observed_session) = 'object');
