-- +goose Up
-- Codex connection setup (docs/adr/0006-codex-app-server-harness.md): one row per
-- machine, non-secret only. Stage stores no Codex credentials: Codex keeps
-- its own login under CODEX_HOME. binary_path is the Codex CLI ('' = codex on
-- PATH); connected records the connect action until Disconnect, so a
-- restart restores the connection. No row reads as never connected.
CREATE TABLE codex_settings (
 machine_id INTEGER PRIMARY KEY REFERENCES machines(id),
 binary_path TEXT NOT NULL DEFAULT '',
 connected INTEGER NOT NULL DEFAULT 0 CHECK(connected IN (0,1)),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'))
);
