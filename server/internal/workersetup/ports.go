package workersetup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Ports reach the main server for identity and public settings, and the
// worker's local store for secrets.
type Ports struct {
	Call  workerchan.Upcall
	Creds harness.Secrets
}

var (
	_ harness.Settings = Ports{}
	_ harness.Identity = Ports{}
)

func (p Ports) call(ctx context.Context, kind string, body, out any) error {
	raw, err := p.Call(ctx, kind, body)
	if err != nil {
		return remote.DecodeSetupError(err)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("malformed server reply to %s: %w", kind, err)
		}
	}
	return nil
}

func (p Ports) Get(ctx context.Context, kind string) (json.RawMessage, error) {
	var settings json.RawMessage
	err := p.call(ctx, remote.KindSettingsGet, remote.KindRequest{Kind: kind}, &settings)
	return settings, err
}

func (p Ports) Save(ctx context.Context, kind string, patch json.RawMessage) error {
	return p.call(ctx, remote.KindSettingsSave, remote.SettingsRequest{Kind: kind, Patch: patch}, nil)
}

func (p Ports) SetLastError(ctx context.Context, kind, msg string) error {
	return p.call(ctx, remote.KindStatusError, remote.StatusErrorRequest{Kind: kind, Message: msg}, nil)
}

func (p Ports) Active(ctx context.Context, kind string) (int64, error) {
	var r remote.IDResult
	err := p.call(ctx, remote.KindIdentityActive, remote.KindRequest{Kind: kind}, &r)
	return r.ID, err
}

func (p Ports) ActiveIdentity(ctx context.Context, kind string) (int64, string, error) {
	var r remote.ActiveIdentityResult
	err := p.call(ctx, remote.KindIdentityActiveIdentity, remote.KindRequest{Kind: kind}, &r)
	return r.ID, r.Identity, err
}

func (p Ports) SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error {
	return p.call(ctx, remote.KindIdentitySet, remote.IdentityRequest{ID: id, Identity: identity, Label: label}, nil)
}

func (p Ports) SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (harness.Switch, error) {
	var r remote.SwitchResult
	err := p.call(ctx, remote.KindIdentitySwitch, remote.SwitchRequest{Kind: kind, Identity: identity, Label: label, Revive: revive}, &r)
	return r.Switch(), err
}

func (p Ports) ConnectionExists(ctx context.Context, id int64) (bool, error) {
	var r remote.ConnectionExistsResult
	err := p.call(ctx, remote.KindIdentityExists, remote.ConnectionRequest{ID: id}, &r)
	return r.Exists, err
}

func (p Ports) RetiredConnections(ctx context.Context, kind string) ([]int64, error) {
	var r remote.IDsResult
	err := p.call(ctx, remote.KindIdentityRetired, remote.KindRequest{Kind: kind}, &r)
	return r.IDs, err
}

func (p Ports) ConnectionEvidence(ctx context.Context, id int64) ([]harness.WorkspaceEvidence, []execution.Observation, bool, error) {
	var e remote.Evidence
	err := p.call(ctx, remote.KindIdentityEvidence, remote.ConnectionRequest{ID: id}, &e)
	return e.Bindings, e.Observations, e.Unknown, err
}
