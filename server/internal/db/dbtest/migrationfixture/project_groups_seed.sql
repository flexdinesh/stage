INSERT INTO users(id,display_name,status) VALUES(7,'Former member','disabled');
INSERT INTO machines(id,label) VALUES(2,'Worker') ON CONFLICT(id) DO NOTHING;
INSERT INTO projects(id,title,created_by_user_id,source_machine_id,archived_at) VALUES
 (4,'App',7,2,NULL),(5,'App',1,NULL,'2026-11-27T00:00:00.000000Z');
INSERT INTO project_aliases(alias_id,project_id,title,merged_by_user_id) VALUES(39,4,'Earlier App',7);
INSERT INTO harness_connections(id,machine_id,harness,active,retired_at) VALUES
 (4,1,'t3',true,NULL),(5,2,'t3',false,'2026-11-27T00:00:00.000000Z'),(7,2,'t3',true,NULL);
INSERT INTO external_projects(connection_id,external_id,title,workspace,available,repo,repo_path,evidence_source,evidence_at) VALUES
 (4,'root','App','/root',true,'example.test/org/app','.','harness','2026-11-27T00:00:00Z'),
 (5,'clone','App','/clone',true,'example.test/org/app','.','harness','2026-11-27T00:00:00Z'),
 (5,'duplicate','App','/other',true,'example.test/org/app','.','harness','2026-11-27T00:00:00Z'),
 (5,'unknown','App','/nested',true,'example.test/org/app','','harness','2026-11-27T00:00:00Z'),
 (7,'clone','App','/clone',true,'example.test/org/app','.','harness','2026-11-27T00:00:00Z');
INSERT INTO external_projects(connection_id,external_id,title,workspace,available,historical_project_id)
 VALUES (4,'historical','Historical Workspace','/history',true,5);
UPDATE external_projects SET historical_project_id=5 WHERE external_id='root';
INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,rule,is_primary,revision) VALUES
 (8,4,4,'root','/root','manual',true,1),(9,4,5,'clone','/clone','remote',true,4),
 (10,5,5,'duplicate','/other','new',true,1),(11,4,5,'unknown','/nested','remote',false,1);
INSERT INTO session_identities(id,connection_id,external_id) VALUES (3,5,'old-session');
INSERT INTO schedules(id,project_id,title,prompt,cron,tz,connection_id,binding_id,created_by_user_id,source_machine_id) VALUES
 (6,4,'Local schedule','prompt','@daily','UTC',4,NULL,7,2),
 (7,4,'Worker schedule','prompt','@daily','UTC',5,9,1,NULL);
INSERT INTO tasks(id,project_id,title,prompt,connection_id,binding_id,created_by_user_id,source_machine_id,state,launch_attempt,session_id,archived_at) VALUES
 (6,4,'Local backlog','prompt',4,8,7,2,'backlog',0,NULL,NULL),
 (7,4,'Worker backlog','prompt',5,NULL,1,NULL,'backlog',0,NULL,NULL),
 (8,4,'In-flight launch','current prompt',5,9,1,2,'launching',1,NULL,NULL),
 (9,5,'Archived result','prompt',5,10,1,2,'done',1,3,'2026-11-27T00:00:00.000000Z');
INSERT INTO task_launch_claims(task_id,attempt) VALUES (8,1);
INSERT INTO task_launch_destinations(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision)
 VALUES (8,1,5,'clone','/original-clone',9,3);
INSERT INTO execution_attempts(task_id,attempt,intent,checkpoint,phase,revision,retry_count,attribution) VALUES
 (8,1,'{"requestId":"old-launch","connectionId":5,"project":"clone","directory":"/original-clone","title":"Frozen title","prompt":"Frozen prompt ✓","config":{"model":"old-model"}}',
 '{"version":1,"data":{"sessionId":"pending-session"}}','unconfirmed',2,1,'{{attribution}}');
INSERT INTO snippets(id,name,body,project_id) VALUES (12,'snippet','Keep scope',4),(13,'archived snippet','Keep old scope',5);
INSERT INTO task_events(task_id,to_state,detail,attribution) VALUES
 (9,'backlog','historical',NULL),(9,'done','accepted','{{attribution}}');
INSERT INTO journal_entries(kind,task_id,text,created_by_user_id,source_machine_id,request_id,attribution)
 VALUES('note',9,'old note ✓',7,2,'historical',NULL),('note',9,'authored note ✓',7,2,'authored','{{attribution}}');
INSERT INTO task_acceptances(task_id,provenance,evidence,note,source_session_id,attribution)
 VALUES(9,'historical_settlement','{{evidence}}','historical',3,NULL),(9,'user','{{evidence}}','authored',3,'{{attribution}}');
