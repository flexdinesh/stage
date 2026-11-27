INSERT INTO users(id,display_name,status) VALUES(7,'Former member','disabled');
INSERT INTO machines(id,label) VALUES(2,'Worker');
INSERT INTO projects(id,title,created_by_user_id,source_machine_id) VALUES(4,'App',7,2),(5,'App',7,2);
INSERT INTO project_aliases(alias_id,project_id,title,merged_by_user_id) VALUES(39,4,'Earlier App',7);
INSERT INTO harness_connections(id,machine_id,harness,active,retired_at)
 VALUES(4,1,'t3',true,NULL),(5,2,'t3',false,'2026-11-27T00:00:00Z');
INSERT INTO external_projects(connection_id,external_id,title,workspace,available,repo,repo_path,evidence_source,evidence_at) VALUES
 (4,'root','App','/root',true,'example.test/Org/App','.','harness','2026-11-27T00:00:00Z'),
 (5,'clone','App','/clone',true,'example.test/Org/App','.','harness','2026-11-27T00:00:00Z'),
 (5,'duplicate','App','/other',true,'example.test/Org/App','.','harness','2026-11-27T00:00:00Z'),
 (5,'unknown','App','/nested',true,'example.test/Org/App','','harness','2026-11-27T00:00:00Z');
INSERT INTO external_projects(connection_id,external_id,title,workspace,available,historical_project_id)
 VALUES (4,'historical','Historical Workspace','/history',true,5);
UPDATE external_projects SET historical_project_id=5 WHERE external_id='root';
INSERT INTO project_bindings(id,project_id,connection_id,external_project_id,workspace,rule) VALUES
 (8,4,4,'root','/root','manual'),(9,4,5,'clone','/clone','remote'),
 (10,5,5,'duplicate','/other','new'),(11,4,5,'unknown','/nested','remote');
INSERT INTO tasks(id,project_id,title,prompt,connection_id,binding_id,created_by_user_id,source_machine_id,workspace_pin)
 VALUES(6,4,'Task','prompt',4,8,7,2,'{"externalId":"root","root":"/root"}');
INSERT INTO snippets(id,name,body,project_id) VALUES(12,'snippet','Keep scope',4);
INSERT INTO task_events(task_id,to_state,detail,attribution) VALUES
 (6,'backlog','historical',NULL),(6,'done','accepted','{{attribution}}');
INSERT INTO journal_entries(kind,task_id,text,created_by_user_id,source_machine_id,request_id,attribution)
 VALUES('note',6,'old note ✓',7,2,'historical',NULL),('note',6,'authored note ✓',7,2,'authored','{{attribution}}');
INSERT INTO task_acceptances(task_id,provenance,evidence,note,attribution)
 VALUES(6,'historical_settlement','{{evidence}}','historical',NULL),(6,'user','{{evidence}}','authored','{{attribution}}');
