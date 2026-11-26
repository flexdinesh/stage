-- +goose Up
-- Expand, copy and verify in one transaction before retiring the old tables.
-- Connection identities, lifetime and defaults live in the connection tables.
-- Unconfigured baseline rows become empty objects. Their modules supply defaults.
CREATE TABLE harness_settings (
 machine_id INTEGER NOT NULL REFERENCES machines(id),
 kind TEXT NOT NULL,
 settings TEXT NOT NULL CHECK (json_valid(settings) AND json_type(settings) = 'object'),
 updated_at TEXT,
 PRIMARY KEY (machine_id, kind)
);

CREATE TABLE harness_secrets (
 machine_id INTEGER NOT NULL REFERENCES machines(id),
 kind TEXT NOT NULL,
 key TEXT NOT NULL,
 value TEXT NOT NULL CHECK (json_valid(value)),
 PRIMARY KEY (machine_id, kind, key)
);

CREATE TABLE harness_sync_status (
 machine_id INTEGER NOT NULL REFERENCES machines(id),
 kind TEXT NOT NULL,
 last_error TEXT,
 last_full_sync_at TEXT,
 PRIMARY KEY (machine_id, kind)
);

CREATE TEMP VIEW expected_harness_settings(machine_id, kind, settings, updated_at) AS
 SELECT machine_id, 't3',
  CASE WHEN machine_id = 1 AND t3_url IS NULL AND public_url IS NULL
            AND client_id IS NULL AND token IS NULL AND expires_at IS NULL AND admin_token IS NULL
            AND last_error IS NULL AND last_full_sync_at IS NULL THEN '{}'
       ELSE json_object('url', t3_url, 'publicUrl', public_url) END,
  updated_at FROM t3_connection
 UNION ALL
 SELECT machine_id, 'opencode',
  CASE WHEN machine_id = 1 AND endpoint = '' AND browser_endpoint = '' AND web_client = '' AND auth = 'none'
            AND username = '' AND password = '' AND token_issued_at IS NULL THEN '{}'
       ELSE json_object('endpoint', endpoint, 'browserEndpoint', browser_endpoint, 'webClient', web_client, 'auth', auth) END, NULL FROM opencode_settings
 UNION ALL
 SELECT machine_id, 'claude', json_object('binary', binary_path, 'configDir', config_dir, 'disconnected', json(CASE disconnected WHEN 1 THEN 'true' ELSE 'false' END)), updated_at FROM claude_settings
 UNION ALL
 SELECT machine_id, 'codex', json_object('binary', binary_path, 'connected', json(CASE connected WHEN 1 THEN 'true' ELSE 'false' END)), updated_at FROM codex_settings;

CREATE TEMP VIEW expected_harness_secrets(machine_id, kind, key, value) AS
 SELECT machine_id, 't3', 'token', json_object('clientId', client_id, 'token', token, 'expiresAt', expires_at)
 FROM t3_connection WHERE client_id IS NOT NULL OR token IS NOT NULL OR expires_at IS NOT NULL
 UNION ALL
 SELECT machine_id, 't3', 'adminToken', json_quote(admin_token) FROM t3_connection WHERE admin_token IS NOT NULL
 UNION ALL
 SELECT machine_id, 'opencode', 'credential',
  CASE WHEN token_issued_at IS NULL THEN json_object('username', username, 'password', password)
       ELSE json_object('username', username, 'password', password, 'tokenIssuedAt', token_issued_at) END
 FROM opencode_settings WHERE username <> '' OR password <> '' OR token_issued_at IS NOT NULL;

CREATE TEMP VIEW expected_harness_sync_status(machine_id, kind, last_error, last_full_sync_at) AS
 SELECT machine_id, 't3', last_error, last_full_sync_at
 FROM t3_connection WHERE last_error IS NOT NULL OR last_full_sync_at IS NOT NULL;

INSERT INTO harness_settings SELECT * FROM expected_harness_settings;
INSERT INTO harness_secrets SELECT * FROM expected_harness_secrets;
INSERT INTO harness_sync_status SELECT * FROM expected_harness_sync_status;

CREATE TEMP TABLE harness_setup_copy_check (matches INTEGER NOT NULL CONSTRAINT harness_setup_copy_verified CHECK (matches = 1));
INSERT INTO harness_setup_copy_check VALUES
 ((SELECT count(*) FROM harness_settings) = (SELECT count(*) FROM expected_harness_settings)
  AND NOT EXISTS (SELECT * FROM expected_harness_settings EXCEPT SELECT * FROM harness_settings)),
 ((SELECT count(*) FROM harness_secrets) = (SELECT count(*) FROM expected_harness_secrets)
  AND NOT EXISTS (SELECT * FROM expected_harness_secrets EXCEPT SELECT * FROM harness_secrets)),
 ((SELECT count(*) FROM harness_sync_status) = (SELECT count(*) FROM expected_harness_sync_status)
  AND NOT EXISTS (SELECT * FROM expected_harness_sync_status EXCEPT SELECT * FROM harness_sync_status));
DROP TABLE harness_setup_copy_check;

DROP VIEW expected_harness_settings;
DROP VIEW expected_harness_secrets;
DROP VIEW expected_harness_sync_status;
DROP TABLE t3_connection;
DROP TABLE opencode_settings;
DROP TABLE claude_settings;
DROP TABLE codex_settings;
