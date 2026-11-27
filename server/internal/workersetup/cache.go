package workersetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/remote"
)

const (
	cacheFile   = "settings-cache.json"
	cacheSchema = 1
)

// ErrNoCache means this worker has no snapshot to restore yet.
var ErrNoCache = errors.New("no harness setup cache yet. Connect to the Stage server to initialize it")

var errCachedSetup = errors.New("reconnect to the Stage server to change cached harness setup")

type cacheRecord struct {
	Schema  int                  `json:"schema"`
	Join    string               `json:"join"`
	Modules remote.SetupSnapshot `json:"modules"`
}

// cache serializes authoritative reads and writes. A refresh cannot replace a
// later snapshot with data read before it.
type cache struct {
	mu    sync.Mutex
	kinds []string
	saved remote.SetupSnapshot
}

func (s *Setup) cachePath() (string, error) {
	if s.o.StateDir == "" {
		return "", errors.New("worker state directory is required for the harness setup cache")
	}
	return filepath.Join(s.o.StateDir, cacheFile), nil
}

func (s *Setup) readCache() (remote.SetupSnapshot, error) {
	path, err := s.cachePath()
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCache
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("harness setup cache must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record cacheRecord
	if err := db.DecodeStrict(raw, &record); err != nil {
		return nil, errors.New("malformed harness setup cache")
	}
	if record.Schema != cacheSchema {
		return nil, errors.New("unsupported harness setup cache schema")
	}
	if record.Join != s.o.CacheIdentity {
		return nil, errors.New("harness setup cache belongs to another join")
	}
	if err := validateInputs(record.Modules); err != nil {
		return nil, err
	}
	return record.Modules, nil
}

func validateInputs(inputs remote.SetupSnapshot) error {
	if inputs == nil {
		return errors.New("missing harness setup snapshot")
	}
	ids := map[int64]bool{}
	valid := 0
	for _, mod := range harnesses.Modules() {
		kind := mod.Descriptor.Kind
		input, ok := inputs[kind]
		if !ok {
			continue
		}
		valid++
		if len(input.Settings) == 0 || mod.ValidateSettings(input.Settings) != nil {
			return fmt.Errorf("invalid public settings for %s", kind)
		}
		id, identity := input.Connection.ID, input.Connection.Identity
		if id < 0 || (id == 0 && identity != "") || ids[id] {
			return fmt.Errorf("invalid active connection for %s", kind)
		}
		if id != 0 {
			ids[id] = true
		}
	}
	if valid != len(inputs) {
		return errors.New("unknown module in harness setup snapshot")
	}
	return nil
}

func sameInputs(a, b remote.SetupInputs) bool {
	return a.Connection == b.Connection && bytes.Equal(a.Settings, b.Settings)
}

func (s *Setup) saveCache(inputs remote.SetupSnapshot) {
	if s.cache.saved != nil && maps.EqualFunc(s.cache.saved, inputs, sameInputs) {
		return
	}
	path, err := s.cachePath()
	if err == nil {
		err = writeCache(path, cacheRecord{Schema: cacheSchema, Join: s.o.CacheIdentity, Modules: inputs})
	}
	if err != nil {
		s.log.Warn("could not save harness setup cache. Check worker state directory permissions", "err", err)
		return
	}
	s.cache.saved = inputs
}

func writeCache(path string, record cacheRecord) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(record)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// cachedPorts supplies one module's last known authoritative setup.
type cachedPorts struct {
	input remote.SetupInputs
}

func (p cachedPorts) Get(context.Context, string) (json.RawMessage, error) {
	return bytes.Clone(p.input.Settings), nil
}

func (p cachedPorts) Active(context.Context, string) (int64, error) {
	return p.input.Connection.ID, nil
}

func (p cachedPorts) ActiveIdentity(context.Context, string) (int64, string, error) {
	return p.input.Connection.ID, p.input.Connection.Identity, nil
}

func (p cachedPorts) ConnectionExists(_ context.Context, id int64) (bool, error) {
	return id != 0 && id == p.input.Connection.ID, nil
}

func (cachedPorts) Save(context.Context, string, json.RawMessage) error {
	return errCachedSetup
}

func (cachedPorts) SetConnectionIdentity(context.Context, int64, string, string) error {
	return errCachedSetup
}

func (cachedPorts) SwitchInstance(context.Context, string, string, string, int64) (harness.Switch, error) {
	return harness.Switch{}, errCachedSetup
}

func (cachedPorts) RetiredConnections(context.Context, string) ([]int64, error) {
	return nil, errCachedSetup
}

func (cachedPorts) ConnectionEvidence(context.Context, int64) ([]harness.WorkspaceEvidence, []execution.Observation, bool, error) {
	return nil, nil, false, errCachedSetup
}
