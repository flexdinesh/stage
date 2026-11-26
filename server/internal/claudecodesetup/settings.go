package claudecodesetup

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// SetupSettings is the machine's persisted setup. Missing fields use their zero values.
type SetupSettings struct {
	// Binary is the command name or path. Empty uses the default lookup.
	Binary string `json:"binary"`
	// ConfigDir is the last connected CLI config directory.
	ConfigDir    string `json:"configDir"`
	Disconnected bool   `json:"disconnected"`
}

func loadSettings(ctx context.Context, settings harness.Settings) (SetupSettings, error) {
	raw, err := settings.Get(ctx, claudecode.Kind)
	if err != nil {
		return SetupSettings{}, err
	}
	return decodeSettings(raw)
}

func decodeSettings(raw json.RawMessage) (SetupSettings, error) {
	var settings *SetupSettings
	if err := db.DecodeStrict(raw, &settings); err != nil {
		return SetupSettings{}, err
	}
	if settings == nil {
		return SetupSettings{}, db.Invalid("settings must be a JSON object")
	}
	return *settings, nil
}

func validateSettings(raw json.RawMessage) error {
	_, err := decodeSettings(raw)
	return err
}

func saveSettings(ctx context.Context, settings harness.Settings, patch any) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return settings.Save(ctx, claudecode.Kind, raw)
}
