package remote

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// ServeSetup answers the setup port calls of workers on hub: each reads or
// writes the calling machine's record through ports(machineID), and never
// another machine's connections. Credentials are never stored here: an
// OpenCode save keeps only the setup. switched runs after an instance
// switch moved or created a connection (may be nil).
func ServeSetup(hub *workerchan.Hub, ports func(machineID int64) machine.StorePorts, switched func(machineID int64, sw db.Switch)) {
	handle := func(kind string, fn func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error)) {
		hub.Handle(kind, func(ctx context.Context, machineID int64, env workerchan.Envelope) (any, error) {
			out, err := fn(ctx, ports(machineID), env.Body)
			if err != nil {
				return nil, EncodeSetupError(err)
			}
			return out, nil
		})
	}
	handle(KindT3Get, func(ctx context.Context, p machine.StorePorts, _ json.RawMessage) (any, error) {
		c, err := p.GetConnection(ctx)
		return T3SettingsOf(c), err
	})
	handle(KindT3SetURL, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r URLRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		return Empty{}, p.SetT3URL(ctx, r.URL)
	})
	handle(KindT3SetPublicURL, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r URLRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		return Empty{}, p.SetT3PublicURL(ctx, r.URL)
	})
	handle(KindT3LastError, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r MessageRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		return Empty{}, p.SetLastError(ctx, r.Message)
	})
	handle(KindOpenCodeGet, func(ctx context.Context, p machine.StorePorts, _ json.RawMessage) (any, error) {
		setup, _, verified, err := p.OpenCodeSettings(ctx)
		return OpenCodeSettings{Setup: setup, Verified: verified}, err
	})
	handle(KindOpenCodeSave, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r OpenCodeSettings
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		// The credential stays on the worker.
		return Empty{}, p.SaveOpenCodeSettings(ctx, r.Setup, config.OpenCodeCredential{})
	})
	handle(KindIdentityActive, func(_ context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r KindRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		return IDResult{ID: p.ActiveID(r.Kind)}, nil
	})
	handle(KindIdentitySwitch, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r SwitchRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		if r.Revive != 0 {
			if err := own(p, r.Revive); err != nil {
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
	handle(KindIdentityGet, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r ConnectionRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		if err := own(p, r.ID); err != nil {
			return nil, err
		}
		return p.GetHarnessConnection(ctx, r.ID)
	})
	handle(KindIdentitySet, func(ctx context.Context, p machine.StorePorts, body json.RawMessage) (any, error) {
		var r IdentityRequest
		if err := decodeBody(body, &r); err != nil {
			return nil, err
		}
		if err := own(p, r.ID); err != nil {
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
		if err := own(p, r.ID); err != nil {
			return nil, err
		}
		bindings, observations, unknown, err := p.ConnectionEvidence(ctx, r.ID)
		if bindings == nil {
			bindings = []db.ProjectBinding{}
		}
		return Evidence{Bindings: bindings, Observations: observations, Unknown: unknown}, err
	})
}

// own refuses a connection of another machine (or none).
func own(p machine.StorePorts, id int64) error {
	if id <= 0 || p.ConnectionMachine(id) != p.Machine() {
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
