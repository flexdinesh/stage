package serve

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
)

type localMachine struct {
	*machine.Local
	cleanup []func()
}

func (m *localMachine) compose(ctx context.Context, store *db.Store, cfg config.Config, reg *harness.Registry, overrides map[string]any, changed func(), cli string, logger *slog.Logger) error {
	p := machine.NewStorePorts(store.OnMachine(m.ID()), reg)
	ports := harness.Ports{
		Settings: p.Settings(), Secrets: p.Secrets(), Identity: p, Install: m.Install,
		SaveSettingsWithSecret: p.SaveSettingsWithSecret, SetLastError: p.SetLastError,
		DataDir: cfg.DataDir, Profile: m.Profile(), Logger: logger, Getenv: cfg.Getenv,
		LookupPath: exec.LookPath, Overrides: overrides, OnChanged: changed, CLI: cli,
	}
	for _, module := range harnesses.Modules() {
		d := module.Descriptor
		if !m.Allows(d).Available {
			continue
		}
		installed, err := module.Compose(ctx, ports)
		if err != nil {
			return fmt.Errorf("compose %s: %w", d.Name, err)
		}
		m.SetConnector(d.Kind, installed.Connector)
		if installed.Close != nil {
			m.cleanup = append(m.cleanup, installed.Close)
		}
	}
	return nil
}

func (m *localMachine) close() {
	for i := len(m.cleanup) - 1; i >= 0; i-- {
		m.cleanup[i]()
	}
}

func remoteSetupHooks(store *db.Store, reg *harness.Registry, kick func()) remote.SetupHooks {
	return remote.SetupHooks{
		Kinds: reg.Kinds(), Active: store.ActiveID,
		Changed: func(int64, string, string) { kick() },
	}
}
