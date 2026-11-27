// Package workersetup composes harness modules on a worker. The main server
// records machine-scoped settings and identity, and secrets stay on the worker.
package workersetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/machine"
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
	// CacheIdentity is a non-secret fingerprint of this join. Empty disables
	// the offline setup cache.
	CacheIdentity string
}

// Setup owns the worker's installed harnesses across channel reconnects.
type Setup struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	composed map[string]*installed
	cache    cache
}

type installed struct {
	input   remote.SetupInputs
	offline bool
	dirty   atomic.Bool
	stop    func() bool
	close   func()

	mu     sync.Mutex
	id     int64
	closed bool
}

// Attach arranges for the worker to compose its harnesses before ready.
func Attach(o Options) *Setup {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	s := &Setup{o: o, log: o.Logger.With("component", "workersetup"), composed: map[string]*installed{}}
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

// Restore composes offline adapters before the first channel connection. The
// worker can still connect and recover from a missing or invalid cache.
func (s *Setup) Restore(ctx context.Context) error {
	if s.o.CacheIdentity == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inputs, err := s.readCache()
	if err != nil {
		return err
	}
	s.cache.mu.Lock()
	s.cache.saved = inputs
	s.cache.mu.Unlock()
	var restoreErr error
	for _, mod := range harnesses.Modules() {
		kind := mod.Descriptor.Kind
		input, ok := inputs[kind]
		if !ok || !s.o.Host.Local().Allows(mod.Descriptor).Available || s.composed[kind] != nil {
			continue
		}
		if err := s.install(ctx, mod, input, true); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
	}
	return restoreErr
}

func (s *Setup) compose(ctx context.Context, serverKinds []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	kinds := []string{}
	modules := harnesses.Modules()
	for _, mod := range modules {
		d := mod.Descriptor
		if s.o.Host.Local().Allows(d).Available && slices.Contains(serverKinds, d.Kind) {
			kinds = append(kinds, d.Kind)
		}
	}

	var inputs remote.SetupSnapshot
	if s.o.CacheIdentity != "" {
		var err error
		inputs, err = s.reconcileCache(ctx, kinds)
		if err != nil {
			return fmt.Errorf("read authoritative harness setup: %w", err)
		}
		for kind, current := range s.composed {
			next, ok := inputs[kind]
			if !ok || current.offline || current.dirty.Load() || !sameInputs(current.input, next) {
				s.remove(kind)
			}
		}
	}
	for _, mod := range modules {
		kind := mod.Descriptor.Kind
		if s.composed[kind] != nil || !slices.Contains(kinds, kind) {
			continue
		}
		if err := s.install(ctx, mod, inputs[kind], false); err != nil {
			if operationUnavailable(err) {
				s.log.Warn("main machine does not offer a required setup operation", "kind", kind, "err", err)
				continue
			}
			return err
		}
	}
	return nil
}

func (s *Setup) install(ctx context.Context, mod harness.Module, input remote.SetupInputs, offline bool) error {
	ctx, cancel := context.WithCancel(ctx)
	local, ports := s.o.Host.Local(), s.o.Ports
	kind := mod.Descriptor.Kind
	state := &installed{input: input, offline: offline}
	if s.o.CacheIdentity != "" && !offline {
		up := ports.Call
		ports.Call = func(callCtx context.Context, op string, body any) (json.RawMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			reply, err := up(callCtx, op, body)
			switch op {
			case remote.KindSettingsSave, remote.KindIdentitySet, remote.KindIdentitySwitch:
				state.dirty.Store(true)
				s.refreshCache(ctx)
			}
			return reply, err
		}
	}
	p := harness.Ports{
		Settings: ports, Secrets: ports.Creds, Identity: ports, SetLastError: ports.SetLastError,
		Install:   func(old, next int64, a execution.Adapter) { state.install(local, old, next, a) },
		OnChanged: s.o.Host.Notify, Profile: local.Profile(),
		DataDir: s.o.StateDir, Logger: s.log, Getenv: s.o.Getenv, LookupPath: exec.LookPath, Overrides: s.o.Overrides,
	}
	if offline {
		cached := cachedPorts{input}
		p.Settings, p.Identity, p.SetLastError = cached, cached, nil
	}
	composition, err := mod.Compose(ctx, p)
	state.close = sync.OnceFunc(func() {
		cancel()
		state.uninstall(local)
		if composition.Close != nil {
			composition.Close()
		}
	})
	if err != nil {
		state.close()
		return fmt.Errorf("%s: %w", mod.Descriptor.Name, err)
	}
	state.stop = context.AfterFunc(ctx, state.close)
	local.SetConnector(kind, composition.Connector)
	s.composed[kind] = state
	return nil
}

func (s *Setup) remove(kind string) {
	state := s.composed[kind]
	state.stop()
	s.o.Host.Local().SetConnector(kind, nil)
	state.close()
	delete(s.composed, kind)
}

func (a *installed) install(local *machine.Local, old, next int64, adapter execution.Adapter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.id = next
	if adapter == nil {
		a.id = 0
	}
	local.Install(old, next, adapter)
}

func (a *installed) uninstall(local *machine.Local) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.id != 0 {
		local.Retired(a.id)
	}
}

func operationUnavailable(err error) bool {
	var unavailable *db.ConflictError
	return errors.As(err, &unavailable) && unavailable.Code == workerchan.CodeOperationUnavailable
}

func (s *Setup) reconcileCache(ctx context.Context, kinds []string) (remote.SetupSnapshot, error) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	inputs, err := s.fetchInputs(ctx, kinds)
	if err != nil {
		return nil, err
	}
	s.cache.kinds = kinds
	s.saveCache(inputs)
	return inputs, nil
}

func (s *Setup) refreshCache(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	inputs, err := s.fetchInputs(ctx, s.cache.kinds)
	if err != nil {
		s.log.Warn("harness setup cache refresh failed and will retry on reconnect", "err", err)
		return
	}
	s.saveCache(inputs)
}

func (s *Setup) fetchInputs(ctx context.Context, kinds []string) (remote.SetupSnapshot, error) {
	var inputs remote.SetupSnapshot
	ports := s.o.Ports
	err := ports.call(ctx, remote.KindSetupSnapshot, remote.SetupSnapshotRequest{Kinds: kinds}, &inputs)
	if operationUnavailable(err) {
		// Older servers can still answer the individual setup reads.
		inputs = make(remote.SetupSnapshot, len(kinds))
		for _, kind := range kinds {
			settings, err := ports.Get(ctx, kind)
			if err != nil {
				return nil, err
			}
			id, identity, err := ports.ActiveIdentity(ctx, kind)
			if err != nil {
				return nil, err
			}
			inputs[kind] = remote.SetupInputs{Settings: settings, Connection: remote.ActiveIdentityResult{ID: id, Identity: identity}}
		}
	} else if err != nil {
		return nil, err
	}
	if err := validateInputs(inputs); err != nil {
		return nil, err
	}
	if len(inputs) != len(kinds) {
		return nil, errors.New("incomplete harness setup snapshot")
	}
	for _, kind := range kinds {
		if _, ok := inputs[kind]; !ok {
			return nil, fmt.Errorf("harness setup snapshot is missing %s", kind)
		}
	}
	return inputs, nil
}
