package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// EnsureOpenCode is machine's active OpenCode connection, created on first
// use.
func (s *Store) EnsureOpenCode(ctx context.Context, machine int64) (int64, error) {
	var id int64
	if _, err := s.pool.Exec(ctx, `INSERT INTO harness_connections(machine_id,harness) SELECT $1,'opencode' WHERE NOT EXISTS(SELECT 1 FROM harness_connections WHERE machine_id=$1 AND harness='opencode' AND active=1)`, machine); err != nil {
		return 0, err
	}
	s.dropConnections()
	err := s.pool.QueryRow(ctx, `SELECT id FROM harness_connections WHERE machine_id=$1 AND harness='opencode' AND active=1`, machine).Scan(&id)
	return id, err
}

// OpenCodeSettings are the settings of machine's active OpenCode connection
// (migration 0018: one row per machine; none reads the defaults), split
// into setup and credential, and the address its service was verified at
// ("" before the first).
func (s *Store) OpenCodeSettings(ctx context.Context, machine int64) (config.OpenCodeSetup, config.OpenCodeCredential, string, error) {
	c := config.OpenCodeSettings{Auth: config.OpenCodeAuthNone}
	var verified string
	err := s.pool.QueryRow(ctx, `SELECT endpoint,browser_endpoint,web_client,username,password,auth,token_issued_at FROM opencode_settings WHERE machine_id=$1`, machine).Scan(&c.Endpoint, &c.BrowserEndpoint, &c.WebClient, &c.Username, &c.Password, &c.Auth, &c.TokenIssuedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		setup, cred := c.Split()
		return setup, cred, "", err
	}
	err = s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT identity FROM harness_connections WHERE machine_id=$1 AND harness='opencode' AND active=1),'')`, machine).Scan(&verified)
	setup, cred := c.Split()
	return setup, cred, verified, err
}

// SaveOpenCodeSettings saves machine's OpenCode setup and credential in one
// write. Its active connection's identity changes only through
// SwitchInstance or SetConnectionIdentity.
func (s *Store) SaveOpenCodeSettings(ctx context.Context, machine int64, setup config.OpenCodeSetup, cred config.OpenCodeCredential) error {
	c := config.JoinOpenCode(setup, cred)
	if c.Auth == "" {
		c.Auth = config.OpenCodeAuthNone
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO opencode_settings(machine_id,endpoint,browser_endpoint,web_client,username,password,auth,token_issued_at) VALUES($8,$1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(machine_id) DO UPDATE SET endpoint=excluded.endpoint,browser_endpoint=excluded.browser_endpoint,web_client=excluded.web_client,username=excluded.username,password=excluded.password,auth=excluded.auth,token_issued_at=excluded.token_issued_at`, c.Endpoint, c.BrowserEndpoint, c.WebClient, c.Username, c.Password, c.Auth, c.TokenIssuedAt, machine)
	return err
}

// ConnectionEvidence includes retired bindings and all session identities.
// An unobserved identity cannot establish continuity across an address change.
func (s *Store) ConnectionEvidence(ctx context.Context, id int64) ([]ProjectBinding, []execution.Observation, bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT external_project_id,workspace FROM project_bindings WHERE connection_id=$1`, id)
	if err != nil {
		return nil, nil, false, err
	}
	bindings, err := collectRows(rows, func(r rowScanner) (ProjectBinding, error) {
		var b ProjectBinding
		err := r.Scan(&b.ExternalProjectID, &b.Workspace)
		return b, err
	})
	if err != nil {
		return nil, nil, false, err
	}
	rows, err = s.pool.Query(ctx, `SELECT o.observation FROM session_identities i LEFT JOIN execution_observations o ON i.id=o.session_id WHERE i.connection_id=$1`, id)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	var observations []execution.Observation
	var unknown bool
	for rows.Next() {
		var o *execution.Observation
		if err := rows.Scan(&o); err != nil {
			return nil, nil, false, err
		}
		if o == nil {
			unknown = true
			continue
		}
		observations = append(observations, *o)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	rows.Close()
	var pending bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1 AND state='launching')`, id).Scan(&pending)
	return bindings, observations, unknown || pending, err
}
