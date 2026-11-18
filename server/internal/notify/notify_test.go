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
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
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
	header http.Header
	body   []byte
}

func newPushServer(t *testing.T) *pushServer {
	p := &pushServer{status: http.StatusCreated}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.reqs = append(p.reqs, pushReq{header: r.Header.Clone(), body: body})
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
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPushSubscription(context.Background(), endpoint,
		base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(auth), "test-agent"); err != nil {
		t.Fatal(err)
	}
}

func seedTask(t *testing.T, store *db.Store, state db.TaskState) db.Task {
	t.Helper()
	ctx := context.Background()
	p, err := store.InsertProject(ctx, db.Project{Title: "stage", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "proj-1", Workspace: "/w/1"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "Say OK", Prompt: "p",
		RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}})
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
		n.OnTransition(context.Background(), t3sync.Change{TaskID: 1, To: to})
		n.OnThreadChange(context.Background(), t3sync.ThreadChange{ThreadID: "th", To: to})
	}
	if len(n.events) != 0 {
		t.Fatalf("queued %d events for non-notifying states", len(n.events))
	}
	for _, to := range []db.TaskState{db.StateNeedsInput, db.StateDone, db.StateFailed, db.StateSettled} {
		n.OnTransition(context.Background(), t3sync.Change{TaskID: 1, To: to})
		n.OnThreadChange(context.Background(), t3sync.ThreadChange{ThreadID: "th", To: to})
	}
	if len(n.events) != 8 {
		t.Fatalf("queued %d events, want 8", len(n.events))
	}
	// A full queue drops instead of blocking.
	done := make(chan struct{})
	go func() {
		for range queueSize + 10 {
			n.OnTransition(context.Background(), t3sync.Change{TaskID: 1, To: db.StateDone})
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
	if err := store.UpsertThreads(ctx, t3.Observations([]t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", Status: t3.StatusWaiting, CreatedAt: now},
		{ThreadID: "ext-2", ProjectID: "proj-unknown", Title: "Other", Status: t3.StatusCompleted, CreatedAt: now},
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertThreadDetail(ctx, t3.ThreadDetail{Thread: t3.Thread{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", Status: t3.StatusWaiting, UpdatedAt: time.Now().UTC()}, PendingRequestCount: 1}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		e    event
		want Payload
	}{
		{"task needs input", event{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}},
			Payload{Title: "Needs input", Body: "Say OK · stage", URL: fmt.Sprintf("/tasks/%d", task.ID), Tag: fmt.Sprintf("task-%d", task.ID), Badge: 2}},
		{"task done", event{task: &t3sync.Change{TaskID: task.ID, To: db.StateDone}},
			Payload{Title: "Done", Body: "Say OK · stage", URL: fmt.Sprintf("/tasks/%d", task.ID), Tag: fmt.Sprintf("task-%d", task.ID), Badge: 2}},
		{"thread with app project", event{thread: &t3sync.ThreadChange{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", To: db.StateNeedsInput}},
			Payload{Title: "Needs input", Body: "Fix bug & test · stage", URL: "/?inbox", Tag: "thread-1:ext-1", Badge: 2}},
		{"thread without app project", event{thread: &t3sync.ThreadChange{ThreadID: "ext-2", ProjectID: "proj-unknown", Title: "Other", To: db.StateFailed}},
			Payload{Title: "Failed", Body: "Other · proj-unknown", URL: "/?inbox", Tag: "thread-1:ext-2", Badge: 2}},
	}
	for _, tc := range tests {
		got, _, err := n.payload(ctx, tc.e)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// PayloadFor builds the push from a stored row; the stream shares it.
func TestPayloadFor(t *testing.T) {
	task, thread, project := int64(7), "provider:session/with%id", "stage"
	created := time.Now()
	tests := []struct {
		name string
		n    db.Notification
		want Payload
	}{
		{"task needs input with thread", db.Notification{ID: 1, Event: db.StateNeedsInput, TaskID: &task, T3ThreadID: &thread,
			Title: "Say OK", ProjectTitle: &project, URL: "/tasks/7", CreatedAt: created},
			Payload{Title: "Needs input", Body: "Say OK · stage", URL: "/tasks/7", Tag: "task-7", Badge: 3}},
		{"task without thread", db.Notification{ID: 2, Event: db.StateDone, TaskID: &task, Title: "Say OK", ProjectTitle: &project, URL: "/tasks/7"},
			Payload{Title: "Done", Body: "Say OK · stage", URL: "/tasks/7", Tag: "task-7", Badge: 3}},
		{"deleted task keeps its URL group", db.Notification{ID: 3, Event: db.StateFailed, T3ThreadID: &thread, Title: "Gone", URL: "/tasks/9"},
			Payload{Title: "Failed", Body: "Gone", URL: "/tasks/9", Tag: "task-9", Badge: 3}},
		{"external thread", db.Notification{ID: 4, Event: db.StateSettled, T3ThreadID: &thread, Title: "Ext", ProjectTitle: &project,
			URL: "/sessions/provider:session%2Fwith%25id"},
			Payload{Title: "Settled", Body: "Ext · stage", URL: "/?inbox", Tag: "thread-1:" + thread, Badge: 3}},
		{"identity-less", db.Notification{ID: 5, Event: db.StateNeedsInput, Title: "Old", URL: "/"},
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
	if _, err := store.SetNotifyEvents(ctx, []db.TaskState{db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateDone}})
	if got, err := store.ListNotifications(ctx, 10, nil); err != nil || len(got) != 0 || signalled() {
		t.Fatalf("filtered event: rows %d err %v, want no row and no signal", len(got), err)
	}
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}})
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

	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}})
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
	sub, err := store.GetPushSubscription(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if sub.LastSuccessAt == nil || sub.LastError != nil {
		t.Fatalf("after 201: %+v", sub)
	}

	// Done → normal urgency; 500 records last_error and keeps the subscription.
	srv.setStatus(http.StatusInternalServerError)
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateDone}})
	reqs = srv.requests()
	if len(reqs) != 2 || reqs[1].header.Get("Urgency") != "normal" {
		t.Fatalf("second push %d %v", len(reqs), reqs[len(reqs)-1].header)
	}
	sub, err = store.GetPushSubscription(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if sub.LastError == nil || !strings.Contains(*sub.LastError, "500") {
		t.Fatalf("after 500: %+v", sub)
	}

	// 410 Gone deletes the subscription.
	srv.setStatus(http.StatusGone)
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateFailed}})
	if _, err := store.GetPushSubscription(ctx, endpoint); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("after 410: %v", err)
	}
}

func TestInboxRecordedWithoutSubscriptions(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	task := seedTask(t, store, db.StateNeedsInput)
	if err := store.UpsertThreads(ctx, t3.Observations([]t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", Status: t3.StatusCompleted, CreatedAt: time.Now().UTC()},
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.SetObservedState(ctx, "ext-1", db.StateDone); err != nil {
		t.Fatal(err)
	}

	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}})
	n.handle(ctx, event{thread: &t3sync.ThreadChange{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", To: db.StateDone}})

	got, err := store.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("notifications %d, want 2", len(got))
	}
	th, tk := got[0], got[1] // newest first
	if tk.Event != db.StateNeedsInput || tk.TaskID == nil || *tk.TaskID != task.ID || tk.Title != "Say OK" ||
		tk.ProjectTitle == nil || *tk.ProjectTitle != "stage" || tk.URL != fmt.Sprintf("/tasks/%d", task.ID) ||
		tk.CurrentState == nil || *tk.CurrentState != db.StateNeedsInput || tk.ReadAt != nil {
		t.Fatalf("task notification %+v", tk)
	}
	if th.Event != db.StateDone || th.TaskID != nil || th.T3ThreadID == nil || *th.T3ThreadID != "ext-1" ||
		th.Title != "External" || th.URL != "/?inbox" || th.CurrentState == nil || *th.CurrentState != db.StateDone {
		t.Fatalf("thread notification %+v", th)
	}
}

func TestNotifyEventsFilter(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	srv := newPushServer(t)
	subscribe(t, store, srv.URL+"/push/1")
	task := seedTask(t, store, db.StateNeedsInput)
	if err := store.UpsertThreads(ctx, t3.Observations([]t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", Status: t3.StatusCompleted, CreatedAt: time.Now().UTC()},
	})); err != nil {
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

	// Done off: done events (task and thread) leave no inbox row and no push.
	if _, err := store.SetNotifyEvents(ctx, []db.TaskState{db.StateNeedsInput, db.StateFailed}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateDone}})
	n.handle(ctx, event{thread: &t3sync.ThreadChange{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", To: db.StateDone}})
	if c, r := count(), len(srv.requests()); c != 0 || r != 0 {
		t.Fatalf("done disabled: %d notifications, %d pushes", c, r)
	}
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}})
	if c, r := count(), len(srv.requests()); c != 1 || r != 1 {
		t.Fatalf("needs input enabled: %d notifications, %d pushes", c, r)
	}

	// Settled: off drops it; on records "Settled" for task and thread.
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateSettled}})
	if c, r := count(), len(srv.requests()); c != 1 || r != 1 {
		t.Fatalf("settled disabled: %d notifications, %d pushes", c, r)
	}
	if _, err := store.SetNotifyEvents(ctx, []db.TaskState{db.StateSettled}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateSettled}})
	n.handle(ctx, event{thread: &t3sync.ThreadChange{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", To: db.StateSettled}})
	if c, r := count(), len(srv.requests()); c != 3 || r != 3 {
		t.Fatalf("settled enabled: %d notifications, %d pushes", c, r)
	}
	if got, err := store.ListNotifications(ctx, 2, nil); err != nil || len(got) != 2 ||
		got[0].Event != db.StateSettled || got[1].Event != db.StateSettled {
		t.Fatalf("settled notifications %+v %v", got, err)
	}
	if p, _, err := n.payload(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: db.StateSettled}}); err != nil || p.Title != "Settled" {
		t.Fatalf("settled payload %+v %v", p, err)
	}

	// All off: nothing recorded or pushed.
	if _, err := store.SetNotifyEvents(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, to := range db.NotificationEvents {
		n.handle(ctx, event{task: &t3sync.Change{TaskID: task.ID, To: to}})
	}
	if c, r := count(), len(srv.requests()); c != 3 || r != 3 {
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
	if len(n.events) != 1 || (<-n.events).to() != db.EventReady {
		t.Fatal("OnReady must queue one ready event")
	}
	if _, err := store.SetNotifyEvents(ctx, []db.TaskState{db.StateDone}); err != nil {
		t.Fatal(err)
	}
	n.handle(ctx, event{ready: &task.ID})
	if got, err := store.ListNotifications(ctx, 10, nil); err != nil || len(got) != 0 || len(srv.requests()) != 0 {
		t.Fatalf("ready disabled: %+v %v", got, err)
	}
	if _, err := store.SetNotifyEvents(ctx, []db.TaskState{db.EventReady}); err != nil {
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
	if err != nil || p.Title != "Ready" || p.URL != fmt.Sprintf("/tasks/%d", task.ID) || p.Tag != fmt.Sprintf("task-%d", task.ID) || p.Body != "Say OK · stage" {
		t.Fatalf("ready payload %+v %v", p, err)
	}
}

func TestSessionNotificationURLsEncodeThreadIDs(t *testing.T) {
	n, store := newNotifier(t)
	ctx := context.Background()
	task := seedTask(t, store, db.StateRunning)
	id := "provider:session/with%id"
	if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput, ThreadID: &id}); err != nil {
		t.Fatal(err)
	}
	want := "/?inbox"
	for _, e := range []event{
		{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}},
		{thread: &t3sync.ThreadChange{ThreadID: id, Title: "External", ProjectID: "p", To: db.StateDone}},
	} {
		payload, record, err := n.payload(ctx, e)
		wantPayload, wantRecord := want, want
		if e.task != nil {
			// Needs input lands on the Needs input panel; the record keeps the task path.
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
	task := seedTask(t, store, db.StateBacklog)
	for _, thread := range []string{"first-session", "second-session"} {
		if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone, ThreadID: &thread}); err != nil {
			t.Fatal(err)
		}
		e := event{task: &t3sync.Change{TaskID: task.ID, To: db.StateDone}}
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
	if err := n.SendTest(ctx, srv.URL+"/unknown"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown endpoint: %v", err)
	}
	endpoint := srv.URL + "/push/t"
	subscribe(t, store, endpoint)
	if err := n.SendTest(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	srv.setStatus(http.StatusBadRequest)
	var perr *PushError
	if err := n.SendTest(ctx, endpoint); !errors.As(err, &perr) || perr.Status != 400 || !strings.Contains(perr.Msg, "nope") {
		t.Fatalf("rejected: %v", err)
	}
	srv.setStatus(http.StatusNotFound)
	if err := n.SendTest(ctx, endpoint); !errors.As(err, &perr) || perr.Status != 404 {
		t.Fatalf("404: %v", err)
	}
	if _, err := store.GetPushSubscription(ctx, endpoint); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("404 kept subscription: %v", err)
	}
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
// distinct across tags, also for thread tags longer than 32 chars.
func TestTopicFor(t *testing.T) {
	long := "thread-0b6f4c1e-3d2a-4f8e-9c7b-5a1d2e3f4a5b"
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
