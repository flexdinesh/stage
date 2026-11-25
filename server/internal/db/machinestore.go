package db

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// MachineStore is the store seen from one machine's harness setup: the
// machine-scoped methods (settings, token, active connections, instance
// switches) are bound to that machine, so setup code written for one
// machine stays unaware of others. Every other Store method is unchanged.
type MachineStore struct {
	*Store
	machine int64
}

// OnMachine is the store bound to machine.
func (s *Store) OnMachine(machine int64) MachineStore {
	return MachineStore{Store: s, machine: machine}
}

// Machine is the bound machine.
func (m MachineStore) Machine() int64 { return m.machine }

// ActiveID is the machine's active connection of harness (0 when none).
func (m MachineStore) ActiveID(harness string) int64 { return m.Store.ActiveID(m.machine, harness) }

// LoadToken implements t3.TokenStore (zero value when none stored).
func (m MachineStore) LoadToken(ctx context.Context) (execution.Credential, error) {
	return m.Store.LoadToken(ctx, m.machine)
}

// SaveToken implements t3.TokenStore.
func (m MachineStore) SaveToken(ctx context.Context, tok execution.Credential) error {
	return m.Store.SaveToken(ctx, m.machine, tok)
}

// SetLastError implements t3.TokenStore ("" clears).
func (m MachineStore) SetLastError(ctx context.Context, msg string) error {
	return m.Store.SetLastError(ctx, m.machine, msg)
}

// GetConnection reads the machine's T3 setup.
func (m MachineStore) GetConnection(ctx context.Context) (Connection, error) {
	return m.Store.GetConnection(ctx, m.machine)
}

// SetT3URL saves the machine's T3 URL setting.
func (m MachineStore) SetT3URL(ctx context.Context, u string) error {
	return m.Store.SetT3URL(ctx, m.machine, u)
}

// SetT3PublicURL saves the machine's T3 browser URL.
func (m MachineStore) SetT3PublicURL(ctx context.Context, u string) error {
	return m.Store.SetT3PublicURL(ctx, m.machine, u)
}

// LoadT3AdminToken reads the machine's T3 admin token.
func (m MachineStore) LoadT3AdminToken(ctx context.Context) (string, error) {
	return m.Store.LoadT3AdminToken(ctx, m.machine)
}

// SaveT3AdminToken saves the machine's T3 admin token.
func (m MachineStore) SaveT3AdminToken(ctx context.Context, tok string) error {
	return m.Store.SaveT3AdminToken(ctx, m.machine, tok)
}

// SetLastFullSync records the machine's last T3 full sync.
func (m MachineStore) SetLastFullSync(ctx context.Context, at time.Time) error {
	return m.Store.SetLastFullSync(ctx, m.machine, at)
}

// SwitchInstance switches the machine's active connection of d.Kind.
func (m MachineStore) SwitchInstance(ctx context.Context, d harness.Descriptor, identity, label string, revive int64) (Switch, error) {
	return m.Store.SwitchInstance(ctx, m.machine, d, identity, label, revive)
}

// RetiredConnections lists the machine's retired connections of harness.
func (m MachineStore) RetiredConnections(ctx context.Context, harness string) ([]int64, error) {
	return m.Store.RetiredConnections(ctx, m.machine, harness)
}

// EnsureOpenCode is the machine's active OpenCode connection.
func (m MachineStore) EnsureOpenCode(ctx context.Context) (int64, error) {
	return m.Store.EnsureOpenCode(ctx, m.machine)
}

// OpenCodeSettings reads the machine's OpenCode setup, credential and
// verified address.
func (m MachineStore) OpenCodeSettings(ctx context.Context) (config.OpenCodeSetup, config.OpenCodeCredential, string, error) {
	return m.Store.OpenCodeSettings(ctx, m.machine)
}

// SaveOpenCodeSettings saves the machine's OpenCode setup and credential.
func (m MachineStore) SaveOpenCodeSettings(ctx context.Context, setup config.OpenCodeSetup, cred config.OpenCodeCredential) error {
	return m.Store.SaveOpenCodeSettings(ctx, m.machine, setup, cred)
}

// ClaudeSettings reads the machine's Claude Code harness setup.
func (m MachineStore) ClaudeSettings(ctx context.Context) (config.ClaudeSettings, error) {
	return m.Store.ClaudeSettings(ctx, m.machine)
}

// SaveClaudeSettings saves the machine's Claude Code harness setup.
func (m MachineStore) SaveClaudeSettings(ctx context.Context, c config.ClaudeSettings) error {
	return m.Store.SaveClaudeSettings(ctx, m.machine, c)
}

// CodexSettings reads the machine's Codex setup.
func (m MachineStore) CodexSettings(ctx context.Context) (config.CodexSettings, error) {
	return m.Store.CodexSettings(ctx, m.machine)
}

// SaveCodexSettings saves the machine's Codex setup.
func (m MachineStore) SaveCodexSettings(ctx context.Context, c config.CodexSettings) error {
	return m.Store.SaveCodexSettings(ctx, m.machine, c)
}
