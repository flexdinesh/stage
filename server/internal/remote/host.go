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
	onAttached func(ctx context.Context)
}

// OnAttached sets the hook Attached runs (the worker composes its
// connection setup once the main server is reachable).
func (h *Host) OnAttached(fn func(ctx context.Context)) {
	h.mu.Lock()
	h.onAttached = fn
	h.mu.Unlock()
}

// Attached runs the OnAttached hook: a channel session went online.
func (h *Host) Attached(ctx context.Context) {
	h.mu.Lock()
	fn := h.onAttached
	h.mu.Unlock()
	if fn != nil {
		fn(ctx)
	}
}

// Ready reports whether the connection setup is composed.
func (h *Host) Ready() bool { return h.ready.Load() }

// NewHost serves local's adapters and ops. It takes local's change hook.
func NewHost(local *machine.Local) *Host {
	h := &Host{local: local, changes: make(chan struct{}, 1)}
	local.OnChange(func(int64, int64) { h.Notify() })
	return h
}

// Notify reports that what the host serves changed (machine.changed).
func (h *Host) Notify() {
	select {
	case h.changes <- struct{}{}:
	default:
	}
}

// SetReady marks the connection setup composed (its connectors are set on
// Local) and reports the change.
func (h *Host) SetReady() {
	h.ready.Store(true)
	h.Notify()
}

// Local is the machine the host serves.
func (h *Host) Local() *machine.Local { return h.local }

// Changes signals that the hosted connections changed (machine.changed).
func (h *Host) Changes() <-chan struct{} { return h.changes }

// Describe is what the host serves now. Connector summaries are memory
// only, but a connector may read its machine's settings from the main
// server (ctx bounds that).
func (h *Host) Describe(ctx context.Context) Description {
	d := Description{Profile: h.local.Profile(), Connections: []Hosted{}, Connectors: []ConnectorState{}, SetupReady: h.ready.Load()}
	for id, a := range h.local.Adapters() {
		d.Connections = append(d.Connections, Hosted{ConnectionID: id, Traits: execution.Traits(a)})
	}
	slices.SortFunc(d.Connections, func(a, b Hosted) int { return cmp.Compare(a.ConnectionID, b.ConnectionID) })
	if d.SetupReady {
		for kind, c := range h.local.Connectors() {
			d.Connectors = append(d.Connectors, ConnectorState{Kind: kind, Summary: c.Summary(ctx)})
		}
		slices.SortFunc(d.Connectors, func(a, b ConnectorState) int { return strings.Compare(a.Kind, b.Kind) })
	}
	return d
}

// Handlers answer the main server's calls by kind.
func (h *Host) Handlers() map[string]workerchan.WorkerHandler {
	return map[string]workerchan.WorkerHandler{
		KindDescribe: func(ctx context.Context, _ workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			return h.Describe(ctx), nil
		},
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
		KindInventory: func(ctx context.Context, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r InventoryRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			ws, err := h.local.Ops().Inventory(ctx, r.Workspaces, r.Roots)
			if ws == nil {
				ws = []execution.WorkspaceInfo{}
			}
			return ws, err
		},
		KindCheckout: func(ctx context.Context, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			var r CheckoutRequest
			if err := decode(env, &r); err != nil {
				return nil, err
			}
			return h.local.Ops().Checkout(ctx, r.Roots, r.Directory)
		},
	}
}

// connector runs fn on the request's kind's connector, then answers with
// the connector's status (refreshed only for a status call that asks):
// refused until the setup is composed, not_hosted for a kind without one.
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
		res, err := fn(ctx, c, r)
		if err != nil {
			return nil, EncodeSetupError(err)
		}
		st, err := c.Status(ctx, env.Kind == KindConnectorStatus && r.Refresh)
		if err != nil {
			return nil, EncodeSetupError(err)
		}
		return ConnectorStatus{Status: st, Result: res}, nil
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
	return &workerchan.RemoteError{Code: CodeUnsupported, Message: "this harness does not support " + what}
}
