package instance

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockContention(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	o, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode %v, %v", st.Mode().Perm(), err)
	}
	m := NewMeta()
	m.Listeners = []string{"unix:///run/x.sock", "http://127.0.0.1:7380"}
	if err := o.Record(m); err != nil {
		t.Fatal(err)
	}
	// Another spelling of the same dir (symlink) contends too.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, link, dir + "/./"} {
		_, err := Lock(d)
		var le *LockedError
		if !errors.As(err, &le) {
			t.Fatalf("Lock(%s) while held: %v", d, err)
		}
		if le.Holder == nil || le.Holder.PID != os.Getpid() || !strings.Contains(le.Error(), "http://127.0.0.1:7380") {
			t.Fatalf("holder %+v: %v", le.Holder, le)
		}
	}
	o.Release()
	o.Release()
	o2, err := Lock(link)
	if err != nil {
		t.Fatalf("relock: %v", err)
	}
	canon, _ := filepath.EvalSymlinks(dir)
	if o2.DataDir != canon {
		t.Fatalf("DataDir %q, want canonical %q", o2.DataDir, canon)
	}
	o2.Release()
}

// A holder killed with SIGKILL leaves its record but not its lock.
func TestLockReleasedOnKill(t *testing.T) {
	if d := os.Getenv("INSTANCE_HELPER"); d != "" {
		o, err := Lock(d)
		if err != nil {
			os.Exit(2)
		}
		_ = o.Record(NewMeta())
		_, _ = os.Stdout.WriteString("locked\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedOnKill$")
	cmd.Env = append(os.Environ(), "INSTANCE_HELPER="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	if _, err := out.Read(buf); err != nil {
		t.Fatal(err)
	}
	var le *LockedError
	if _, err := Lock(dir); !errors.As(err, &le) || le.Holder == nil || le.Holder.PID != cmd.Process.Pid {
		t.Fatalf("Lock while helper holds: %v", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	o, err := Lock(dir)
	if err != nil {
		t.Fatalf("Lock after kill: %v", err)
	}
	o.Release()
}

func TestNotify(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := Notify("READY=1"); err != nil {
		t.Fatalf("no NOTIFY_SOCKET: %v", err)
	}
	dir, err := os.MkdirTemp("", "sdn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "n.sock")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	for _, s := range []string{"READY=1", "STOPPING=1"} {
		if err := Notify(s); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, err := c.Read(buf)
		if err != nil || string(buf[:n]) != s {
			t.Fatalf("got %q, %v; want %q", buf[:n], err, s)
		}
	}
}

// A managed holder's conflict error says how to stop it; ReadHolder sees
// a live holder's record and nothing once it is released.
func TestManagedHolder(t *testing.T) {
	dir := t.TempDir()
	if held, m, err := ReadHolder(dir); held || m != nil || err != nil {
		t.Fatalf("empty dir: %v %v %v", held, m, err)
	}
	o, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta := NewMeta()
	meta.Managed, meta.Manager, meta.Service = true, "systemd", "stage.service"
	if err := o.Record(meta); err != nil {
		t.Fatal(err)
	}
	held, m, err := ReadHolder(dir)
	if !held || m == nil || m.InstanceID != meta.InstanceID || !m.Managed || err != nil {
		t.Fatalf("ReadHolder: %v %+v %v", held, m, err)
	}
	_, err = Lock(dir)
	var le *LockedError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "`stage service stop`") || !strings.Contains(err.Error(), "systemd stage.service") {
		t.Fatalf("managed conflict: %v", err)
	}
	o.Release()
	if held, _, _ := ReadHolder(dir); held {
		t.Fatal("held after release")
	}
}
