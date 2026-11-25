-- +goose Up
-- Codex connection setup (docs/adr/0006-codex-app-server-harness.md): one row per
-- machine, non-secret only. Stage stores no Codex credentials: Codex keeps
-- its own login under CODEX_HOME. binary_path is the Codex CLI ('' = codex on
-- PATH); connected records the connect action until Disconnect, so a
-- restart restores the connection. No row reads as never connected.
CREATE TABLE codex_settings (
 machine_id bigint PRIMARY KEY REFERENCES machines(id),
 binary_path text NOT NULL DEFAULT '',
 connected boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now()
);
