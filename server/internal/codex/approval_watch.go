package codex

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// One watch connection owns the subscriptions and pending requests for its
// lifetime. Codex uses daemon-wide RPC IDs, also echoed on other clients.
type approvalWatch struct {
	ctx context.Context
	c   *conn

	mu      sync.Mutex
	threads map[string]*threadApprovals
}

type threadApprovals struct {
	pending map[string]*approval
	files   map[fileItem]string // bounded path summaries for in-progress file items
}

type fileItem struct{ turn, id string }

// TrackedEvents holds a subscription only for the active threads on the
// runner's watch. The runner replaces the watch when ownership changes and
// restarts it after a connection loss.
func (a *Adapter) TrackedEvents(ctx context.Context, refs []execution.SessionRef, sink execution.Sink) error {
	if len(refs) == 0 {
		return a.Events(ctx, sink)
	}
	if _, err := a.client.connect(ctx); err != nil {
		return err
	}
	if !a.client.respondAvailability().Available {
		return a.Events(ctx, sink)
	}
	tracked := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if a.valid(ref) {
			tracked[ref.ExternalSessionID] = true
		}
	}
	queue := make(chan string, 256)
	w := &approvalWatch{ctx: ctx, threads: make(map[string]*threadApprovals)}
	c, err := a.client.session(ctx, func(n notification) {
		w.notify(n)
		queueObservation(queue, n)
	}, w.receive)
	if err != nil {
		return err
	}
	w.c = c
	a.mu.Lock()
	if ctx.Err() == nil {
		a.approvals = w
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.approvals == w {
			a.approvals = nil
		}
		a.mu.Unlock()
		c.close()
	}()
	refresh := func() error {
		for id := range tracked {
			if err := ctx.Err(); err != nil {
				return err
			}
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			thread, err := a.read(call, id)
			cancel()
			if err != nil && !errors.Is(err, execution.ErrNotFound) {
				continue
			}
			if err := w.syncThread(ctx, id, thread.Status.Type == statusActive); err != nil {
				return err
			}
		}
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	// Status events drive changes. This reconciles dropped events and
	// temporary read failures without duplicating the runner's fast polls.
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return errClosed
		case <-tick.C:
			if err := refresh(); err != nil {
				return err
			}
		case id := <-queue:
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			o, err := a.Observe(call, a.ref(id))
			cancel()
			if tracked[id] && (err == nil || errors.Is(err, execution.ErrNotFound)) {
				if err := w.syncThread(ctx, id, o.Active); err != nil {
					return err
				}
			}
			if err == nil {
				_ = sink(ctx, o)
			}
		}
	}
}

func (w *approvalWatch) syncThread(ctx context.Context, id string, active bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	_, subscribed := w.threads[id]
	if active == subscribed {
		w.mu.Unlock()
		return nil
	}
	if active {
		// Install the receiver before resume can replay any requests.
		w.threads[id] = &threadApprovals{pending: make(map[string]*approval), files: make(map[fileItem]string)}
	} else {
		delete(w.threads, id)
	}
	w.mu.Unlock()

	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !active {
		return w.c.call(call, "thread/unsubscribe", map[string]any{"threadId": id}, nil)
	}
	var res threadResponse
	if err := resumeThread(call, w.c, id, &res); err != nil {
		if notLoaded(err) {
			w.mu.Lock()
			delete(w.threads, id)
			w.mu.Unlock()
			return nil
		}
		return err
	}
	if res.Thread.ID != id {
		return errors.New("codex resumed a different thread")
	}
	if res.Thread.Status.Type != statusActive {
		return w.syncThread(ctx, id, false)
	}
	return nil
}

func (w *approvalWatch) receive(r serverRequest) {
	p, a := decodeApproval(r)
	if a == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if thread := w.threads[p.ThreadID]; thread != nil {
		key := string(r.ID)
		if _, exists := thread.pending[key]; !exists {
			thread.pending[key] = a
		}
	}
}

func (w *approvalWatch) notify(n notification) {
	switch n.Method {
	case notifyResolved, "turn/completed", "item/started", "item/completed":
	default:
		return
	}
	var p struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		RequestID json.RawMessage `json:"requestId"`
		Turn      struct {
			ID string `json:"id"`
		} `json:"turn"`
		Item struct {
			ID      string       `json:"id"`
			Type    string       `json:"type"`
			Changes []fileChange `json:"changes"`
		} `json:"item"`
	}
	if json.Unmarshal(n.Params, &p) != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	thread := w.threads[p.ThreadID]
	if thread == nil {
		return
	}
	switch n.Method {
	case notifyResolved:
		delete(thread.pending, string(p.RequestID))
	case "turn/completed":
		for id, a := range thread.pending {
			if a.turnID == p.Turn.ID || p.Turn.ID == "" {
				delete(thread.pending, id)
			}
		}
		for key := range thread.files {
			if key.turn == p.Turn.ID || p.Turn.ID == "" {
				delete(thread.files, key)
			}
		}
	case "item/started":
		if p.Item.Type == "fileChange" && len(thread.files) < 32 {
			thread.files[fileItem{p.TurnID, p.Item.ID}] = filePreview(p.Item.Changes)
		}
	case "item/completed":
		delete(thread.files, fileItem{p.TurnID, p.Item.ID})
	}
}

type fileChange struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
	Kind struct {
		Type     string `json:"type"`
		MovePath string `json:"move_path"`
	} `json:"kind"`
}

func filePreview(changes []fileChange) string {
	const pathLimit = 50
	var lines []string
	for _, change := range changes[:min(len(changes), pathLimit)] {
		path := change.Path
		if change.Kind.MovePath != "" {
			path += " → " + change.Kind.MovePath
		}
		if change.Diff != "" {
			var added, removed int
			switch change.Kind.Type {
			case "add":
				added = strings.Count(strings.TrimSuffix(change.Diff, "\n"), "\n") + 1
			case "delete":
				removed = strings.Count(strings.TrimSuffix(change.Diff, "\n"), "\n") + 1
			default:
				inHunk := false
				for line := range strings.SplitSeq(change.Diff, "\n") {
					switch {
					case strings.HasPrefix(line, "@@"):
						inHunk = true
					case inHunk && strings.HasPrefix(line, "+"):
						added++
					case inHunk && strings.HasPrefix(line, "-"):
						removed++
					}
				}
			}
			path += fmt.Sprintf("  +%d −%d", added, removed)
		}
		lines = append(lines, path)
	}
	if len(changes) > pathLimit {
		lines = append(lines, fmt.Sprintf("… and %d more paths", len(changes)-pathLimit))
	}
	return strings.Join(lines, "\n")
}

func (a *Adapter) approvalStream() *approvalWatch {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.approvals
	if w == nil || w.ctx.Err() != nil || !w.c.alive() {
		return nil
	}
	return w
}

// Requests lists approvals currently pending on a tracked thread.
func (a *Adapter) Requests(_ context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	if !a.valid(ref) {
		return nil, execution.ErrNotFound
	}
	w := a.approvalStream()
	if w == nil {
		return nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	thread := w.threads[ref.ExternalSessionID]
	if thread == nil {
		return nil, nil
	}
	out := make([]execution.Request, 0, len(thread.pending))
	for _, a := range thread.pending {
		r := a.request
		if a.method == methodFileApproval {
			r.Preview = thread.files[fileItem{a.turnID, a.itemID}]
			if r.Preview == "" {
				r.Detail = strings.TrimSpace(r.Detail + "\nReview the changes in Codex with the resume command.")
			}
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b execution.Request) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

// Respond sends a result on the same connection that received the request.
// A competing answer removes the entry via serverRequest/resolved.
func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, id string, r execution.Response) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	w := a.approvalStream()
	if w == nil {
		return execution.ErrNotFound
	}
	w.mu.Lock()
	thread := w.threads[ref.ExternalSessionID]
	if thread == nil || w.ctx.Err() != nil {
		w.mu.Unlock()
		return execution.ErrNotFound
	}
	for key, a := range thread.pending {
		if a.request.ID != id {
			continue
		}
		result, err := a.answer(r)
		if err != nil {
			w.mu.Unlock()
			return err
		}
		delete(thread.pending, key)
		w.mu.Unlock()
		if err := w.c.respond(ctx, a.rpcID, result); err != nil {
			// The write's outcome is unknown. A new connection replays it
			// only if Codex still considers it pending.
			w.c.close()
			return sessionErr(err)
		}
		return nil
	}
	w.mu.Unlock()
	return execution.ErrNotFound
}
