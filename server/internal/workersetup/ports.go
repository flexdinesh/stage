package workersetup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Ports are the worker's setup ports: the main server's record of the
// machine (connections and non-secret settings) through setup calls over
// the channel, and the harness credentials in the worker's credentials
// file, which never leave it.
type Ports struct {
	Call  workerchan.Upcall
	Creds *worker.CredentialStore
}

var (
	_ t3setup.Settings       = Ports{}
	_ t3setup.Identity       = Ports{}
	_ t3.TokenStore          = Ports{}
	_ opencodesetup.Settings = Ports{}
	_ opencodesetup.Identity = Ports{}
)

// call sends kind with body and decodes the reply into out (nil: none).
func (p Ports) call(ctx context.Context, kind string, body, out any) error {
	raw, err := p.Call(ctx, kind, body)
	if err != nil {
		return remote.DecodeSetupError(err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("malformed server reply to %s: %w", kind, err)
	}
	return nil
}

// GetConnection is the machine's T3 setup as the main server records it.
func (p Ports) GetConnection(ctx context.Context) (db.Connection, error) {
	var s remote.T3Settings
	err := p.call(ctx, remote.KindT3Get, nil, &s)
	return s.Connection(), err
}

func (p Ports) SetT3URL(ctx context.Context, u string) error {
	return p.call(ctx, remote.KindT3SetURL, remote.URLRequest{URL: u}, nil)
}

// LoadToken reads the T3 token from the credentials file.
func (p Ports) LoadToken(context.Context) (execution.Credential, error) {
	var tok execution.Credential
	err := p.Creds.Get(t3.Kind, &tok)
	return tok, err
}

// SaveToken writes the T3 token to the credentials file.
func (p Ports) SaveToken(_ context.Context, tok execution.Credential) error {
	return p.Creds.Set(t3.Kind, tok)
}

// SetLastError records the T3 connection's error on the main server.
func (p Ports) SetLastError(ctx context.Context, msg string) error {
	return p.call(ctx, remote.KindT3LastError, remote.MessageRequest{Message: msg}, nil)
}

// OpenCodeSettings is the setup and verified address from the main server
// and the credential from the credentials file.
func (p Ports) OpenCodeSettings(ctx context.Context) (config.OpenCodeSetup, config.OpenCodeCredential, string, error) {
	var s remote.OpenCodeSettings
	if err := p.call(ctx, remote.KindOpenCodeGet, nil, &s); err != nil {
		return config.OpenCodeSetup{}, config.OpenCodeCredential{}, "", err
	}
	var cred config.OpenCodeCredential
	err := p.Creds.Get(opencode.Kind, &cred)
	return s.Setup, cred, s.Verified, err
}

// SaveOpenCodeSettings saves the setup on the main server, then the
// credential in the credentials file.
func (p Ports) SaveOpenCodeSettings(ctx context.Context, setup config.OpenCodeSetup, cred config.OpenCodeCredential) error {
	if err := p.call(ctx, remote.KindOpenCodeSave, remote.OpenCodeSettings{Setup: setup}, nil); err != nil {
		return err
	}
	return p.Creds.Set(opencode.Kind, cred)
}

// Active is the machine's active connection of kind (0: none).
func (p Ports) Active(ctx context.Context, kind string) (int64, error) {
	var r remote.IDResult
	err := p.call(ctx, remote.KindIdentityActive, remote.KindRequest{Kind: kind}, &r)
	return r.ID, err
}

// EnsureOpenCode is not served to workers: a remote machine's OpenCode
// connection is created by the first service that connects (Ports.Active
// with opencodesetup.Ports.Active).
func (p Ports) EnsureOpenCode(ctx context.Context) (int64, error) {
	return p.Active(ctx, opencode.Kind)
}

func (p Ports) SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error {
	return p.call(ctx, remote.KindIdentitySet, remote.IdentityRequest{ID: id, Identity: identity, Label: label}, nil)
}

func (p Ports) SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (db.Switch, error) {
	var r remote.SwitchResult
	err := p.call(ctx, remote.KindIdentitySwitch, remote.SwitchRequest{Kind: kind, Identity: identity, Label: label, Revive: revive}, &r)
	return r.Switch(), err
}

func (p Ports) GetHarnessConnection(ctx context.Context, id int64) (db.HarnessConnection, error) {
	var c db.HarnessConnection
	err := p.call(ctx, remote.KindIdentityGet, remote.ConnectionRequest{ID: id}, &c)
	return c, err
}

func (p Ports) RetiredConnections(ctx context.Context, kind string) ([]int64, error) {
	var r remote.IDsResult
	err := p.call(ctx, remote.KindIdentityRetired, remote.KindRequest{Kind: kind}, &r)
	return r.IDs, err
}

func (p Ports) ConnectionEvidence(ctx context.Context, id int64) ([]db.ProjectBinding, []execution.Observation, bool, error) {
	var e remote.Evidence
	err := p.call(ctx, remote.KindIdentityEvidence, remote.ConnectionRequest{ID: id}, &e)
	return e.Bindings, e.Observations, e.Unknown, err
}
