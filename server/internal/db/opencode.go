package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// EnsureOpenCode is machine's active OpenCode connection, created on first
// use.
func (s *Store) EnsureOpenCode(ctx context.Context, machine int64) (int64, error) {
	id, err := write(ctx, s, func(tx store.Tx) (int64, error) { return tx.Connections().EnsureOpenCode(ctx, machine) })
	s.dropConnections()
	return id, err
}

// OpenCodeSettings are the settings of machine's active OpenCode connection
// (migration 0018: one row per machine; none reads the defaults), split
// into setup and credential, and the address its service was verified at
// ("" before the first).
func (s *Store) OpenCodeSettings(ctx context.Context, machine int64) (config.OpenCodeSetup, config.OpenCodeCredential, string, error) {
	c := config.OpenCodeSettings{Auth: config.OpenCodeAuthNone}
	var verified string
	err := s.e.Read(ctx, func(tx store.Tx) error {
		stored, found, err := tx.Connections().OpenCodeSettings(ctx, machine)
		if err != nil {
			return err
		}
		if found {
			c = stored
		}
		verified, err = tx.Connections().OpenCodeIdentity(ctx, machine)
		return err
	})
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
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SaveOpenCodeSettings(ctx, machine, c) })
}

// ConnectionEvidence includes retired bindings and all session identities.
// An unobserved identity cannot establish continuity across an address change.
func (s *Store) ConnectionEvidence(ctx context.Context, id int64) ([]ProjectBinding, []execution.Observation, bool, error) {
	var bindings []ProjectBinding
	var observed []*execution.Observation
	var pending bool
	err := s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		bindings, observed, pending, err = tx.Connections().Evidence(ctx, id)
		return err
	})
	if err != nil {
		return nil, nil, false, err
	}
	var observations []execution.Observation
	unknown := false
	for _, o := range observed {
		if o == nil {
			unknown = true
			continue
		}
		observations = append(observations, *o)
	}
	return bindings, observations, unknown || pending, nil
}
