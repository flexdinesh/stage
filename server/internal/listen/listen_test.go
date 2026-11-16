package listen

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/filelock"
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

// A socket that cannot be probed (EACCES) may be live: left alone.
func TestListenUnixKeepsUnprobeableSocket(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses socket permissions")
	}
	path := sockPath(t)
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	a := Addr{Network: "unix", Address: path}
	if l, err := a.Listen(); err == nil {
		_ = l.Close()
		t.Fatal("listen replaced a socket it could not probe")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("unprobeable socket removed: %v", err)
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

func TestParseDefaultSocketAndList(t *testing.T) {
	a, err := Parse("unix")
	if err != nil || a != (Addr{Network: "unix", Private: true}) || a.String() != "unix" {
		t.Fatalf("Parse(unix) = %+v, %v", a, err)
	}
	got, err := ParseList(" unix , 127.0.0.1:7380,unix:/run/s.sock")
	if err != nil {
		t.Fatal(err)
	}
	want := []Addr{{Network: "unix", Private: true}, {Network: "tcp", Address: "127.0.0.1:7380"}, {Network: "unix", Address: "/run/s.sock"}}
	if len(got) != len(want) {
		t.Fatalf("ParseList = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, in := range []string{"", "unix,", ",unix", "unix,unix", "127.0.0.1:1,127.0.0.1:1", "unix:/a.sock,unix:///a.sock", "unix,nope"} {
		if got, err := ParseList(in); err == nil {
			t.Errorf("ParseList(%q) = %+v, want error", in, got)
		}
	}
}

func TestSocketPathLimit(t *testing.T) {
	if maxSocketPath("darwin") != 103 || maxSocketPath("linux") != 107 {
		t.Fatal("socket path limits")
	}
	long := "/" + strings.Repeat("x", MaxSocketPath())
	if _, err := Parse("unix:" + long); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over-limit path: %v", err)
	}
	ok := "/" + strings.Repeat("x", MaxSocketPath()-1)
	if _, err := Parse("unix:" + ok); err != nil {
		t.Fatalf("at-limit path: %v", err)
	}
	if _, err := (Addr{Network: "unix", Address: long}).Listen(); err == nil {
		t.Fatal("Listen accepted an over-limit path")
	}
	if _, err := (Addr{Network: "unix", Private: true}).Listen(); err == nil {
		t.Fatal("Listen accepted an unresolved default socket")
	}
}

// Two servers on one socket path: the second fails on the ownership lock
// and the first keeps serving; after Close the lock is free again.
func TestListenUnixOwnershipLock(t *testing.T) {
	path := sockPath(t)
	a := Addr{Network: "unix", Address: path}
	l, err := a.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if held, err := filelock.Held(LockPath(path)); err != nil || !held {
		t.Fatalf("socket lock held=%v err=%v", held, err)
	}
	if l2, err := a.Listen(); err == nil {
		_ = l2.Close()
		t.Fatal("second listen succeeded")
	} else if !strings.Contains(err.Error(), "in use by another stage server") {
		t.Fatalf("second listen: %v", err)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("first listener lost its socket: %v", err)
	}
	_ = c.Close()
	_ = l.Close()
	_ = l.Close()
	if held, _ := filelock.Held(LockPath(path)); held {
		t.Fatal("socket lock held after close")
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket left after close: %v", err)
	}
}

// A crashed owner leaves its socket file; under the lock it is replaced.
func TestListenUnixReplacesCrashedOwnersSocket(t *testing.T) {
	path := sockPath(t)
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	l, err := Addr{Network: "unix", Address: path}.Listen()
	if err != nil {
		t.Fatalf("listen over crashed owner's socket: %v", err)
	}
	_ = l.Close()
}

func TestListenPrivateSocket(t *testing.T) {
	base := sockPath(t)
	dir := filepath.Join(filepath.Dir(base), "run", "stage")
	path := filepath.Join(dir, "stage.sock")
	l, err := Addr{Network: "unix", Address: path, Private: true}.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for p, want := range map[string]fs.FileMode{dir: 0o700, path: 0o600, LockPath(path): 0o600} {
		st, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", p, st.Mode().Perm(), want)
		}
	}
}

func TestPrivateDirRefusesUnsafeDirs(t *testing.T) {
	root := filepath.Dir(sockPath(t))
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{open, link, file} {
		l, err := Addr{Network: "unix", Address: filepath.Join(d, "s.sock"), Private: true}.Listen()
		if err == nil {
			_ = l.Close()
			t.Errorf("private socket in %s accepted", d)
		}
	}
	if err := EnsurePrivateDir(private); err != nil {
		t.Fatalf("private dir refused: %v", err)
	}
}

// A failed bind closes what OpenAll already opened.
func TestOpenAllPartialFailure(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	path := sockPath(t)
	addrs := []Addr{{Network: "unix", Address: path}, {Network: "tcp", Address: busy.Addr().String()}}
	if ls, err := OpenAll(addrs); err == nil {
		for _, l := range ls {
			_ = l.Close()
		}
		t.Fatal("OpenAll succeeded with a busy port")
	} else if !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Fatalf("error does not name the address: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket left after partial failure: %v", err)
	}
	if held, _ := filelock.Held(LockPath(path)); held {
		t.Fatal("socket lock held after partial failure")
	}
	ls, err := OpenAll(addrs[:1])
	if err != nil {
		t.Fatalf("reopen after partial failure: %v", err)
	}
	_ = ls[0].Close()
}
