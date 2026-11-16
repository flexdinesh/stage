-- +goose Up
-- Forward only. Drop old task notes deliberately; preserve task_events and
-- all referenced task rows. No historical events are inferred or backfilled.
ALTER TABLE tasks DROP COLUMN notes;
ALTER TABLE tasks ADD COLUMN recorded_work INTEGER NOT NULL DEFAULT 0 CHECK (recorded_work IN (0,1));
ALTER TABLE task_trees ADD COLUMN recorded_work INTEGER NOT NULL DEFAULT 0 CHECK (recorded_work IN (0,1));
ALTER TABLE task_trees ADD COLUMN membership_revision INTEGER NOT NULL DEFAULT 0;

CREATE TABLE journal_entries (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL CHECK (kind IN ('note','event')),
 task_id INTEGER,
 tree_id INTEGER,
 source_task_id INTEGER,
 source_thread_id TEXT,
 attempt INTEGER,
 category TEXT NOT NULL DEFAULT '' CHECK (category IN ('','finding','decision','blocker','outcome')),
 text TEXT NOT NULL DEFAULT '',
 event_type TEXT NOT NULL DEFAULT '',
 from_tree_id INTEGER,
 to_tree_id INTEGER,
 request_id TEXT,
 created_at TEXT NOT NULL DEFAULT (stage_now()),
 CHECK (task_id IS NOT NULL OR tree_id IS NOT NULL),
 CHECK (kind <> 'note' OR ((task_id IS NULL) <> (tree_id IS NULL) AND request_id IS NOT NULL AND event_type = ''))
);
-- Journal references are immutable provenance, not cascading foreign keys.
CREATE UNIQUE INDEX journal_task_request ON journal_entries(task_id, request_id) WHERE kind = 'note' AND task_id IS NOT NULL;
CREATE UNIQUE INDEX journal_tree_request ON journal_entries(tree_id, request_id) WHERE kind = 'note' AND tree_id IS NOT NULL;
CREATE INDEX journal_task_read ON journal_entries(task_id, kind, id DESC);
CREATE INDEX journal_tree_read ON journal_entries(tree_id, kind, id DESC);
CREATE INDEX journal_source_read ON journal_entries(tree_id, source_task_id, id DESC);
CREATE INDEX journal_task_category ON journal_entries(task_id, category, id DESC);
CREATE INDEX journal_tree_category ON journal_entries(tree_id, category, id DESC);
CREATE TABLE journal_launch_context (
 task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 attempt INTEGER NOT NULL,
 instructions TEXT NOT NULL,
 PRIMARY KEY (task_id, attempt)
);

UPDATE tasks SET recorded_work = 1 WHERE launch_attempt > 0 OR t3_thread_id IS NOT NULL OR adopted = 1
 OR EXISTS (SELECT 1 FROM task_events e WHERE e.task_id = tasks.id AND e.to_state IN ('launching','running','needs_input','done','settled','failed','interrupted'));
UPDATE task_trees SET recorded_work = 1 WHERE EXISTS (SELECT 1 FROM tasks t WHERE t.tree_id = task_trees.id AND t.recorded_work = 1);
INSERT INTO journal_entries(kind, task_id, event_type, text)
 SELECT 'event', id, 'journal_history_started', 'Journal history starts here; earlier task events remain in task history.' FROM tasks;
INSERT INTO journal_entries(kind, tree_id, event_type, text)
 SELECT 'event', id, 'journal_history_started', 'Tree journal history starts here; earlier membership history was not recorded.' FROM task_trees;

-- All task-event producers share this transactional journal write.
-- +goose StatementBegin
CREATE TRIGGER task_event_journal AFTER INSERT ON task_events BEGIN
 INSERT INTO journal_entries(kind, task_id, tree_id, source_thread_id, attempt, event_type, text, created_at)
 SELECT 'event', t.id, t.tree_id, t.t3_thread_id, t.launch_attempt,
 CASE WHEN NEW.from_state IS NULL THEN 'task_created' WHEN NEW.from_state <> NEW.to_state THEN 'task_state_changed' ELSE 'task_event' END,
 NEW.detail || CASE WHEN NEW.from_state IS NOT NULL AND NEW.from_state <> NEW.to_state THEN ' (' || NEW.from_state || ' -> ' || NEW.to_state || ')' ELSE '' END, NEW.at
 FROM tasks t WHERE t.id = NEW.task_id;
 UPDATE tasks SET recorded_work = 1 WHERE id = NEW.task_id AND (launch_attempt > 0 OR t3_thread_id IS NOT NULL OR adopted = 1 OR NEW.to_state IN ('launching','running','needs_input','done','settled','failed','interrupted'));
 UPDATE task_trees SET recorded_work = 1 WHERE id = (SELECT tree_id FROM tasks WHERE id = NEW.task_id AND recorded_work = 1);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_note_protection AFTER INSERT ON journal_entries WHEN NEW.kind = 'note' BEGIN
 UPDATE tasks SET recorded_work = 1 WHERE id = NEW.task_id;
 UPDATE task_trees SET recorded_work = 1, updated_at = NEW.created_at
 WHERE id = NEW.tree_id OR id = (SELECT tree_id FROM tasks WHERE id = NEW.task_id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_tree_created AFTER INSERT ON task_trees BEGIN
 INSERT INTO journal_entries(kind, tree_id, event_type, text) VALUES ('event', NEW.id, 'tree_created', NEW.title);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_tree_config AFTER UPDATE OF title, start_mode ON task_trees
WHEN OLD.title <> NEW.title OR OLD.start_mode <> NEW.start_mode BEGIN
 INSERT INTO journal_entries(kind, tree_id, event_type, text)
 VALUES ('event', NEW.id, 'tree_config_changed', 'Title: ' || OLD.title || ' -> ' || NEW.title || '; start mode: ' || OLD.start_mode || ' -> ' || NEW.start_mode);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_tree_status AFTER UPDATE OF status ON task_trees WHEN OLD.status <> NEW.status BEGIN
 INSERT INTO journal_entries(kind, tree_id, event_type, text) VALUES ('event', NEW.id, 'tree_status_changed', OLD.status || ' -> ' || NEW.status);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_membership AFTER UPDATE OF tree_id ON tasks WHEN OLD.tree_id IS NOT NEW.tree_id BEGIN
 INSERT INTO journal_entries(kind, tree_id, source_task_id, event_type, text, from_tree_id, to_tree_id)
 SELECT 'event', OLD.tree_id, NEW.id, 'task_moved', NEW.title, OLD.tree_id, NEW.tree_id WHERE OLD.tree_id IS NOT NULL;
 INSERT INTO journal_entries(kind, tree_id, source_task_id, event_type, text, from_tree_id, to_tree_id)
 SELECT 'event', NEW.tree_id, NEW.id, 'task_moved', NEW.title, OLD.tree_id, NEW.tree_id WHERE NEW.tree_id IS NOT NULL;
 UPDATE task_trees SET membership_revision = membership_revision + 1, updated_at = stage_now(),
 recorded_work = max(recorded_work, NEW.recorded_work)
 WHERE id IN (OLD.tree_id, NEW.tree_id);
 UPDATE task_trees SET status = 'archived', closed_at = stage_now()
 WHERE id = OLD.tree_id AND NOT EXISTS (SELECT 1 FROM tasks WHERE tree_id = OLD.tree_id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_need_added AFTER INSERT ON task_needs BEGIN
 INSERT INTO journal_entries(kind, tree_id, source_task_id, event_type, text)
 SELECT 'event', tree_id, NEW.task_id, 'needs_changed', 'Added need #' || NEW.need_id FROM tasks WHERE id = NEW.task_id AND tree_id IS NOT NULL;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_need_removed AFTER DELETE ON task_needs BEGIN
 INSERT INTO journal_entries(kind, tree_id, source_task_id, event_type, text)
 SELECT 'event', tree_id, OLD.task_id, 'needs_changed', 'Removed need #' || OLD.need_id FROM tasks WHERE id = OLD.task_id AND tree_id IS NOT NULL;
END;
-- +goose StatementEnd

-- Direct inserts into a tree obey the same membership revision invariant.
-- Normal Store creation inserts standalone, then uses joinTree.
-- +goose StatementBegin
CREATE TRIGGER journal_member_created AFTER INSERT ON tasks WHEN NEW.tree_id IS NOT NULL BEGIN
 INSERT INTO journal_entries(kind, tree_id, source_task_id, event_type, text, to_tree_id)
 VALUES ('event', NEW.tree_id, NEW.id, 'task_moved', NEW.title, NEW.tree_id);
 UPDATE task_trees SET membership_revision = membership_revision + 1, updated_at = stage_now(),
 recorded_work = max(recorded_work, NEW.recorded_work, NEW.launch_attempt > 0, NEW.t3_thread_id IS NOT NULL, NEW.adopted)
 WHERE id = NEW.tree_id;
END;
-- +goose StatementEnd
