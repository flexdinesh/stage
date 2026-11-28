-- +goose Up
-- Tree steering delivery (ADR 0013 "Tree finish"): finish_event_id is the
-- binding's tree_finished event (enqueued once); final_state_revision is the
-- steerer task's state revision when the final digest was sent; completion
-- records the one auto-complete decision after it (completed or skipped).
ALTER TABLE tree_steerers ADD COLUMN finish_event_id INTEGER;
ALTER TABLE tree_steerers ADD COLUMN final_state_revision INTEGER;
ALTER TABLE tree_steerers ADD COLUMN completion TEXT CHECK (completion IN ('completed', 'skipped'));
