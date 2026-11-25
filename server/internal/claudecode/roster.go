package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Entry is one session in `claude agents --json --all` (ADR 0005 E2).
// Background entries have ID and State; interactive ones (a terminal or SDK
// session, T3's among them) have neither. PID and Status are absent once
// the process exited (stopped, failed, reaped).
type Entry struct {
	PID        int    `json:"pid"`
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	StartedAt  int64  `json:"startedAt"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
	State      string `json:"state"`
}

// Session kinds.
const (
	KindBackground  = "background"
	KindInteractive = "interactive"
)

// Background reports whether e is a `--bg` session.
func (e Entry) Background() bool { return e.Kind == KindBackground }

// ParseRoster parses the session list. Entries without a session id are
// dropped; an unparsable list is an error.
func ParseRoster(b []byte) ([]Entry, error) {
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, fmt.Errorf("malformed claude agents output: %w", err)
	}
	out := es[:0]
	for _, e := range es {
		if e.SessionID != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// MapState is the Stage state of a listed session (ADR 0005 decision 4).
// A background session maps by state; an interactive one by status.
func MapState(e Entry) (string, error) {
	if e.Background() {
		switch e.State {
		case "working":
			return "running", nil
		case "blocked":
			return "needs_input", nil
		case "done":
			return "done", nil
		case "failed":
			return "failed", nil
		case "stopped":
			return "interrupted", nil
		}
		return "", fmt.Errorf("unknown Claude session state %q", e.State)
	}
	switch e.Status {
	case "busy":
		return "running", nil
	case "waiting":
		return "needs_input", nil
	case "idle", "":
		return "idle", nil
	}
	return "", fmt.Errorf("unknown Claude session status %q", e.Status)
}

// roster is the session list shared by every session of a connection: one
// CLI read per tick, reused for TTL.
type roster struct {
	mu      sync.Mutex
	cli     *CLI
	ttl     time.Duration
	now     func() time.Time
	at      time.Time
	entries []Entry
}

// list is the cached session list, read again once it is older than ttl
// (or fresh is set). Concurrent callers share one read.
func (r *roster) list(ctx context.Context, fresh bool) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !fresh && !r.at.IsZero() && r.now().Sub(r.at) < r.ttl {
		return r.entries, nil
	}
	return r.load(ctx)
}

// listSince is the session list as read at or after at: concurrent
// callers woken by one change share one read.
func (r *roster) listSince(ctx context.Context, at time.Time) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.at.IsZero() && !r.at.Before(at) {
		return r.entries, nil
	}
	return r.load(ctx)
}

// load reads the list from the CLI (r.mu held).
func (r *roster) load(ctx context.Context) ([]Entry, error) {
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := r.cli.Run(call, "", "agents", "--json", "--all")
	if err != nil {
		return nil, fmt.Errorf("claude agents: %w", err)
	}
	if res.Code != 0 {
		return nil, exitError("agents", res)
	}
	es, err := ParseRoster([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	r.entries, r.at = es, r.now()
	return es, nil
}

// cached is the last list read, without reading ("" ok false when none).
func (r *roster) cached() ([]Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries, !r.at.IsZero()
}

// invalidate makes the next list read the CLI.
func (r *roster) invalidate() {
	r.mu.Lock()
	r.at = time.Time{}
	r.mu.Unlock()
}

func findSession(es []Entry, id string) (Entry, bool) {
	for _, e := range es {
		if e.SessionID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func findShort(es []Entry, short string) (Entry, bool) {
	for _, e := range es {
		if e.Background() && e.ID == short {
			return e, true
		}
	}
	return Entry{}, false
}
