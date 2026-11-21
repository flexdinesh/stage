// Package listen parses and opens the server's listen addresses
// (STAGE_LISTEN, `stage serve --listen`): TCP host:port, an explicit unix
// socket, or the private default socket (docs/runtime.md).
package listen

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/flexdinesh/stage/server/internal/filelock"
)

// Default is the listen address when none is configured. Localhost only:
// the server has no auth.
const Default = "127.0.0.1:7380"

// DefaultSocket is the keyword for this data dir's private default socket
// (resolved by config).
const DefaultSocket = "unix"

// Addr is a parsed listen address.
type Addr struct {
	Network string // "tcp" or "unix"
	// Address is host:port, or the absolute socket path ("" for an
	// unresolved DefaultSocket).
	Address string
	// Private marks the default socket: mode 0600 in a 0700 directory owned
	// by this user (checked on Listen). Explicit sockets are 0660.
	Private bool
}

// MaxSocketPath is the longest unix socket path the OS accepts (sun_path
// minus its NUL): 103 bytes on macOS, 107 elsewhere (Linux).
func MaxSocketPath() int { return maxSocketPath(runtime.GOOS) }

func maxSocketPath(goos string) int {
	if goos == "darwin" || strings.HasSuffix(goos, "bsd") {
		return 103
	}
	return 107
}

// CheckSocketPath rejects a socket path the OS cannot bind.
func CheckSocketPath(p string) error { return CheckSocketPathFor(runtime.GOOS, p) }

// CheckSocketPathFor is CheckSocketPath for goos (paths of another OS are
// derived and tested on any host).
func CheckSocketPathFor(goos, p string) error {
	if n, max := len(p), maxSocketPath(goos); n > max {
		return fmt.Errorf("unix socket path %s is %d bytes, over the %d-byte limit; use a shorter unix:/abs/path.sock", p, n, max)
	}
	return nil
}

// Parse accepts "host:port" (empty host = all interfaces),
// "unix:/abs/path.sock" (the URL form unix:///abs/path.sock also works), or
// "unix" (DefaultSocket, unresolved: Address "").
func Parse(s string) (Addr, error) {
	if s == DefaultSocket {
		return Addr{Network: "unix", Private: true}, nil
	}
	if p, ok := strings.CutPrefix(s, "unix:"); ok {
		p = strings.TrimPrefix(p, "//")
		if !filepath.IsAbs(p) {
			return Addr{}, fmt.Errorf("listen %q: unix socket path must be absolute (unix:/abs/path.sock)", s)
		}
		p = filepath.Clean(p)
		if err := CheckSocketPath(p); err != nil {
			return Addr{}, fmt.Errorf("listen %q: %w", s, err)
		}
		return Addr{Network: "unix", Address: p}, nil
	}
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return Addr{}, fmt.Errorf("listen %q: want host:port, unix:/abs/path.sock or unix", s)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Addr{}, fmt.Errorf("listen %q: invalid port", s)
	}
	return Addr{Network: "tcp", Address: s}, nil
}

// ParseList parses a comma-separated list of addresses (STAGE_LISTEN).
// Empty items and duplicates are errors.
func ParseList(s string) ([]Addr, error) {
	var out []Addr
	seen := map[string]bool{}
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("listen %q: empty address in list", s)
		}
		a, err := Parse(item)
		if err != nil {
			return nil, err
		}
		if seen[a.String()] {
			return nil, fmt.Errorf("listen %q: %s listed twice", s, a)
		}
		seen[a.String()] = true
		out = append(out, a)
	}
	return out, nil
}

// String is the address in Parse's syntax.
func (a Addr) String() string {
	if a.Network == "unix" {
		if a.Address == "" {
			return DefaultSocket
		}
		return "unix:" + a.Address
	}
	return a.Address
}

// URL is a client URL (STAGE_URL syntax) that reaches this address from the
// same host: http://host:port (wildcard hosts become loopback) or
// unix:///abs/path.sock.
func (a Addr) URL() string {
	if a.Network == "unix" {
		return "unix://" + a.Address
	}
	host, port, err := net.SplitHostPort(a.Address)
	if err != nil {
		return "http://" + a.Address
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// Listener is an open address. Close closes it, removes a unix socket file
// and then releases the socket's ownership lock. Safe to call more than
// once (http.Server.Shutdown closes it too).
type Listener struct {
	net.Listener
	Spec Addr // the configured address (resolved)
	lock *filelock.Lock
	once sync.Once
	err  error
}

// Close implements net.Listener.
func (l *Listener) Close() error {
	l.once.Do(func() {
		l.err = l.Listener.Close() // unlinks a unix socket it created
		if l.lock != nil {
			l.lock.Release()
		}
	})
	return l.err
}

// LockPath is the ownership lock file of a unix socket path.
func LockPath(socket string) string { return socket + ".lock" }

// Listen opens the address. A unix socket is owned through an exclusive
// lock on LockPath(path), held until Close: a second server on the same
// path fails, and under the lock an existing socket file that refuses
// connections is stale and replaced (a non-socket file, a socket that
// answers, e.g. a server predating the lock, or one that cannot be probed
// is refused). Explicit sockets are made 0660; Private ones 0600, in a
// directory created 0700 and checked to be owned by this user.
func (a Addr) Listen() (*Listener, error) {
	if a.Network != "unix" {
		l, err := net.Listen("tcp", a.Address)
		if err != nil {
			return nil, err
		}
		return &Listener{Listener: l, Spec: a}, nil
	}
	if a.Address == "" {
		return nil, errors.New("listen unix: default socket path not resolved")
	}
	if err := CheckSocketPath(a.Address); err != nil {
		return nil, err
	}
	mode, lockMode := fs.FileMode(0o660), fs.FileMode(0o644)
	if a.Private {
		mode, lockMode = 0o600, 0o600
		if err := EnsurePrivateDir(filepath.Dir(a.Address)); err != nil {
			return nil, err
		}
	}
	lock, err := filelock.Acquire(LockPath(a.Address), lockMode)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, &InUseError{Path: a.Address}
	}
	if err != nil {
		return nil, err
	}
	if err := removeStale(a.Address); err != nil {
		lock.Release()
		return nil, err
	}
	l, err := net.Listen("unix", a.Address)
	if err != nil {
		lock.Release()
		return nil, err
	}
	if err := os.Chmod(a.Address, mode); err != nil {
		_ = l.Close()
		lock.Release()
		return nil, err
	}
	return &Listener{Listener: l, Spec: a, lock: lock}, nil
}

// InUseError: another process holds a unix socket's lock (a live server or
// worker gateway owns the path).
type InUseError struct{ Path string }

func (e *InUseError) Error() string {
	return fmt.Sprintf("%s is in use by another stage server (%s held)", e.Path, LockPath(e.Path))
}

// OpenAll opens addrs in order. On any failure it closes those already
// opened (removing their sockets and releasing their locks) and returns the
// error.
func OpenAll(addrs []Addr) ([]*Listener, error) {
	var out []*Listener
	for _, a := range addrs {
		l, err := a.Listen()
		if err != nil {
			for _, o := range out {
				_ = o.Close()
			}
			return nil, fmt.Errorf("listen %s: %w", a, err)
		}
		out = append(out, l)
	}
	return out, nil
}

// EnsurePrivateDir creates dir (and missing parents) 0700 and refuses a
// symlink, a non-directory, a directory owned by another user, or one with
// any group/other permission bits.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("socket dir %s is not a directory (symlinks are refused)", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
		return fmt.Errorf("socket dir %s is owned by uid %d, not this user (%d)", dir, sys.Uid, os.Geteuid())
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket dir %s has mode %v; want 0700 (no group/other access)", dir, st.Mode().Perm())
	}
	return nil
}

func removeStale(path string) error {
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		_ = c.Close()
		return fmt.Errorf("%s is in use by another server", path)
	}
	// Only a refused connection proves nothing listens; on anything else
	// (EACCES, timeout) the socket may be live, so leave it.
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("%s exists and may be in use (not removed): %w", path, err)
	}
	return os.Remove(path)
}
