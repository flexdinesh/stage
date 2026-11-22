package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// CredentialsFile holds the machine's harness credentials, in the worker
// dir (0600). They never leave the worker (ADR 0002): the main server
// keeps only connection identity, status and non-secret settings. Join and
// leave delete it.
const CredentialsFile = "credentials.json"

// CredentialsSchema versions credentials.json.
const CredentialsSchema = 1

// Credentials are the harness secrets of this machine's connections, by
// harness kind; each harness's connection setup owns its entry's shape.
type Credentials struct {
	Schema    int                        `json:"schema"`
	Harnesses map[string]json.RawMessage `json:"harnesses,omitempty"`
}

// CredentialStore reads and writes a worker dir's credentials file. Safe
// for concurrent use.
type CredentialStore struct {
	path string
	mu   sync.Mutex
}

// NewCredentialStore is the credentials file of the worker config at
// configPath.
func NewCredentialStore(configPath string) *CredentialStore {
	return &CredentialStore{path: filepath.Join(Dir(configPath), CredentialsFile)}
}

// Path is the credentials file.
func (s *CredentialStore) Path() string { return s.path }

// Get decodes kind's credential into out (left as is when there is none).
func (s *CredentialStore) Get(kind string, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load()
	if err != nil {
		return err
	}
	raw, ok := c.Harnesses[kind]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %s: %w", CredentialsFile, kind, err)
	}
	return nil
}

// Set saves v as kind's credential (0600).
func (s *CredentialStore) Set(kind string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load()
	if err != nil {
		return err
	}
	if c.Harnesses == nil {
		c.Harnesses = map[string]json.RawMessage{}
	}
	c.Harnesses[kind] = raw
	c.Schema = CredentialsSchema
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(b, '\n'), 0o600)
}

func (s *CredentialStore) load() (Credentials, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Credentials{Schema: CredentialsSchema}, nil
	}
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, fmt.Errorf("%s: %w", CredentialsFile, err)
	}
	return c, nil
}
