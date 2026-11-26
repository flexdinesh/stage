package opencodesetup

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

const credentialKey = "credential"

// Module supplies OpenCode setup on the machine that runs its adapter.
var Module = harness.Module{
	Descriptor:       opencode.Descriptor,
	Compose:          compose,
	ValidateSettings: validateSettings,
	LegacySecrets: []harness.LegacySecret{{
		Entry: opencode.Kind, Kind: opencode.Kind, Key: credentialKey, Validate: validateCredential,
	}},
	Env: harness.EnvOptions{
		Removed: []string{"OPENCODE_URL", "OPENCODE_PUBLIC_URL", "OPENCODE_WEB_URL", "OPENCODE_USERNAME", "OPENCODE_PASSWORD"},
	},
	Owners: []string{"internal/opencode/", "internal/opencodesetup/", "internal/cli/slots_opencode.go"},
}

func compose(ctx context.Context, p harness.Ports) (harness.Composition, error) {
	ctx, cancel := context.WithCancel(ctx)
	s, err := newService(ctx, p, nil)
	if err != nil {
		cancel()
		return harness.Composition{}, err
	}
	return harness.Composition{Connector: NewConnector(s), Close: cancel}, nil
}

func validateSettings(raw json.RawMessage) error {
	setup, err := decodeSettings(raw)
	if err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"Service URL", setup.Endpoint}, {"Browser service URL", setup.BrowserEndpoint}, {"Web client URL", setup.WebClient},
	} {
		if field.value != "" {
			if _, err := opencode.ValidateURL(field.name, field.value); err != nil {
				return db.Invalid("%s", err)
			}
		}
	}
	return nil
}

func decodeSettings(raw json.RawMessage) (SetupSettings, error) {
	var setup *SetupSettings
	if err := db.DecodeStrict(raw, &setup); err != nil {
		return SetupSettings{}, err
	}
	if setup == nil {
		return SetupSettings{}, db.Invalid("OpenCode settings must be a JSON object")
	}
	switch setup.Auth {
	case "", AuthNone, AuthLocal, AuthPaired, AuthBasic:
		return *setup, nil
	default:
		return SetupSettings{}, db.Invalid("invalid OpenCode authentication mode")
	}
}

func validateCredential(raw json.RawMessage) error {
	_, err := decodeCredential(raw)
	return err
}

func decodeCredential(raw json.RawMessage) (Credential, error) {
	var credential *Credential
	if err := db.DecodeStrict(raw, &credential); err != nil || credential == nil {
		return Credential{}, errors.New("invalid OpenCode credential")
	}
	return *credential, nil
}
