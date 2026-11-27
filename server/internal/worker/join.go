package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/filelock"
)

// JoinOptions configure Join.
type JoinOptions struct {
	// Link is the pairing link (<server>/join#<code>).
	Link string
	// ConfigPath is where worker.json is written.
	ConfigPath string
	// Label is the name this worker suggests (default: the host name). A
	// label given when the code was created wins on the server.
	Label string
	HTTP  *http.Client
	Now   func() time.Time
}

// AlreadyJoinedError: worker.json exists; leave first.
type AlreadyJoinedError struct {
	Path   string
	Config Config
}

func (e *AlreadyJoinedError) Error() string {
	return fmt.Sprintf("this machine already joined %s as machine %d (%s): run `stage worker leave` first", e.Config.URL, e.Config.MachineID, e.Path)
}

// Join redeems the link's pairing code for a machine token and writes
// worker.json (0600). It refuses when worker.json already exists.
func Join(ctx context.Context, o JoinOptions) (Config, error) {
	base, code, err := ParseLink(o.Link)
	if err != nil {
		return Config{}, err
	}
	if c, err := LoadConfig(o.ConfigPath); err == nil {
		return Config{}, &AlreadyJoinedError{Path: o.ConfigPath, Config: c}
	} else if !errors.Is(err, ErrNotJoined) {
		return Config{}, err
	}
	if _, err := os.Stat(o.ConfigPath); !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("%s exists but is unusable; remove it or run `stage worker leave --local`", o.ConfigPath)
	}
	label := o.Label
	if label == "" {
		label, _ = os.Hostname()
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	// Prove the directory is writable before spending the one-time code.
	if err := os.MkdirAll(filepath.Dir(o.ConfigPath), 0o700); err != nil {
		return Config{}, fmt.Errorf("worker dir: %w", err)
	}
	var out struct {
		MachineID int64  `json:"machineId"`
		Label     string `json:"label"`
		Token     string `json:"token"`
	}
	a := newAPI(base, "", o.HTTP)
	if err := a.do(ctx, "POST", "/api/worker/join", map[string]string{"code": code, "label": label, "version": buildinfo.Version}, &out); err != nil {
		return Config{}, joinError(err)
	}
	c := Config{Schema: ConfigSchema, URL: base, MachineID: out.MachineID, Label: out.Label, Token: out.Token, JoinedAt: now().UTC()}
	removeDetachedObservationFiles(o.ConfigPath)
	if err := SaveConfig(o.ConfigPath, c); err != nil {
		return Config{}, fmt.Errorf("joined as machine %d but could not save %s (remove the machine in Settings and pair again): %w", c.MachineID, o.ConfigPath, err)
	}
	// A previous machine's final state (revoked, stopped) and harness
	// credentials no longer apply.
	_ = os.Remove(filepath.Join(Dir(o.ConfigPath), StateFile))
	_ = os.Remove(filepath.Join(Dir(o.ConfigPath), CredentialsFile))
	return c, nil
}

func joinError(err error) error {
	var he *HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("join: %w", err)
	}
	switch {
	case he.Code == "pairing_code_invalid":
		return errors.New("join: the pairing code is invalid, used or expired: create a new one (Settings > Machines > Add, or `stage machine pair`)")
	case he.Code == "api_version_mismatch":
		return fmt.Errorf("join: %s", he.Msg)
	}
	return fmt.Errorf("join: %w", err)
}

// LeaveOptions configure Leave.
type LeaveOptions struct {
	ConfigPath string
	// Local only deletes the local config (the main server is unreachable
	// or already removed the machine).
	Local bool
	HTTP  *http.Client
}

// LeaveResult is what Leave did.
type LeaveResult struct {
	Config Config `json:"-"`
	// Removed: the main server removed the machine (false with Local, or
	// when it had already been removed).
	Removed bool `json:"removed"`
	// AlreadyRemoved: the server refused the token (removed before).
	AlreadyRemoved bool `json:"alreadyRemoved"`
	// Running: a worker process still holds the directory; it stops on its
	// next request (its token is revoked).
	Running bool `json:"running"`
}

// Leave removes this machine from the main server (unless Local) and
// deletes worker.json, worker.state.json and the harness credentials
// (credentials.json). The lock file is kept (lock files are never deleted).
func Leave(ctx context.Context, o LeaveOptions) (LeaveResult, error) {
	var res LeaveResult
	c, err := LoadConfig(o.ConfigPath)
	if err != nil && !(o.Local && !errors.Is(err, ErrNotJoined)) {
		return res, err
	}
	res.Config = c
	if !o.Local {
		err := newAPI(c.URL, c.Token, o.HTTP).do(ctx, "POST", "/api/worker/leave", nil, nil)
		switch {
		case err == nil:
			res.Removed = true
		case Revoked(err):
			res.AlreadyRemoved = true
		default:
			return res, fmt.Errorf("leave: %w (to forget this machine locally anyway: `stage worker leave --local`, then remove it in Settings > Machines)", err)
		}
	}
	dir := Dir(o.ConfigPath)
	for _, f := range []string{o.ConfigPath, filepath.Join(dir, StateFile), filepath.Join(dir, CredentialsFile)} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
	}
	res.Running, _, _ = ReadHolder(dir)
	if !res.Running {
		removeDetachedObservationFiles(o.ConfigPath)
	}
	return res, nil
}

// A running worker still owns these files. Leave lets it clear them after it
// stops. Join fingerprints also prevent an old worker's files being reused.
func removeDetachedObservationFiles(configPath string) {
	dir := Dir(configPath)
	path := filepath.Join(dir, LockFile)
	if held, err := filelock.Held(path); err != nil || held {
		return
	}
	lock, err := filelock.Acquire(path, 0o600)
	if err != nil {
		return
	}
	defer lock.Release()
	if _, err := os.Lstat(configPath); errors.Is(err, os.ErrNotExist) {
		removeObservationFiles(dir)
	}
}

func removeObservationFiles(dir string) {
	_ = os.Remove(filepath.Join(dir, watchFile))
	_ = os.Remove(filepath.Join(dir, "settings-cache.json"))
	_ = os.RemoveAll(filepath.Join(dir, outboxDir))
}
