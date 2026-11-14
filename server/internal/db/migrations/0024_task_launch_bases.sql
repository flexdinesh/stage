-- +goose Up

-- Predecessor base (docs/api.md "Predecessor base"): a linked follower with
-- workspace.base "predecessor" starts its worktree from its predecessor's
-- branch, or from its own baseRef when that branch is unusable. Each launch
-- attempt records the base it used (source predecessor) or the fallback and
-- why (source fallback, reason).
CREATE TABLE task_launch_bases (
    id                bigserial PRIMARY KEY,
    task_id           bigint NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    attempt           integer NOT NULL,
    base_ref          text NOT NULL,
    start_from_origin boolean NOT NULL,
    source            text NOT NULL CHECK (source IN ('predecessor', 'fallback')),
    source_task_id    bigint,
    source_thread_id  text,
    source_title      text NOT NULL DEFAULT '',
    reason            text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, attempt)
);

-- +goose Down
DROP TABLE task_launch_bases;
