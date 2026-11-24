-- +goose Up
-- Business rules leave the triggers (docs/storage.md "Journal events"). Forward-only.
--   Journal events, recorded work, membership revisions and archiving an
--   emptied tree: internal/db writes them in the same Write as their cause
--   (journal_events.go), with the triggers' attribution (default user, no
--   source machine).
--   thread_project_insert: mirroring a new T3 thread sets stage_project_id.
--   acceptance_session_insert: an acceptance is inserted with the task's session.
--   task_handoffs_source_session_id_*: a handoff resolves its source session
--     (evidence, else the legacy thread id under the local active T3, else none).
-- The other compatibility triggers stay until #417.
DROP TRIGGER task_event_journal;
DROP TRIGGER journal_note_protection;
DROP TRIGGER journal_tree_created;
DROP TRIGGER journal_tree_config;
DROP TRIGGER journal_tree_status;
DROP TRIGGER journal_membership;
DROP TRIGGER journal_need_added;
DROP TRIGGER journal_need_removed;
DROP TRIGGER journal_member_created;
DROP TRIGGER thread_project_insert;
DROP TRIGGER acceptance_session_insert;
DROP TRIGGER task_handoffs_source_session_id_insert;
DROP TRIGGER task_handoffs_source_session_id_update;
