-- +goose Up
-- Notification events by subject: tasks gain `completed` (accepted by
-- inference or an agent), trees `tree_completed`, schedules
-- `schedule_started` and `schedule_skipped`. `done` stays for untracked
-- sessions. Widen the event check and add the tree and schedule subjects
-- (expand only). Saved filters gain completed, tree_completed and
-- schedule_skipped (schedule_started stays opt-in).
ALTER TABLE notifications DROP CONSTRAINT notifications_event_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_event_check
 CHECK (event IN ('needs_input','ready','done','failed','completed','tree_completed','schedule_started','schedule_skipped'));
ALTER TABLE notifications ADD COLUMN tree_id bigint REFERENCES task_trees(id) ON DELETE SET NULL;
ALTER TABLE notifications ADD COLUMN schedule_id bigint REFERENCES schedules(id) ON DELETE SET NULL;

UPDATE notification_preferences AS p
SET notify_events = p.notify_events || COALESCE((
 SELECT jsonb_agg(n.value ORDER BY n.ord)
 FROM jsonb_array_elements_text('["completed","tree_completed","schedule_skipped"]'::jsonb) WITH ORDINALITY AS n(value, ord)
 WHERE NOT p.notify_events ? n.value
), '[]'::jsonb);
