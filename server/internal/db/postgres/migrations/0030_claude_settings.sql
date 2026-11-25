-- +goose Up
-- Claude Code harness setup per machine (#372): the claude command name or
-- path the user set (binary_path, '' = default lookup; not "binary", a
-- reserved word on Postgres), the config directory Claude reported when it
-- was last connected (config_dir, the connection's identity, checked by
-- health) and whether the user disconnected it. A machine with no row reads
-- the defaults and gets one on its first write.

CREATE TABLE claude_settings (
 machine_id bigint PRIMARY KEY REFERENCES machines(id),
 binary_path text NOT NULL DEFAULT '',
 config_dir text NOT NULL DEFAULT '',
 disconnected boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now()
);
