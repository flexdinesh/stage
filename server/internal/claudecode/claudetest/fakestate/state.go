// Package fakestate is the state file the fake claude CLI (fakeclaude) and
// its test driver (claudetest) share.
package fakestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Launch modes.
const (
	// FailBeforeCreate: --bg exits 1 before creating a session.
	FailBeforeCreate = "fail-before-create"
	// CrashAfterCreate: --bg creates the session, then exits 1 without
	// printing its id (a lost reply).
	CrashAfterCreate = "crash-after-create"
)

// Entry mirrors one `claude agents --json` entry.
type Entry struct {
	PID        int    `json:"pid,omitempty"`
	ID         string `json:"id,omitempty"`
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	StartedAt  int64  `json:"startedAt"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	Status     string `json:"status,omitempty"`
	WaitingFor string `json:"waitingFor,omitempty"`
	State      string `json:"state,omitempty"`
}

// Session is a listed session; Removed ones are no longer listed (rm).
// Settings is the --settings file it was launched with (its hooks).
type Session struct {
	Entry
	Removed  bool   `json:"removed,omitempty"`
	Settings string `json:"settings,omitempty"`
}

// Call is one invocation of the fake.
type Call struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
	Dir  string            `json:"dir"`
}

// State is the fake's whole world.
type State struct {
	Version     string    `json:"version"`
	LoggedIn    bool      `json:"loggedIn"`
	ConfigDir   string    `json:"configDir"`
	ProjectsDir string    `json:"projectsDir"`
	Trusted     []string  `json:"trusted"`
	Launch      string    `json:"launch,omitempty"`
	HangVersion bool      `json:"hangVersion,omitempty"`
	FailAgents  bool      `json:"failAgents,omitempty"`
	// CopyOnResume: `--bg --resume` starts a copy even of an exited
	// session (it got busy between Stage's check and the resume).
	CopyOnResume bool `json:"copyOnResume,omitempty"`
	// FailResume: `--bg --resume` exits 1 without waking the session.
	FailResume bool      `json:"failResume,omitempty"`
	Sessions   []Session `json:"sessions"`
	Calls       []Call    `json:"calls"`
}

// Trusts reports whether dir or a parent is trusted.
func (s *State) Trusts(dir string) bool {
	for _, t := range s.Trusted {
		if dir == t || strings.HasPrefix(dir, t+"/") {
			return true
		}
	}
	return false
}

// Read reads the state file.
func Read(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Update locks the state file, applies fn and writes it back.
func Update(path string, fn func(*State) error) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	s, err := Read(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := fn(&s); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TranscriptPath is a session's transcript, as Claude lays it out.
func TranscriptPath(projects, cwd, id string) string {
	enc := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	return filepath.Join(projects, enc, id+".jsonl")
}

func appendRecord(projects, cwd, id string, rec map[string]any) error {
	p := TranscriptPath(projects, cwd, id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	rec["sessionId"], rec["cwd"], rec["isSidechain"] = id, cwd, false
	if _, ok := rec["timestamp"]; !ok {
		rec["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

var seq int

func uuidFor(id, kind string) string {
	seq++
	return fmt.Sprintf("%s-%s-%d", id[:8], kind, seq)
}

// AppendUser writes a typed user prompt. sessionKind is "bg" in a
// background session, "" otherwise (Claude omits it).
func AppendUser(projects, cwd, id, entrypoint, sessionKind, prompt string, at time.Time) error {
	return AppendUserOn(projects, cwd, id, entrypoint, sessionKind, prompt, "", at)
}

// AppendUserOn is AppendUser in a checkout on branch (Claude's gitBranch;
// "" outside Git).
func AppendUserOn(projects, cwd, id, entrypoint, sessionKind, prompt, branch string, at time.Time) error {
	rec := map[string]any{
		"type": "user", "entrypoint": entrypoint, "uuid": uuidFor(id, "u"), "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": prompt},
	}
	if sessionKind != "" {
		rec["sessionKind"] = sessionKind
	}
	if branch != "" {
		rec["gitBranch"] = branch
	}
	return appendRecord(projects, cwd, id, rec)
}

// ReplyUsage is the usage of every reply AppendReply writes, and
// ReplyDurationMs its turn's duration.
var ReplyUsage = map[string]any{"input_tokens": 10, "cache_read_input_tokens": 100, "cache_creation_input_tokens": 20, "output_tokens": 5}

const ReplyDurationMs = 1000

// AppendReply writes an assistant text message, then (turnOver) the
// turn_duration record that ends the turn.
func AppendReply(projects, cwd, id, text string, turnOver bool) error {
	msg := "msg_" + uuidFor(id, "m")
	if err := appendRecord(projects, cwd, id, map[string]any{
		"type": "assistant", "entrypoint": "cli", "uuid": uuidFor(id, "a"),
		"message": map[string]any{"id": msg, "role": "assistant", "model": "claude-haiku-5-5", "content": []map[string]any{{"type": "text", "text": text}}, "usage": ReplyUsage},
	}); err != nil {
		return err
	}
	if !turnOver {
		return nil
	}
	return appendRecord(projects, cwd, id, map[string]any{"type": "system", "subtype": "turn_duration", "durationMs": ReplyDurationMs, "uuid": uuidFor(id, "s")})
}
