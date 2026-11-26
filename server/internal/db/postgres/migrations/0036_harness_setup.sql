-- +goose Up
-- Expand, copy and verify in one transaction before retiring the old tables.
-- Connection identities, lifetime and defaults live in the connection tables.
-- Unconfigured baseline rows become empty objects. Their modules supply defaults.
LOCK TABLE t3_connection, opencode_settings, claude_settings, codex_settings IN ACCESS EXCLUSIVE MODE;

CREATE TABLE harness_settings (
 machine_id bigint NOT NULL REFERENCES machines(id),
 kind text NOT NULL,
 settings jsonb NOT NULL CHECK (jsonb_typeof(settings) = 'object'),
 updated_at timestamptz,
 PRIMARY KEY (machine_id, kind)
);

CREATE TABLE harness_secrets (
 machine_id bigint NOT NULL REFERENCES machines(id),
 kind text NOT NULL,
 key text NOT NULL,
 value jsonb NOT NULL,
 PRIMARY KEY (machine_id, kind, key)
);

CREATE TABLE harness_sync_status (
 machine_id bigint NOT NULL REFERENCES machines(id),
 kind text NOT NULL,
 last_error text,
 last_full_sync_at timestamptz,
 PRIMARY KEY (machine_id, kind)
);

CREATE TEMP VIEW expected_harness_settings(machine_id, kind, settings, updated_at) AS
 SELECT machine_id, 't3',
  CASE WHEN machine_id = 1 AND t3_url IS NULL AND public_url IS NULL
            AND client_id IS NULL AND token IS NULL AND expires_at IS NULL AND admin_token IS NULL
            AND last_error IS NULL AND last_full_sync_at IS NULL THEN '{}'::jsonb
       ELSE jsonb_build_object('url', t3_url, 'publicUrl', public_url) END,
  updated_at FROM t3_connection
 UNION ALL
 SELECT machine_id, 'opencode',
  CASE WHEN machine_id = 1 AND endpoint = '' AND browser_endpoint = '' AND web_client = '' AND auth = 'none'
            AND username = '' AND password = '' AND token_issued_at IS NULL THEN '{}'::jsonb
       ELSE jsonb_build_object('endpoint', endpoint, 'browserEndpoint', browser_endpoint, 'webClient', web_client, 'auth', auth) END, NULL::timestamptz FROM opencode_settings
 UNION ALL
 SELECT machine_id, 'claude', jsonb_build_object('binary', binary_path, 'configDir', config_dir, 'disconnected', disconnected), updated_at FROM claude_settings
 UNION ALL
 SELECT machine_id, 'codex', jsonb_build_object('binary', binary_path, 'connected', connected), updated_at FROM codex_settings;

CREATE TEMP VIEW expected_harness_secrets(machine_id, kind, key, value) AS
 SELECT machine_id, 't3', 'token', jsonb_build_object('clientId', client_id, 'token', token, 'expiresAt', to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
 FROM t3_connection WHERE client_id IS NOT NULL OR token IS NOT NULL OR expires_at IS NOT NULL
 UNION ALL
 SELECT machine_id, 't3', 'adminToken', to_jsonb(admin_token) FROM t3_connection WHERE admin_token IS NOT NULL
 UNION ALL
 SELECT machine_id, 'opencode', 'credential',
  CASE WHEN token_issued_at IS NULL THEN jsonb_build_object('username', username, 'password', password)
       ELSE jsonb_build_object('username', username, 'password', password, 'tokenIssuedAt', to_char(token_issued_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')) END
 FROM opencode_settings WHERE username <> '' OR password <> '' OR token_issued_at IS NOT NULL;

CREATE TEMP VIEW expected_harness_sync_status(machine_id, kind, last_error, last_full_sync_at) AS
 SELECT machine_id, 't3', last_error, last_full_sync_at
 FROM t3_connection WHERE last_error IS NOT NULL OR last_full_sync_at IS NOT NULL;

INSERT INTO harness_settings SELECT * FROM expected_harness_settings;
INSERT INTO harness_secrets SELECT * FROM expected_harness_secrets;
INSERT INTO harness_sync_status SELECT * FROM expected_harness_sync_status;

-- +goose StatementBegin
DO $$
BEGIN
 IF (SELECT count(*) FROM harness_settings) <> (SELECT count(*) FROM expected_harness_settings)
    OR EXISTS (SELECT * FROM expected_harness_settings EXCEPT SELECT * FROM harness_settings) THEN
  RAISE EXCEPTION 'harness settings copy verification failed';
 END IF;
 IF (SELECT count(*) FROM harness_secrets) <> (SELECT count(*) FROM expected_harness_secrets)
    OR EXISTS (SELECT * FROM expected_harness_secrets EXCEPT SELECT * FROM harness_secrets) THEN
  RAISE EXCEPTION 'harness secrets copy verification failed';
 END IF;
 IF (SELECT count(*) FROM harness_sync_status) <> (SELECT count(*) FROM expected_harness_sync_status)
    OR EXISTS (SELECT * FROM expected_harness_sync_status EXCEPT SELECT * FROM harness_sync_status) THEN
  RAISE EXCEPTION 'harness sync status copy verification failed';
 END IF;
END
$$;
-- +goose StatementEnd

DROP VIEW expected_harness_settings;
DROP VIEW expected_harness_secrets;
DROP VIEW expected_harness_sync_status;
DROP TABLE t3_connection;
DROP TABLE opencode_settings;
DROP TABLE claude_settings;
DROP TABLE codex_settings;
