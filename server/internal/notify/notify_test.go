package notify

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

var testDB dbtest.DB

const subject = "https://stage.test"

func newNotifier(t *testing.T) (*Notifier, *db.Store) {
	store := db.NewStore(testDB.Reset(t))
	return New(store, subject, slog.New(slog.NewTextHandler(io.Discard, nil))), store
}

// pushServer is a fake push service recording requests.
type pushServer struct {
	*httptest.Server
	mu     gosync.Mutex
	status int
	reqs   []pushReq
}

type pushReq struct {
	path   string
	header http.Header
	body   []byte
}

func newPushServer(t *testing.T) *pushServer {
	p := &pushServer{status: http.StatusCreated}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.reqs = append(p.reqs, pushReq{path: r.URL.Path, header: r.Header.Clone(), body: body})
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *pushServer) setStatus(s int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = s
}

func (p *pushServer) requests() []pushReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pushReq(nil), p.reqs...)
}

// subscribe stores a subscription with real browser-style keys.
func subscribe(t *testing.T, store *db.Store, endpoint string) {
	t.Helper()
	subscribeSession(t, store, 0, endpoint)
}

func subscribeSession(t *testing.T, store *db.Store, sessionID int64, endpoint string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPushSubscription(t.Context(), sessionID, endpoint,
		base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(auth), "test-agent"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedFanoutUsesCurrentRecipients(t *testing.T) {
	n, database := newNotifier(t)
	n.SharedAccess = true
	ctx := t.Context()
	var owner, member, desktop int64
	var first, second, muted int64
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := database.WriteAccess(ctx, func(a store.Access) error {
		on := true
		if _, err := db.ConfigureAccessTx(ctx, a, &on); err != nil {
			return err
		}
		if err := a.ClaimOwner(ctx, store.AccountInput{DisplayName: "Owner", Email: "owner@example.test"}); err != nil {
			return err
		}
		if err := a.MarkClaimed(ctx, now); err != nil {
			return err
		}
		owner = db.DefaultUser
		var err error
		member, err = a.InsertMember(ctx, store.AccountInput{DisplayName: "Member", Email: "member@example.test"})
		if err != nil {
			return err
		}
		for i, binding := range []struct {
			user int64
			id   *int64
		}{{owner, &first}, {owner, &second}, {member, &muted}, {member, &desktop}} {
			audience := store.SessionBrowser
			if binding.id == &desktop {
				audience = store.SessionCLI
			}
			*binding.id, err = a.InsertSession(ctx, store.TokenDigest{byte(i + 1)}, store.AuthSession{
				UserID: binding.user, Audience: audience, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	memberCtx := db.WithUser(ctx, member)
	if _, err := database.SetNotifyEvents(memberCtx, nil); err != nil {
		t.Fatal(err)
	}
	srv := newPushServer(t)
	n.HTTPClient = srv.Client()
	subscribeSession(t, database, first, srv.URL+"/owner-one")
	subscribeSession(t, database, second, srv.URL+"/owner-two")
	subscribeSession(t, database, muted, srv.URL+"/member")
	if err := database.UpsertPushSubscription(ctx, desktop, srv.URL+"/desktop", "unused", "unused", "test"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("native session registered browser push: %v", err)
	}
	task := seedTask(t, database, db.StateNeedsInput)
	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}})
	requests := srv.requests()
	if len(requests) != 2 || requests[0].path != "/owner-one" || requests[1].path != "/owner-two" ||
		requests[0].header.Get("Topic") == requests[1].header.Get("Topic") {
		t.Fatalf("shared push requests: %v", requests)
	}
	if inbox, err := database.ListNotifications(memberCtx, 10, nil); err != nil || len(inbox) != 1 || inbox[0].ReadAt == nil {
		t.Fatalf("muted event did not remain in history as read: %v, %v", inbox, err)
	}
}

func seedTask(t *testing.T, store *db.Store, state db.TaskState) db.Task {
	t.Helper()
	ctx := context.Background()
	p, err := store.InsertProject(ctx, db.Project{Title: "stage", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "proj-1", Workspace: "/w/1"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "Say OK", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if state != db.StateBacklog {
		if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: state}); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func TestEventSelection(t *testing.T) {
	n := New(nil, subject, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, to := range []db.TaskState{db.StateInterrupted, db.StateCancelled, db.StateRunning, db.StateLaunching, db.StateBacklog} {
		n.OnTransition(context.Background(), workevent.TaskChange{TaskID: 1, To: to})
		n.OnSessionChange(context.Background(), workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "external"}, To: to})
	}
	if len(n.events) != 0 {
		t.Fatalf("queued %d events for non-notifying states", len(n.events))
	}
	// A task's finished run no longer notifies (acceptance does); an
	// untracked session's still does ("Session finished").
	n.OnTransition(context.Background(), workevent.TaskChange{TaskID: 1, To: db.StateDone})
	if len(n.events) != 0 {
		t.Fatalf("queued %d events for a task's finished run", len(n.events))
	}
	for _, to := range []db.TaskState{db.StateNeedsInput, db.StateDone, db.StateFailed} {
		n.OnTransition(context.Background(), workevent.TaskChange{TaskID: 1, To: to})
		n.OnSessionChange(context.Background(), workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "external"}, To: to})
	}
	if len(n.events) != 5 {
		t.Fatalf("queued %d events, want 5", len(n.events))
	}
	// A full queue drops instead of blocking.
	done := make(chan struct{})
	go func() {
		for range queueSize + 10 {
			n.OnTransition(context.Background(), workevent.TaskChange{TaskID: 1, To: db.StateFailed})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("enqueue blocked on a full queue")
	}
	if len(n.events) != queueSize {
		t.Fatalf("queue len %d, want %d", len(n.events), queueSize)
	}
}

func TestPayload(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	task := seedTask(t, store, db.StateNeedsInput)
	now := time.Now().UTC()
	waiting := t3.ThreadDetail{Thread: t3.Thread{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", Status: t3.StatusWaiting, CreatedAt: now, UpdatedAt: now}, PendingRequestCount: 1}
	if err := store.PutObservation(ctx, waiting.Observation()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		e    event
		want Payload
	}{
		{"task needs input", event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}},
			Payload{Title: "Needs input", Body: "Say OK · stage", URL: fmt.Sprintf("/tasks/%d", task.ID), Tag: fmt.Sprintf("task-%d", task.ID), Badge: 2}},
		{"task failed", event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateFailed}},
			Payload{Title: "Failed", Body: "Say OK · stage", URL: fmt.Sprintf("/tasks/%d", task.ID), Tag: fmt.Sprintf("task-%d", task.ID), Badge: 2}},
		{"session with Stage project", event{session: &workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "ext-1"}, ProjectID: "proj-1", Title: "Fix bug & test", To: db.StateNeedsInput}},
			Payload{Title: "Needs input", Body: "Fix bug & test · stage", URL: "/?inbox", Tag: "session-1:ext-1", Badge: 2}},
		{"session without Stage project", event{session: &workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "ext-2"}, ProjectID: "proj-unknown", Title: "Other", To: db.StateFailed}},
			Payload{Title: "Failed", Body: "Other · proj-unknown", URL: "/?inbox", Tag: "session-1:ext-2", Badge: 2}},
	}
	for _, tc := range tests {
		recs, err := notificationRecords(ctx, n.store, tc.e)
		if err != nil || len(recs) != 1 {
			t.Fatalf("%s: %v, %v", tc.name, recs, err)
		}
		badge, err := n.store.NeedsInputCount(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := PayloadFor(notificationFrom(recs[0]), badge)
		if got != tc.want {
			t.Fatalf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// PayloadFor builds the push from a stored row; the stream shares it.
func TestPayloadFor(t *testing.T) {
	task, sessionID, project := int64(7), "provider:session/with%id", "stage"
	tree, schedule := int64(3), int64(4)
	created := time.Now()
	tests := []struct {
		name string
		n    db.Notification
		want Payload
	}{
		{"task needs input with session", db.Notification{ID: 1, Event: db.EventNeedsInput, TaskID: &task, ExternalSessionID: &sessionID,
			Title: "Say OK", ProjectTitle: &project, URL: "/tasks/7", CreatedAt: created},
			Payload{Title: "Needs input", Body: "Say OK · stage", URL: "/tasks/7", Tag: "task-7", Badge: 3}},
		{"older task run finished, no session", db.Notification{ID: 2, Event: db.EventDone, TaskID: &task, Title: "Say OK", ProjectTitle: &project, URL: "/tasks/7"},
			Payload{Title: "Run finished", Body: "Say OK · stage", URL: "/tasks/7", Tag: "task-7", Badge: 3}},
		{"task completed", db.Notification{ID: 6, Event: db.EventCompleted, TaskID: &task, ExternalSessionID: &sessionID, Title: "Say OK", ProjectTitle: &project, URL: "/tasks/7"},
			Payload{Title: "Task completed", Body: "Say OK · stage", URL: "/tasks/7", Tag: "task-7", Badge: 3}},
		{"tree completed", db.Notification{ID: 7, Event: db.EventTreeCompleted, TreeID: &tree, Title: "Ship auth", URL: "/trees/3"},
			Payload{Title: "Tree completed", Body: "Ship auth", URL: "/trees/3", Tag: "tree-3", Badge: 3}},
		{"deleted tree keeps its URL group", db.Notification{ID: 8, Event: db.EventTreeCompleted, Title: "Ship auth", URL: "/trees/3"},
			Payload{Title: "Tree completed", Body: "Ship auth", URL: "/trees/3", Tag: "tree-3", Badge: 3}},
		{"schedule started", db.Notification{ID: 9, Event: db.EventScheduleStarted, ScheduleID: &schedule, Title: "Nightly", ProjectTitle: &project, URL: "/schedules/4"},
			Payload{Title: "Schedule started", Body: "Nightly · stage", URL: "/schedules/4", Tag: "schedule-4", Badge: 3}},
		{"schedule skipped", db.Notification{ID: 10, Event: db.EventScheduleSkipped, ScheduleID: &schedule, Title: "Nightly", ProjectTitle: &project, URL: "/schedules/4"},
			Payload{Title: "Schedule skipped", Body: "Nightly · stage", URL: "/schedules/4", Tag: "schedule-4", Badge: 3}},
		{"deleted task keeps its URL group", db.Notification{ID: 3, Event: db.EventFailed, ExternalSessionID: &sessionID, Title: "Gone", URL: "/tasks/9"},
			Payload{Title: "Failed", Body: "Gone", URL: "/tasks/9", Tag: "task-9", Badge: 3}},
		{"external session", db.Notification{ID: 4, Event: db.EventDone, ConnectionID: 1, ExternalSessionID: &sessionID, Title: "Ext", ProjectTitle: &project,
			URL: "/sessions/provider:session%2Fwith%25id"},
			Payload{Title: "Session finished", Body: "Ext · stage", URL: "/?inbox", Tag: "session-1:" + sessionID, Badge: 3}},
		{"identity-less", db.Notification{ID: 5, Event: db.EventNeedsInput, Title: "Old", URL: "/"},
			Payload{Title: "Needs input", Body: "Old", URL: "/?inbox", Tag: "", Badge: 3}},
	}
	for _, tc := range tests {
		if got := PayloadFor(tc.n, 3); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// handle signals the inbox hub only when it records a row.
func TestHandleSignalsInbox(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	n.Inbox = NewHub(t.Context())
	wake, unsub := n.Inbox.Subscribe()
	defer unsub()
	task := seedTask(t, store, db.StateNeedsInput)
	signalled := func() bool {
		select {
		case <-wake:
			return true
		default:
			return false
		}
	}
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventNeedsInput}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateFailed}})
	if got, err := store.ListNotifications(ctx, 10, nil); err != nil || len(got) != 0 || signalled() {
		t.Fatalf("filtered event: rows %d err %v, want no row and no signal", len(got), err)
	}
	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}})
	if got, err := store.ListNotifications(ctx, 10, nil); err != nil || len(got) != 1 || !signalled() {
		t.Fatalf("recorded event: rows %d err %v, want one row and a signal", len(got), err)
	}
}

func TestSendPath(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	srv := newPushServer(t)
	n.HTTPClient = srv.Client()
	endpoint := srv.URL + "/push/abc"
	subscribe(t, store, endpoint)
	task := seedTask(t, store, db.StateNeedsInput)

	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}})
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("push requests %d, want 1", len(reqs))
	}
	h := reqs[0].header
	keys, err := store.VAPIDKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	auth := h.Get("Authorization")
	if !strings.HasPrefix(auth, "vapid t=") || !strings.HasSuffix(auth, ", k="+keys.PublicKey) {
		t.Fatalf("Authorization %q", auth)
	}
	if got := jwtSub(t, auth); got != subject {
		t.Fatalf("VAPID sub %q, want %q", got, subject)
	}
	if h.Get("TTL") != "86400" || h.Get("Urgency") != "high" || h.Get("Content-Encoding") != "aes128gcm" {
		t.Fatalf("headers %v", h)
	}
	if got, want := h.Get("Topic"), topicFor(fmt.Sprintf("task-%d", task.ID)); got != want {
		t.Fatalf("Topic %q, want %q", got, want)
	}
	if len(reqs[0].body) == 0 {
		t.Fatal("empty push body")
	}
	sub, err := store.GetPushSubscription(ctx, 0, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if sub.LastSuccessAt == nil || sub.LastError != nil {
		t.Fatalf("after 201: %+v", sub)
	}

	// Ready → normal urgency; 500 records last_error and keeps the subscription.
	srv.setStatus(http.StatusInternalServerError)
	n.handle(ctx, event{ready: &task.ID})
	reqs = srv.requests()
	if len(reqs) != 2 || reqs[1].header.Get("Urgency") != "normal" {
		t.Fatalf("second push %d %v", len(reqs), reqs[len(reqs)-1].header)
	}
	sub, err = store.GetPushSubscription(ctx, 0, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if sub.LastError == nil || !strings.Contains(*sub.LastError, "500") {
		t.Fatalf("after 500: %+v", sub)
	}

	// 410 Gone deletes the subscription.
	srv.setStatus(http.StatusGone)
	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateFailed}})
	if _, err := store.GetPushSubscription(ctx, 0, endpoint); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("after 410: %v", err)
	}
}

func TestInboxRecordedWithoutSubscriptions(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	task := seedTask(t, store, db.StateNeedsInput)
	observed := t3.Thread{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", Status: t3.StatusCompleted, CreatedAt: time.Now().UTC()}
	if err := store.PutObservation(ctx, observed.Observation()); err != nil {
		t.Fatal(err)
	}

	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}})
	n.handle(ctx, event{session: &workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "ext-1"}, ProjectID: "proj-1", Title: "External", To: db.StateDone}})

	got, err := store.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("notifications %d, want 2", len(got))
	}
	session, tk := got[0], got[1] // newest first
	if tk.Event != db.EventNeedsInput || tk.TaskID == nil || *tk.TaskID != task.ID || tk.Title != "Say OK" ||
		tk.ProjectTitle == nil || *tk.ProjectTitle != "stage" || tk.URL != fmt.Sprintf("/tasks/%d", task.ID) ||
		tk.CurrentState == nil || *tk.CurrentState != db.StateNeedsInput || tk.ReadAt != nil {
		t.Fatalf("task notification %+v", tk)
	}
	// A session no task owns: its observed title, and the title of the one
	// Stage project its harness project is bound to.
	if session.Event != db.EventDone || session.TaskID != nil || session.ExternalSessionID == nil || *session.ExternalSessionID != "ext-1" ||
		session.Title != "External" || session.ProjectTitle == nil || *session.ProjectTitle != "stage" ||
		session.URL != "/?inbox" || session.CurrentState == nil || *session.CurrentState != db.StateDone {
		t.Fatalf("session notification %+v", session)
	}
}

func TestNotifyEventsFilter(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	srv := newPushServer(t)
	subscribe(t, store, srv.URL+"/push/1")
	task := seedTask(t, store, db.StateNeedsInput)
	observed := t3.Thread{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", Status: t3.StatusCompleted, CreatedAt: time.Now().UTC()}
	if err := store.PutObservation(ctx, observed.Observation()); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		got, err := store.ListNotifications(ctx, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}

	session := event{session: &workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "ext-1"}, ProjectID: "proj-1", Title: "External", To: db.StateDone}}
	// Session finished off: the session's done leaves no inbox row and no push.
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventNeedsInput, db.EventFailed}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, session)
	if c, r := count(), len(srv.requests()); c != 0 || r != 0 {
		t.Fatalf("done disabled: %d notifications, %d pushes", c, r)
	}
	n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}})
	if c, r := count(), len(srv.requests()); c != 1 || r != 1 {
		t.Fatalf("needs input enabled: %d notifications, %d pushes", c, r)
	}

	// Session finished on records the session's done.
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventDone}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, session)
	if c, r := count(), len(srv.requests()); c != 2 || r != 2 {
		t.Fatalf("done enabled: %d notifications, %d pushes", c, r)
	}
	if got, err := store.ListNotifications(ctx, 1, nil); err != nil || len(got) != 1 || got[0].Event != db.EventDone {
		t.Fatalf("done notification %+v %v", got, err)
	}
	if p, _, err := n.payload(ctx, session); err != nil || p.Title != "Session finished" {
		t.Fatalf("done payload %+v %v", p, err)
	}

	// All off: nothing recorded or pushed.
	if _, err := store.SetNotifyEvents(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, to := range []db.TaskState{db.StateNeedsInput, db.StateFailed} {
		n.handle(ctx, event{task: &workevent.TaskChange{TaskID: task.ID, To: to}})
	}
	n.handle(ctx, event{ready: &task.ID})
	n.handle(ctx, session)
	if c, r := count(), len(srv.requests()); c != 2 || r != 2 {
		t.Fatalf("all disabled: %d notifications, %d pushes", c, r)
	}
}

// The ready event (release runner OnReady): queued, filtered by the ready
// preference, recorded on the task with a "Ready" push.
func TestReadyNotification(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	srv := newPushServer(t)
	subscribe(t, store, srv.URL+"/push/1")
	task := seedTask(t, store, db.StateBacklog)

	n.OnReady(ctx, task.ID)
	if len(n.events) != 1 || (<-n.events).ready == nil {
		t.Fatal("OnReady must queue one ready event")
	}
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventDone}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{ready: &task.ID})
	if got, err := store.ListNotifications(ctx, 10, nil); err != nil || len(got) != 0 || len(srv.requests()) != 0 {
		t.Fatalf("ready disabled: %+v %v", got, err)
	}
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventReady}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{ready: &task.ID})
	got, err := store.ListNotifications(ctx, 10, nil)
	if err != nil || len(got) != 1 || len(srv.requests()) != 1 {
		t.Fatalf("ready enabled: %+v %v", got, err)
	}
	if r := got[0]; r.Event != db.EventReady || r.TaskID == nil || *r.TaskID != task.ID || r.URL != fmt.Sprintf("/tasks/%d", task.ID) {
		t.Fatalf("ready notification %+v", r)
	}
	p, _, err := n.payload(ctx, event{ready: &task.ID})
	if err != nil || p.Title != "Ready to start" || p.URL != fmt.Sprintf("/tasks/%d", task.ID) || p.Tag != fmt.Sprintf("task-%d", task.ID) || p.Body != "Say OK · stage" {
		t.Fatalf("ready payload %+v %v", p, err)
	}
}

func TestNotificationNavigationWithExternalSessionID(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	task := seedTask(t, store, db.StateRunning)
	id := "provider:session/with%id"
	if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput, Session: &db.SessionRef{ConnectionID: store.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: id}}); err != nil {
		t.Fatal(err)
	}
	want := "/?inbox"
	for _, e := range []event{
		{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateNeedsInput}},
		{session: &workevent.SessionChange{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: id}, Title: "External", ProjectID: "p", To: db.StateDone}},
	} {
		payload, record, err := n.payload(ctx, e)
		wantPayload, wantRecord := want, want
		if e.task != nil {
			// Task notifications keep the task path.
			wantPayload, wantRecord = fmt.Sprintf("/tasks/%d", task.ID), fmt.Sprintf("/tasks/%d", task.ID)
		}
		if err != nil || payload.URL != wantPayload || record.URL != wantRecord {
			t.Fatalf("payload=%+v record=%+v err=%v", payload, record, err)
		}
	}
}

func TestTaskNotificationGroupSurvivesSessionChange(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	if _, err := store.RefreshWorkspaces(ctx, dbtest.T3Connection, []t3.Project{{ID: "proj-1", WorkspaceRoot: "/w/1"}}); err != nil {
		t.Fatal(err)
	}
	task := seedTask(t, store, db.StateBacklog)
	for _, sessionID := range []string{"first-session", "second-session"} {
		if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone, Session: &db.SessionRef{ConnectionID: store.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: sessionID}}); err != nil {
			t.Fatal(err)
		}
		e := event{task: &workevent.TaskChange{TaskID: task.ID, To: db.StateFailed}}
		payload, _, err := n.payload(ctx, e)
		if err != nil || payload.URL != fmt.Sprintf("/tasks/%d", task.ID) {
			t.Fatalf("session navigation: %+v %v", payload, err)
		}
		n.handle(ctx, e)
	}
	// A started task cannot be deleted (archive only), so its group keeps the task.
	var conflict *db.ConflictError
	if err := store.DeleteTask(ctx, task.ID); !errors.As(err, &conflict) {
		t.Fatalf("delete started task: %v", err)
	}
	latest, _, err := store.NotificationReadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := store.ListNotificationGroups(ctx, 10, nil, latest)
	if err != nil || len(groups) != 1 || groups[0].Key != fmt.Sprintf("task:%d", task.ID) || groups[0].EventCount != 2 ||
		groups[0].TaskID == nil || *groups[0].TaskID != task.ID {
		t.Fatalf("task lost stable notification group: %+v %v", groups, err)
	}
}

func TestSendTest(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	srv := newPushServer(t)
	n.HTTPClient = srv.Client()
	if err := n.SendTest(ctx, 0, srv.URL+"/unknown"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown endpoint: %v", err)
	}
	endpoint := srv.URL + "/push/t"
	subscribe(t, store, endpoint)
	if err := n.SendTest(ctx, 0, endpoint); err != nil {
		t.Fatal(err)
	}
	srv.setStatus(http.StatusBadRequest)
	var perr *PushError
	if err := n.SendTest(ctx, 0, endpoint); !errors.As(err, &perr) || perr.Status != 400 || !strings.Contains(perr.Msg, "nope") {
		t.Fatalf("rejected: %v", err)
	}
	srv.setStatus(http.StatusNotFound)
	if err := n.SendTest(ctx, 0, endpoint); !errors.As(err, &perr) || perr.Status != 404 {
		t.Fatalf("404: %v", err)
	}
	if _, err := store.GetPushSubscription(ctx, 0, endpoint); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("404 kept subscription: %v", err)
	}
}

// payload builds e's push and its single inbox record as handle does.
func (n *Notifier) payload(ctx context.Context, e event) (Payload, db.NewNotification, error) {
	recs, err := notificationRecords(ctx, n.store, e)
	if err != nil {
		return Payload{}, db.NewNotification{}, err
	}
	if len(recs) != 1 {
		return Payload{}, db.NewNotification{}, fmt.Errorf("%d records, want 1", len(recs))
	}
	badge, err := n.store.NeedsInputCount(ctx)
	if err != nil {
		return Payload{}, db.NewNotification{}, err
	}
	return PayloadFor(notificationFrom(recs[0]), badge), recs[0], nil
}

// jwtSub decodes the sub claim of the VAPID JWT in an Authorization header.
func jwtSub(t *testing.T, auth string) string {
	t.Helper()
	tok, _, _ := strings.Cut(strings.TrimPrefix(auth, "vapid t="), ",")
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims.Sub
}

// Topic: at most 32 URL-safe base64 chars (RFC 8030 §5.4), stable per tag,
// distinct across tags, also for session tags longer than 32 chars.
func TestTopicFor(t *testing.T) {
	long := "session-0b6f4c1e-3d2a-4f8e-9c7b-5a1d2e3f4a5b"
	seen := map[string]string{}
	for _, tag := range []string{"task-7", "task-8", "test", long} {
		got := topicFor(tag)
		if len(got) != 32 || strings.Trim(got, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			t.Fatalf("topicFor(%q) = %q, want 32 URL-safe base64 chars", tag, got)
		}
		if topicFor(tag) != got {
			t.Fatalf("topicFor(%q) not stable", tag)
		}
		if prev, ok := seen[got]; ok {
			t.Fatalf("topicFor(%q) == topicFor(%q)", tag, prev)
		}
		seen[got] = tag
	}
}

func TestVAPIDSubject(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for in, want := range map[string]string{
		"https://stage.example": "https://stage.example",
		"http://stage.lan:7380": FallbackSubject,
		"":                      FallbackSubject,
	} {
		if got := New(nil, in, log).subject; got != want {
			t.Errorf("STAGE_PUBLIC_URL %q: subject %q, want %q", in, got, want)
		}
	}
}

func acceptTask(t *testing.T, store *db.Store, id int64) int64 {
	t.Helper()
	ctx := context.Background()
	p, err := store.CompletionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.ChangeAcceptance(ctx, id, db.AcceptanceRequest{RequestID: fmt.Sprintf("accept-%d-%d", id, p.Revision), Revision: p.Revision, EvidenceVersion: p.EvidenceVersion}, false)
	if err != nil || r.AcceptanceID == nil {
		t.Fatalf("accept %d: %+v, %v", id, r, err)
	}
	return *r.AcceptanceID
}

// recorded lists the inbox rows newer than after, oldest first.
func recorded(t *testing.T, store *db.Store, after int64) []db.Notification {
	t.Helper()
	rows, err := store.ListNotifications(context.Background(), 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []db.Notification
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].ID > after {
			out = append(out, rows[i])
		}
	}
	return out
}

func lastID(rows []db.Notification) int64 {
	if len(rows) == 0 {
		return 0
	}
	return rows[len(rows)-1].ID
}

// Completion (inference or an agent): completed while the acceptance is
// current; tree_completed once, for the newest acceptance of an open tree.
func TestCompletionNotifications(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	srv := newPushServer(t)
	subscribe(t, store, srv.URL+"/push/1")
	first := seedTask(t, store, db.StateDone)
	second, err := store.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: first.ProjectID, Title: "Ship it", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.CreateTree(ctx, db.NewTree{Title: "Ship auth", Tasks: []db.TreeMember{{TaskID: first.ID}, {TaskID: second.ID}}})
	if err != nil {
		t.Fatal(err)
	}

	n.OnCompleted(ctx, first.ID, 0)
	if len(n.events) != 1 || (<-n.events).completed == nil {
		t.Fatal("OnCompleted must queue one completion")
	}

	// Not accepted (or reopened since): nothing.
	n.handle(ctx, event{completed: &completion{TaskID: first.ID}})
	if rows := recorded(t, store, 0); len(rows) != 0 {
		t.Fatalf("unaccepted task notified: %+v", rows)
	}

	// One member accepted: completed only, on the task's group.
	firstAcceptance := acceptTask(t, store, first.ID)
	n.handle(ctx, event{completed: &completion{TaskID: first.ID}})
	rows := recorded(t, store, 0)
	if len(rows) != 1 || rows[0].Event != db.EventCompleted || rows[0].TaskID == nil || *rows[0].TaskID != first.ID ||
		rows[0].URL != fmt.Sprintf("/tasks/%d", first.ID) || db.NotificationKey(rows[0]) != fmt.Sprintf("task:%d", first.ID) {
		t.Fatalf("completed row %+v", rows)
	}
	if p := PayloadFor(rows[0], 0); p.Title != "Task completed" || p.Body != "Say OK · stage" {
		t.Fatalf("completed payload %+v", p)
	}
	// A stale acceptance id (replaced since) records nothing.
	n.handle(ctx, event{completed: &completion{TaskID: first.ID, AcceptanceID: firstAcceptance + 100}})
	if got := recorded(t, store, lastID(rows)); len(got) != 0 {
		t.Fatalf("stale acceptance notified: %+v", got)
	}

	// The last member: completed, then tree_completed on the tree's group.
	secondAcceptance := acceptTask(t, store, second.ID)
	n.handle(ctx, event{completed: &completion{TaskID: second.ID, AcceptanceID: secondAcceptance}})
	got := recorded(t, store, lastID(rows))
	if len(got) != 2 || got[0].Event != db.EventCompleted || got[1].Event != db.EventTreeCompleted ||
		got[1].TreeID == nil || *got[1].TreeID != tree.ID || got[1].TaskID != nil || got[1].Title != "Ship auth" ||
		got[1].URL != fmt.Sprintf("/trees/%d", tree.ID) || db.NotificationKey(got[1]) != fmt.Sprintf("tree:%d", tree.ID) {
		t.Fatalf("last member rows %+v", got)
	}
	if p := PayloadFor(got[1], 0); p.Title != "Tree completed" || p.URL != fmt.Sprintf("/trees/%d", tree.ID) || p.Tag != fmt.Sprintf("tree-%d", tree.ID) {
		t.Fatalf("tree payload %+v", p)
	}
	if len(srv.requests()) != 3 {
		t.Fatalf("pushes %d, want 3", len(srv.requests()))
	}
	rows = append(rows, got...)

	// An older member's late completion does not complete the tree again.
	n.handle(ctx, event{completed: &completion{TaskID: first.ID, AcceptanceID: firstAcceptance}})
	got = recorded(t, store, lastID(rows))
	if len(got) != 1 || got[0].Event != db.EventCompleted {
		t.Fatalf("older member rows %+v", got)
	}
	rows = append(rows, got...)

	// A closed tree releases nothing, and completes nothing.
	if _, err := store.SetTreeStatus(ctx, tree.ID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{completed: &completion{TaskID: second.ID, AcceptanceID: secondAcceptance}})
	got = recorded(t, store, lastID(rows))
	if len(got) != 1 || got[0].Event != db.EventCompleted {
		t.Fatalf("closed tree rows %+v", got)
	}
	rows = append(rows, got...)

	// The filter applies per event.
	if _, err := store.SetTreeStatus(ctx, tree.ID, db.TreeOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventTreeCompleted}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{completed: &completion{TaskID: second.ID, AcceptanceID: secondAcceptance}})
	got = recorded(t, store, lastID(rows))
	if len(got) != 1 || got[0].Event != db.EventTreeCompleted {
		t.Fatalf("filtered rows %+v", got)
	}
}

// Cron fires: started notifies schedule_started, skipped and missed
// schedule_skipped, on the schedule's group; manual runs never notify.
func TestScheduleRunNotifications(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	task := seedTask(t, store, db.StateBacklog)
	sc, err := store.CreateSchedule(ctx, db.NewSchedule{ConnectionID: dbtest.T3Connection, ProjectID: task.ProjectID, Title: "Nightly", Prompt: "p", Cron: "0 3 * * *", Tz: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{db.RunManual, "", "unknown"} {
		n.OnScheduleRun(ctx, sc.ID, outcome)
	}
	if len(n.events) != 0 {
		t.Fatalf("queued %d events for outcomes that do not notify", len(n.events))
	}
	if _, err := store.SetNotifyEvents(ctx, db.NotificationEvents); err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{db.RunStarted, db.RunSkipped, db.RunMissed} {
		n.OnScheduleRun(ctx, sc.ID, outcome)
		n.handle(ctx, <-n.events)
	}
	rows := recorded(t, store, 0)
	want := []db.NotificationEvent{db.EventScheduleStarted, db.EventScheduleSkipped, db.EventScheduleSkipped}
	if len(rows) != len(want) {
		t.Fatalf("schedule rows %+v", rows)
	}
	for i, r := range rows {
		if r.Event != want[i] || r.ScheduleID == nil || *r.ScheduleID != sc.ID || r.TaskID != nil || r.Title != "Nightly" ||
			r.ProjectTitle == nil || *r.ProjectTitle != "stage" || db.NotificationKey(r) != fmt.Sprintf("schedule:%d", sc.ID) {
			t.Fatalf("schedule row %d: %+v", i, r)
		}
	}
	if p := PayloadFor(rows[0], 0); p.Title != "Schedule started" || p.URL != fmt.Sprintf("/schedules/%d", sc.ID) || p.Tag != fmt.Sprintf("schedule-%d", sc.ID) {
		t.Fatalf("started payload %+v", p)
	}
	if p := PayloadFor(rows[1], 0); p.Title != "Schedule skipped" || p.Body != "Nightly · stage" {
		t.Fatalf("skipped payload %+v", p)
	}

	// Run started is opt-in: the default filter leaves it out.
	if _, err := store.SetNotifyEvents(ctx, db.DefaultNotifyEvents); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{schedule: &scheduleRun{ScheduleID: sc.ID, Outcome: db.RunStarted}})
	if got := recorded(t, store, lastID(rows)); len(got) != 0 {
		t.Fatalf("schedule_started recorded while off: %+v", got)
	}
}
