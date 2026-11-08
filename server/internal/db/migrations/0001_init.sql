-- +goose Up

-- Single-row T3 connection state (OAuth mcp-client token, sync bookkeeping).
CREATE TABLE t3_connection (
    id                smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    client_id         text,
    token             text,
    expires_at        timestamptz,
    last_error        text,
    last_full_sync_at timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);
INSERT INTO t3_connection (id) VALUES (1);

-- App projects, 1:1 with T3 projects.
CREATE TABLE projects (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    t3_project_id  text NOT NULL UNIQUE,
    title          text NOT NULL,
    workspace_root text NOT NULL,
    source         text NOT NULL CHECK (source IN ('app', 't3')),
    archived_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX projects_workspace_root_idx ON projects (workspace_root);

-- Mirror of T3 threads (all projects, full history).
CREATE TABLE threads (
    t3_thread_id          text PRIMARY KEY,
    t3_project_id         text NOT NULL,
    title                 text NOT NULL DEFAULT '',
    status                text NOT NULL,
    settled               boolean NOT NULL DEFAULT false,
    settled_at            timestamptz,
    pending_request_count integer NOT NULL DEFAULT 0,
    provider_instance_id  text,
    model                 text,
    runtime_mode          text,
    interaction_mode      text,
    branch                text,
    worktree_path         text,
    parent_thread_id      text,
    relationship          text,
    creation_source       text,
    link                  text,
    created_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL,
    synced_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX threads_t3_project_id_idx ON threads (t3_project_id);
CREATE INDEX threads_created_at_idx ON threads (created_at);
CREATE INDEX threads_updated_at_idx ON threads (updated_at);
CREATE INDEX threads_status_idx ON threads (status);

-- Sequential chains; steps are tasks ordered by chain_position.
CREATE TABLE chains (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title      text NOT NULL,
    state      text NOT NULL DEFAULT 'idle' CHECK (state IN ('idle', 'running', 'paused', 'done')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chains_state_idx ON chains (state);

CREATE TABLE tasks (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id           bigint NOT NULL REFERENCES projects (id),
    title                text NOT NULL,
    prompt               text NOT NULL,
    provider_instance_id text,
    model                text,
    -- [{"id":"effort","value":"low"}] (T3 ProviderOptionSelection[])
    model_options        jsonb NOT NULL DEFAULT '[]'::jsonb,
    runtime_mode         text NOT NULL DEFAULT 'full-access'
        CHECK (runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')),
    interaction_mode     text NOT NULL DEFAULT 'default' CHECK (interaction_mode IN ('default', 'plan')),
    -- {"type":"root"} | {"type":"worktree","baseRef":"origin/main","branch":"feat-x"|null}
    workspace            jsonb NOT NULL DEFAULT '{"type":"root"}'::jsonb,
    tags                 text[] NOT NULL DEFAULT '{}',
    notes                text NOT NULL DEFAULT '',
    state                text NOT NULL DEFAULT 'backlog' CHECK (state IN (
        'backlog', 'launching', 'running', 'needs_input', 'completed',
        'settled', 'failed', 'interrupted', 'cancelled'
    )),
    t3_thread_id         text UNIQUE,
    chain_id             bigint REFERENCES chains (id) ON DELETE SET NULL,
    chain_position       integer,
    launch_attempt       integer NOT NULL DEFAULT 0,
    last_error           text,
    launched_at          timestamptz,
    finished_at          timestamptz,
    settled_at           timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tasks_state_idx ON tasks (state);
CREATE INDEX tasks_project_id_idx ON tasks (project_id);
CREATE INDEX tasks_chain_idx ON tasks (chain_id, chain_position);
CREATE INDEX tasks_created_at_idx ON tasks (created_at);
CREATE INDEX tasks_tags_idx ON tasks USING gin (tags);

CREATE TABLE task_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id    bigint NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    from_state text,
    to_state   text NOT NULL,
    detail     text NOT NULL DEFAULT '',
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_events_task_id_idx ON task_events (task_id, at);

CREATE TABLE templates (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name                 text NOT NULL UNIQUE,
    prompt               text NOT NULL DEFAULT '',
    provider_instance_id text,
    model                text,
    model_options        jsonb NOT NULL DEFAULT '[]'::jsonb,
    runtime_mode         text NOT NULL DEFAULT 'full-access'
        CHECK (runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')),
    interaction_mode     text NOT NULL DEFAULT 'default' CHECK (interaction_mode IN ('default', 'plan')),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE templates;
DROP TABLE task_events;
DROP TABLE tasks;
DROP TABLE chains;
DROP TABLE threads;
DROP TABLE projects;
DROP TABLE t3_connection;
