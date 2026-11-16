package launch

// Phase is where the connection to the Stage server stands (the TS
// HostPhase of app/src/platform/index.ts).
type Phase string

// Phases.
const (
	// PhaseStarting: ensuring the per-user service and waiting for readiness.
	PhaseStarting Phase = "starting"
	// PhaseReady: the server answers with a compatible API.
	PhaseReady Phase = "ready"
	// PhaseReconnecting: it was ready and stopped answering; retrying.
	PhaseReconnecting Phase = "reconnecting"
	// PhaseStopped: not running (after the reconnect window).
	PhaseStopped Phase = "stopped"
	// PhaseFailed: the service or server failed.
	PhaseFailed Phase = "failed"
	// PhaseUnsupported: no usable per-user service manager.
	PhaseUnsupported Phase = "unsupported"
	// PhaseMisconfigured: invalid configuration, permissions or app location.
	PhaseMisconfigured Phase = "misconfigured"
	// PhaseConflict: another (terminal) server owns the data and cannot be used.
	PhaseConflict Phase = "conflict"
	// PhaseIncompatible: the server speaks another API version.
	PhaseIncompatible Phase = "incompatible"
)

// Step is the progress while starting.
type Step string

// Steps.
const (
	StepService Step = "service"
	StepServer  Step = "server"
	StepAPI     Step = "api"
)

// Problem is one diagnostic of `stage service status`.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

// AppInfo describes the desktop app and its bundled CLI.
type AppInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Identity   string `json:"identity"`
	APIVersion int    `json:"apiVersion"`
}

// Server is the last Stage server the app reached.
type Server struct {
	Version    string `json:"version"`
	APIVersion int    `json:"apiVersion"`
	Identity   string `json:"identity"`
	// PublicURL is the server's STAGE_PUBLIC_URL, null when unset.
	PublicURL *string `json:"publicUrl"`
	// Socket is the Unix socket path the app talks to.
	Socket string `json:"socket"`
	// Managed: supervised by the per-user service (false: started from a
	// terminal).
	Managed bool `json:"managed"`
	PID     *int `json:"pid"`
}

// State is the connection state the host shows (the TS HostState of
// app/src/platform/index.ts, field for field).
type State struct {
	Phase Phase `json:"phase"`
	// Step is the progress while starting, null otherwise.
	Step *Step `json:"step"`
	// Title is a one-line headline for the phase.
	Title string `json:"title"`
	// Message is a detail sentence, or "".
	Message string `json:"message"`
	// Fix says what to do about it, or "".
	Fix string `json:"fix"`
	// Command is a shell command that fixes or works around it, or "".
	Command  string    `json:"command"`
	Problems []Problem `json:"problems"`
	// LogTail is the last server log lines, oldest first.
	LogTail []string `json:"logTail"`
	App     AppInfo  `json:"app"`
	Server  *Server  `json:"server"`
	// Foreground: connected to a server started from a terminal, not the
	// service.
	Foreground bool `json:"foreground"`
	// VersionSkew: the server build differs from the app's bundled server.
	VersionSkew bool `json:"versionSkew"`
	// Epoch increments each time the connection becomes ready, so live
	// streams reopen.
	Epoch int `json:"epoch"`
	// Busy: a retry, start or restart is running.
	Busy bool `json:"busy"`
	// Dev: development run against a foreground dev server.
	Dev bool `json:"dev"`
}

// Usable reports whether the app can talk to the server.
func (s State) Usable() bool { return s.Phase == PhaseReady }

func (s State) clone() State {
	out := s
	out.Problems = append([]Problem{}, s.Problems...)
	out.LogTail = append([]string{}, s.LogTail...)
	if s.Step != nil {
		st := *s.Step
		out.Step = &st
	}
	if s.Server != nil {
		srv := *s.Server
		if srv.PublicURL != nil {
			u := *srv.PublicURL
			srv.PublicURL = &u
		}
		if srv.PID != nil {
			p := *srv.PID
			srv.PID = &p
		}
		out.Server = &srv
	}
	return out
}

func stepPtr(s Step) *Step { return &s }
