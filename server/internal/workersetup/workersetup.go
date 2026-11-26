// Package workersetup composes harness modules on a worker. The main server
// records machine-scoped settings and identity, and secrets stay on the worker.
package workersetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

type Options struct {
	Host      *remote.Host
	Ports     Ports
	Getenv    func(string) string
	Overrides map[string]any
	StateDir  string
	Logger    *slog.Logger
}

// Setup owns the worker's installed harnesses across channel reconnects.
type Setup struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	composed map[string]bool
}

// Attach arranges for the worker to compose its harnesses before ready.
func Attach(o Options) *Setup {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	s := &Setup{o: o, log: o.Logger.With("component", "workersetup"), composed: map[string]bool{}}
	var removed []string
	for _, key := range harnesses.Env().Removed {
		if o.Getenv(key) != "" {
			removed = append(removed, key)
		}
	}
	if len(removed) > 0 {
		s.log.Warn(strings.Join(removed, ", ") + " no longer read; set the connection up in Settings > Connections")
	}
	o.Host.OnAttached(s.compose)
	return s
}

func (s *Setup) compose(ctx context.Context, serverKinds []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	local, ports := s.o.Host.Local(), s.o.Ports
	p := harness.Ports{
		Settings: ports, Secrets: ports.Creds, Identity: ports, SetLastError: ports.SetLastError,
		Install: local.Install, OnChanged: s.o.Host.Notify, Profile: local.Profile(),
		DataDir: s.o.StateDir, Logger: s.log, Getenv: s.o.Getenv, LookupPath: exec.LookPath, Overrides: s.o.Overrides,
	}
	for _, mod := range harnesses.Modules() {
		d := mod.Descriptor
		if s.composed[d.Kind] || !local.Allows(d).Available || !slices.Contains(serverKinds, d.Kind) {
			continue
		}
		composition, err := mod.Compose(ctx, p)
		if err != nil {
			if composition.Close != nil {
				composition.Close()
			}
			var unavailable *db.ConflictError
			if errors.As(err, &unavailable) && unavailable.Code == workerchan.CodeOperationUnavailable {
				s.log.Warn("main machine does not offer a required setup operation", "kind", d.Kind, "err", err)
				continue
			}
			return fmt.Errorf("%s: %w", d.Name, err)
		}
		if composition.Close != nil {
			context.AfterFunc(ctx, composition.Close)
		}
		local.SetConnector(d.Kind, composition.Connector)
		s.composed[d.Kind] = true
	}
	return nil
}
