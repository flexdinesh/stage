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
	"os"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/db/dbtest"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("t3stage_test_notify")
	if testDB.Skip != "" {
		fmt.Println("notify integration tests skipped:", testDB.Skip)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

const subject = "https://t3stage.test"

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
	p, err := store.InsertProject(ctx, db.Project{T3ProjectID: "proj-1", Title: "t3-stage", WorkspaceRoot: "/w/1", Source: db.SourceApp})
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
	for _, to := range []db.TaskState{db.StateInterrupted, db.StateCancelled, db.StateSettled, db.StateRunning, db.StateLaunching, db.StateBacklog} {
		n.OnTransition(context.Background(), t3sync.Change{TaskID: 1, To: to})
		n.OnThreadChange(context.Background(), t3sync.ThreadChange{ThreadID: "th", To: to})
	}
	if len(n.events) != 0 {
		t.Fatalf("queued %d events for non-notifying states", len(n.events))
	}
	for _, to := range []db.TaskState{db.StateNeedsInput, db.StateDone, db.StateFailed} {
		n.OnTransition(context.Background(), t3sync.Change{TaskID: 1, To: to})
		n.OnThreadChange(context.Background(), t3sync.ThreadChange{ThreadID: "th", To: to})
	}
	if len(n.events) != 6 {
		t.Fatalf("queued %d events, want 6", len(n.events))
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
	if err := store.UpsertThreads(ctx, []t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", Status: t3.StatusWaiting, CreatedAt: now},
		{ThreadID: "ext-2", ProjectID: "proj-unknown", Title: "Other", Status: t3.StatusCompleted, CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetObservedState(ctx, "ext-1", db.StateNeedsInput); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		e    event
		want Payload
	}{
		{"task needs input", event{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}},
			Payload{Title: "Needs input", Body: "Say OK · t3-stage", URL: fmt.Sprintf("/tasks/%d", task.ID), Tag: fmt.Sprintf("task-%d", task.ID), Badge: 2}},
		{"task done", event{task: &t3sync.Change{TaskID: task.ID, To: db.StateDone}},
			Payload{Title: "Done", Body: "Say OK · t3-stage", URL: fmt.Sprintf("/tasks/%d", task.ID), Tag: fmt.Sprintf("task-%d", task.ID), Badge: 2}},
		{"thread with app project", event{thread: &t3sync.ThreadChange{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", To: db.StateNeedsInput}},
			Payload{Title: "Needs input", Body: "Fix bug & test · t3-stage", URL: "/sessions/ext-1", Tag: "thread-ext-1", Badge: 2}},
		{"thread without app project", event{thread: &t3sync.ThreadChange{ThreadID: "ext-2", ProjectID: "proj-unknown", Title: "Other", To: db.StateFailed}},
			Payload{Title: "Failed", Body: "Other · proj-unknown", URL: "/sessions/ext-2", Tag: "thread-ext-2", Badge: 2}},
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
	if err := store.UpsertThreads(ctx, []t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "External", Status: t3.StatusCompleted, CreatedAt: time.Now().UTC()},
	}); err != nil {
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
		tk.ProjectTitle == nil || *tk.ProjectTitle != "t3-stage" || tk.URL != fmt.Sprintf("/tasks/%d", task.ID) ||
		tk.CurrentState == nil || *tk.CurrentState != db.StateNeedsInput || tk.ReadAt != nil {
		t.Fatalf("task notification %+v", tk)
	}
	if th.Event != db.StateDone || th.TaskID != nil || th.T3ThreadID == nil || *th.T3ThreadID != "ext-1" ||
		th.Title != "External" || th.URL != "/sessions/ext-1" || th.CurrentState == nil || *th.CurrentState != db.StateDone {
		t.Fatalf("thread notification %+v", th)
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
	want := "/sessions/provider:session%2Fwith%25id"
	for _, e := range []event{
		{task: &t3sync.Change{TaskID: task.ID, To: db.StateNeedsInput}},
		{thread: &t3sync.ThreadChange{ThreadID: id, Title: "External", ProjectID: "p", To: db.StateDone}},
	} {
		payload, record, err := n.payload(ctx, e)
		if err != nil || payload.URL != want || record.URL != want {
			t.Fatalf("payload=%+v record=%+v err=%v", payload, record, err)
		}
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
