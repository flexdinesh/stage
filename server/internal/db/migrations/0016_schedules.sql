-- +goose Up

-- Recurring task templates: on each cron fire (evaluated in tz) a schedule
-- spawns a task with its config and images (docs/api.md "Schedule rules").
CREATE TABLE schedules (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id           bigint NOT NULL REFERENCES projects (id),
    title                text NOT NULL,
    prompt               text NOT NULL,
    provider_instance_id text,
    model                text,
    model_options        jsonb NOT NULL DEFAULT '[]',
    runtime_mode         text NOT NULL DEFAULT 'full-access'
        CHECK (runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')),
    interaction_mode     text NOT NULL DEFAULT 'default' CHECK (interaction_mode IN ('default', 'plan')),
    workspace            jsonb NOT NULL DEFAULT '{"type":"root"}',
    tags                 text[] NOT NULL DEFAULT '{}',
    notes                text NOT NULL DEFAULT '',
    cron                 text NOT NULL,
    tz                   text NOT NULL,
    paused               boolean NOT NULL DEFAULT false,
    -- Next fire (UTC); NULL while paused.
    next_run_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE NOT paused;
CREATE INDEX schedules_project_idx ON schedules (project_id);

-- One row per fire: started (task spawned), skipped (previous run active),
-- missed (server down across the fire time, one row per outage), manual (Run now).
CREATE TABLE schedule_runs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    schedule_id bigint NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
    fire_at     timestamptz NOT NULL,
    outcome     text NOT NULL CHECK (outcome IN ('started', 'skipped', 'missed', 'manual')),
    task_id     bigint REFERENCES tasks (id) ON DELETE SET NULL,
    detail      text NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedule_runs_schedule_idx ON schedule_runs (schedule_id, id DESC);

-- One-shot start (start_at, entered in start_tz) of a backlog task, and the
-- schedule that spawned a task.
ALTER TABLE tasks
    ADD COLUMN start_at timestamptz,
    ADD COLUMN start_tz text,
    ADD COLUMN schedule_id bigint REFERENCES schedules (id) ON DELETE SET NULL,
    ADD CONSTRAINT tasks_start_pair CHECK ((start_at IS NULL) = (start_tz IS NULL)),
    ADD CONSTRAINT tasks_start_or_after CHECK (start_at IS NULL OR after_task_id IS NULL);
CREATE INDEX tasks_due_start_idx ON tasks (start_at) WHERE state = 'backlog' AND start_at IS NOT NULL;
CREATE INDEX tasks_schedule_idx ON tasks (schedule_id) WHERE schedule_id IS NOT NULL;

-- Schedule images. Staged = task_id and schedule_id both NULL.
ALTER TABLE task_attachments
    ADD COLUMN schedule_id bigint REFERENCES schedules (id) ON DELETE CASCADE,
    ADD CONSTRAINT task_attachments_one_owner CHECK (task_id IS NULL OR schedule_id IS NULL);
DROP INDEX task_attachments_staged_idx;
CREATE INDEX task_attachments_staged_idx ON task_attachments (created_at) WHERE task_id IS NULL AND schedule_id IS NULL;
CREATE INDEX task_attachments_schedule_idx ON task_attachments (schedule_id, id) WHERE schedule_id IS NOT NULL;

-- +goose Down
DROP INDEX task_attachments_schedule_idx;
DROP INDEX task_attachments_staged_idx;
DELETE FROM task_attachments WHERE schedule_id IS NOT NULL;
ALTER TABLE task_attachments DROP CONSTRAINT task_attachments_one_owner, DROP COLUMN schedule_id;
CREATE INDEX task_attachments_staged_idx ON task_attachments (created_at) WHERE task_id IS NULL;
DROP INDEX tasks_schedule_idx;
DROP INDEX tasks_due_start_idx;
ALTER TABLE tasks DROP CONSTRAINT tasks_start_or_after, DROP CONSTRAINT tasks_start_pair,
    DROP COLUMN schedule_id, DROP COLUMN start_tz, DROP COLUMN start_at;
DROP TABLE schedule_runs;
DROP TABLE schedules;
