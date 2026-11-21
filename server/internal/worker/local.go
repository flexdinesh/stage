package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/stage/server/internal/filelock"
)

// Holder is worker.lock's record: the process that runs the worker of this
// directory. Informational (status, the worker service's readiness); the
// flock is the proof.
type Holder struct {
	PID       int       `json:"pid"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	// Managed: run by the worker service (`stage worker run --managed`).
	Managed bool   `json:"managed"`
	Manager string `json:"manager,omitempty"` // systemd | launchd
	Service string `json:"service,omitempty"` // unit or label
}

// LockedError: another worker holds the directory (exit 75).
type LockedError struct {
	Dir    string
	Holder *Holder
}

func (e *LockedError) Error() string {
	msg := fmt.Sprintf("worker dir %s is in use by another stage worker", e.Dir)
	if h := e.Holder; h != nil {
		msg += fmt.Sprintf(" (pid %d, version %s)", h.PID, h.Version)
		if h.Managed {
			msg += "; it is the worker service: `stage worker service stop` stops it"
		}
	}
	return msg
}

// lock owns a worker directory for the process lifetime.
type lock struct{ l *filelock.Lock }

// acquire locks dir/worker.lock and records h.
func acquire(dir string, h Holder) (*lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("worker dir: %w", err)
	}
	path := filepath.Join(dir, LockFile)
	l, err := filelock.Acquire(path, 0o600)
	if errors.Is(err, filelock.ErrLocked) {
		_, holder, _ := ReadHolder(dir)
		return nil, &LockedError{Dir: dir, Holder: holder}
	}
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err == nil {
		f := l.File()
		if err = f.Truncate(0); err == nil {
			if _, err = f.WriteAt(append(b, '\n'), 0); err == nil {
				err = f.Sync()
			}
		}
	}
	if err != nil {
		l.Release()
		return nil, err
	}
	return &lock{l: l}, nil
}

func (l *lock) release() { l.l.Release() }

// ReadHolder reports whether a worker holds dir and, when its record is
// readable, which one.
func ReadHolder(dir string) (bool, *Holder, error) {
	path := filepath.Join(dir, LockFile)
	held, err := filelock.Held(path)
	if err != nil || !held {
		return false, nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return true, nil, nil
	}
	var h Holder
	if json.Unmarshal(b, &h) != nil || h.PID == 0 {
		return true, nil, nil
	}
	return true, &h, nil
}

// Channel states the running worker reports.
const (
	StateConnecting  = "connecting"   // dialing, or backing off after a failure
	StateOnline      = "online"       // the channel is live
	StateNeedsUpdate = "needs_update" // the main server speaks another API version
	StateRevoked     = "revoked"      // the token was refused: the machine was removed
	StateStopped     = "stopped"      // the worker exited
)

// StateSchema versions worker.state.json.
const StateSchema = 1

// State is worker.state.json: what the worker of this directory last
// reported. Only meaningful with the lock held, except a final revoked or
// stopped.
type State struct {
	Schema    int       `json:"schema"`
	PID       int       `json:"pid"`
	Managed   bool      `json:"managed"`
	State     string    `json:"state"`
	Since     time.Time `json:"since"`
	LastError string    `json:"lastError,omitempty"`
	URL       string    `json:"url"`
	MachineID int64     `json:"machineId"`
	// Session is the live channel session (online only).
	Session string `json:"session,omitempty"`
	// ServerVersion is the main server's build, when known.
	ServerVersion string `json:"serverVersion,omitempty"`
}

// ReadState reads dir/worker.state.json (nil when missing).
func ReadState(dir string) (*State, error) {
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", StateFile, err)
	}
	return &s, nil
}

func writeState(dir string, s State) error {
	s.Schema = StateSchema
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, StateFile), append(b, '\n'), 0o600)
}
