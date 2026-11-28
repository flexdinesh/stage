-- +goose Up
-- Notification events by subject: tasks gain `completed` (accepted by
-- inference or an agent), trees `tree_completed`, schedules
-- `schedule_started` and `schedule_skipped`. `done` stays for untracked
-- sessions. Rebuild to widen the event check and add the tree and schedule
-- subjects (expand only). The rebuild keeps 0054's note; legacy_alter_table
-- keeps the recipients' and holds' references to notifications. Saved filters gain completed, tree_completed and
-- schedule_skipped (schedule_started stays opt-in).
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN;

CREATE TABLE notifications_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 event TEXT NOT NULL CONSTRAINT notifications_event_check CHECK (event IN (
  'needs_input','ready','done','failed','completed','tree_completed','schedule_started','schedule_skipped'
 )),
 task_id INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
 title TEXT NOT NULL,
 project_title TEXT,
 url TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now')),
 read_at TEXT,
 session_id INTEGER REFERENCES session_identities(id),
 tree_id INTEGER REFERENCES task_trees (id) ON DELETE SET NULL,
 schedule_id INTEGER REFERENCES schedules (id) ON DELETE SET NULL,
 note TEXT
);
INSERT INTO notifications_new(id,event,task_id,title,project_title,url,created_at,read_at,session_id,note)
 SELECT id,event,task_id,title,project_title,url,created_at,read_at,session_id,note FROM notifications;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('notifications','notifications_new')) WHERE name='notifications_new';
DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;
CREATE INDEX notifications_created_idx ON notifications (created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (id) WHERE read_at IS NULL;

UPDATE notification_preferences AS p
SET notify_events=(
 SELECT json_group_array(value) FROM (
  SELECT value, key AS ord FROM json_each(p.notify_events)
  UNION ALL
  SELECT n.value, 1000 + n.key FROM json_each('["completed","tree_completed","schedule_skipped"]') AS n
  WHERE NOT EXISTS (SELECT 1 FROM json_each(p.notify_events) AS e WHERE e.value = n.value)
  ORDER BY ord
 )
);

COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
