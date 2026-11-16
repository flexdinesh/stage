package service

import (
	"github.com/flexdinesh/stage/server/internal/instance"
)

// ReportSchema versions the JSON of Status and Report (docs/service.md).
const ReportSchema = 1

// Exit codes of `stage service` (the desktop's contract, docs/service.md).
const (
	CodeOK          = 0
	CodeFailed      = 1 // the server failed, or a manager command failed
	CodeUsage       = 2
	CodeTimeout     = 3 // --wait expired; status: starting or stopping
	CodeNotRunning  = 4 // status only: not running or not installed
	CodeConflict    = 5 // the data dir is held by another (foreground) server
	CodeUnavailable = 6 // no usable service manager
	CodeInvalid     = 7 // refused executable, invalid service.env, foreign/shadowed definition, masked unit
)

// Error is a command failure with its exit code.
type Error struct {
	Code    int
	Msg     string
	Fix     string
	LogTail []string
}

func (e *Error) Error() string { return e.Msg }

// Status is `stage service status --json`.
type Status struct {
	Schema     int              `json:"schema"`
	Identity   string           `json:"identity"`
	State      string           `json:"state"` // not-installed | stopped | starting | running | stopping | failed | unknown
	Ready      bool             `json:"ready"`
	Manager    ManagerStatus    `json:"manager"`
	Definition DefinitionStatus `json:"definition"`
	Process    ProcessStatus    `json:"process"`
	DataDir    string           `json:"dataDir"`
	ServiceEnv FileStatus       `json:"serviceEnv"`
	Socket     SocketStatus     `json:"socket"`
	Lock       LockStatus       `json:"lock"`
	Stage      StageStatus      `json:"stage"`
	T3         *T3Status        `json:"t3"`
	Logs       LogsInfo         `json:"logs"`
	CLI        CLIStatus        `json:"cli"`
	Problems   []Problem        `json:"problems"`
}

// ManagerStatus is the service manager.
type ManagerStatus struct {
	Kind      string `json:"kind"`
	Reachable bool   `json:"reachable"`
	Version   string `json:"version,omitempty"`
	Linger    *bool  `json:"linger,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DefinitionStatus is the unit file or plist.
type DefinitionStatus struct {
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Present      bool     `json:"present"`
	Generated    bool     `json:"generated"`
	Schema       int      `json:"schema,omitempty"`
	Current      bool     `json:"current"`
	Exe          string   `json:"exe,omitempty"`
	ExeExists    bool     `json:"exeExists"`
	LoadedFrom   string   `json:"loadedFrom,omitempty"`
	DropIns      []string `json:"dropIns,omitempty"`
	NeedsReload  bool     `json:"needsReload"`
	LoginStartup bool     `json:"loginStartup"`
	LoginCopy    string   `json:"loginCopyPath,omitempty"`
	Pinned       []KVJSON `json:"pinnedEnv,omitempty"`
}

// KVJSON is a pinned variable.
type KVJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ProcessStatus is the supervised process.
type ProcessStatus struct {
	PID            int    `json:"pid,omitempty"`
	Restarts       int    `json:"restarts"`
	LastExitStatus *int   `json:"lastExitStatus"`
	Result         string `json:"result,omitempty"`
}

// FileStatus is service.env.
type FileStatus struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Error   string `json:"error,omitempty"`
}

// SocketStatus is the private default socket.
type SocketStatus struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Locked bool   `json:"locked"`
}

// LockStatus is the data dir lock.
type LockStatus struct {
	Held           bool           `json:"held"`
	Holder         *instance.Meta `json:"holder"`
	OwnedByService bool           `json:"ownedByService"`
}

// StageStatus is /api/health over the socket.
type StageStatus struct {
	URL        string `json:"url"`
	Reachable  bool   `json:"reachable"`
	Ready      bool   `json:"ready"`
	Version    string `json:"version,omitempty"`
	APIVersion int    `json:"apiVersion,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	DB         string `json:"db,omitempty"`
	Error      string `json:"error,omitempty"`
}

// T3Status is T3 as the server sees it (independent of Stage's own health).
type T3Status struct {
	Connected bool    `json:"connected"`
	LastError *string `json:"lastError"`
}

// CLIStatus is the invoking CLI.
type CLIStatus struct {
	Version    string `json:"version"`
	APIVersion int    `json:"apiVersion"`
	Exe        string `json:"exe"`
	// AgentResolvesService: agent commands in this shell reach the service
	// socket (client resolution, docs/runtime.md).
	AgentResolvesService bool `json:"agentResolvesService"`
}

// Problem is one diagnostic.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Report is the result of a mutating command (`--json`).
type Report struct {
	Schema   int      `json:"schema"`
	Command  string   `json:"command"`
	OK       bool     `json:"ok"`
	Code     int      `json:"code"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Warnings []string `json:"warnings"`
	Notes    []string `json:"notes"`
	Changes  Changes  `json:"changes"`
	LogTail  []string `json:"logTail"`
	Status   *Status  `json:"status,omitempty"`
}

// Changes are what a command changed.
type Changes struct {
	DefinitionWritten bool  `json:"definitionWritten"`
	DefinitionRemoved bool  `json:"definitionRemoved"`
	ManagerReloaded   bool  `json:"managerReloaded"`
	ServiceEnvCreated bool  `json:"serviceEnvCreated"`
	Started           bool  `json:"started"`
	Stopped           bool  `json:"stopped"`
	PendingRestart    bool  `json:"pendingRestart"`
	LoginStartup      *bool `json:"loginStartup,omitempty"`
}
