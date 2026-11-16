package target

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// storeVersion is the schema version of servers.json.
const storeVersion = 1

// maxName caps a server name.
const maxName = 60

// Saved is one saved remote server in servers.json.
type Saved struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// file is <userData>/servers.json (docs/desktop.md, Servers):
//
//	{"version":1,"active":"local","servers":[{"id":"s-1a2b3c4d","name":"Studio","url":"http://192.168.1.20:7380"}]}
type file struct {
	Version int     `json:"version"`
	Active  string  `json:"active"`
	Servers []Saved `json:"servers"`
}

// Errors of the store's edits.
var (
	ErrNotFound    = errors.New("no such server")
	ErrLocal       = errors.New("this machine's server cannot be changed or removed")
	ErrInvalidName = errors.New("enter a name")
)

// Store is the saved server list. The local target is implicit: always
// first, never saved, never removed. Its methods are safe for concurrent
// use; every edit is written at once (atomically, 0600).
type Store struct {
	path string

	mu  sync.Mutex
	doc file
}

// Open loads path. A missing file is the local target alone without
// error; a corrupt or unknown-version file is the same with the error
// (the caller logs it; the next edit replaces the file). "" keeps the
// list in memory only.
func Open(path string) (*Store, error) {
	s := &Store{path: path, doc: file{Version: storeVersion, Active: LocalID, Servers: []Saved{}}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var doc file
	if err := json.Unmarshal(b, &doc); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if doc.Version != storeVersion {
		return s, fmt.Errorf("%s: unsupported version %d", path, doc.Version)
	}
	seen := map[string]bool{LocalID: true}
	for _, sv := range doc.Servers {
		u, uerr := NormalizeURL(sv.URL)
		if !validID(sv.ID) || seen[sv.ID] || uerr != nil || strings.TrimSpace(sv.Name) == "" {
			err = errors.Join(err, fmt.Errorf("%s: dropped invalid server %q", path, sv.ID))
			continue
		}
		seen[sv.ID] = true
		s.doc.Servers = append(s.doc.Servers, Saved{ID: sv.ID, Name: strings.TrimSpace(sv.Name), URL: u})
	}
	if seen[doc.Active] {
		s.doc.Active = doc.Active
	}
	return s, err
}

// validID: an id never contains "/" (notification ids are <id>/<tag>).
func validID(id string) bool {
	if id == "" || id == LocalID || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "s-" + hex.EncodeToString(b[:])
}

func (sv Saved) target() Target {
	return Target{ID: sv.ID, Name: sv.Name, Kind: KindRemote, URL: sv.URL}
}

// List is every target, local first, then the saved ones in the order
// added.
func (s *Store) List() []Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Target{Local("")}
	for _, sv := range s.doc.Servers {
		out = append(out, sv.target())
	}
	return out
}

// Get is the target with id.
func (s *Store) Get(id string) (Target, bool) {
	if id == LocalID {
		return Local(""), true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sv := range s.doc.Servers {
		if sv.ID == id {
			return sv.target(), true
		}
	}
	return Target{}, false
}

// Active is the target the app connects to (local by default).
func (s *Store) Active() Target {
	s.mu.Lock()
	id := s.doc.Active
	s.mu.Unlock()
	if t, ok := s.Get(id); ok {
		return t
	}
	return Local("")
}

// SetActive makes id the active target and saves it.
func (s *Store) SetActive(id string) error {
	if _, ok := s.Get(id); !ok {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc.Active == id {
		return nil
	}
	prev := s.doc.Active
	s.doc.Active = id
	if err := s.saveLocked(); err != nil {
		s.doc.Active = prev
		return err
	}
	return nil
}

// Add saves a remote server.
func (s *Store) Add(name, rawURL string) (Target, error) {
	name, u, err := s.check("", name, rawURL)
	if err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sv := Saved{ID: newID(), Name: name, URL: u}
	for s.hasIDLocked(sv.ID) {
		sv.ID = newID()
	}
	prev := slices.Clone(s.doc.Servers)
	s.doc.Servers = append(s.doc.Servers, sv)
	if err := s.saveLocked(); err != nil {
		s.doc.Servers = prev
		return Target{}, err
	}
	return sv.target(), nil
}

// Update renames a saved server or changes its URL.
func (s *Store) Update(id, name, rawURL string) (Target, error) {
	if id == LocalID {
		return Target{}, ErrLocal
	}
	name, u, err := s.check(id, name, rawURL)
	if err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.doc.Servers, func(sv Saved) bool { return sv.ID == id })
	if i < 0 {
		return Target{}, ErrNotFound
	}
	prev := s.doc.Servers[i]
	s.doc.Servers[i] = Saved{ID: id, Name: name, URL: u}
	if err := s.saveLocked(); err != nil {
		s.doc.Servers[i] = prev
		return Target{}, err
	}
	return s.doc.Servers[i].target(), nil
}

// Remove deletes a saved server; when it was active, local becomes
// active.
func (s *Store) Remove(id string) error {
	if id == LocalID {
		return ErrLocal
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.doc.Servers, func(sv Saved) bool { return sv.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	prev, prevActive := slices.Clone(s.doc.Servers), s.doc.Active
	s.doc.Servers = slices.Delete(s.doc.Servers, i, i+1)
	if s.doc.Active == id {
		s.doc.Active = LocalID
	}
	if err := s.saveLocked(); err != nil {
		s.doc.Servers, s.doc.Active = prev, prevActive
		return err
	}
	return nil
}

func (s *Store) hasIDLocked(id string) bool {
	return slices.ContainsFunc(s.doc.Servers, func(sv Saved) bool { return sv.ID == id })
}

// check validates a name and URL for server id ("" for a new one): names
// and URLs are unique (names case-insensitively, including "This
// machine").
func (s *Store) check(id, name, rawURL string) (string, string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", "", ErrInvalidName
	case len([]rune(name)) > maxName:
		return "", "", fmt.Errorf("the name is longer than %d characters", maxName)
	case strings.EqualFold(name, LocalName):
		return "", "", fmt.Errorf("%q is this machine's server; pick another name", name)
	}
	u, err := NormalizeURL(rawURL)
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sv := range s.doc.Servers {
		if sv.ID == id {
			continue
		}
		if strings.EqualFold(sv.Name, name) {
			return "", "", fmt.Errorf("a server named %q is already saved", sv.Name)
		}
		if sv.URL == u {
			return "", "", fmt.Errorf("%s is already saved as %q", u, sv.Name)
		}
	}
	return name, u, nil
}

// saveLocked writes the list atomically (temp file, fsync, 0600, rename).
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(s.doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	ok = true
	return nil
}
