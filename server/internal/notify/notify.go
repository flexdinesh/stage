// Package notify sends Web Push notifications for task and external thread
// state changes (docs/api.md "Notifications (Web Push)", "Notification rules").
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
)

// Payload is the JSON the service worker shows.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
	Badge int    `json:"badge"`
}

// PushError: the push service rejected the message or was unreachable
// (API 502). Status 0 = no response.
type PushError struct {
	Status int
	Msg    string
}

func (e *PushError) Error() string { return e.Msg }

// event is a queued notification: exactly one of task / thread set.
type event struct {
	task   *t3sync.Change
	thread *t3sync.ThreadChange
}

func (e event) to() db.TaskState {
	if e.task != nil {
		return e.task.To
	}
	return e.thread.To
}

// urgencyFor: high for needs input, normal otherwise.
func urgencyFor(to db.TaskState) webpush.Urgency {
	if to == db.StateNeedsInput {
		return webpush.UrgencyHigh
	}
	return webpush.UrgencyNormal
}

const queueSize = 256

// Notifier queues state-change events (hooks never block) and a worker
// (Run) sends them to every subscription.
type Notifier struct {
	store   *db.Store
	log     *slog.Logger
	subject string // VAPID subject (APP_PUBLIC_URL)
	events  chan event

	// HTTPClient sends push requests (tests inject).
	HTTPClient webpush.HTTPClient
	// SendTimeout bounds one push request.
	SendTimeout time.Duration
}

// New returns a Notifier. subject must be an https: URL (webpush-go sends it
// as-is; anything else is treated as an e-mail and prefixed with mailto:).
func New(store *db.Store, subject string, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "notify")
	if !strings.HasPrefix(subject, "https:") {
		log.Warn("APP_PUBLIC_URL is not https; VAPID subject will be sent as mailto:", "url", subject)
	}
	return &Notifier{
		store: store, log: log, subject: subject,
		events:      make(chan event, queueSize),
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		SendTimeout: 20 * time.Second,
	}
}

// OnTransition is the sync.Service task hook: transitions into needs_input,
// done or failed are queued.
func (n *Notifier) OnTransition(_ context.Context, c t3sync.Change) {
	if t3sync.Notifiable(c.To) {
		n.enqueue(event{task: &c})
	}
}

// OnThreadChange is the sync.Service external thread hook.
func (n *Notifier) OnThreadChange(_ context.Context, c t3sync.ThreadChange) {
	if t3sync.Notifiable(c.To) {
		n.enqueue(event{thread: &c})
	}
}

func (n *Notifier) enqueue(e event) {
	select {
	case n.events <- e:
	default:
		n.log.Warn("notification queue full; dropping event")
	}
}

// Run sends queued events until ctx is done.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.events:
			n.handle(ctx, e)
		}
	}
}

func (n *Notifier) handle(ctx context.Context, e event) {
	p, err := n.payload(ctx, e)
	if err != nil {
		n.log.Warn("build notification", "err", err)
		return
	}
	subs, err := n.store.ListPushSubscriptions(ctx)
	if err != nil {
		n.log.Warn("list push subscriptions", "err", err)
		return
	}
	for _, sub := range subs {
		if err := n.send(ctx, sub, p, urgencyFor(e.to())); err != nil && ctx.Err() == nil {
			n.log.Warn("push failed", "endpoint", sub.Endpoint, "err", err)
		}
	}
}

var titles = map[db.TaskState]string{
	db.StateNeedsInput: "Needs input",
	db.StateDone:       "Done",
	db.StateFailed:     "Failed",
}

// payload builds the notification (lookups at send time).
func (n *Notifier) payload(ctx context.Context, e event) (Payload, error) {
	var p Payload
	switch {
	case e.task != nil:
		t, err := n.store.GetTask(ctx, e.task.TaskID)
		if err != nil {
			return p, fmt.Errorf("task %d: %w", e.task.TaskID, err)
		}
		id := strconv.FormatInt(t.ID, 10)
		p = Payload{Title: titles[e.task.To], Body: t.Title + " · " + t.ProjectTitle,
			URL: "/tasks/" + id, Tag: "task-" + id}
	case e.thread != nil:
		c := e.thread
		title, project := c.Title, c.ProjectID
		if th, err := n.store.GetThread(ctx, c.ThreadID); err == nil {
			title = th.Title
			if th.ProjectTitle != nil {
				project = *th.ProjectTitle
			}
		} else if !errors.Is(err, db.ErrNotFound) {
			return p, fmt.Errorf("thread %s: %w", c.ThreadID, err)
		}
		p = Payload{Title: titles[c.To], Body: title + " · " + project,
			URL: "/history?q=" + url.QueryEscape(title), Tag: "thread-" + c.ThreadID}
	default:
		return p, errors.New("empty event")
	}
	badge, err := n.store.NeedsInputCount(ctx)
	if err != nil {
		return p, err
	}
	p.Badge = badge
	return p, nil
}

// PublicKey is the VAPID public key (generated on first use).
func (n *Notifier) PublicKey(ctx context.Context) (string, error) {
	k, err := n.store.VAPIDKeys(ctx)
	return k.PublicKey, err
}

// SendTest sends a test notification to one subscription synchronously.
// db.ErrNotFound for an unknown endpoint, *PushError when rejected.
func (n *Notifier) SendTest(ctx context.Context, endpoint string) error {
	sub, err := n.store.GetPushSubscription(ctx, endpoint)
	if err != nil {
		return err
	}
	badge, err := n.store.NeedsInputCount(ctx)
	if err != nil {
		return err
	}
	p := Payload{Title: "t3-stage", Body: "Test notification", URL: "/settings", Tag: "test", Badge: badge}
	return n.send(ctx, sub, p, webpush.UrgencyNormal)
}

// send pushes p to sub and records the outcome: 2xx → last_success_at;
// 404/410 → subscription deleted; otherwise last_error. Failures return
// *PushError.
func (n *Notifier) send(ctx context.Context, sub db.PushSubscription, p Payload, urgency webpush.Urgency) error {
	keys, err := n.store.VAPIDKeys(ctx)
	if err != nil {
		return err
	}
	msg, err := json.Marshal(p)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, n.SendTimeout)
	defer cancel()
	resp, err := webpush.SendNotificationWithContext(callCtx, msg, &webpush.Subscription{
		Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient: n.HTTPClient, Subscriber: n.subject, TTL: 86400, Urgency: urgency,
		VAPIDPublicKey: keys.PublicKey, VAPIDPrivateKey: keys.PrivateKey,
	})
	if err != nil {
		return n.fail(ctx, sub.Endpoint, &PushError{Msg: "push request failed: " + err.Error()})
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return n.store.RecordPushResult(ctx, sub.Endpoint, nil)
	}
	perr := &PushError{Status: resp.StatusCode,
		Msg: fmt.Sprintf("push service returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if err := n.store.DeletePushSubscription(ctx, sub.Endpoint); err != nil {
			return err
		}
		n.log.Info("push subscription expired; deleted", "endpoint", sub.Endpoint, "status", resp.StatusCode)
		return perr
	}
	return n.fail(ctx, sub.Endpoint, perr)
}

func (n *Notifier) fail(ctx context.Context, endpoint string, perr *PushError) error {
	msg := perr.Msg
	if err := n.store.RecordPushResult(ctx, endpoint, &msg); err != nil {
		n.log.Warn("record push error", "endpoint", endpoint, "err", err)
	}
	return perr
}
