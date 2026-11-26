package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// ServeSetup answers worker setup calls through the authenticated machine's
// ports. The modules define the allowed public settings. switched runs when
// a connection is created or replaced and may be nil.
func ServeSetup(hub *workerchan.Hub, ports func(machineID int64) machine.StorePorts, switched func(machineID int64, sw db.Switch), modules []harness.Module) {
	validators := make(map[string]func(json.RawMessage) error, len(modules))
	for _, mod := range modules {
		validators[mod.Descriptor.Kind] = mod.ValidateSettings
	}
	initializing := []string{
		KindSettingsGet, KindStatusError, KindIdentityActive, KindIdentityActiveIdentity,
		KindIdentitySet, KindIdentitySwitch,
	}
	handle := func(kind string, fn func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error)) {
		register := hub.Handle
		if slices.Contains(initializing, kind) {
			register = hub.HandleInitialization
		}
		register(kind, func(ctx context.Context, machineID int64, env workerchan.Envelope) (any, error) {
			out, err := fn(ctx, ports(machineID), env.Body)
			if err != nil {
				return nil, EncodeSetupError(err)
			}
			return out, nil
		})
	}
	handle(KindSettingsGet, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r KindRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		validate := validators[r.Kind]
		if validate == nil {
			return nil, db.Invalid("unregistered harness %q", r.Kind)
		}
		public, err := p.Settings().Get(ctx, r.Kind)
		if err != nil {
			return nil, err
		}
		if err := validate(public); err != nil {
			return nil, db.Invalid("saved settings for %s are invalid; update them in Settings > Connections", r.Kind)
		}
		return public, nil
	})
	handle(KindSettingsSave, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r SettingsRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		validate := validators[r.Kind]
		if validate == nil {
			return nil, db.Invalid("no public settings schema for harness %q", r.Kind)
		}
		if err := validate(r.Patch); err != nil {
			return nil, db.Invalid("invalid settings for %s: %s", r.Kind, err)
		}
		return Empty{}, p.Settings().Save(ctx, r.Kind, r.Patch)
	})
	handle(KindStatusError, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r StatusErrorRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		if _, ok := validators[r.Kind]; !ok {
			return nil, db.Invalid("unregistered harness %q", r.Kind)
		}
		return Empty{}, p.SetLastError(ctx, r.Kind, r.Message)
	})
	handle(KindIdentityActive, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r KindRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		id, err := p.Active(ctx, r.Kind)
		return IDResult{ID: id}, err
	})
	handle(KindIdentityActiveIdentity, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r KindRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		id, identity, err := p.ActiveIdentity(ctx, r.Kind)
		return ActiveIdentityResult{ID: id, Identity: identity}, err
	})
	handle(KindIdentitySwitch, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r SwitchRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		if r.Revive != 0 {
			if err := own(ctx, p, r.Revive); err != nil {
				return nil, err
			}
		}
		sw, err := p.SwitchInstance(ctx, r.Kind, r.Identity, r.Label, r.Revive)
		if err != nil {
			return nil, err
		}
		if switched != nil && sw.Old != sw.New {
			switched(p.Machine(), sw)
		}
		return SwitchOf(sw), nil
	})
	handle(KindIdentityExists, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r ConnectionRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		exists, err := p.ConnectionExists(ctx, r.ID)
		return ConnectionExistsResult{Exists: exists}, err
	})
	handle(KindIdentitySet, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r IdentityRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		return Empty{}, p.SetConnectionIdentity(ctx, r.ID, r.Identity, r.Label)
	})
	handle(KindIdentityRetired, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r KindRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		ids, err := p.RetiredConnections(ctx, r.Kind)
		if ids == nil {
			ids = []int64{}
		}
		return IDsResult{IDs: ids}, err
	})
	handle(KindIdentityEvidence, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r ConnectionRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		bindings, observations, unknown, err := p.ConnectionEvidence(ctx, r.ID)
		if bindings == nil {
			bindings = []harness.WorkspaceEvidence{}
		}
		return Evidence{Bindings: bindings, Observations: observations, Unknown: unknown}, err
	})
}

func own(ctx context.Context, p machine.StorePorts, id int64) error {
	exists, err := p.ConnectionExists(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: connection %d is not on this machine", db.ErrNotFound, id)
	}
	return nil
}

func decodeBody(body json.RawMessage, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return db.Invalid("malformed setup request: %s", err)
	}
	return nil
}
