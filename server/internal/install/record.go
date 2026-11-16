package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const recordSchema = 1

// Record states.
const (
	StateActive     = "active"
	StateActivating = "activating"
)

// Record is install.json: what this installation is and which files
// outside its app directory it owns (docs/install.md#layout).
type Record struct {
	Schema      int     `json:"schema"`
	Identity    string  `json:"identity"`
	Channel     string  `json:"channel"`
	Mode        string  `json:"mode"`
	Version     string  `json:"version"`
	Commit      string  `json:"commit"`
	CLIName     string  `json:"cliName"`
	App         string  `json:"app"`
	CLI         string  `json:"cli"`
	Link        string  `json:"link"`
	Entry       string  `json:"entry,omitempty"`
	Files       []Owned `json:"files"`
	State       string  `json:"state"`
	From        string  `json:"from,omitempty"`
	To          string  `json:"to,omitempty"`
	InstalledAt string  `json:"installedAt"`
}

// Owned is a file outside the app directory that the installer wrote.
type Owned struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // symlink | file
	Target string `json:"target,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// readRecord reads path; a missing file is (nil, nil).
func readRecord(path string) (*Record, error) {
	data, err := readCapped(path, metaMax)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != recordSchema {
		return nil, fmt.Errorf("%s: schema %d, this Stage reads %d", path, r.Schema, recordSchema)
	}
	return &r, nil
}

func writeRecord(path string, r *Record) error {
	r.Schema = recordSchema
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

// owns reports whether r recorded o's path with the same fingerprint as
// is on disk now.
func (r *Record) owned(path string) (Owned, bool) {
	if r == nil {
		return Owned{}, false
	}
	for _, o := range r.Files {
		if o.Path == path {
			return o, true
		}
	}
	return Owned{}, false
}

// fingerprint describes what is at path now ("" kind: nothing there).
func fingerprint(path string) (Owned, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Owned{Path: path}, nil
	}
	if err != nil {
		return Owned{}, err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(path)
		if err != nil {
			return Owned{}, err
		}
		return Owned{Path: path, Kind: "symlink", Target: t}, nil
	case st.Mode().IsRegular():
		sum, _, err := fileSHA256(path)
		if err != nil {
			return Owned{}, err
		}
		return Owned{Path: path, Kind: "file", SHA256: sum}, nil
	default:
		return Owned{Path: path, Kind: "other"}, nil
	}
}

func same(a, b Owned) bool {
	return a.Kind == b.Kind && a.Target == b.Target && a.SHA256 == b.SHA256
}

// writeFileAtomic writes b to path through a temp file, fsync and rename.
func writeFileAtomic(path string, b []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	if werr == nil {
		werr = f.Chmod(perm)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// symlinkAtomic points path at target through a temp link and a rename.
func symlinkAtomic(target, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".stage-tmp")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}
