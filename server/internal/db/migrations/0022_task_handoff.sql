-- +goose Up

-- Run-order handoff (docs/api.md "Handoff"): a linked follower's launch
-- prompt gets a block with its settled predecessor's final reply, PRs,
-- branch/worktree and thread link. handoff toggles it per task (default on).
-- task_handoffs records what each launch attempt sent (status sent) or why it
-- launched without one (status skipped, reason).
ALTER TABLE tasks ADD COLUMN handoff boolean NOT NULL DEFAULT true;
CREATE TABLE task_handoffs (
    id               bigserial PRIMARY KEY,
    task_id          bigint NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    attempt          integer NOT NULL,
    source_task_id   bigint,
    source_thread_id text NOT NULL,
    source_title     text NOT NULL DEFAULT '',
    status           text NOT NULL CHECK (status IN ('sent', 'skipped')),
    text             text NOT NULL DEFAULT '',
    reason           text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, attempt)
);

-- +goose Down
DROP TABLE task_handoffs;
ALTER TABLE tasks DROP COLUMN handoff;
