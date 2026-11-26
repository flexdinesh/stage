package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/flexdinesh/stage/server/internal/harness"
)

// CredentialsFile holds the machine's harness credentials, in the worker
// dir (0600). They never leave the worker (ADR 0002): the main server
// keeps only connection identity, status and non-secret settings. Join and
// leave delete it.
const CredentialsFile = "credentials.json"

// CredentialsSchema versions credentials.json.
const CredentialsSchema = 2

// Credentials holds secrets by harness kind and key.
type Credentials struct {
	Schema    int                                   `json:"schema"`
	Harnesses map[string]map[string]json.RawMessage `json:"harnesses,omitempty"`
}

// LegacySecret maps a schema 1 entry to its schema 2 kind and key.
type LegacySecret = harness.LegacySecret

// CredentialStore reads and writes a worker dir's credentials file. Safe
// for concurrent use.
type CredentialStore struct {
	path   string
	legacy []LegacySecret
}

var _ harness.Secrets = (*CredentialStore)(nil)

// The worker directory lock excludes other worker processes. This lock
// serializes access even when separate store handles refer to the same file.
var credentialsMu sync.Mutex

// NewCredentialStore is the credentials file of the worker config at
// configPath. Legacy mappings are supplied by the credential owners.
func NewCredentialStore(configPath string, legacy ...LegacySecret) *CredentialStore {
	return &CredentialStore{
		path:   filepath.Join(Dir(configPath), CredentialsFile),
		legacy: slices.Clone(legacy),
	}
}

// Path is the credentials file.
func (s *CredentialStore) Path() string { return s.path }

// Get returns a credential as raw JSON, or nil when it is missing.
func (s *CredentialStore) Get(ctx context.Context, kind, key string) (json.RawMessage, error) {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	c, upgrade, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if upgrade {
		if err := s.save(ctx, c); err != nil {
			return nil, err
		}
	}
	return c.Harnesses[kind][key], nil
}

// Set saves a credential as raw JSON (0600).
func (s *CredentialStore) Set(ctx context.Context, kind, key string, value json.RawMessage) error {
	if !json.Valid(value) {
		return fmt.Errorf("%s: invalid JSON for %s/%s", CredentialsFile, kind, key)
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	c, _, err := s.load(ctx)
	if err != nil {
		return err
	}
	c.set(kind, key, value)
	return s.save(ctx, c)
}

// Delete removes a credential. A missing credential is already deleted.
func (s *CredentialStore) Delete(ctx context.Context, kind, key string) error {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	c, upgrade, err := s.load(ctx)
	if err != nil {
		return err
	}
	keys := c.Harnesses[kind]
	if _, ok := keys[key]; !ok && !upgrade {
		return nil
	}
	delete(keys, key)
	if len(keys) == 0 {
		delete(c.Harnesses, kind)
	}
	return s.save(ctx, c)
}

func (c *Credentials) set(kind, key string, value json.RawMessage) {
	if c.Harnesses == nil {
		c.Harnesses = make(map[string]map[string]json.RawMessage)
	}
	if c.Harnesses[kind] == nil {
		c.Harnesses[kind] = make(map[string]json.RawMessage)
	}
	c.Harnesses[kind][key] = value
}

func (s *CredentialStore) load(ctx context.Context) (Credentials, bool, error) {
	if err := ctx.Err(); err != nil {
		return Credentials{}, false, err
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Credentials{Schema: CredentialsSchema}, false, nil
	}
	if err != nil {
		return Credentials{}, false, err
	}
	c, upgrade, err := s.decode(b)
	if err != nil {
		return Credentials{}, false, fmt.Errorf("%s: %w", CredentialsFile, err)
	}
	return c, upgrade, nil
}

func (s *CredentialStore) decode(b []byte) (Credentials, bool, error) {
	var file struct {
		Schema    int             `json:"schema"`
		Harnesses json.RawMessage `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return Credentials{}, false, err
	}
	c := Credentials{Schema: CredentialsSchema}
	switch file.Schema {
	case CredentialsSchema:
		if len(file.Harnesses) > 0 {
			if err := json.Unmarshal(file.Harnesses, &c.Harnesses); err != nil {
				return Credentials{}, false, err
			}
			if c.Harnesses == nil {
				return Credentials{}, false, errors.New("harnesses must be an object")
			}
			for kind, keys := range c.Harnesses {
				if keys == nil {
					return Credentials{}, false, fmt.Errorf("harness %q must be an object", kind)
				}
			}
		}
		return c, false, nil
	case 1:
		c, err := s.migrate(file.Harnesses)
		return c, true, err
	default:
		return Credentials{}, false, fmt.Errorf("unsupported schema %d", file.Schema)
	}
}

func (s *CredentialStore) migrate(raw json.RawMessage) (Credentials, error) {
	c := Credentials{Schema: CredentialsSchema}
	if len(raw) == 0 {
		return c, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return Credentials{}, err
	}
	if entries == nil {
		return Credentials{}, errors.New("harnesses must be an object")
	}
	mappings := make(map[string]LegacySecret, len(s.legacy))
	for _, mapping := range s.legacy {
		if _, exists := mappings[mapping.Entry]; exists {
			return Credentials{}, fmt.Errorf("duplicate legacy mapping for %q", mapping.Entry)
		}
		mappings[mapping.Entry] = mapping
	}
	for entry, value := range entries {
		mapping, ok := mappings[entry]
		if !ok {
			return Credentials{}, fmt.Errorf("no legacy mapping for %q", entry)
		}
		if _, exists := c.Harnesses[mapping.Kind][mapping.Key]; exists {
			return Credentials{}, fmt.Errorf("multiple legacy entries map to %s/%s", mapping.Kind, mapping.Key)
		}
		if mapping.Validate != nil && mapping.Validate(value) != nil {
			return Credentials{}, fmt.Errorf("invalid legacy credential %q", entry)
		}
		c.set(mapping.Kind, mapping.Key, value)
	}
	return c, nil
}

func (s *CredentialStore) save(ctx context.Context, c Credentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(b, '\n'), 0o600)
}
