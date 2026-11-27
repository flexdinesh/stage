// Package remote is a remote machine over its worker channel (ADR 0002,
// docs/execution.md "Remote machines"). The main server hosts a Machine
// whose Adapter proxies execution.Adapter, its optional interfaces and
// machine.Ops through workerchan.Hub calls; the worker answers them with a
// Host that runs the real adapters and machine.LocalOps. The coordinator,
// sync, the runners and the contract suite cannot tell it from a local
// machine: an offline worker is an offline harness.
//
// This file is the RPC protocol both sides share.
package remote

import (
	"errors"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Calls the main server sends a worker (Envelope.ConnectionID names the
// harness connection for adapter.*).
const (
	KindHealth               = "adapter.health"
	KindFeatures             = "adapter.features"
	KindFeaturesForWorkspace = "adapter.featuresForWorkspace"
	KindWorkspaces           = "adapter.workspaces"
	KindObserve              = "adapter.observe"
	KindLaunch               = "adapter.launch"
	KindStop                 = "adapter.stop"
	KindAttention            = "adapter.attention"
	KindSessionSearch        = "adapter.sessionSearch"
	KindOpening              = "adapter.opening"
	KindResolveLink          = "adapter.resolveLink"
	KindCorrelate            = "adapter.correlate"
	KindEvents               = "adapter.events"
	KindRequests             = "adapter.requests"
	KindRespond              = "adapter.respond"
	KindSend                 = "adapter.send"
	KindAct                  = "adapter.act"
	KindInventory            = "ops.inventory"
)

// Worker-initiated calls. The callbacks name the pending request they
// belong to (Envelope.Call) and are refused once it stopped waiting.
const (
	// KindCheckpointSave (callback of adapter.launch) saves a launch
	// checkpoint before the adapter's next external effect.
	KindCheckpointSave = "checkpoint.save"
	// KindLaunchImage (callback of adapter.launch) fetches one of the
	// intent's images (ImageRequest -> execution.Image).
	KindLaunchImage = "launch.image"
	// KindObservation (callback of adapter.events) ingests one pushed
	// observation.
	KindObservation = "events.observation"
)

// Result error codes beyond workerchan's.
const (
	CodeUnavailable         = "unavailable" // execution.ErrUnavailable
	CodeNotFound            = "not_found"   // execution.ErrNotFound
	CodeNoReply             = "no_reply"    // execution.ErrNoReply
	CodeSessionSearchCursor = "session_search_cursor"
	// CodeRejected: the worker's adapter certified that no execution
	// started (execution.Rejected). Nothing else maps to it.
	CodeRejected = "rejected"
	// CodeRefused: the worker's adapter will not track the session
	// (execution.Refused).
	CodeRefused = "refused"
	// CodeInvalidAnswer: the worker's adapter refused an answer
	// (execution.Invalid).
	CodeInvalidAnswer = "invalid_answer"
	// CodeBusy: the worker's adapter can't send the session a message now
	// (execution.Busy).
	CodeBusy = "busy"
	// CodeNotHosted: the worker hosts no adapter for the connection.
	CodeNotHosted = "not_hosted"
	// CodeUnsupported: the worker's adapter lacks the optional interface.
	CodeUnsupported = "unsupported"
)

// description carries placement metadata and connector summaries in ready.details.
type description struct {
	Profile    harness.Profile  `json:"profile"`
	Connectors []ConnectorState `json:"connectors"`
}

// SessionRequest names a session (observe, stop, requests).
type SessionRequest struct {
	Session execution.SessionRef `json:"session"`
}

// WorkspaceRequest is adapter.featuresForWorkspace: a pinned harness
// project and directory.
type WorkspaceRequest struct {
	Project   string `json:"project"`
	Directory string `json:"directory"`
}

// OpeningRequest is adapter.opening.
type OpeningRequest struct {
	Session execution.SessionRef `json:"session"`
	Full    bool                 `json:"full"`
}

// LaunchRequest is adapter.launch. Intent.Images carry no Data: the worker
// fetches each by index (launch.image) while the launch waits.
type LaunchRequest struct {
	Intent     execution.Intent     `json:"intent"`
	Checkpoint execution.Checkpoint `json:"checkpoint"`
}

// ImageRequest is launch.image.
type ImageRequest struct {
	Index int `json:"index"`
}

// LinkRequest is adapter.resolveLink.
type LinkRequest struct {
	Link string `json:"link"`
}

// CorrelateRequest is adapter.correlate (Intent.Images without Data).
type CorrelateRequest struct {
	Intent execution.Intent `json:"intent"`
}

// RespondRequest is adapter.respond.
type RespondRequest struct {
	Session   execution.SessionRef `json:"session"`
	RequestID string               `json:"requestId"`
	Response  execution.Response   `json:"response"`
}

// SendRequest is adapter.send.
type SendRequest struct {
	Session execution.SessionRef `json:"session"`
	Message execution.Message    `json:"message"`
}

// InventoryRequest is ops.inventory.
type InventoryRequest struct {
	Workspaces []execution.WorkspaceInfo `json:"workspaces"`
}

// Empty is a result with no body.
type Empty struct{}

// encodeError is an adapter error as a result error: the execution
// sentinels and Rejected keep their meaning across the channel.
func encodeError(err error) error {
	var rejected *execution.Rejected
	var refused *execution.Refused
	var invalid *execution.Invalid
	var busy *execution.Busy
	var re *workerchan.RemoteError
	switch {
	case errors.As(err, &re):
		return err
	case errors.As(err, &rejected):
		return &workerchan.RemoteError{Code: CodeRejected, Message: rejected.Reason}
	case errors.As(err, &refused):
		return &workerchan.RemoteError{Code: CodeRefused, Message: refused.Reason}
	case errors.As(err, &invalid):
		return &workerchan.RemoteError{Code: CodeInvalidAnswer, Message: invalid.Reason}
	case errors.As(err, &busy):
		return &workerchan.RemoteError{Code: CodeBusy, Message: busy.Reason}
	case errors.Is(err, execution.ErrUnavailable):
		return &workerchan.RemoteError{Code: CodeUnavailable, Message: err.Error()}
	case errors.Is(err, execution.ErrNotFound):
		return &workerchan.RemoteError{Code: CodeNotFound, Message: err.Error()}
	case errors.Is(err, execution.ErrNoReply):
		return &workerchan.RemoteError{Code: CodeNoReply, Message: err.Error()}
	case errors.Is(err, execution.ErrSessionSearchCursor):
		return &workerchan.RemoteError{Code: CodeSessionSearchCursor, Message: err.Error()}
	}
	return err
}

// ErrOffline: the machine's worker is not connected; it wraps
// execution.ErrUnavailable (an offline harness, never failure).
var ErrOffline = fmt.Errorf("%w: its machine is offline (the worker is not connected)", execution.ErrUnavailable)

// decodeError is a channel call's error as the adapter contract's: offline,
// not hosted or an older worker is unavailable; a lost reply, a timeout or
// any other failure stays uncertain; only the worker adapter's certified
// rejection is execution.Rejected.
func decodeError(err error) error {
	var re *workerchan.RemoteError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workerchan.ErrOffline):
		return ErrOffline
	case errors.Is(err, workerchan.ErrNegotiating):
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, err)
	case !errors.As(err, &re):
		return err
	}
	switch re.Code {
	case CodeRejected:
		return &execution.Rejected{Reason: re.Message}
	case CodeRefused:
		return &execution.Refused{Reason: re.Message}
	case CodeInvalidAnswer:
		return &execution.Invalid{Reason: re.Message}
	case CodeBusy:
		return &execution.Busy{Reason: re.Message}
	case CodeUnavailable:
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, re.Message)
	case CodeNotFound:
		return fmt.Errorf("%w: %s", execution.ErrNotFound, re.Message)
	case CodeNoReply:
		return fmt.Errorf("%w: %s", execution.ErrNoReply, re.Message)
	case CodeSessionSearchCursor:
		return execution.ErrSessionSearchCursor
	case CodeNotHosted:
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, re.Message)
	case CodeUnsupported, workerchan.CodeOperationUnavailable, workerchan.CodeUnknownKind:
		return &db.ConflictError{Code: workerchan.CodeOperationUnavailable, Msg: re.Message}
	}
	return err
}
