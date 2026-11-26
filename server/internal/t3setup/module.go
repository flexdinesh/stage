package t3setup

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

const (
	envFake     = "T3_FAKE"
	envFakeSeed = "T3_FAKE_SEED"
	envBin      = "T3_BIN"
)

// Module composes T3 on either the server's machine or a worker.
var Module = harness.Module{
	Descriptor:       t3.Descriptor,
	Compose:          compose,
	ValidateSettings: validateSettings,
	LegacySecrets: []harness.LegacySecret{
		{Entry: t3.Kind, Kind: t3.Kind, Key: tokenKey, Validate: validateSecret[t3.StoredToken]},
		{Entry: "t3-admin", Kind: t3.Kind, Key: adminTokenKey, Validate: validateSecret[string]},
	},
	Env: harness.EnvOptions{
		Keys:    []string{envFake, envFakeSeed, envBin},
		Removed: []string{"T3_URL", "T3_PUBLIC_URL", "T3_ADMIN_TOKEN"},
		WorkerTemplate: `# The t3 CLI auto-pair runs (default: looked up on PATH). Harness
# connections are set up in Settings > Connections, never here.
# T3_BIN=
`,
	},
	Owners: []string{"internal/t3/", "internal/t3setup/", "internal/cli/slots_t3.go"},
}

func compose(ctx context.Context, p harness.Ports) (harness.Composition, error) {
	raw, err := p.Settings.Get(ctx, t3.Kind)
	if err != nil {
		return harness.Composition{}, err
	}
	settings, err := decodeSettings(raw)
	if err != nil {
		return harness.Composition{}, err
	}
	endpoint := Resolve(settings)
	admin, err := loadSecret[string](ctx, p.Secrets, adminTokenKey)
	if err != nil {
		return harness.Composition{}, fmt.Errorf("load T3 admin token: %w", err)
	}
	active, err := p.Identity.Active(ctx, t3.Kind)
	if err != nil {
		return harness.Composition{}, err
	}

	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	clientName := buildinfo.Identity
	if getenv("STAGE_ENV") == "dev" {
		clientName += " dev"
	}
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	client, _ := p.Overrides[t3.Kind].(t3.Client)
	var mc *t3.MCPClient
	switch {
	case client != nil:
	case getenv(envFake) == "1":
		fake := t3.NewFake()
		if getenv(envFakeSeed) == "1" {
			fake.SeedDemo()
		}
		client = fake
		logger.Info("using fake T3 client")
	default:
		mc = t3.NewMCPClient(t3.Options{
			BaseURL: endpoint.URL, PublicURL: endpoint.PublicURL, AdminToken: admin, ClientName: clientName,
			Store:      credentials{secrets: p.Secrets, setLastError: p.SetLastError},
			HTTPClient: &http.Client{Timeout: 60 * time.Second}, Logger: logger,
		})
		client = mc
		logger.Info("using T3 MCP client", "url", endpoint.URL, "source", endpoint.Source)
	}
	install := func(old, next int64) {
		p.Install(old, next, t3.NewConnectionAdapter(client, next))
	}
	setup := New(Options{
		Settings: p.Settings, Secrets: p.Secrets, Identity: p.Identity,
		Client: client, Endpoint: endpoint, AdminToken: admin, ClientName: clientName,
		Fake: IsFake(client), Local: &LocalCLI{Bin: getenv(envBin), LookPath: p.LookupPath},
		Logger: logger, OnPaired: p.OnChanged,
		OnSwitched: func(sw harness.Switch) { install(sw.Old, sw.New) },
	})
	if active != 0 {
		install(active, active)
	}
	result := harness.Composition{Connector: NewConnector(setup)}
	if mc != nil {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			mc.Run(runCtx)
		}()
		result.Close = func() {
			cancel()
			<-done
		}
	}
	return result, nil
}
