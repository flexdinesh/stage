package worker

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Status is `stage worker status`: the local config, the local worker
// process, and this machine as the main server sees it.
type Status struct {
	Schema int          `json:"schema"`
	Config ConfigStatus `json:"config"`
	// Process is the worker holding this directory (null when none).
	Process *ProcessStatus `json:"process"`
	// Server is the live check against the main server (null when not
	// joined).
	Server *ServerStatus `json:"server"`
}

// ConfigStatus is worker.json, without the token.
type ConfigStatus struct {
	Path      string     `json:"path"`
	Joined    bool       `json:"joined"`
	URL       string     `json:"url,omitempty"`
	MachineID int64      `json:"machineId,omitempty"`
	Label     string     `json:"label,omitempty"`
	JoinedAt  *time.Time `json:"joinedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// ProcessStatus is the local worker process.
type ProcessStatus struct {
	PID       int       `json:"pid"`
	Managed   bool      `json:"managed"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	// State is its channel state (connecting, online, needs_update).
	State            string    `json:"state"`
	Since            time.Time `json:"since"`
	LastError        string    `json:"lastError,omitempty"`
	Observation      string    `json:"observation,omitempty"`
	ObservationError string    `json:"observationError,omitempty"`
	ObservationLost  uint64    `json:"observationLost,omitempty"`
	// Gateway is its agent gateway's listener URLs.
	Gateway []string `json:"gateway,omitempty"`
}

// ServerStatus is GET /api/worker/machine.
type ServerStatus struct {
	Reachable bool `json:"reachable"`
	// Status is the machine's status on the server (online, offline,
	// needs_update), or revoked when the token is refused.
	Status     string     `json:"status,omitempty"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Status exit codes.
const (
	StatusOnline    = 0 // joined, and the server reports the machine online
	StatusNotOnline = 1 // joined, but offline, unreachable or needs update
	StatusNotJoined = 4 // no worker.json
	StatusRevoked   = 7 // the server refused the token
)

// ReadStatus reports the worker at configPath and its exit code.
func ReadStatus(ctx context.Context, configPath string, hc *http.Client) (Status, int) {
	st := Status{Schema: 1, Config: ConfigStatus{Path: configPath}}
	dir := Dir(configPath)
	if held, h, _ := ReadHolder(dir); held && h != nil {
		p := &ProcessStatus{PID: h.PID, Managed: h.Managed, Version: h.Version, StartedAt: h.StartedAt}
		if s, _ := ReadState(dir); s != nil && s.PID == h.PID {
			p.State, p.Since, p.LastError, p.Gateway = s.State, s.Since, s.LastError, s.Gateway
			p.Observation, p.ObservationError = s.Observation, s.ObservationError
			p.ObservationLost = s.ObservationLost
		}
		st.Process = p
	}
	c, err := LoadConfig(configPath)
	if errors.Is(err, ErrNotJoined) {
		return st, StatusNotJoined
	}
	if err != nil {
		st.Config.Error = err.Error()
		return st, StatusNotOnline
	}
	joined := c.JoinedAt
	st.Config = ConfigStatus{Path: configPath, Joined: true, URL: c.URL, MachineID: c.MachineID, Label: c.Label, JoinedAt: &joined}
	srv := &ServerStatus{}
	st.Server = srv
	var m RemoteMachine
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = newAPI(c.URL, c.Token, hc).do(ctx, "GET", "/api/worker/machine", nil, &m)
	switch {
	case err == nil:
		srv.Reachable, srv.Status, srv.LastSeenAt = true, m.Status, m.LastSeenAt
		srv.Error = m.StatusReason
		if m.Status == StateOnline {
			return st, StatusOnline
		}
	case Revoked(err):
		srv.Reachable, srv.Status, srv.Error = true, StateRevoked, err.Error()
		return st, StatusRevoked
	default:
		srv.Error = err.Error()
	}
	return st, StatusNotOnline
}
