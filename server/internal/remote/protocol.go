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

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Calls the main server sends a worker (Envelope.ConnectionID names the
// harness connection for adapter.*).
const (
	KindHealth               = "adapter.health"
	KindFeatures             = "adapter.features"
	KindFeaturesForWorkspace = "adapter.featuresForWorkspace"
	KindWorkspaces           = "adapter.workspaces"
	KindRegisterWorkspace    = "adapter.registerWorkspace"
	KindObserve              = "adapter.observe"
	KindLaunch               = "adapter.launch"
	KindStop                 = "adapter.stop"
	KindAttention            = "adapter.attention"
	KindOpening              = "adapter.opening"
	KindResolveLink          = "adapter.resolveLink"
	KindCorrelate            = "adapter.correlate"
	KindEvents               = "adapter.events"
	KindRequests             = "adapter.requests"
	KindRespond              = "adapter.respond"
	KindSend                 = "adapter.send"
	KindInventory            = "ops.inventory"
	KindCheckout             = "ops.checkout"
	KindDescribe             = "machine.describe"
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
	// KindChanged (no request) reports that the worker's hosted
	// connections changed; the main server describes the machine again.
	KindChanged = "machine.changed"
)

// Result error codes beyond workerchan's.
const (
	CodeUnavailable = "unavailable" // execution.ErrUnavailable
	CodeNotFound    = "not_found"   // execution.ErrNotFound
	CodeNoReply     = "no_reply"    // execution.ErrNoReply
	// CodeRejected: the worker's adapter certified that no execution
	// started (execution.Rejected). Nothing else maps to it.
	CodeRejected = "rejected"
	// CodeNotHosted: the worker hosts no adapter for the connection.
	CodeNotHosted = "not_hosted"
	// CodeUnsupported: the worker's adapter lacks the optional interface.
	CodeUnsupported = "unsupported"
)

// Description is machine.describe's result: what the worker hosts.
type Description struct {
	Profile     harness.Profile `json:"profile"`
	Connections []Hosted        `json:"connections"`
}

// Hosted is one connection the worker has an adapter for, with the
// optional interfaces it implements (execution.Traits).
type Hosted struct {
	ConnectionID int64    `json:"connectionId"`
	Traits       []string `json:"traits"`
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

// RegisterRequest is adapter.registerWorkspace.
type RegisterRequest struct {
	Title     string `json:"title"`
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
	Roots      []machine.SharedRoot      `json:"roots"`
}

// CheckoutRequest is ops.checkout.
type CheckoutRequest struct {
	Roots     []machine.SharedRoot `json:"roots"`
	Directory string               `json:"directory"`
}

// Empty is a result with no body.
type Empty struct{}

// encodeError is an adapter error as a result error: the execution
// sentinels and Rejected keep their meaning across the channel.
func encodeError(err error) error {
	var rejected *execution.Rejected
	var re *workerchan.RemoteError
	switch {
	case errors.As(err, &re):
		return err
	case errors.As(err, &rejected):
		return &workerchan.RemoteError{Code: CodeRejected, Message: rejected.Reason}
	case errors.Is(err, execution.ErrUnavailable):
		return &workerchan.RemoteError{Code: CodeUnavailable, Message: err.Error()}
	case errors.Is(err, execution.ErrNotFound):
		return &workerchan.RemoteError{Code: CodeNotFound, Message: err.Error()}
	case errors.Is(err, execution.ErrNoReply):
		return &workerchan.RemoteError{Code: CodeNoReply, Message: err.Error()}
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
	case !errors.As(err, &re):
		return err
	}
	switch re.Code {
	case CodeRejected:
		return &execution.Rejected{Reason: re.Message}
	case CodeUnavailable:
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, re.Message)
	case CodeNotFound:
		return fmt.Errorf("%w: %s", execution.ErrNotFound, re.Message)
	case CodeNoReply:
		return fmt.Errorf("%w: %s", execution.ErrNoReply, re.Message)
	case CodeNotHosted, CodeUnsupported:
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, re.Message)
	case workerchan.CodeUnknownKind:
		return fmt.Errorf("%w: the machine's worker needs an update (%s)", execution.ErrUnavailable, re.Message)
	}
	return err
}
