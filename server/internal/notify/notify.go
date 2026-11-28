// Package notify sends Web Push notifications for task and external session
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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// Payload is the JSON the service worker shows.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
	Badge int    `json:"badge"`
}

// PushReference carries no work details through the push service. The worker
// must redeem it with the browser session that owns Binding.
type PushReference struct {
	Binding string `json:"binding"`
	EventID int64  `json:"eventId"`
}

// PushError: the push service rejected the message or was unreachable
// (API 502). Status 0 = no response.
type PushError struct {
	Status int
	Msg    string
}

func (e *PushError) Error() string { return e.Msg }

// event is a queued notification: exactly one of task / session / ready /
// completed / schedule / saved set.
type event struct {
	task      *workevent.TaskChange
	session   *workevent.SessionChange
	ready     *int64 // a manual tree member whose needs are completed
	completed *completion
	schedule  *scheduleRun
	saved     *db.NewNotification
	savedID   int64
}

// completion is a task accepted by inference or by an agent. AcceptanceID 0
// stands for the task's current acceptance when the event is handled.
type completion struct {
	TaskID, AcceptanceID int64
}

// scheduleRun is a recorded cron fire (db.RunStarted, RunSkipped, RunMissed).
type scheduleRun struct {
	ScheduleID int64
	Outcome    string
}

// direct is the event of a task or session change, and whether it notifies.
func (e event) direct() (db.NotificationEvent, bool) {
	switch {
	case e.task != nil:
		return db.NotificationEvent(e.task.To), workevent.TaskNotifiable(e.task.To)
	case e.session != nil:
		return db.NotificationEvent(e.session.To), workevent.SessionNotifiable(e.session.To)
	}
	return "", false
}

// urgencyFor: high for needs input, normal otherwise.
func urgencyFor(ev db.NotificationEvent) webpush.Urgency {
	if ev == db.EventNeedsInput {
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
	// Inbox is signalled after each recorded notification (nil = none).
	Inbox *Hub
	// SharedAccess records workspace events even when every recipient mutes them.
	SharedAccess bool
	// Holds holds tree members' needs_input notifications while a steerer
	// can answer them (nil: never held; ADR 0013).
	Holds *Holds
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

// OnTransition is the sync.Service task hook: transitions into needs_input
// or failed are queued.
func (n *Notifier) OnTransition(_ context.Context, c workevent.TaskChange) {
	if workevent.TaskNotifiable(c.To) {
		n.enqueue(event{task: &c})
	}
}

// OnSessionChange is the sync.Service external session hook.
func (n *Notifier) OnSessionChange(_ context.Context, c workevent.SessionChange) {
	if workevent.SessionNotifiable(c.To) {
		n.enqueue(event{session: &c})
	}
}

// OnReady is the release runner hook: a manual tree member whose needs are all
// completed (the ready event).
func (n *Notifier) OnReady(_ context.Context, taskID int64) {
	n.enqueue(event{ready: &taskID})
}

// OnCompleted queues a task accepted by inference (harness closure) or by an
// agent; a person's own completion never calls it. acceptanceID 0 = the
// task's current acceptance. It records completed, and tree_completed when
// that acceptance was the last one its open tree needed.
func (n *Notifier) OnCompleted(_ context.Context, taskID, acceptanceID int64) {
	n.enqueue(event{completed: &completion{TaskID: taskID, AcceptanceID: acceptanceID}})
}

// OnScheduleRun is the schedule runner hook for a cron fire's outcome:
// started notifies schedule_started, skipped and missed schedule_skipped.
func (n *Notifier) OnScheduleRun(_ context.Context, scheduleID int64, outcome string) {
	switch outcome {
	case db.RunStarted, db.RunSkipped, db.RunMissed:
		n.enqueue(event{schedule: &scheduleRun{ScheduleID: scheduleID, Outcome: outcome}})
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
	if e.saved != nil {
		if n.SharedAccess {
			n.sendAll(ctx, e.savedID, e.saved.Event, PayloadFor(notificationFrom(*e.saved), 0))
			return
		}
		badge, err := n.store.NeedsInputCount(ctx)
		if err != nil {
			n.log.Warn("build notification", "err", err)
			return
		}
		n.sendAll(ctx, e.savedID, e.saved.Event, PayloadFor(notificationFrom(*e.saved), badge))
		return
	}
	// A hold comes before the filter: release applies it (holds.go).
	if n.Holds != nil {
		held, err := n.Holds.hold(ctx, n.store, e.task)
		if err != nil && ctx.Err() == nil {
			n.log.Warn("hold notification; notifying", "err", err)
		}
		if held {
			n.Holds.Kick()
			return
		}
	}
	recs, err := notificationRecords(ctx, n.store, e)
	if err != nil {
		n.log.Warn("build notification", "err", err)
		return
	}
	for _, rec := range recs {
		n.deliver(ctx, rec)
	}
}

// deliver records rec (personal mode: only when the owner's filter allows
// it) and pushes it.
func (n *Notifier) deliver(ctx context.Context, rec db.NewNotification) {
	if !n.SharedAccess && !n.enabled(ctx, rec.Event) {
		return
	}
	badge := 0
	if !n.SharedAccess {
		var err error
		if badge, err = n.store.NeedsInputCount(ctx); err != nil {
			n.log.Warn("build notification", "err", err)
			return
		}
	}
	p := PayloadFor(notificationFrom(rec), badge)
	// Shared delivery needs a committed event and receipt to resolve. Personal
	// mode keeps its best-effort direct push if recording fails.
	id, err := n.store.InsertNotification(ctx, rec)
	if err != nil {
		if ctx.Err() == nil {
			n.log.Warn("record notification", "err", err)
		}
		if n.SharedAccess {
			return
		}
	} else {
		n.Inbox.Signal()
	}
	n.sendAll(ctx, id, rec.Event, p)
}

func (n *Notifier) sendAll(ctx context.Context, id int64, ev db.NotificationEvent, p Payload) {
	subs, err := n.store.ListPushSubscriptions(ctx)
	if err != nil {
		n.log.Warn("list push subscriptions", "err", err)
		return
	}
	for _, sub := range subs {
		var message any = p
		if sub.Binding != "" {
			_, err := n.store.NotificationForDelivery(db.WithUser(ctx, sub.UserID), id)
			if err != nil {
				if !errors.Is(err, db.ErrNotFound) && ctx.Err() == nil {
					n.log.Warn("check push recipient", "subscription", sub.ID, "err", err)
				}
				continue
			}
			message = PushReference{Binding: sub.Binding, EventID: id}
		} else if n.SharedAccess {
			continue
		}
		if err := n.send(ctx, sub, message, p.Tag, urgencyFor(ev)); err != nil && ctx.Err() == nil {
			n.log.Warn("push failed", "subscription", sub.ID)
		}
	}
}

// RecordObservation saves an inbox entry inside the observation transaction.
// The returned callback signals the inbox and queues Web Push after commit.
func (n *Notifier) RecordObservation(ctx context.Context, tx *db.Store, change workevent.ObservationChange) (func(), error) {
	e := event{task: change.Task, session: change.Session}
	ev, ok := e.direct()
	if !ok {
		return nil, nil
	}
	if n.Holds != nil {
		held, err := n.Holds.hold(ctx, tx, change.Task)
		if err != nil {
			return nil, err
		}
		if held {
			return n.Holds.Kick, nil
		}
	}
	if !n.SharedAccess {
		prefs, err := tx.GetPreferences(db.WithUser(ctx, db.DefaultUser))
		if err != nil || !slices.Contains(prefs.NotifyEvents, ev) {
			return nil, err
		}
	}
	recs, err := notificationRecords(ctx, tx, e)
	if err != nil || len(recs) != 1 {
		return nil, err
	}
	rec := recs[0]
	rec.ObservedAt = &change.At
	id, err := tx.InsertNotification(ctx, rec)
	if err != nil {
		return nil, err
	}
	return func() {
		n.Inbox.Signal()
		n.enqueue(event{saved: &rec, savedID: id})
	}, nil
}

// enabled checks the personal-mode filter.
// Read at send time; a failed read notifies (a missed event costs more).
func (n *Notifier) enabled(ctx context.Context, ev db.NotificationEvent) bool {
	p, err := n.store.GetPreferences(db.WithUser(ctx, db.DefaultUser))
	if err != nil {
		if ctx.Err() == nil {
			n.log.Warn("read notification filter; notifying", "err", err)
		}
		return true
	}
	return slices.Contains(p.NotifyEvents, ev)
}

// titles name each event with its subject. A done row is a session's
// ("Session finished"), or an older task row's ("Run finished").
var titles = map[db.NotificationEvent]string{
	db.EventNeedsInput:      "Needs input",
	db.EventFailed:          "Failed",
	db.EventReady:           "Ready to start",
	db.EventCompleted:       "Task completed",
	db.EventTreeCompleted:   "Tree completed",
	db.EventScheduleStarted: "Schedule started",
	db.EventScheduleSkipped: "Schedule skipped",
	db.EventDone:            "Session finished",
}

// PayloadFor is the notification shown for a stored inbox row (Web Push and
// GET /api/notifications/stream): task rows with a session open the task,
// tree and schedule rows their page, session rows the inbox.
func PayloadFor(n db.Notification, badge int) Payload {
	key := db.NotificationKey(n)
	p := Payload{Title: titles[n.Event], Body: n.Title, URL: n.URL, Tag: db.TagForKey(key), Badge: badge}
	if n.ProjectTitle != nil {
		p.Body += " · " + *n.ProjectTitle
	}
	if n.Note != nil {
		p.Body += " · " + *n.Note
	}
	kind, id, _ := strings.Cut(key, ":")
	switch kind {
	case "task":
		if n.Event == db.EventDone {
			p.Title = "Run finished"
		}
		if n.Event == db.EventNeedsInput || n.ExternalSessionID != nil {
			p.URL = taskURL(id, n.Event)
		}
	case "tree":
		p.URL = "/trees/" + id
	case "schedule":
		p.URL = "/schedules/" + id
	default:
		p.URL = "/?inbox"
	}
	return p
}

// notificationFrom is the row rec will be stored as (no id or timestamps).
func notificationFrom(rec db.NewNotification) db.Notification {
	n := db.Notification{Event: rec.Event, TaskID: rec.TaskID, TreeID: rec.TreeID, ScheduleID: rec.ScheduleID,
		Title: rec.Title, ProjectTitle: rec.ProjectTitle, URL: rec.URL, Note: rec.Note}
	if rec.Session != nil {
		n.ConnectionID = rec.Session.ConnectionID
		n.ExternalSessionID = &rec.Session.ExternalSessionID
	}
	return n
}

// notificationRecords builds the inbox records for e (lookups at send
// time): one, or none when its subject no longer qualifies; a completion
// may add tree_completed.
func notificationRecords(ctx context.Context, store *db.Store, e event) ([]db.NewNotification, error) {
	switch {
	case e.task != nil:
		t, err := store.GetTask(ctx, e.task.TaskID)
		if err != nil {
			return nil, fmt.Errorf("task %d: %w", e.task.TaskID, err)
		}
		// Retain task identity across restarts and ON DELETE SET NULL. Push
		// navigation can open the current session without changing this key.
		return []db.NewNotification{taskRecord(db.NotificationEvent(e.task.To), t, t.Session)}, nil
	case e.ready != nil:
		t, err := store.GetTask(ctx, *e.ready)
		if err != nil {
			return nil, fmt.Errorf("task %d: %w", *e.ready, err)
		}
		// A backlog task (no session yet): the payload opens the task.
		return []db.NewNotification{taskRecord(db.EventReady, t, nil)}, nil
	case e.completed != nil:
		return completionRecords(ctx, store, *e.completed)
	case e.schedule != nil:
		sc, err := store.GetSchedule(ctx, e.schedule.ScheduleID)
		if err != nil {
			return nil, fmt.Errorf("schedule %d: %w", e.schedule.ScheduleID, err)
		}
		ev := db.EventScheduleSkipped
		if e.schedule.Outcome == db.RunStarted {
			ev = db.EventScheduleStarted
		}
		project := sc.ProjectTitle
		return []db.NewNotification{{Event: ev, ScheduleID: &sc.ID, Title: sc.Title, ProjectTitle: &project,
			URL: "/schedules/" + strconv.FormatInt(sc.ID, 10)}}, nil
	case e.session != nil:
		// A session no task owns: its observed title and, when its harness
		// project is bound to one Stage project, that project's title.
		c := e.session
		project := c.ProjectID
		if c.Session != nil {
			title, ok, err := store.BoundProjectTitle(ctx, c.Session.ConnectionID, c.ProjectID)
			if err != nil {
				return nil, fmt.Errorf("session %s: %w", c.Session.ExternalSessionID, err)
			}
			if ok {
				project = title
			}
		}
		return []db.NewNotification{{Event: db.NotificationEvent(c.To), Session: c.Session, Title: c.Title, ProjectTitle: &project, URL: "/?inbox"}}, nil
	}
	return nil, errors.New("empty event")
}

func taskRecord(ev db.NotificationEvent, t db.Task, session *db.SessionRef) db.NewNotification {
	project := t.ProjectTitle
	return db.NewNotification{Event: ev, TaskID: &t.ID, Session: session, Title: t.Title, ProjectTitle: &project,
		URL: taskURL(strconv.FormatInt(t.ID, 10), ev)}
}

func taskURL(id string, event db.NotificationEvent) string {
	url := "/tasks/" + id
	if event == db.EventNeedsInput {
		return url + "/session"
	}
	return url
}

// completionRecords: completed while c's acceptance is still the task's
// current one, plus tree_completed when every member of its open tree is
// accepted and c's acceptance is the newest of them (so concurrent last
// completions record the tree once; a reopen starts a new episode).
func completionRecords(ctx context.Context, store *db.Store, c completion) ([]db.NewNotification, error) {
	t, err := store.GetTask(ctx, c.TaskID)
	if err != nil {
		return nil, fmt.Errorf("task %d: %w", c.TaskID, err)
	}
	if t.AcceptanceID == nil || c.AcceptanceID != 0 && *t.AcceptanceID != c.AcceptanceID {
		return nil, nil // reopened or replaced since
	}
	accepted := *t.AcceptanceID
	recs := []db.NewNotification{taskRecord(db.EventCompleted, t, t.Session)}
	if t.TreeID == nil {
		return recs, nil
	}
	tree, err := store.GetTree(ctx, *t.TreeID)
	if err != nil {
		return nil, fmt.Errorf("tree %d: %w", *t.TreeID, err)
	}
	if tree.Status != db.TreeOpen {
		return recs, nil
	}
	for _, m := range tree.Tasks {
		if m.AcceptanceID == nil || *m.AcceptanceID > accepted {
			return recs, nil
		}
	}
	return append(recs, db.NewNotification{Event: db.EventTreeCompleted, TreeID: &tree.ID, Title: tree.Title,
		URL: "/trees/" + strconv.FormatInt(tree.ID, 10)}), nil
}

// PublicKey is the VAPID public key (generated on first use).
func (n *Notifier) PublicKey(ctx context.Context) (string, error) {
	k, err := n.store.VAPIDKeys(ctx)
	return k.PublicKey, err
}

// SendTest sends a test notification to one subscription synchronously.
// db.ErrNotFound for an unknown endpoint, *PushError when rejected.
func (n *Notifier) SendTest(ctx context.Context, sessionID int64, endpoint string) error {
	sub, err := n.store.GetPushSubscription(ctx, sessionID, endpoint)
	if err != nil {
		return err
	}
	if sub.Binding != "" {
		return n.send(ctx, sub, PushReference{Binding: sub.Binding}, "test", webpush.UrgencyNormal)
	}
	badge, err := n.store.NeedsInputCount(ctx)
	if err != nil {
		return err
	}
	return n.send(ctx, sub, testPayload(badge), "test", webpush.UrgencyNormal)
}

func testPayload(badge int) Payload {
	return Payload{Title: "stage", Body: "Test notification", URL: "/settings", Tag: "test", Badge: badge}
}

func (n *Notifier) ResolvePush(ctx context.Context, sessionID int64, ref PushReference) (Payload, error) {
	row, badge, err := n.store.PushDelivery(ctx, sessionID, ref.Binding, ref.EventID)
	if err != nil {
		return Payload{}, err
	}
	if ref.EventID == 0 {
		return testPayload(badge), nil
	}
	return PayloadFor(row, badge), nil
}

// topicFor is the Web Push Topic for tag: the push service keeps only the
// latest pending message per topic. Topic allows at most 32 URL-safe base64
// chars, so the tag is hashed (session tags exceed it).
func topicFor(tag string) string {
	sum := sha256.Sum256([]byte(tag))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}

// send pushes p to sub and records the outcome: 2xx → last_success_at;
// 404/410 → subscription deleted; otherwise last_error. Failures return
// *PushError.
func (n *Notifier) send(ctx context.Context, sub db.PushSubscription, message any, tag string, urgency webpush.Urgency) error {
	keys, err := n.store.VAPIDKeys(ctx)
	if err != nil {
		return err
	}
	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}
	opts := &webpush.Options{
		HTTPClient: n.HTTPClient, Subscriber: n.subject, TTL: 86400, Urgency: urgency,
		VAPIDPublicKey: keys.PublicKey, VAPIDPrivateKey: keys.PrivateKey,
	}
	if tag != "" {
		if sub.Binding != "" {
			tag = sub.Binding + ":" + tag
		}
		opts.Topic = topicFor(tag)
	}
	callCtx, cancel := context.WithTimeout(ctx, n.SendTimeout)
	defer cancel()
	resp, err := webpush.SendNotificationWithContext(callCtx, msg, &webpush.Subscription{
		Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, opts)
	if err != nil {
		return n.fail(ctx, sub, &PushError{Msg: "push request failed: " + err.Error()})
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return n.store.RecordPushResult(ctx, sub, nil)
	}
	perr := &PushError{Status: resp.StatusCode,
		Msg: fmt.Sprintf("push service returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if err := n.store.DeleteExpiredPushSubscription(ctx, sub); err != nil {
			return err
		}
		n.log.Info("push subscription expired; deleted", "subscription", sub.ID, "status", resp.StatusCode)
		return perr
	}
	return n.fail(ctx, sub, perr)
}

func (n *Notifier) fail(ctx context.Context, sub db.PushSubscription, perr *PushError) error {
	msg := perr.Msg
	if err := n.store.RecordPushResult(ctx, sub, &msg); err != nil {
		n.log.Warn("record push error", "subscription", sub.ID, "err", err)
	}
	return perr
}
