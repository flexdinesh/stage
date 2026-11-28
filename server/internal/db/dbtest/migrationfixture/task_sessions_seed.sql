INSERT INTO harness_connections(id,harness,machine_id) VALUES(17,'t3',1);
INSERT INTO projects(id,title) VALUES(19,'History');
INSERT INTO session_identities(id,connection_id,external_id) VALUES(21,17,'launched'),(22,17,'adopted');
INSERT INTO tasks(id,project_id,title,prompt,state,connection_id,session_id,launch_attempt,created_at,launched_at)
 VALUES (1,19,'Launched','p','done',17,21,3,'2026-11-29T00:00:00.000000Z','2026-11-29T00:01:00.000000Z'),
        (2,19,'Adopted','','backlog',17,22,0,'2026-11-29T00:02:00.000000Z',NULL),
        (3,19,'Unlaunched','p','backlog',17,NULL,0,'2026-11-29T00:03:00.000000Z',NULL);
