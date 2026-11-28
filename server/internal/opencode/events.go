package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Pushed observation over OpenCode's V2 event stream (execution.EventSource).
// GET /api/event (event.subscribe) streams events of every location; an
// event about a session's status, requests, archive or title is a wake-up:
// Stage re-reads the session (Observe) and sinks that snapshot. The stream is
// volatile (a slow consumer overflows and fails it; events during a
// disconnection are missed), so polling stays as reconciliation.

var _ execution.EventSource = (*Adapter)(nil)

// essentialEvents must all be advertised for Stage to subscribe; optional
// ones are added when advertised.
var (
	essentialEvents = []string{
		"session.status", "session.idle",
		"session.execution.started", "session.execution.succeeded",
		"session.execution.failed", "session.execution.interrupted",
		"permission.asked", "permission.replied",
		"form.created", "form.replied", "form.cancelled",
		"session.archived", "session.unarchived",
	}
	optionalEvents = []string{"session.renamed"}
)

const (
	eventConnected = "server.connected"
	// eventFailure names the frame OpenCode sends when the stream fails
	// (x-effect-stream failureEvent), such as on overflow.
	eventFailure      = "effect/http-api/stream/failure"
	defaultEventsIdle = 45 * time.Second // OpenCode sends a heartbeat every 15 s
	eventsConnect     = 20 * time.Second
	maxDirtySessions  = 256
	maxEventLine      = 4 << 20
)

var (
	errEventsIdle    = errors.New("OpenCode event stream went silent (no heartbeat)")
	errEventsConnect = errors.New("OpenCode event stream did not answer in time")
)

// inspectEvents is the event types to subscribe to: nil unless op is
// event.subscribe and its types query lists every essential event.
func inspectEvents(op operation) []string {
	if op.OperationID != "event.subscribe" {
		return nil
	}
	var params []struct {
		Name, In string
		Schema   json.RawMessage
	}
	if json.Unmarshal(op.Parameters, &params) != nil {
		return nil
	}
	listed := map[string]bool{}
	for _, p := range params {
		if p.Name == "types" && p.In == "query" {
			collectEnum(p.Schema, listed)
		}
	}
	types := make([]string, 0, len(essentialEvents)+len(optionalEvents))
	for _, t := range essentialEvents {
		if !listed[t] {
			return nil
		}
		types = append(types, t)
	}
	for _, t := range optionalEvents {
		if listed[t] {
			types = append(types, t)
		}
	}
	return types
}

// collectEnum adds the string enum values of a schema and its anyOf, oneOf
// and items subschemas.
func collectEnum(raw json.RawMessage, into map[string]bool) {
	var s struct {
		Enum         []json.RawMessage
		AnyOf, OneOf []json.RawMessage
		Items        json.RawMessage
	}
	if json.Unmarshal(raw, &s) != nil {
		return
	}
	for _, e := range s.Enum {
		var v string
		if json.Unmarshal(e, &v) == nil {
			into[v] = true
		}
	}
	for _, sub := range append(s.AnyOf, s.OneOf...) {
		collectEnum(sub, into)
	}
	if len(s.Items) > 0 {
		collectEnum(s.Items, into)
	}
}

// Events pushes an observation of each session an event names. A service
// without the event stream returns nil at once (polling only). It returns
// an error when the stream fails, ends or goes silent, so the runner calls
// again.
func (a *Adapter) Events(ctx context.Context, sink execution.Sink) error {
	contract, err := a.checkContract(ctx)
	if err != nil {
		return err
	}
	if contract.events == nil {
		return nil
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	body, err := a.subscribe(ctx, cancel, contract.events)
	if err != nil {
		return err
	}
	defer body.Close()
	dirty := newDirtySet()
	read := make(chan error, 1)
	go func() { read <- a.readEvents(body, cancel, contract.events, dirty.mark) }()
	for {
		select {
		case <-ctx.Done():
			return streamErr(ctx, ctx.Err())
		case err := <-read:
			return streamErr(ctx, err)
		case <-dirty.ready:
			for _, id := range dirty.take() {
				o, err := a.Observe(ctx, execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: id})
				if err != nil {
					continue // polling reconciles
				}
				if err := sink(ctx, o); err != nil && ctx.Err() != nil {
					return streamErr(ctx, ctx.Err())
				}
			}
		}
	}
}

// streamErr prefers the stream's own cause (silence, no answer), then the
// caller's cancellation, over the read error either produced.
func streamErr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, errEventsIdle) || errors.Is(cause, errEventsConnect) {
		return cause
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// subscribe opens GET /api/event for types. The stream has no overall
// timeout; the response must start within eventsConnect.
func (a *Adapter) subscribe(ctx context.Context, cancel context.CancelCauseFunc, types []string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint+"/api/event?"+url.Values{"types": types}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if a.password != "" {
		req.SetBasicAuth(a.username, a.password)
	}
	client := *a.http
	client.Timeout = 0
	timer := time.AfterFunc(eventsConnect, func() { cancel(errEventsConnect) })
	resp, err := client.Do(req)
	timer.Stop()
	if err != nil {
		return nil, streamErr(ctx, fmt.Errorf("OpenCode event stream: %w", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var body struct{ Message string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
		return nil, responseError{code: resp.StatusCode, message: body.Message}
	}
	if media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); media != "text/event-stream" {
		resp.Body.Close()
		return nil, errors.New("OpenCode event stream is not text/event-stream")
	}
	return resp.Body, nil
}

// readEvents parses the server-sent events in r and marks each session a
// watched event names. It returns when the stream fails, ends or breaks the
// protocol; silence for eventsIdle cancels the stream.
func (a *Adapter) readEvents(r io.Reader, cancel context.CancelCauseFunc, types []string, mark func(string)) error {
	idle := a.eventsIdle
	if idle <= 0 {
		idle = defaultEventsIdle
	}
	watchdog := time.AfterFunc(idle, func() { cancel(errEventsIdle) })
	defer watchdog.Stop()
	watched := make(map[string]bool, len(types))
	for _, t := range types {
		watched[t] = true
	}
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	var name string
	var data strings.Builder
	connected := false
	for lines.Scan() {
		watchdog.Reset(idle)
		line := lines.Text()
		if line != "" {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "": // comment, such as ": heartbeat"
			case "event":
				name = value
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				if data.Len()+len(value) > maxEventLine {
					return errors.New("OpenCode event exceeded the supported size")
				}
				data.WriteString(value)
			}
			continue
		}
		event, payload := name, data.String()
		name = ""
		data.Reset()
		switch {
		case event == eventFailure:
			return errors.New("OpenCode failed the event stream (overflow or server error)")
		case event != "" && event != "message":
			return fmt.Errorf("unexpected OpenCode stream event %q", event)
		case payload == "":
			continue
		}
		var e struct {
			Type string
			Data struct {
				SessionID string
				Form      struct{ SessionID string }
			}
		}
		if err := json.Unmarshal([]byte(payload), &e); err != nil {
			return fmt.Errorf("invalid OpenCode event: %w", err)
		}
		if !connected {
			if e.Type != eventConnected {
				return errors.New("OpenCode event stream did not start with server.connected")
			}
			connected = true
			continue
		}
		if !watched[e.Type] {
			continue
		}
		id := e.Data.SessionID
		if id == "" {
			id = e.Data.Form.SessionID
		}
		if sessionID.MatchString(id) {
			mark(id)
		}
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("OpenCode event stream: %w", err)
	}
	return errors.New("OpenCode event stream ended")
}

// dirtySet is the sessions waiting to be observed, in the order first
// marked. Marking never blocks the stream reader; past maxDirtySessions a
// session is dropped (polling reconciles it).
type dirtySet struct {
	mu    sync.Mutex
	order []string
	has   map[string]bool
	ready chan struct{}
}

func newDirtySet() *dirtySet {
	return &dirtySet{has: map[string]bool{}, ready: make(chan struct{}, 1)}
}

func (d *dirtySet) mark(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.has[id] || len(d.order) >= maxDirtySessions {
		return
	}
	d.has[id] = true
	d.order = append(d.order, id)
	select {
	case d.ready <- struct{}{}:
	default:
	}
}

func (d *dirtySet) take() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.order
	d.order, d.has = nil, map[string]bool{}
	return out
}
