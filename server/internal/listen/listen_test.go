package listen

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Addr{
		"127.0.0.1:7380":         {Network: "tcp", Address: "127.0.0.1:7380"},
		"0.0.0.0:80":             {Network: "tcp", Address: "0.0.0.0:80"},
		":7380":                  {Network: "tcp", Address: ":7380"},
		"[::1]:7380":             {Network: "tcp", Address: "[::1]:7380"},
		"unix:/run/stage.sock":   {Network: "unix", Address: "/run/stage.sock"},
		"unix:/run//x/../s.sock": {Network: "unix", Address: "/run/s.sock"},
		"unix:///run/s.sock":     {Network: "unix", Address: "/run/s.sock"},
	} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "7380", "localhost", "host:0", "host:65536", "host:http", "unix:", "unix:rel.sock", "unix://abs.sock"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want error", in, got)
		}
	}
}

func TestURL(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:7380":   "http://127.0.0.1:7380",
		"0.0.0.0:7380":     "http://127.0.0.1:7380",
		":7380":            "http://127.0.0.1:7380",
		"[::]:7380":        "http://[::1]:7380",
		"example.lan:80":   "http://example.lan:80",
		"unix:/run/s.sock": "unix:///run/s.sock",
	} {
		a, err := Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.URL(); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
		if got := a.String(); got != in {
			t.Errorf("String(%q) = %q", in, got)
		}
	}
}

// sockPath is short enough for the unix socket path limit (~104-108 bytes).
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "stl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestListenUnix(t *testing.T) {
	path := sockPath(t)
	a, err := Parse("unix:" + path)
	if err != nil {
		t.Fatal(err)
	}
	// A stale socket file (no listener) is replaced.
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket not left behind: %v", err)
	}

	l, err := a.Listen()
	if err != nil {
		t.Fatalf("listen over stale socket: %v", err)
	}
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o660 {
		t.Errorf("socket mode %v, want 0660", st.Mode().Perm())
	}
	// A live socket is refused, and stays.
	if l2, err := a.Listen(); err == nil {
		_ = l2.Close()
		t.Fatal("second listen on a live socket succeeded")
	}
	if c, err := net.Dial("unix", path); err != nil {
		t.Fatalf("live socket removed by refused listen: %v", err)
	} else {
		_ = c.Close()
	}
	_ = l.Close()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket file left after close: %v", err)
	}
}

func TestListenUnixRefusesRegularFile(t *testing.T) {
	path := sockPath(t)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := Addr{Network: "unix", Address: path}
	if l, err := a.Listen(); err == nil {
		_ = l.Close()
		t.Fatal("listen replaced a regular file")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "x" {
		t.Fatalf("regular file changed: %q, %v", b, err)
	}
}
