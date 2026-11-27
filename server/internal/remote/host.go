package remote

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Host is the worker side of a remote machine: the in-process machine
// whose adapters and ops answer the main server's calls. Setup code
// installs adapters through Local().Install, as on the main server's own
// machine; each install is reported to the main server (Changes).
type Host struct {
	local   *machine.Local
	changes chan struct{}
	// ready: the worker composed its connection setup (SetReady); until
	// then connector calls are refused as not ready.
	ready atomic.Bool

	mu         sync.Mutex
	onAttached func(ctx context.Context, kinds []string) error
	// Scoped runs must check ownership while other setup calls for the kind
	// cannot replace the connection.
	connectorLocks map[string]*sync.Mutex
}

// OnAttached sets the hook Attached runs (the worker composes its
// connection setup once the main server is reachable).
func (h *Host) OnAttached(fn func(ctx context.Context, kinds []string) error) {
	h.mu.Lock()
	h.onAttached = fn
	h.mu.Unlock()
}

// Attached composes the host before the session can publish ready.
func (h *Host) Attached(ctx context.Context, kinds []string) error {
	h.ready.Store(false)
	h.mu.Lock()
	fn := h.onAttached
	h.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, kinds); err != nil {
			return err
		}
	}
	h.ready.Store(true)
	return nil
}

// Ready reports whether the connection setup is composed.
func (h *Host) Ready() bool { return h.ready.Load() }

// NewHost serves local's adapters and ops. It takes local's change hook.
func NewHost(local *machine.Local) *Host {
	h := &Host{local: local, changes: make(chan struct{}, 1), connectorLocks: map[string]*sync.Mutex{}}
	local.OnChange(func(int64, int64) { h.Notify() })
	return h
}

// Notify reports that what the host serves changed.
func (h *Host) Notify() {
	select {
	case h.changes <- struct{}{}:
	default:
	}
}

// Local is the machine the host serves.
func (h *Host) Local() *machine.Local { return h.local }

// Changes signals that the hosted connections changed.
func (h *Host) Changes() <-chan struct{} { return h.changes }

// Snapshot captures the configured connectors and installed adapter traits.
func (h *Host) Snapshot(ctx context.Context) (workerchan.Ready, error) {
	r := workerchan.Ready{Kinds: []string{}, Connections: []workerchan.Connection{}}
	d := description{Profile: h.local.Profile(), Connectors: []ConnectorState{}}
	for id, a := range h.local.Adapters() {
		r.Connections = append(r.Connections, workerchan.Connection{ConnectionID: id, Traits: execution.Traits(a)})
	}
	slices.SortFunc(r.Connections, func(a, b workerchan.Connection) int { return cmp.Compare(a.ConnectionID, b.ConnectionID) })
	for kind, c := range h.local.Connectors() {
		r.Kinds = append(r.Kinds, kind)
		d.Connectors = append(d.Connectors, ConnectorState{Kind: kind, Summary: c.Summary(ctx)})
	}
	slices.Sort(r.Kinds)
	slices.SortFunc(d.Connectors, func(a, b ConnectorState) int { return strings.Compare(a.Kind, b.Kind) })
	var err error
	r.Details, err = json.Marshal(d)
	return r, err
}

// Handlers answer the main server's calls by kind.
func (h *Host) Handlers() map[string]workerchan.WorkerHandler {
	return map[string]workerchan.WorkerHandler{
		KindConnectorStatus: h.connector(func(ctx context.Context, c harness.Connector, r ConnectorRequest) (harness.Result, error) {
			return harness.Result{}, nil
		}),
		KindConnectorConfigure: h.connector(func(ctx context.Context, c harness.Connector, r ConnectorRequest) (harness.Result, error) {
			return c.Configure(ctx, r.Settings)
		}),
		KindConnectorRun: h.connector(func(ctx context.Context, c harness.Connector, r ConnectorRequest) (harness.Result, error) {
			return c.Run(ctx, r.Action, r.Input)
		}),
		KindConnectorDisconnect: h.connector(func(ctx context.Context, c harness.Connector, _ ConnectorRequest) (harness.Result, error) {
			return harness.Result{}, c.Disconnect(ctx)
		}),
		KindHealth: h.with(func(ctx context.Context, a execution.Adapter, _ workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			return a.Health(ctx), nil
		}),
		KindFeatures: h.with(func(ctx context.Context, a execution.Adapter, _ workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			return a.Features(ctx)
		}),
		KindFeaturesForWorkspace: h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r WorkspaceRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			// Without workspace-scoped features the harness-wide ones apply.
			if wf, ok := execution.As[execution.WorkspaceFeatures](a); ok {
				return wf.FeaturesForWorkspace(ctx, r.Project, r.Directory)
			}
			return a.Features(ctx)
		}),
		KindWorkspaces: h.with(func(ctx context.Context, a execution.Adapter, _ workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			ws, err := a.Workspaces(ctx)
			if ws == nil {
				ws = []execution.WorkspaceInfo{}
			}
			return ws, err
		}),
		KindObserve: h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r SessionRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return a.Observe(ctx, r.Session)
		}),
		KindLaunch: h.with(launch),
		KindStop: h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r SessionRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return Empty{}, a.Stop(ctx, r.Session)
		}),
		KindAttention: h.with(func(ctx context.Context, a execution.Adapter, _ workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			refs, err := a.Attention(ctx)
			if refs == nil {
				refs = []execution.SessionRef{}
			}
			return refs, err
		}),
		KindOpening: h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r OpeningRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return a.Opening(ctx, r.Session, r.Full)
		}),
		KindResolveLink: h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			lr, ok := execution.As[execution.LinkResolver](a)
			if !ok {
				return nil, unsupported("link resolution")
			}
			var r LinkRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return lr.ResolveLink(ctx, r.Link)
		}),
		KindCorrelate: h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			c, ok := execution.As[execution.Correlator](a)
			if !ok {
				return nil, unsupported("launch correlation")
			}
			var r CorrelateRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return c.Correlate(ctx, r.Intent)
		}),
		KindEvents: h.with(func(ctx context.Context, a execution.Adapter, _ workerchan.Envelope, up workerchan.Upcall) (any, error) {
			src, ok := execution.As[execution.EventSource](a)
			if !ok {
				return nil, unsupported("pushed events")
			}
			return Empty{}, src.Events(ctx, func(ctx context.Context, o execution.Observation) error {
				_, err := up(ctx, KindObservation, o)
				return err
			})
		}),
		KindRequests: h.interactive(func(ctx context.Context, in execution.Interactive, env workerchan.Envelope) (any, error) {
			var r SessionRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			rs, err := in.Requests(ctx, r.Session)
			if rs == nil {
				rs = []execution.Request{}
			}
			return rs, err
		}),
		KindRespond: h.interactive(func(ctx context.Context, in execution.Interactive, env workerchan.Envelope) (any, error) {
			var r RespondRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return Empty{}, in.Respond(ctx, r.Session, r.RequestID, r.Response)
		}),
		KindSend: h.interactive(func(ctx context.Context, in execution.Interactive, env workerchan.Envelope) (any, error) {
			var r SendRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return Empty{}, in.Send(ctx, r.Session, r.Message)
		}),
		KindAct: h.with(act),
		KindInventory: func(ctx context.Context, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r InventoryRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			ws, err := h.local.Ops().Inventory(ctx, r.Workspaces)
			if ws == nil {
				ws = []execution.WorkspaceInfo{}
			}
			return ws, err
		},
	}
}

// connector answers setup calls once the connector is composed.
func (h *Host) connector(fn func(ctx context.Context, c harness.Connector, r ConnectorRequest) (harness.Result, error)) workerchan.WorkerHandler {
	return func(ctx context.Context, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
		var r ConnectorRequest
		if err := decode(env, &r); err != nil {
			return nil, err
		}
		if !h.ready.Load() {
			return nil, setupNotReady()
		}
		c, ok := h.local.Connector(r.Kind)
		if !ok {
			return nil, &workerchan.RemoteError{Code: CodeNotHosted, Message: "this machine's worker has no connection setup for " + r.Kind}
		}
		if env.Kind != KindConnectorStatus || r.Refresh {
			h.mu.Lock()
			lock := h.connectorLocks[r.Kind]
			if lock == nil {
				lock = &sync.Mutex{}
				h.connectorLocks[r.Kind] = lock
			}
			h.mu.Unlock()
			lock.Lock()
			defer lock.Unlock()
		}
		if env.Kind == KindConnectorRun {
			if err := ctx.Err(); err != nil {
				return nil, encodeActionError(err)
			}
			if r.ConnectionID < 0 {
				return nil, encodeError(&execution.Invalid{Reason: "Invalid setup action connection ID."})
			}
			if r.ConnectionID != 0 && c.Summary(ctx).ConnectionID != r.ConnectionID {
				return nil, EncodeSetupError(db.ConnectionRetired())
			}
			if err := ctx.Err(); err != nil {
				return nil, encodeActionError(err)
			}
		}
		res, err := fn(ctx, c, r)
		if err != nil {
			if env.Kind == KindConnectorRun {
				err = encodeActionError(err)
			}
			return nil, EncodeSetupError(err)
		}
		if env.Kind == KindConnectorRun {
			// A status read can fail independently of the completed mutation.
			// RefreshCapabilities publishes its summary for the main server.
			return workerchan.RefreshCapabilities{Value: connectorRunReply{Result: &res}}, nil
		}
		st, err := c.Status(ctx, env.Kind == KindConnectorStatus && r.Refresh)
		if err != nil {
			return nil, EncodeSetupError(err)
		}
		out := ConnectorStatus{Status: st, Result: res}
		if env.Kind != KindConnectorStatus {
			return workerchan.RefreshCapabilities{Value: out}, nil
		}
		if r.Refresh {
			h.Notify()
		}
		return out, nil
	}
}

type adapterCall func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, up workerchan.Upcall) (any, error)

// with runs fn on the call's connection's adapter (not_hosted when the
// worker has none) and encodes its error for the channel.
func (h *Host) with(fn adapterCall) workerchan.WorkerHandler {
	return func(ctx context.Context, env workerchan.Envelope, up workerchan.Upcall) (any, error) {
		a, ok := h.local.Adapter(env.ConnectionID)
		if !ok {
			return nil, &workerchan.RemoteError{Code: CodeNotHosted, Message: fmt.Sprintf("this machine's worker hosts no adapter for connection %d", env.ConnectionID)}
		}
		out, err := fn(ctx, a, env, up)
		if err != nil {
			return nil, encodeError(err)
		}
		return out, nil
	}
}

func (h *Host) interactive(fn func(ctx context.Context, in execution.Interactive, env workerchan.Envelope) (any, error)) workerchan.WorkerHandler {
	return h.with(func(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
		in, ok := execution.As[execution.Interactive](a)
		if !ok {
			return nil, unsupported("interaction")
		}
		return fn(ctx, in, env)
	})
}

// launch fetches the intent's images, then runs the adapter's launch with
// a checkpoint save that calls back to the main server before each
// external effect: when the save fails (the main server stopped waiting,
// the channel broke, the attempt changed), the adapter stops.
func launch(ctx context.Context, a execution.Adapter, env workerchan.Envelope, up workerchan.Upcall) (any, error) {
	var r LaunchRequest
	if err := decode(env, &r); err != nil {
		return nil, err
	}
	in := r.Intent
	for i := range in.Images {
		raw, err := up(ctx, KindLaunchImage, ImageRequest{Index: i})
		if err != nil {
			return nil, fmt.Errorf("fetch launch image %d: %w", i, err)
		}
		var img execution.Image
		if err := json.Unmarshal(raw, &img); err != nil {
			return nil, fmt.Errorf("malformed launch image %d: %w", i, err)
		}
		in.Images[i] = img
	}
	save := func(ctx context.Context, cp execution.Checkpoint) error {
		_, err := up(ctx, KindCheckpointSave, cp)
		return err
	}
	return a.Launch(ctx, in, r.Checkpoint, save)
}

func decode(env workerchan.Envelope, v any) error {
	if err := json.Unmarshal(env.Body, v); err != nil {
		return fmt.Errorf("malformed %s request: %w", env.Kind, err)
	}
	return nil
}

func unsupported(what string) error {
	return &workerchan.RemoteError{Code: workerchan.CodeOperationUnavailable, Message: "this harness does not support " + what}
}
