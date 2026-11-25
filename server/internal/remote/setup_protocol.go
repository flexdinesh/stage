package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Connection setup over the channel (docs/execution.md "Remote machines").
// The worker runs the machine's real connectors; the main server proxies
// them. The setup ports the connectors need from the main server (its
// record of the machine's connections and their non-secret settings) are
// worker-initiated calls, always scoped to the calling machine. Harness
// credentials never cross: they stay in the worker's credentials file.

// Connector calls the main server sends a worker.
const (
	KindConnectorStatus     = "connector.status"
	KindConnectorConfigure  = "connector.configure"
	KindConnectorRun        = "connector.run"
	KindConnectorDisconnect = "connector.disconnect"
)

// Setup port calls a worker sends the main server (no request: answered by
// the main server's hub handlers, scoped to the worker's machine).
const (
	KindT3Get          = "setup.t3.get"
	KindT3SetURL       = "setup.t3.setURL"
	KindT3SetPublicURL = "setup.t3.setPublicURL"
	KindT3LastError    = "setup.t3.lastError"
	KindOpenCodeGet    = "setup.opencode.get"
	KindOpenCodeSave   = "setup.opencode.save"
	// KindClaudeGet and KindClaudeSave carry config.ClaudeSettings (the
	// command, the config directory connected with, disconnected): Claude
	// Code's setup holds no secrets.
	KindClaudeGet        = "setup.claude.get"
	KindClaudeSave       = "setup.claude.save"
	KindIdentityActive   = "setup.identity.active"
	KindIdentitySwitch   = "setup.identity.switch"
	KindIdentityGet      = "setup.identity.get"
	KindIdentitySet      = "setup.identity.set"
	KindIdentityRetired  = "setup.identity.retired"
	KindIdentityEvidence = "setup.identity.evidence"
)

// Setup result error codes beyond the adapter calls'.
const (
	CodeInvalid  = "invalid"  // db.InvalidError (400)
	CodeConflict = "conflict" // db.ConflictError (409); "conflict:<code>" keeps its code
	CodeHarness  = "harness"  // db.HarnessError (502)
	// CodeSetupNotReady: the worker has not composed its connectors yet.
	CodeSetupNotReady = "setup_not_ready"

	conflictPrefix       = CodeConflict + ":"
	setupNotReadyMessage = "the machine's Stage worker is still starting its harness setup; try again in a moment"
)

// ConnectorState is one connector a worker hosts, with its memory-only
// summary (Description.Connectors).
type ConnectorState struct {
	Kind    string          `json:"kind"`
	Summary harness.Summary `json:"summary"`
}

// ConnectorRequest names a connector call's harness kind and carries its
// input: Refresh for status, Settings for configure, Action and Input for
// run.
type ConnectorRequest struct {
	Kind     string          `json:"kind"`
	Refresh  bool            `json:"refresh,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
	Action   string          `json:"action,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// ConnectorStatus is connector.status's (and every mutation's) result: the
// status after the call, and what the call replaced.
type ConnectorStatus struct {
	Status harness.Status `json:"status"`
	Result harness.Result `json:"result"`
}

// T3Settings is the non-secret part of a machine's T3 setup (db.Connection).
type T3Settings struct {
	LastError      *string    `json:"lastError"`
	LastFullSyncAt *time.Time `json:"lastFullSyncAt"`
	T3URL          *string    `json:"t3Url"`
	T3PublicURL    *string    `json:"t3PublicUrl"`
	EnvironmentID  *string    `json:"environmentId"`
	ConnectionID   int64      `json:"connectionId"`
}

// T3SettingsOf is c on the wire.
func T3SettingsOf(c db.Connection) T3Settings {
	return T3Settings{LastError: c.LastError, LastFullSyncAt: c.LastFullSyncAt, T3URL: c.T3URL, T3PublicURL: c.T3PublicURL, EnvironmentID: c.EnvironmentID, ConnectionID: c.ConnectionID}
}

// Connection is s as the store's type.
func (s T3Settings) Connection() db.Connection {
	return db.Connection{LastError: s.LastError, LastFullSyncAt: s.LastFullSyncAt, T3URL: s.T3URL, T3PublicURL: s.T3PublicURL, EnvironmentID: s.EnvironmentID, ConnectionID: s.ConnectionID}
}

// URLRequest is setup.t3.setURL ("" clears).
type URLRequest struct {
	URL string `json:"url"`
}

// MessageRequest is setup.t3.lastError ("" clears).
type MessageRequest struct {
	Message string `json:"message"`
}

// OpenCodeSettings is setup.opencode.get's result (no credential) and
// setup.opencode.save's request (Verified ignored).
type OpenCodeSettings struct {
	Setup    config.OpenCodeSetup `json:"setup"`
	Verified string               `json:"verified,omitempty"`
}

// KindRequest names a harness kind (setup.identity.active, retired).
type KindRequest struct {
	Kind string `json:"kind"`
}

// IDResult is a connection id (0: none).
type IDResult struct {
	ID int64 `json:"id"`
}

// IDsResult is a list of connection ids.
type IDsResult struct {
	IDs []int64 `json:"ids"`
}

// ConnectionRequest names one of the machine's connections.
type ConnectionRequest struct {
	ID int64 `json:"id"`
}

// SwitchRequest is setup.identity.switch.
type SwitchRequest struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	Label    string `json:"label"`
	Revive   int64  `json:"revive"`
}

// SwitchResult is db.Switch on the wire.
type SwitchResult struct {
	Old     int64 `json:"old"`
	New     int64 `json:"new"`
	Changed bool  `json:"changed"`
	Revived bool  `json:"revived"`
}

// SwitchOf is sw on the wire.
func SwitchOf(sw db.Switch) SwitchResult {
	return SwitchResult{Old: sw.Old, New: sw.New, Changed: sw.Changed, Revived: sw.Revived}
}

// Switch is r as the store's type.
func (r SwitchResult) Switch() db.Switch {
	return db.Switch{Old: r.Old, New: r.New, Changed: r.Changed, Revived: r.Revived}
}

// IdentityRequest is setup.identity.set.
type IdentityRequest struct {
	ID       int64  `json:"id"`
	Identity string `json:"identity"`
	Label    string `json:"label"`
}

// Evidence is setup.identity.evidence's result (db.Store.ConnectionEvidence).
type Evidence struct {
	Bindings     []db.ProjectBinding     `json:"bindings"`
	Observations []execution.Observation `json:"observations"`
	Unknown      bool                    `json:"unknown"`
}

// EncodeSetupError is a setup error as a result error: the store's typed
// errors keep their meaning (and HTTP status) across the channel; the
// execution sentinels as for adapter calls.
func EncodeSetupError(err error) error {
	var (
		re       *workerchan.RemoteError
		invalid  *db.InvalidError
		conflict *db.ConflictError
		harnErr  *db.HarnessError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &re):
		return err
	case errors.As(err, &invalid):
		return &workerchan.RemoteError{Code: CodeInvalid, Message: invalid.Msg}
	case errors.As(err, &conflict):
		code := CodeConflict
		if conflict.Code != "" {
			code = conflictPrefix + conflict.Code
		}
		return &workerchan.RemoteError{Code: code, Message: conflict.Msg}
	case errors.As(err, &harnErr):
		return &workerchan.RemoteError{Code: CodeHarness, Message: harnErr.Error()}
	case errors.Is(err, db.ErrNotFound):
		return &workerchan.RemoteError{Code: CodeNotFound, Message: err.Error()}
	}
	return encodeError(err)
}

// DecodeSetupError is a setup call's error as the store's typed error. An
// offline machine, a worker still starting, or one without the call is
// ErrOffline-like unavailability (execution.ErrUnavailable).
func DecodeSetupError(err error) error {
	var re *workerchan.RemoteError
	if err == nil || !errors.As(err, &re) {
		return decodeError(err)
	}
	switch {
	case re.Code == CodeInvalid:
		return db.Invalid("%s", re.Message)
	case re.Code == CodeConflict:
		return db.Conflict("%s", re.Message)
	case strings.HasPrefix(re.Code, conflictPrefix):
		return &db.ConflictError{Code: strings.TrimPrefix(re.Code, conflictPrefix), Msg: re.Message}
	case re.Code == CodeHarness:
		return &db.HarnessError{Err: errors.New(re.Message)}
	case re.Code == CodeNotFound:
		return fmt.Errorf("%w: %s", db.ErrNotFound, re.Message)
	case re.Code == CodeSetupNotReady:
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, re.Message)
	}
	return decodeError(err)
}

// setupNotReady: the worker has not composed its connectors yet.
func setupNotReady() error {
	return &workerchan.RemoteError{Code: CodeSetupNotReady, Message: setupNotReadyMessage}
}
