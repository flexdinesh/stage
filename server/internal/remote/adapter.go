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

	mu       sync.Mutex
	health   execution.Health
	healthAt time.Time
}

var (
	_ execution.Adapter           = (*Adapter)(nil)
	_ execution.Partial           = (*Adapter)(nil)
	_ execution.LinkResolver      = (*Adapter)(nil)
	_ execution.WorkspaceFeatures = (*Adapter)(nil)
	_ execution.Correlator        = (*Adapter)(nil)
	_ execution.EventSource       = (*Adapter)(nil)
	_ execution.Interactive       = (*Adapter)(nil)
)

// Implements reports whether the worker's adapter has trait. Before the
// worker first described its machine, every trait is assumed: the call
// itself then answers (offline or unsupported).
func (a *Adapter) Implements(trait string) bool {
	traits, known := a.m.traits(a.conn)
	return !known || slices.Contains(traits, trait)
}

func (a *Adapter) call(ctx context.Context, kind string, body, out any, timeout time.Duration, cb workerchan.Callback) error {
	raw, err := a.m.hub.CallWith(ctx, a.m.id, kind, a.conn, body, timeout, cb)
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
	last, at := a.health, a.healthAt
	a.mu.Unlock()
	if reason, offline := a.m.offline(); offline {
		return execution.Health{Availability: execution.Availability{Reason: reason}, Identity: last.Identity, CheckedAt: now.UTC()}
	}
	if !at.IsZero() && now.Sub(at) < HealthTTL {
		return last
	}
	var h execution.Health
	ctx, cancel := context.WithTimeout(ctx, HealthTimeout)
	defer cancel()
	if err := a.call(ctx, KindHealth, nil, &h, HealthTimeout, nil); err != nil {
		h = execution.Health{Availability: execution.Availability{Reason: "Its machine's worker did not answer: " + err.Error()}, Identity: last.Identity, CheckedAt: now.UTC()}
	}
	a.mu.Lock()
	a.health, a.healthAt = h, now
	a.mu.Unlock()
	return h
}

func (a *Adapter) Features(ctx context.Context) (execution.Capabilities, error) {
	var c execution.Capabilities
	err := a.call(ctx, KindFeatures, nil, &c, CallTimeout, nil)
	return c, err
}

func (a *Adapter) FeaturesForWorkspace(ctx context.Context, project, directory string) (execution.Capabilities, error) {
	var c execution.Capabilities
	err := a.call(ctx, KindFeaturesForWorkspace, WorkspaceRequest{Project: project, Directory: directory}, &c, CallTimeout, nil)
	return c, err
}

func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	var ws []execution.WorkspaceInfo
	err := a.call(ctx, KindWorkspaces, nil, &ws, CallTimeout, nil)
	return ws, err
}

func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	var o execution.Observation
	err := a.call(ctx, KindObserve, SessionRequest{Session: ref}, &o, CallTimeout, nil)
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

// Destinations makes no call: it is read for every task in a list, so the
// caller falls back to the links of the session's stored observation (the
// adapters report the same links there).
func (a *Adapter) Destinations(context.Context, execution.SessionRef) execution.Links {
	return execution.Links{}
}

func (a *Adapter) ResolveLink(ctx context.Context, link string) (execution.SessionRef, error) {
	var ref execution.SessionRef
	err := a.call(ctx, KindResolveLink, LinkRequest{Link: link}, &ref, CallTimeout, nil)
	return ref, err
}

func (a *Adapter) Correlate(ctx context.Context, in execution.Intent) (execution.Observation, error) {
	var o execution.Observation
	err := a.call(ctx, KindCorrelate, CorrelateRequest{Intent: withoutImageData(in)}, &o, CallTimeout, nil)
	return o, err
}

// Events is one long-running adapter.events call: the worker streams its
// adapter's pushed observations as callbacks, each ingested by sink before
// the worker continues (a sink error reaches the worker's adapter). It
// returns nil when the machine is offline, the window ends or the stream
// breaks: polling reconciles and the caller calls it again.
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
	_, err := a.m.hub.CallWith(ctx, a.m.id, KindEvents, a.conn, nil, EventsWindow, cb)
	var re *workerchan.RemoteError
	switch {
	case err == nil, ctx.Err() != nil, errors.Is(err, workerchan.ErrOffline), errors.Is(err, workerchan.ErrLostReply):
		return nil
	case errors.As(err, &re) && (re.Code == CodeUnsupported || re.Code == workerchan.CodeTimeout || re.Code == workerchan.CodeCancelled):
		return nil
	}
	return decodeError(err)
}

func (a *Adapter) Requests(ctx context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	var rs []execution.Request
	err := a.call(ctx, KindRequests, SessionRequest{Session: ref}, &rs, CallTimeout, nil)
	return rs, err
}

func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, requestID string, r execution.Response) error {
	return a.call(ctx, KindRespond, RespondRequest{Session: ref, RequestID: requestID, Response: r}, nil, CallTimeout, nil)
}

func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	return a.call(ctx, KindSend, SendRequest{Session: ref, Message: m}, nil, CallTimeout, nil)
}
