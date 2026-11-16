// Package instance owns a data dir for one server process: an exclusive
// lock on <dataDir>/stage.lock held for the process's lifetime, so two
// servers (on any listen addresses) never run schedulers or sync against
// one database, plus systemd readiness notification (docs/runtime.md).
package instance

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/filelock"
)

// LockFile is the data dir's ownership lock (never deleted).
const LockFile = "stage.lock"

// Meta describes the owning process. It is informational (error messages,
// diagnostics): the held lock, not this record, proves ownership.
type Meta struct {
	PID        int       `json:"pid"`
	InstanceID string    `json:"instanceId"`
	Identity   string    `json:"identity"`
	Version    string    `json:"version"`
	Commit     string    `json:"commit"`
	StartedAt  time.Time `json:"startedAt"`
	Listeners  []string  `json:"listeners"`
	// Managed: started by the per-user service (`stage serve --managed`),
	// supervised by Manager ("systemd" | "launchd") as Service (unit name or
	// launchd label).
	Managed bool   `json:"managed,omitempty"`
	Manager string `json:"manager,omitempty"`
	Service string `json:"service,omitempty"`
}

// NewMeta describes this process (listeners filled in after binding).
func NewMeta() Meta {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return Meta{
		PID: os.Getpid(), InstanceID: hex.EncodeToString(b), Identity: buildinfo.Identity,
		Version: buildinfo.Version, Commit: buildinfo.Revision(), StartedAt: time.Now().UTC().Truncate(time.Second),
	}
}

// LockedError: another live process owns the data dir.
type LockedError struct {
	DataDir string
	Holder  *Meta // nil when its record is unreadable (e.g. still starting)
}

func (e *LockedError) Error() string {
	msg := fmt.Sprintf("data dir %s is in use by another stage server", e.DataDir)
	if h := e.Holder; h != nil {
		msg += fmt.Sprintf(" (pid %d, version %s", h.PID, h.Version)
		if len(h.Listeners) > 0 {
			msg += ", listening on " + strings.Join(h.Listeners, ", ")
		}
		msg += ")"
		if h.Managed {
			msg += fmt.Sprintf("; it is the %s service (%s %s): stop it with `stage service stop`", h.Identity, h.Manager, h.Service)
		}
	}
	return msg
}

// ReadHolder reports whether a live process holds dataDir's lock and, when
// it does, its record (nil while the holder is still writing it or when it
// is unreadable). It never creates or locks anything for long.
func ReadHolder(dataDir string) (bool, *Meta, error) {
	path := filepath.Join(dataDir, LockFile)
	held, err := filelock.Held(path)
	if err != nil || !held {
		return false, nil, err
	}
	// Record truncates before writing: retry briefly on an empty record.
	for i := 0; ; i++ {
		if b, err := os.ReadFile(path); err == nil {
			var m Meta
			if json.Unmarshal(b, &m) == nil && m.PID != 0 {
				return true, &m, nil
			}
		}
		if i == 10 {
			return true, nil, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Owner is a held data dir.
type Owner struct {
	DataDir string // canonical
	lock    *filelock.Lock
}

// Lock creates dataDir (0700) if needed, canonicalizes it (symlinks
// resolved, so one directory has one owner however it is spelled) and takes
// its exclusive lock. Held by a live process: *LockedError.
func Lock(dataDir string) (*Owner, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	canon, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	path := filepath.Join(canon, LockFile)
	l, err := filelock.Acquire(path, 0o600)
	if errors.Is(err, filelock.ErrLocked) {
		le := &LockedError{DataDir: canon}
		if b, err := os.ReadFile(path); err == nil {
			var m Meta
			if json.Unmarshal(b, &m) == nil && m.PID != 0 {
				le.Holder = &m
			}
		}
		return nil, le
	}
	if err != nil {
		return nil, err
	}
	return &Owner{DataDir: canon, lock: l}, nil
}

// Record replaces the lock file's owner record.
func (o *Owner) Record(m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	f := o.lock.File()
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(append(b, '\n'), 0); err != nil {
		return err
	}
	return f.Sync()
}

// Release frees the data dir. Safe to call more than once.
func (o *Owner) Release() { o.lock.Release() }

// Notify sends a systemd notification (sd_notify: "READY=1",
// "STOPPING=1") to $NOTIFY_SOCKET; a no-op when it is unset (foreground,
// launchd). Abstract sockets ("@name") are supported.
func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(state)); err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	return nil
}
