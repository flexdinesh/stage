-- +goose Up
-- Acceptance alone records completion. Preserve its snapshots and requests.
UPDATE tasks SET state='done', finished_at=COALESCE(finished_at, settled_at) WHERE state='settled';
UPDATE task_events SET to_state='done' WHERE to_state='settled';
UPDATE task_events SET from_state='done' WHERE from_state='settled';
UPDATE notifications SET event='done' WHERE event='settled';

UPDATE preferences p
SET notify_events=COALESCE((
 SELECT jsonb_agg(e ORDER BY ord)
 FROM jsonb_array_elements(p.notify_events) WITH ORDINALITY AS events(e, ord)
 WHERE e <> '"settled"'::jsonb
), '[]'::jsonb);
ALTER TABLE preferences ALTER COLUMN notify_events SET DEFAULT '["needs_input","failed","done"]';

ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check
 CHECK (state IN ('backlog','launching','running','needs_input','done','failed','interrupted','cancelled'));
ALTER TABLE notifications DROP CONSTRAINT notifications_event_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_event_check
 CHECK (event IN ('needs_input','ready','done','failed'));
ALTER TABLE tasks DROP COLUMN settled_at;
