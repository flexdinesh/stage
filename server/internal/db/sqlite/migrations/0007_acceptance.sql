-- +goose Up
-- Acceptance is Stage-owned. Execution and all historical records remain intact.
ALTER TABLE tasks ADD COLUMN acceptance_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN acceptance_id INTEGER;
CREATE TABLE task_acceptances (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 accepted_at TEXT NOT NULL DEFAULT (stage_now()),
 captured_at TEXT NOT NULL DEFAULT (stage_now()),
 provenance TEXT NOT NULL CHECK (provenance IN ('user','historical_settlement')),
 evidence TEXT NOT NULL CHECK (json_valid(evidence)),
 note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX task_acceptances_task ON task_acceptances(task_id, id);
CREATE TABLE acceptance_requests (
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 request_id TEXT NOT NULL,
 input TEXT NOT NULL,
 result TEXT NOT NULL,
 PRIMARY KEY(task_id, request_id)
);
CREATE TABLE task_launch_claims (
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 attempt INTEGER NOT NULL,
 PRIMARY KEY(task_id, attempt)
);
CREATE TABLE task_launch_inputs (
 task_id INTEGER NOT NULL,
 attempt INTEGER NOT NULL,
 source_task_id INTEGER NOT NULL,
 acceptance_id INTEGER NOT NULL REFERENCES task_acceptances(id),
 PRIMARY KEY(task_id, attempt, source_task_id),
 FOREIGN KEY(task_id, attempt) REFERENCES task_launch_claims(task_id, attempt)
);

INSERT INTO task_acceptances(task_id, accepted_at, provenance, evidence)
 SELECT t.id, COALESCE(t.settled_at, stage_now()), 'historical_settlement',
 json_object('taskId',t.id,'title',t.title,'treeId',t.tree_id,'treeTitle',tr.title,
 'threadId',t.t3_thread_id,'threadLink',th.link,'projectId',t.project_id,
 'branch',th.branch,'worktreePath',th.worktree_path,
 'executionState',t.state,'runId',th.latest_run_id,'replyRunId',th.last_word_run_id,
 'replyItemId',th.last_word_item_id,'reply',substr(th.last_word_text,1,4000),
 'replyTruncated',json(CASE WHEN th.last_word_truncated = 1 OR length(th.last_word_text)>4000 THEN 'true' ELSE 'false' END),
 'replyAt',th.last_word_at,'observedAt',th.updated_at,'historicalSettledAt',t.settled_at,
 'artifacts',json(COALESCE(th.pull_requests,'[]')),
 'journalThroughId',COALESCE((SELECT max(j.id) FROM journal_entries j WHERE j.task_id=t.id OR j.tree_id=t.tree_id),0))
 FROM tasks t LEFT JOIN threads th ON th.t3_thread_id=t.t3_thread_id
 LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE t.state='settled';
UPDATE tasks SET acceptance_id=(SELECT id FROM task_acceptances a WHERE a.task_id=tasks.id),
 acceptance_revision=1 WHERE state='settled';
UPDATE tasks SET recorded_work=1 WHERE acceptance_id IS NOT NULL;
UPDATE task_trees SET recorded_work=1 WHERE EXISTS (SELECT 1 FROM tasks t WHERE t.tree_id=task_trees.id AND t.acceptance_id IS NOT NULL);
INSERT INTO journal_entries(kind,task_id,tree_id,event_type,text)
 SELECT 'event',id,tree_id,'task_completed','Migrated current T3 settlement as historical acceptance; evidence captured from local cache.' FROM tasks WHERE acceptance_id IS NOT NULL;

-- Existing attempts retain their original recorded handoffs and launch identity.
INSERT INTO task_launch_claims SELECT id,launch_attempt FROM tasks WHERE launch_attempt>0;
INSERT INTO task_launch_inputs
 SELECT t.id,t.launch_attempt,d.id,d.acceptance_id FROM tasks t
 JOIN task_needs n ON n.task_id=t.id JOIN tasks d ON d.id=n.need_id
 WHERE t.launch_attempt>0 AND d.acceptance_id IS NOT NULL;
