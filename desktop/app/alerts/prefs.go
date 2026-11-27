package alerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// prefsVersion is the schema version of notifications.json; version 1
// (one server) migrates to this machine's entry.
const prefsVersion = 2

// maxShown caps ServerPrefs.Shown.
const maxShown = 50

// Prefs is <userData>/notifications.json (docs/desktop.md): the device
// toggle and, per server id, its stream cursor and shown notifications.
//
//	{"version":2,"enabled":true,"denied":false,"servers":{"local":{"cursor":123,"shown":["local/task-7"]}}}
type Prefs struct {
	Version int `json:"version"`
	// Enabled is the device toggle (on by default).
	Enabled bool `json:"enabled"`
	// Denied: the OS refused the last Show (ErrDenied).
	Denied bool `json:"denied"`
	// Servers holds each server's stream state by server id.
	Servers map[string]*ServerPrefs `json:"servers"`

	// migrated: loaded from version 1 (the next save writes version 2).
	migrated bool
}

// ServerPrefs is one server's stream state.
type ServerPrefs struct {
	// SessionScope binds the persisted state to the target and sign-in.
	SessionScope string `json:"sessionScope,omitempty"`
	// Cursor is the last notification id handled; nil before the first
	// hello.
	Cursor *int64 `json:"cursor,omitempty"`
	// Shown are notification IDs shown and not yet closed, oldest first,
	// at most 50, so a later run can close stale ones (version 1 IDs keep
	// their bare form, "task-7").
	Shown []string `json:"shown"`
}

// prefsV1 is the version 1 file (one server).
type prefsV1 struct {
	Enabled bool     `json:"enabled"`
	Cursor  *int64   `json:"cursor,omitempty"`
	Denied  bool     `json:"denied"`
	Shown   []string `json:"shown"`
}

// DefaultPrefs are the preferences of a fresh profile.
func DefaultPrefs() Prefs {
	return Prefs{Version: prefsVersion, Enabled: true, Servers: map[string]*ServerPrefs{}}
}

func (sp *ServerPrefs) clone() *ServerPrefs {
	out := &ServerPrefs{SessionScope: sp.SessionScope, Shown: slices.Clone(sp.Shown)}
	if sp.Cursor != nil {
		c := *sp.Cursor
		out.Cursor = &c
	}
	if out.Shown == nil {
		out.Shown = []string{}
	}
	if len(out.Shown) > maxShown {
		out.Shown = slices.Clone(out.Shown[len(out.Shown)-maxShown:])
	}
	return out
}

func (p Prefs) clone() Prefs {
	servers := make(map[string]*ServerPrefs, len(p.Servers))
	for id, sp := range p.Servers {
		if sp != nil {
			servers[id] = sp.clone()
		}
	}
	p.Servers = servers
	return p
}

// server is the entry of id, created when missing.
func (p *Prefs) server(id string) *ServerPrefs {
	if p.Servers == nil {
		p.Servers = map[string]*ServerPrefs{}
	}
	sp := p.Servers[id]
	if sp == nil {
		sp = &ServerPrefs{Shown: []string{}}
		p.Servers[id] = sp
	}
	return sp
}

// addShown appends id (moving it to the end) and keeps the newest maxShown.
func (sp *ServerPrefs) addShown(id string) {
	sp.Shown = slices.DeleteFunc(sp.Shown, func(s string) bool { return s == id })
	sp.Shown = append(sp.Shown, id)
	if n := len(sp.Shown); n > maxShown {
		sp.Shown = slices.Clone(sp.Shown[n-maxShown:])
	}
}

// removeShown drops id; it reports whether it was there.
func (sp *ServerPrefs) removeShown(id string) bool {
	n := len(sp.Shown)
	sp.Shown = slices.DeleteFunc(sp.Shown, func(s string) bool { return s == id })
	return len(sp.Shown) != n
}

// LoadPrefs reads path. A missing file is DefaultPrefs without error; a
// version 1 file migrates to this machine's entry ("local"); an
// unreadable, corrupt or unknown-version file is DefaultPrefs with the
// error (the caller logs it and the next save replaces the file).
func LoadPrefs(path string) (Prefs, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultPrefs(), nil
	}
	if err != nil {
		return DefaultPrefs(), err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return DefaultPrefs(), fmt.Errorf("%s: %w", path, err)
	}
	p := DefaultPrefs()
	switch head.Version {
	case 1:
		v1 := prefsV1{Enabled: true}
		if err := json.Unmarshal(b, &v1); err != nil {
			return DefaultPrefs(), fmt.Errorf("%s: %w", path, err)
		}
		p.Enabled, p.Denied, p.migrated = v1.Enabled, v1.Denied, true
		p.Servers[target.LocalID] = &ServerPrefs{Cursor: v1.Cursor, Shown: v1.Shown}
	case prefsVersion:
		if err := json.Unmarshal(b, &p); err != nil {
			return DefaultPrefs(), fmt.Errorf("%s: %w", path, err)
		}
	default:
		return DefaultPrefs(), fmt.Errorf("%s: unsupported version %d", path, head.Version)
	}
	p.Version = prefsVersion
	for id, sp := range p.Servers {
		if sp == nil || id == "" || strings.Contains(id, "/") {
			delete(p.Servers, id)
			continue
		}
		if sp.Cursor != nil && *sp.Cursor < 0 {
			sp.Cursor = nil
		}
	}
	return p.clone(), nil
}

// SavePrefs writes p to path atomically (temp file, fsync, 0600, rename).
func SavePrefs(path string, p Prefs) error {
	p = p.clone()
	p.Version = prefsVersion
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
