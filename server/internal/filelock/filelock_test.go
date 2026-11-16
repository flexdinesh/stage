package filelock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	if held, err := Held(path); err != nil || held {
		t.Fatalf("missing file held=%v err=%v", held, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("Held created the file")
	}
	l, err := Acquire(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// flock is per open file description: a second open in this process
	// contends like another process would.
	start := time.Now()
	if _, err := Acquire(path, 0o600); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: %v", err)
	}
	if d := time.Since(start); d < retryWait || d > 3*retryWait {
		t.Fatalf("second acquire returned after %v, want about %v", d, retryWait)
	}
	if held, err := Held(path); err != nil || !held {
		t.Fatalf("held=%v err=%v", held, err)
	}
	l.Release()
	l.Release()
	if held, err := Held(path); err != nil || held {
		t.Fatalf("after release held=%v err=%v", held, err)
	}
	l2, err := Acquire(path, 0o600)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	l2.Release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file removed: %v", err)
	}
}

// A probe (Held) racing a starting owner does not fail it.
func TestAcquireRetriesPastProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	l, err := Acquire(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(100 * time.Millisecond); l.Release() }()
	l2, err := Acquire(path, 0o600)
	if err != nil {
		t.Fatalf("acquire after brief hold: %v", err)
	}
	l2.Release()
}

// The kernel frees a lock whose owner is killed.
func TestKilledOwnerReleases(t *testing.T) {
	if os.Getenv("FILELOCK_HELPER") != "" {
		if _, err := Acquire(os.Getenv("FILELOCK_HELPER"), 0o600); err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString("locked\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "x.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestKilledOwnerReleases$")
	cmd.Env = append(os.Environ(), "FILELOCK_HELPER="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	if _, err := out.Read(buf); err != nil || string(buf) != "locked\n" {
		t.Fatalf("helper: %q %v", buf, err)
	}
	if held, _ := Held(path); !held {
		t.Fatal("helper's lock not seen")
	}
	if _, err := Acquire(path, 0o600); !errors.Is(err, ErrLocked) {
		t.Fatalf("acquire while helper holds: %v", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	l, err := Acquire(path, 0o600)
	if err != nil {
		t.Fatalf("acquire after kill: %v", err)
	}
	l.Release()
}

// AcquireContext waits for a held lock until ctx ends.
func TestAcquireContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	l, err := Acquire(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireContext(ctx, path, 0o600); !errors.Is(err, ErrLocked) {
		t.Fatalf("held: %v", err)
	}
	go func() { time.Sleep(50 * time.Millisecond); l.Release() }()
	l2, err := AcquireContext(context.Background(), path, 0o600)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	l2.Release()
}
