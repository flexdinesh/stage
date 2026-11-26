package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// Setup port calls a worker sends the main server, scoped to the worker's machine.
const (
	KindSettingsGet            = "setup.settings.get"
	KindSettingsSave           = "setup.settings.save"
	KindStatusError            = "setup.status.error"
	KindIdentityActive         = "setup.identity.active"
	KindIdentityActiveIdentity = "setup.identity.activeIdentity"
	KindIdentitySwitch         = "setup.identity.switch"
	KindIdentityExists         = "setup.identity.exists"
	KindIdentitySet            = "setup.identity.set"
	KindIdentityRetired        = "setup.identity.retired"
	KindIdentityEvidence       = "setup.identity.evidence"
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
// run. ConnectionID can fence a run to the connector's active connection.
type ConnectorRequest struct {
	Kind         string          `json:"kind"`
	Refresh      bool            `json:"refresh,omitempty"`
	Settings     json.RawMessage `json:"settings,omitempty"`
	Action       string          `json:"action,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ConnectionID int64           `json:"connectionId,omitempty"`
}

// ConnectorStatus is the status, configure and disconnect reply: the
// status after the call, and what the call replaced.
type ConnectorStatus struct {
	Status harness.Status `json:"status"`
	Result harness.Result `json:"result"`
}

type connectorRunReply struct {
	// Older workers include the status after the action.
	Status *harness.Status `json:"status,omitempty"`
	Result *harness.Result `json:"result"`
}

// KindRequest names a harness on the authenticated machine.
type KindRequest struct {
	Kind string `json:"kind"`
}

// SettingsRequest carries a patch to a harness's public settings.
type SettingsRequest struct {
	Kind  string          `json:"kind"`
	Patch json.RawMessage `json:"patch"`
}

// StatusErrorRequest records a harness error. An empty message clears it.
type StatusErrorRequest struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// IDResult is a connection id (0: none).
type IDResult struct {
	ID int64 `json:"id"`
}

// ActiveIdentityResult is the active connection id and harness instance identity.
type ActiveIdentityResult struct {
	ID       int64  `json:"id"`
	Identity string `json:"identity"`
}

// ConnectionExistsResult reports whether a connection belongs to the worker's machine.
type ConnectionExistsResult struct {
	Exists bool `json:"exists"`
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
	Bindings     []harness.WorkspaceEvidence `json:"bindings"`
	Observations []execution.Observation     `json:"observations"`
	Unknown      bool                        `json:"unknown"`
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

// DecodeSetupError restores the meaning of a failed setup call.
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
