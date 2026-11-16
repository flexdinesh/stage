// Package notify sends Web Push notifications for task and external thread
// state changes (docs/api.md "Notifications (Web Push)", "Notification rules").
package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
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

// event is a queued notification: exactly one of task / thread / ready set.
type event struct {
	task   *t3sync.Change
	thread *t3sync.ThreadChange
	ready  *int64 // a manual tree member whose needs settled
}

func (e event) to() db.TaskState {
	switch {
	case e.task != nil:
		return e.task.To
	case e.ready != nil:
		return db.EventReady
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
	subject string // VAPID subject (STAGE_PUBLIC_URL, else FallbackSubject)
	events  chan event

	// HTTPClient sends push requests (tests inject).
	HTTPClient webpush.HTTPClient
	// SendTimeout bounds one push request.
	SendTimeout time.Duration
}

// FallbackSubject is the VAPID subject when STAGE_PUBLIC_URL is not https.
const FallbackSubject = "https://github.com/flexdinesh/stage"

// New returns a Notifier. publicURL (STAGE_PUBLIC_URL) is the VAPID subject
// when it is an https: URL (webpush-go sends anything else as mailto:);
// otherwise FallbackSubject, with a warning.
func New(store *db.Store, publicURL string, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "notify")
	subject := publicURL
	if !strings.HasPrefix(subject, "https:") {
		subject = FallbackSubject
		log.Warn("STAGE_PUBLIC_URL is not an https URL; using the fallback VAPID subject (push and install need HTTPS)", "url", publicURL, "subject", subject)
	}
	return &Notifier{
		store: store, log: log, subject: subject,
		events:      make(chan event, queueSize),
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		SendTimeout: 20 * time.Second,
	}
}

// OnTransition is the sync.Service task hook: transitions into needs_input,
// done, failed or settled are queued.
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

// OnReady is the release runner hook: a manual tree member whose needs all
// settled (the ready event).
func (n *Notifier) OnReady(_ context.Context, taskID int64) {
	n.enqueue(event{ready: &taskID})
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
	if !n.enabled(ctx, e.to()) {
		return
	}
	p, rec, err := n.payload(ctx, e)
	if err != nil {
		n.log.Warn("build notification", "err", err)
		return
	}
	// Inbox first (also with no subscriptions); a failed insert still pushes.
	if _, err := n.store.InsertNotification(ctx, rec); err != nil && ctx.Err() == nil {
		n.log.Warn("record notification", "err", err)
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

// enabled: the global filter (preferences.notifyEvents) allows the event.
// Read at send time; a failed read notifies (a missed event costs more).
func (n *Notifier) enabled(ctx context.Context, to db.TaskState) bool {
	p, err := n.store.GetPreferences(ctx)
	if err != nil {
		if ctx.Err() == nil {
			n.log.Warn("read notification filter; notifying", "err", err)
		}
		return true
	}
	return slices.Contains(p.NotifyEvents, to)
}

var titles = map[db.TaskState]string{
	db.StateNeedsInput: "Needs input",
	db.EventReady:      "Ready",
	db.StateDone:       "Done",
	db.StateFailed:     "Failed",
	db.StateSettled:    "Settled",
}

// answerAnchor lands needs-input pushes on the Needs input panel.
func answerAnchor(to db.TaskState) string {
	if to == db.StateNeedsInput {
		return "#answer"
	}
	return ""
}

// payload builds the push payload and its inbox record (lookups at send time).
func (n *Notifier) payload(ctx context.Context, e event) (Payload, db.NewNotification, error) {
	var p Payload
	var rec db.NewNotification
	switch {
	case e.task != nil:
		t, err := n.store.GetTask(ctx, e.task.TaskID)
		if err != nil {
			return p, rec, fmt.Errorf("task %d: %w", e.task.TaskID, err)
		}
		id := strconv.FormatInt(t.ID, 10)
		p = Payload{Title: titles[e.task.To], Body: t.Title + " · " + t.ProjectTitle,
			URL: "/tasks/" + id, Tag: "task-" + id}
		if t.T3ThreadID != nil {
			p.URL = "/sessions/" + url.PathEscape(*t.T3ThreadID)
		}
		p.URL += answerAnchor(e.task.To)
		project := t.ProjectTitle
		// Retain task identity across restarts and ON DELETE SET NULL. Push
		// navigation can open the current session without changing this key.
		rec = db.NewNotification{Event: e.task.To, TaskID: &t.ID, T3ThreadID: t.T3ThreadID,
			Title: t.Title, ProjectTitle: &project, URL: "/tasks/" + id}
	case e.ready != nil:
		t, err := n.store.GetTask(ctx, *e.ready)
		if err != nil {
			return p, rec, fmt.Errorf("task %d: %w", *e.ready, err)
		}
		id := strconv.FormatInt(t.ID, 10)
		path := "/tasks/" + id
		p = Payload{Title: titles[db.EventReady], Body: t.Title + " · " + t.ProjectTitle, URL: path, Tag: "task-" + id}
		project := t.ProjectTitle
		rec = db.NewNotification{Event: db.EventReady, TaskID: &t.ID, Title: t.Title, ProjectTitle: &project, URL: path}
	case e.thread != nil:
		c := e.thread
		title, project := c.Title, c.ProjectID
		if th, err := n.store.GetThread(ctx, c.ThreadID); err == nil {
			title = th.Title
			if th.ProjectTitle != nil {
				project = *th.ProjectTitle
			}
		} else if !errors.Is(err, db.ErrNotFound) {
			return p, rec, fmt.Errorf("thread %s: %w", c.ThreadID, err)
		}
		path := "/sessions/" + url.PathEscape(c.ThreadID)
		p = Payload{Title: titles[c.To], Body: title + " · " + project, URL: path + answerAnchor(c.To), Tag: "thread-" + c.ThreadID}
		threadID := c.ThreadID
		rec = db.NewNotification{Event: c.To, T3ThreadID: &threadID, Title: title, ProjectTitle: &project, URL: path}
	default:
		return p, rec, errors.New("empty event")
	}
	badge, err := n.store.NeedsInputCount(ctx)
	if err != nil {
		return p, rec, err
	}
	p.Badge = badge
	return p, rec, nil
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
	p := Payload{Title: "stage", Body: "Test notification", URL: "/settings", Tag: "test", Badge: badge}
	return n.send(ctx, sub, p, webpush.UrgencyNormal)
}

// topicFor is the Web Push Topic for tag: the push service keeps only the
// latest pending message per topic. Topic allows at most 32 URL-safe base64
// chars, so the tag is hashed (thread tags exceed it).
func topicFor(tag string) string {
	sum := sha256.Sum256([]byte(tag))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
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
	opts := &webpush.Options{
		HTTPClient: n.HTTPClient, Subscriber: n.subject, TTL: 86400, Urgency: urgency,
		VAPIDPublicKey: keys.PublicKey, VAPIDPrivateKey: keys.PrivateKey,
	}
	if p.Tag != "" {
		opts.Topic = topicFor(p.Tag)
	}
	callCtx, cancel := context.WithTimeout(ctx, n.SendTimeout)
	defer cancel()
	resp, err := webpush.SendNotificationWithContext(callCtx, msg, &webpush.Subscription{
		Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, opts)
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
