// Package listen parses and opens the server's listen address (STAGE_LISTEN,
// `stage serve --listen`): a TCP host:port or a unix socket.
package listen

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Default is the listen address when none is configured. Localhost only:
// the server has no auth.
const Default = "127.0.0.1:7380"

// Addr is a parsed listen address.
type Addr struct {
	Network string // "tcp" or "unix"
	Address string // host:port, or the absolute socket path
}

// Parse accepts "host:port" (empty host = all interfaces) or
// "unix:/abs/path.sock" (the URL form unix:///abs/path.sock also works).
func Parse(s string) (Addr, error) {
	if p, ok := strings.CutPrefix(s, "unix:"); ok {
		p = strings.TrimPrefix(p, "//")
		if !filepath.IsAbs(p) {
			return Addr{}, fmt.Errorf("listen %q: unix socket path must be absolute (unix:/abs/path.sock)", s)
		}
		return Addr{Network: "unix", Address: filepath.Clean(p)}, nil
	}
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return Addr{}, fmt.Errorf("listen %q: want host:port or unix:/abs/path.sock", s)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Addr{}, fmt.Errorf("listen %q: invalid port", s)
	}
	return Addr{Network: "tcp", Address: s}, nil
}

// String is the address in Parse's syntax.
func (a Addr) String() string {
	if a.Network == "unix" {
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

// Listen opens the address. For a unix socket it first removes a stale
// socket file (refusing a live one or a non-socket file) and makes the
// socket 0660. Closing the listener removes the socket file.
func (a Addr) Listen() (net.Listener, error) {
	if a.Network != "unix" {
		return net.Listen("tcp", a.Address)
	}
	if err := removeStale(a.Address); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", a.Address)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(a.Address, 0o660); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
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
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return fmt.Errorf("%s is in use by another server", path)
	}
	return os.Remove(path)
}
