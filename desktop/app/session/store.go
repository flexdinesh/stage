// Package session owns the desktop's upstream Stage browser sessions.
package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

type binding struct {
	Endpoint     string `json:"endpoint"`
	Canonical    string `json:"canonical"`
	Installation string `json:"installation"`
}

type credential struct {
	Binding binding   `json:"binding"`
	Value   string    `json:"session"`
	Expires time.Time `json:"expires"`
	Scope   string    `json:"scope"`
}

type slot struct {
	credential
	ctx    context.Context
	cancel context.CancelFunc
}

// Store keeps one credential per saved target. The slot pointer identifies
// the generation, so a late response cannot change a more recent session.
type Store struct {
	mu      sync.Mutex
	dir     string
	slots   map[string]*slot
	changed func(target.Target)
}

// Open uses an owner-only subdirectory. An empty dir is for in-memory and
// test use. The callback runs after the store releases its lock.
func Open(dir string, changed func(target.Target)) (*Store, error) {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := private(dir, 0o700, true); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, slots: map[string]*slot{}, changed: changed}, nil
}

func private(path string, mode os.FileMode, dir bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() != dir || (!dir && !info.Mode().IsRegular()) || info.Mode().Perm() != mode || !ownedByUser(info) {
		return errors.New("Stage session storage must be owned by this user and private")
	}
	return nil
}

func endpoint(t target.Target) string {
	if t.Remote() {
		return t.URL
	}
	return "unix:" + t.Socket
}

func newSlot(c credential) *slot {
	s := &slot{credential: c}
	if c.Value == "" {
		s.ctx, s.cancel = context.WithCancel(context.Background())
	} else {
		s.ctx, s.cancel = context.WithDeadline(context.Background(), c.Expires)
	}
	return s
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
}

func (s *Store) read(id string) credential {
	var c credential
	if s.dir != "" && private(s.path(id), 0o600, false) == nil {
		if b, err := os.ReadFile(s.path(id)); err == nil {
			if err := json.Unmarshal(b, &c); err != nil || !validToken(c.Value) || c.Scope == "" {
				return credential{}
			}
		}
	}
	return c
}

func (s *Store) current(t target.Target) *slot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots[t.ID] == nil {
		s.slots[t.ID] = newSlot(s.read(t.ID))
	}
	return s.slots[t.ID]
}

var errChanged = errors.New("Stage session changed. Try again.")

func (s *Store) replace(ctx context.Context, t target.Target, old *slot, c credential) error {
	s.mu.Lock()
	if ctx.Err() != nil || s.slots[t.ID] != old {
		s.mu.Unlock()
		return errChanged
	}
	err := s.persist(t.ID, c)
	if err == nil || c.Value == "" {
		old.cancel()
		s.slots[t.ID] = newSlot(c)
	}
	s.mu.Unlock()
	if (err == nil || c.Value == "") && s.changed != nil {
		s.changed(t)
	}
	return err
}

// Forget clears a removed or edited target, including an in-flight login.
func (s *Store) Forget(t target.Target) error {
	old := s.current(t)
	if old.Value != "" && old.Binding.Endpoint != endpoint(t) {
		return nil
	}
	return s.replace(context.Background(), t, old, credential{})
}

func (s *Store) persist(id string, c credential) error {
	if s.dir == "" {
		return nil
	}
	if c.Value == "" {
		return removeFile(s.path(id))
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path(id))
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
