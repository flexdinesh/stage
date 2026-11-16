// Package logfile is a size-rotated log file: the macOS managed server's
// own log (launchd does not rotate StandardOutPath files; docs/service.md).
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/flexdinesh/stage/server/internal/listen"
)

// Defaults for the managed server's log: stage.log plus two rotated files
// (stage.log.1, stage.log.2), each up to 10 MiB.
const (
	DefaultMaxBytes = 10 << 20
	DefaultKeep     = 2
)

// Rotator is an io.Writer appending to Path. A write that would grow the
// file past MaxBytes first renames Path to Path.1 (shifting older files up
// to Path.<Keep>, dropping the oldest). Files are 0600 in a 0700 directory.
// Safe for concurrent use.
type Rotator struct {
	Path     string
	MaxBytes int64
	Keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// Open creates path's directory (0700, owned by this user) and opens path
// for appending.
func Open(path string, maxBytes int64, keep int) (*Rotator, error) {
	if err := listen.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}
	r := &Rotator{Path: path, MaxBytes: maxBytes, Keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Rotator) open() error {
	f, err := os.OpenFile(r.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

// Write implements io.Writer.
func (r *Rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.MaxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *Rotator) rotate() error {
	_ = r.f.Close()
	r.f = nil
	for i := r.Keep; i >= 1; i-- {
		src := r.Path
		if i > 1 {
			src = fmt.Sprintf("%s.%d", r.Path, i-1)
		}
		_ = os.Rename(src, fmt.Sprintf("%s.%d", r.Path, i))
	}
	if r.Keep < 1 {
		_ = os.Remove(r.Path)
	}
	return r.open()
}

// Close closes the file.
func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
