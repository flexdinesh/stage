SELECT CASE WHEN count(*)=2 THEN 1 ELSE 0 END FROM task_sessions;
SELECT count(*) FROM task_sessions s JOIN tasks t ON t.id=s.task_id
 WHERE s.task_id=1 AND s.attempt=3 AND s.session_id=21 AND s.recorded_at=t.launched_at;
SELECT count(*) FROM task_sessions s JOIN tasks t ON t.id=s.task_id
 WHERE s.task_id=2 AND s.attempt=0 AND s.session_id=22 AND s.recorded_at=t.created_at;
