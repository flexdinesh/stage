package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// EnsureOpenCode is the active OpenCode connection, created on first use.
func (s *Store) EnsureOpenCode(ctx context.Context) (int64, error) {
	var id int64
	if _, err := s.pool.Exec(ctx, `INSERT INTO harness_connections(harness) SELECT 'opencode' WHERE NOT EXISTS(SELECT 1 FROM harness_connections WHERE harness='opencode' AND active=1)`); err != nil {
		return 0, err
	}
	s.dropConnections()
	err := s.pool.QueryRow(ctx, `SELECT id FROM harness_connections WHERE harness='opencode' AND active=1`).Scan(&id)
	return id, err
}

// OpenCodeSettings are the settings of the active OpenCode connection and
// the address its service was verified at ("" before the first).
func (s *Store) OpenCodeSettings(ctx context.Context) (config.OpenCodeSettings, string, error) {
	var c config.OpenCodeSettings
	var verified string
	err := s.pool.QueryRow(ctx, `SELECT endpoint,browser_endpoint,web_client,username,password,
 COALESCE((SELECT identity FROM harness_connections WHERE harness='opencode' AND active=1),'') FROM opencode_settings WHERE id=1`).Scan(&c.Endpoint, &c.BrowserEndpoint, &c.WebClient, &c.Username, &c.Password, &verified)
	return c, verified, err
}

// SaveOpenCodeSettings saves the active connection's settings. Its identity
// changes only through SwitchInstance or SetConnectionIdentity.
func (s *Store) SaveOpenCodeSettings(ctx context.Context, c config.OpenCodeSettings) error {
	_, err := s.pool.Exec(ctx, `UPDATE opencode_settings SET endpoint=$1,browser_endpoint=$2,web_client=$3,username=$4,password=$5 WHERE id=1`, c.Endpoint, c.BrowserEndpoint, c.WebClient, c.Username, c.Password)
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
