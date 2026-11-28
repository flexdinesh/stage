package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Call budgets. A call ends earlier when its context's deadline does.
const (
	// CallTimeout bounds one call (the channel's default).
	CallTimeout = workerchan.DefaultTimeout
	// HealthTimeout bounds a health check.
	HealthTimeout = 10 * time.Second
	// HealthTTL is how long an online health answer is reused: health is
	// read per connection in lists and every poll.
	HealthTTL = 5 * time.Second
	// EventsWindow is how long one adapter.events call streams before it is
	// called again (sync.Service.Stream runs it in a loop).
	EventsWindow = 10 * time.Minute
)

// Adapter is the main server's proxy of one connection's adapter on a
// remote machine. It implements every optional interface in Go and reports
// which ones the worker's adapter has (execution.Partial), so product code
// reaches them through execution.As.
type Adapter struct {
	m    *Machine
	conn int64

	mu             sync.Mutex
	health         execution.Health
	healthAt       time.Time
	healthSnapshot *workerchan.Ready
}

var (
	_ execution.Adapter           = (*Adapter)(nil)
	_ execution.Catalog           = (*Adapter)(nil)
	_ execution.Inventory         = (*Adapter)(nil)
	_ execution.Sessions          = (*Adapter)(nil)
	_ execution.Launcher          = (*Adapter)(nil)
	_ execution.Partial           = (*Adapter)(nil)
	_ execution.LinkResolver      = (*Adapter)(nil)
	_ execution.WorkspaceFeatures = (*Adapter)(nil)
	_ execution.Correlator        = (*Adapter)(nil)
	_ execution.EventSource       = (*Adapter)(nil)
	_ execution.Interactive       = (*Adapter)(nil)
	_ execution.ActivityReader    = (*Adapter)(nil)
	_ execution.QueueManager      = (*Adapter)(nil)
	_ execution.Actor             = (*Adapter)(nil)
	_ execution.SessionSearcher   = (*Adapter)(nil)
	_ execution.SessionNamer      = (*Adapter)(nil)
	_ execution.WorkspaceAdder    = (*Adapter)(nil)
	_ execution.DirectoryBrowser  = (*Adapter)(nil)
)

// Implements reads the current peer's declaration, never the Go proxy's methods.
func (a *Adapter) Implements(trait string) bool {
	return a.m.implements(a.conn, trait)
}

func callTrait[T any](ctx context.Context, a *Adapter, kind string, body, out any) error {
	return a.request(ctx, kind, body, out, CallTimeout, nil, execution.TraitOf[T]())
}

func (a *Adapter) call(ctx context.Context, kind string, body, out any, timeout time.Duration, cb workerchan.Callback) error {
	return a.request(ctx, kind, body, out, timeout, cb, "")
}

func (a *Adapter) request(ctx context.Context, kind string, body, out any, timeout time.Duration, cb workerchan.Callback, trait string) error {
	raw, err := a.m.hub.CallWithTrait(ctx, a.m.id, kind, a.conn, body, timeout, cb, trait)
	if err = decodeError(err); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("malformed worker reply to %s: %w", kind, err)
	}
	return nil
}

// Health answers at once while the machine is offline; an online answer is
// reused for HealthTTL.
func (a *Adapter) Health(ctx context.Context) execution.Health {
	now := time.Now()
	a.mu.Lock()
	last, at, cached := a.health, a.healthAt, a.healthSnapshot
	a.mu.Unlock()
	if reason, offline := a.m.offline(); offline {
		return execution.Health{Availability: execution.Availability{Reason: reason}, Identity: last.Identity, CheckedAt: now.UTC()}
	}
	snapshot, _ := a.m.hub.Advertisement(a.m.id)
	if snapshot != nil && snapshot == cached && now.Sub(at) < HealthTTL {
		return last
	}
	var h execution.Health
	ctx, cancel := context.WithTimeout(ctx, HealthTimeout)
	defer cancel()
	if err := a.call(ctx, KindHealth, nil, &h, HealthTimeout, nil); err != nil {
		h = execution.Health{Availability: execution.Availability{Reason: "Its machine's worker did not answer: " + err.Error()}, Identity: last.Identity, CheckedAt: now.UTC()}
	}
	a.mu.Lock()
	a.health, a.healthAt, a.healthSnapshot = h, now, snapshot
	a.mu.Unlock()
	return h
}

func (a *Adapter) Features(ctx context.Context) (execution.Capabilities, error) {
	var c execution.Capabilities
	err := a.call(ctx, KindFeatures, nil, &c, CallTimeout, nil)
	a.limitOperations(&c)
	return c, err
}

func (a *Adapter) FeaturesForWorkspace(ctx context.Context, project, directory string) (execution.Capabilities, error) {
	var c execution.Capabilities
	err := callTrait[execution.WorkspaceFeatures](ctx, a, KindFeaturesForWorkspace, WorkspaceRequest{Project: project, Directory: directory}, &c)
	a.limitOperations(&c)
	return c, err
}

var operationKinds = map[execution.Op][]string{
	execution.OpLaunch: {KindLaunch, KindWorkspaces}, execution.OpImages: {KindLaunch},
	execution.OpStop: {KindStop}, execution.OpAdopt: {KindObserve, KindWorkspaces}, execution.OpReply: {KindObserve},
	execution.OpOpening: {KindOpening}, execution.OpAttention: {KindAttention},
	execution.OpRespond: {KindRequests, KindRespond}, execution.OpSend: {KindSend},
	execution.OpSteer: {KindDeliver}, execution.OpQueue: {KindDeliver}, execution.OpActivity: {KindActivity},
	execution.OpQueueManage:     {KindQueue},
	execution.OpSessionRename:   {KindSessionRename},
	execution.OpWorkspaceAdd:    {KindWorkspaceAdd, KindWorkspaces},
	execution.OpDirectoryBrowse: {KindDirectoryBrowse},
}

func (a *Adapter) limitOperations(c *execution.Capabilities) {
	snapshot, _ := a.m.hub.Advertisement(a.m.id)
	if snapshot == nil {
		return
	}
	for op, availability := range c.Operations {
		if !availability.Available {
			continue
		}
		for _, kind := range operationKinds[op] {
			if !slices.Contains(snapshot.ChannelKinds, kind) {
				c.Operations[op] = execution.Availability{Reason: "Update Stage on the worker machine to use " + string(op) + "."}
				break
			}
		}
	}
	// Older workers omit the session surface operations entirely.
	for _, op := range []execution.Op{execution.OpSteer, execution.OpQueue, execution.OpActivity, execution.OpQueueManage} {
		if _, declared := c.Operations[op]; !declared && !slices.Contains(snapshot.ChannelKinds, operationKinds[op][0]) {
			if c.Operations == nil {
				c.Operations = make(map[execution.Op]execution.Availability)
			}
			c.Operations[op] = execution.Availability{Reason: "Update Stage on the worker machine to use " + string(op) + "."}
		}
	}
	// A worker predating session search cannot declare its availability.
	if !slices.Contains(snapshot.ChannelKinds, KindSessionSearch) {
		if c.Operations == nil {
			c.Operations = make(map[execution.Op]execution.Availability)
		}
		c.Operations[execution.OpSessionSearch] = execution.Availability{
			Reason: "Update Stage on the worker machine to search sessions, or enter a session link or ID.",
		}
	} else if c.Operations[execution.OpSessionSearch].Available && !a.Implements(execution.TraitOf[execution.SessionSearcher]()) {
		c.Operations[execution.OpSessionSearch] = execution.Availability{
			Reason: "This worker connection does not offer session search. Enter a session link or ID.",
		}
	}
	// A worker predating session rename cannot declare it either.
	if !slices.Contains(snapshot.ChannelKinds, KindSessionRename) {
		if c.Operations == nil {
			c.Operations = make(map[execution.Op]execution.Availability)
		}
		c.Operations[execution.OpSessionRename] = execution.Availability{Reason: "Update Stage on the worker machine to rename sessions."}
	} else if c.Operations[execution.OpSessionRename].Available && !a.Implements(execution.TraitOf[execution.SessionNamer]()) {
		c.Operations[execution.OpSessionRename] = execution.Availability{Reason: "This worker connection does not offer session rename."}
	}
	if c.Operations[execution.OpWorkspaceAdd].Available && !a.Implements(execution.TraitOf[execution.WorkspaceAdder]()) {
		c.Operations[execution.OpWorkspaceAdd] = execution.Availability{Reason: "This worker connection does not offer adding workspaces."}
	}
	if c.Operations[execution.OpDirectoryBrowse].Available && !a.Implements(execution.TraitOf[execution.DirectoryBrowser]()) {
		c.Operations[execution.OpDirectoryBrowse] = execution.Availability{Reason: "This worker connection does not offer folder browsing. Type the path."}
	}
}

func (a *Adapter) RenameSession(ctx context.Context, ref execution.SessionRef, title string) error {
	return callTrait[execution.SessionNamer](ctx, a, KindSessionRename, SessionRenameRequest{Session: ref, Title: title}, nil)
}

func (a *Adapter) SearchSessions(ctx context.Context, q execution.SessionSearchQuery) (execution.SessionSearchPage, error) {
	var page execution.SessionSearchPage
	err := callTrait[execution.SessionSearcher](ctx, a, KindSessionSearch, q, &page)
	return page, err
}

func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	var ws []execution.WorkspaceInfo
	err := a.call(ctx, KindWorkspaces, nil, &ws, CallTimeout, nil)
	return ws, err
}

func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	var o execution.Observation
	err := a.call(ctx, KindObserve, ObserveRequest{Session: ref, Mode: execution.SessionReadModeFrom(ctx)}, &o, CallTimeout, nil)
	return o, err
}

// Launch sends the intent without image bytes. While it waits, the worker
// calls back to save each checkpoint (save runs here, before the worker's
// adapter performs the following external effect; its failure stops the
// adapter) and to fetch each image. A lost reply is an uncertain outcome:
// the attempt stays unconfirmed and replays from its saved checkpoint.
func (a *Adapter) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	images := in.Images
	var saving sync.Mutex
	cb := func(ctx context.Context, env workerchan.Envelope) (any, error) {
		switch env.Kind {
		case KindCheckpointSave:
			var c execution.Checkpoint
			if err := json.Unmarshal(env.Body, &c); err != nil {
				return nil, fmt.Errorf("malformed checkpoint: %w", err)
			}
			saving.Lock()
			defer saving.Unlock()
			if err := save(ctx, c); err != nil {
				return nil, err
			}
			return Empty{}, nil
		case KindLaunchImage:
			var r ImageRequest
			if err := json.Unmarshal(env.Body, &r); err != nil {
				return nil, fmt.Errorf("malformed image request: %w", err)
			}
			if r.Index < 0 || r.Index >= len(images) {
				return nil, fmt.Errorf("launch has no image %d", r.Index)
			}
			return images[r.Index], nil
		}
		return nil, &workerchan.RemoteError{Code: workerchan.CodeUnknownKind, Message: "adapter.launch takes no " + env.Kind + " callback"}
	}
	var o execution.Observation
	err := a.call(ctx, KindLaunch, LaunchRequest{Intent: withoutImageData(in), Checkpoint: cp}, &o, CallTimeout, cb)
	return o, err
}

// withoutImageData is in with image metadata only.
func withoutImageData(in execution.Intent) execution.Intent {
	if in.Images == nil {
		return in
	}
	imgs := make([]execution.Image, len(in.Images))
	for i, img := range in.Images {
		imgs[i] = execution.Image{Name: img.Name, MimeType: img.MimeType}
	}
	in.Images = imgs
	return in
}

func (a *Adapter) Stop(ctx context.Context, ref execution.SessionRef) error {
	return a.call(ctx, KindStop, SessionRequest{Session: ref}, nil, CallTimeout, nil)
}

func (a *Adapter) Attention(ctx context.Context) ([]execution.SessionRef, error) {
	var refs []execution.SessionRef
	err := a.call(ctx, KindAttention, nil, &refs, CallTimeout, nil)
	return refs, err
}

func (a *Adapter) Opening(ctx context.Context, ref execution.SessionRef, full bool) (*execution.Reply, error) {
	var r *execution.Reply
	err := a.call(ctx, KindOpening, OpeningRequest{Session: ref, Full: full}, &r, CallTimeout, nil)
	return r, err
}

func (a *Adapter) ResolveLink(ctx context.Context, link string) (execution.SessionRef, error) {
	var ref execution.SessionRef
	err := callTrait[execution.LinkResolver](ctx, a, KindResolveLink, LinkRequest{Link: link}, &ref)
	return ref, err
}

func (a *Adapter) Correlate(ctx context.Context, in execution.Intent) (execution.Observation, error) {
	var o execution.Observation
	err := callTrait[execution.Correlator](ctx, a, KindCorrelate, CorrelateRequest{Intent: withoutImageData(in)}, &o)
	return o, err
}

// Events is one long-running adapter.events call: the worker streams its
// adapter's pushed observations as callbacks, each ingested by sink before
// the worker continues (a sink error reaches the worker's adapter). It
// renews after its bounded window. Unexpected termination reaches connection status.
func (a *Adapter) Events(ctx context.Context, sink execution.Sink) error {
	cb := func(ctx context.Context, env workerchan.Envelope) (any, error) {
		if env.Kind != KindObservation {
			return nil, &workerchan.RemoteError{Code: workerchan.CodeUnknownKind, Message: "adapter.events takes no " + env.Kind + " callback"}
		}
		var o execution.Observation
		if err := json.Unmarshal(env.Body, &o); err != nil {
			return nil, fmt.Errorf("malformed observation: %w", err)
		}
		if err := sink(ctx, o); err != nil {
			return nil, err
		}
		return Empty{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, EventsWindow)
	defer cancel()
	err := a.request(ctx, KindEvents, nil, nil, EventsWindow, cb, execution.TraitOf[execution.EventSource]())
	if ctx.Err() != nil || errors.Is(err, workerchan.ErrReplyTimeout) {
		return nil
	}
	return err
}

func (a *Adapter) Requests(ctx context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	var rs []execution.Request
	err := callTrait[execution.Interactive](ctx, a, KindRequests, SessionRequest{Session: ref}, &rs)
	return rs, err
}

func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, requestID string, r execution.Response) error {
	return callTrait[execution.Interactive](ctx, a, KindRespond, RespondRequest{Session: ref, RequestID: requestID, Response: r}, nil)
}

func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	op, err := m.Delivery.Operation()
	if err != nil {
		return err
	}
	return callTrait[execution.Interactive](ctx, a, operationKinds[op][0], SendRequest{Session: ref, Message: m}, nil)
}

func (a *Adapter) Activity(ctx context.Context, ref execution.SessionRef, q execution.ActivityQuery) (execution.Activity, error) {
	var out execution.Activity
	err := callTrait[execution.ActivityReader](ctx, a, KindActivity, ActivityRequest{Session: ref, Query: q}, &out)
	return out, err
}

func (a *Adapter) Queued(ctx context.Context, ref execution.SessionRef) ([]execution.QueuedMessage, error) {
	var out []execution.QueuedMessage
	err := callTrait[execution.QueueManager](ctx, a, KindQueue, QueueRequest{Session: ref, Action: "list"}, &out)
	return out, err
}

func (a *Adapter) CancelQueued(ctx context.Context, ref execution.SessionRef, id string) error {
	return callTrait[execution.QueueManager](ctx, a, KindQueue, QueueRequest{Session: ref, Action: "cancel", ID: id}, nil)
}

func (a *Adapter) PromoteQueued(ctx context.Context, ref execution.SessionRef, id string) error {
	return callTrait[execution.QueueManager](ctx, a, KindQueue, QueueRequest{Session: ref, Action: "steer", ID: id}, nil)
}
