package alerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// prefsVersion is the schema version of notifications.json.
const prefsVersion = 1

// maxShown caps Prefs.Shown.
const maxShown = 50

// Prefs is <userData>/notifications.json (docs/desktop.md):
//
//	{"version":1,"enabled":true,"cursor":123,"denied":false,"shown":["task-7"]}
type Prefs struct {
	Version int `json:"version"`
	// Enabled is the device toggle (on by default).
	Enabled bool `json:"enabled"`
	// Cursor is the last notification id handled; nil before the first
	// hello.
	Cursor *int64 `json:"cursor,omitempty"`
	// Denied: the OS refused the last Show (ErrDenied).
	Denied bool `json:"denied"`
	// Shown are notification IDs shown and not yet closed, oldest first,
	// at most 50, so a later run can close stale ones.
	Shown []string `json:"shown"`
}

// DefaultPrefs are the preferences of a fresh profile.
func DefaultPrefs() Prefs {
	return Prefs{Version: prefsVersion, Enabled: true, Shown: []string{}}
}

func (p Prefs) clone() Prefs {
	if p.Cursor != nil {
		c := *p.Cursor
		p.Cursor = &c
	}
	p.Shown = slices.Clone(p.Shown)
	if p.Shown == nil {
		p.Shown = []string{}
	}
	return p
}

// addShown appends id (moving it to the end) and keeps the newest maxShown.
func (p *Prefs) addShown(id string) {
	p.Shown = slices.DeleteFunc(p.Shown, func(s string) bool { return s == id })
	p.Shown = append(p.Shown, id)
	if n := len(p.Shown); n > maxShown {
		p.Shown = slices.Clone(p.Shown[n-maxShown:])
	}
}

// removeShown drops id; it reports whether it was there.
func (p *Prefs) removeShown(id string) bool {
	n := len(p.Shown)
	p.Shown = slices.DeleteFunc(p.Shown, func(s string) bool { return s == id })
	return len(p.Shown) != n
}

// LoadPrefs reads path. A missing file is DefaultPrefs without error; an
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
	p := DefaultPrefs()
	if err := json.Unmarshal(b, &p); err != nil {
		return DefaultPrefs(), fmt.Errorf("%s: %w", path, err)
	}
	if p.Version != prefsVersion {
		return DefaultPrefs(), fmt.Errorf("%s: unsupported version %d", path, p.Version)
	}
	if p.Cursor != nil && *p.Cursor < 0 {
		p.Cursor = nil
	}
	if len(p.Shown) > maxShown {
		p.Shown = p.Shown[len(p.Shown)-maxShown:]
	}
	return p.clone(), nil
}

// SavePrefs writes p to path atomically (temp file, fsync, 0600, rename).
func SavePrefs(path string, p Prefs) error {
	p = p.clone()
	p.Version = prefsVersion
	if len(p.Shown) > maxShown {
		p.Shown = p.Shown[len(p.Shown)-maxShown:]
	}
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
