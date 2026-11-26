package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Why a remote machine's setup cannot answer now (UnavailableError.Code).
const (
	CodeMachineOffline     = "machine_offline"
	CodeMachineNeedsUpdate = "machine_needs_update"
	CodeMachineStarting    = "machine_setup_starting"
)

// UnavailableError: a remote machine's connection setup cannot answer now
// (offline, incompatible channel, or negotiating). It
// wraps execution.ErrUnavailable: an offline machine is an offline
// harness, never a failure.
type UnavailableError struct {
	Code    string
	Message string
}

func (e *UnavailableError) Error() string { return e.Message }
func (e *UnavailableError) Unwrap() error { return execution.ErrUnavailable }

// SetupHooks connect remote connectors to the main server's record.
type SetupHooks struct {
	// Kinds are the harness kinds a worker sets up (registry order); a
	// hosted kind only where its machine allows it (Machine.Allows).
	Kinds []string
	// Active is the machine's active connection of kind (0: none).
	Active func(machineID int64, kind string) int64
	// Changed runs after a setting change, action or disconnect succeeded
	// (follow the connections; a pairing starts a full sync). May be nil.
	Changed func(machineID int64, kind string, action string)
}

// Connector is the main server's proxy of one harness kind's connection
// setup on a remote machine. Summary is memory only: the connection is the
// main server's record and the rest is the worker's last report (describe,
// or the last setup call's status).
type Connector struct {
	m    *Machine
	kind string
}

var _ harness.Connector = (*Connector)(nil)

// summary is the worker's last report for kind (zero when none).
func (m *Machine) summary(kind string) harness.Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshSnapshot()
	return m.sums[kind]
}

// remember keeps a status's summary as kind's last report.
func (m *Machine) remember(session, kind string, s harness.Summary) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hub.SessionID(m.id) == session {
		m.refreshSnapshot()
		m.sums[kind] = s
	}
}

// unavailable is why the machine's setup cannot answer now (nil: it can).
func (m *Machine) unavailable() error {
	p := m.hub.Presence(m.id)
	switch {
	case p.Online:
		return nil
	case p.Negotiating:
		return &UnavailableError{Code: CodeMachineStarting, Message: workerchan.ErrNegotiating.Error()}
	case p.NeedsUpdate != nil:
		return &UnavailableError{Code: CodeMachineNeedsUpdate, Message: p.NeedsUpdate.Reason}
	}
	return &UnavailableError{Code: CodeMachineOffline, Message: "This machine is offline: its Stage worker is not connected. Its harness setup returns when it reconnects."}
}

func (c *Connector) Summary(context.Context) harness.Summary {
	s := c.m.summary(c.kind)
	if c.m.hosts != nil && c.m.hosts.setup.Active != nil {
		s.ConnectionID = c.m.hosts.setup.Active(c.m.id, c.kind)
	}
	if err := c.m.unavailable(); err != nil {
		s.Connected = false
		if s.Configured {
			s.LastError = err.Error()
		}
	}
	return s
}

// call runs one connector call on the worker and keeps its status.
func (c *Connector) call(ctx context.Context, kind string, r ConnectorRequest) (ConnectorStatus, error) {
	if kind == KindConnectorRun {
		if err := ctx.Err(); err != nil {
			return ConnectorStatus{}, err
		}
	}
	if err := c.m.unavailable(); err != nil {
		return ConnectorStatus{}, err
	}
	snapshot, session := c.m.hub.Advertisement(c.m.id)
	if snapshot == nil {
		return ConnectorStatus{}, &UnavailableError{Code: CodeMachineStarting, Message: workerchan.ErrNegotiating.Error()}
	}
	if !slices.Contains(snapshot.Kinds, c.kind) {
		return ConnectorStatus{}, &UnavailableError{Code: CodeMachineNeedsUpdate, Message: "Update Stage on the worker machine to set up " + c.kind + "."}
	}
	r.Kind = c.kind
	var raw json.RawMessage
	var err error
	if r.ConnectionID != 0 {
		// A worker that predates adapter.act would silently ignore the fence.
		raw, err = c.m.hub.CallRequiringKind(ctx, c.m.id, kind, KindAct, r, CallTimeout)
	} else {
		raw, err = c.m.hub.Call(ctx, c.m.id, kind, 0, r, CallTimeout)
	}
	if kind == KindConnectorRun {
		if err != nil {
			var re *workerchan.RemoteError
			if errors.As(err, &re) && re.Code == workerchan.CodeOperationUnavailable {
				return ConnectorStatus{}, &UnavailableError{Code: CodeMachineNeedsUpdate, Message: "Update Stage on the worker machine to run connection actions."}
			}
			return ConnectorStatus{}, actionCallError(err, connectorError)
		}
		var out connectorRunReply
		if err := jsonUnmarshal(raw, &out); err != nil {
			return ConnectorStatus{}, &execution.ActionUncertain{Cause: err}
		}
		if out.Result == nil {
			return ConnectorStatus{}, &execution.ActionUncertain{Cause: errors.New("worker returned no setup action result")}
		}
		if out.Status != nil {
			c.m.remember(session, c.kind, out.Status.Summary)
		}
		return ConnectorStatus{Result: *out.Result}, nil
	}
	if err != nil {
		return ConnectorStatus{}, connectorError(err)
	}
	var out ConnectorStatus
	if err := jsonUnmarshal(raw, &out); err != nil {
		return ConnectorStatus{}, err
	}
	c.m.remember(session, c.kind, out.Status.Summary)
	return out, nil
}

// connectorError maps a connector call's failure: offline and a worker
// still starting are UnavailableError; the store's typed errors keep their
// status.
func connectorError(err error) error {
	var re *workerchan.RemoteError
	switch {
	case errors.Is(err, workerchan.ErrOffline):
		return &UnavailableError{Code: CodeMachineOffline, Message: "This machine is offline: its Stage worker is not connected. Its harness setup returns when it reconnects."}
	case errors.Is(err, workerchan.ErrLostReply):
		return &UnavailableError{Code: CodeMachineOffline, Message: "This machine's Stage worker did not answer in time; check it is running and try again."}
	case errors.As(err, &re) && re.Code == CodeSetupNotReady:
		return &UnavailableError{Code: CodeMachineStarting, Message: "This machine's Stage worker is still starting its harness setup. Try again in a moment."}
	case errors.As(err, &re) && (re.Code == CodeNotHosted || re.Code == workerchan.CodeUnknownKind):
		return &UnavailableError{Code: CodeMachineNeedsUpdate, Message: "This machine's Stage worker cannot set up this harness: " + re.Message}
	}
	return DecodeSetupError(err)
}

func (c *Connector) Status(ctx context.Context, refresh bool) (harness.Status, error) {
	out, err := c.call(ctx, KindConnectorStatus, ConnectorRequest{Refresh: refresh})
	return out.Status, err
}

func (c *Connector) mutate(ctx context.Context, kind, action string, r ConnectorRequest) (harness.Result, error) {
	out, err := c.call(ctx, kind, r)
	if err != nil {
		return harness.Result{}, err
	}
	if h := c.m.hosts; h != nil {
		if h.setup.Changed != nil {
			h.setup.Changed(c.m.id, c.kind, action)
		}
	}
	return out.Result, nil
}

func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	return c.mutate(ctx, KindConnectorConfigure, "configure", ConnectorRequest{Settings: settings})
}

func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	return c.run(ctx, 0, action, input)
}

// RunForConnection runs a setup action only while connectionID owns this connector.
func (c *Connector) RunForConnection(ctx context.Context, connectionID int64, action string, input json.RawMessage) (harness.Result, error) {
	if connectionID <= 0 {
		return harness.Result{}, &execution.Invalid{Reason: "Invalid setup action connection ID."}
	}
	return c.run(ctx, connectionID, action, input)
}

func (c *Connector) run(ctx context.Context, connectionID int64, action string, input json.RawMessage) (harness.Result, error) {
	if action == "" {
		return harness.Result{}, fmt.Errorf("%w: empty action", harness.ErrUnknownAction)
	}
	return c.mutate(ctx, KindConnectorRun, action, ConnectorRequest{Action: action, Input: input, ConnectionID: connectionID})
}

func (c *Connector) Disconnect(ctx context.Context) error {
	_, err := c.mutate(ctx, KindConnectorDisconnect, "disconnect", ConnectorRequest{})
	return err
}
