package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

func TestWorkerObservationChannelReplaysAfterOfflineRestartAndLostAck(t *testing.T) {
	r := newObserverChannel(t)
	r.disconnect()
	r.push(r.stream, db.StateNeedsInput, time.Second)
	r.push(r.stream, db.StateRunning, 2*time.Second)
	r.stopWorker()

	// The saved watch can start a new local subscription without the main
	// server. The saved outbox still contains the first process's transitions.
	r.startWorker()
	stream := receiveObserver(t, r.adapter.streams, "restored offline subscription")
	done := r.push(stream, db.StateDone, 3*time.Second)
	r.taskIn(db.StateRunning)
	disk := r.outbox()
	var queued []observe.Record
	for _, entry := range disk.Pending {
		o := entry.Record.Observation
		if o != nil && o.UpdatedAt.After(r.adapter.baseline.UpdatedAt) {
			queued = append(queued, entry.Record)
		}
	}
	want := []db.TaskState{db.StateNeedsInput, db.StateRunning, db.StateDone}
	if len(queued) != len(want) {
		t.Fatalf("offline transitions %+v, want %v", queued, want)
	}
	for i, record := range queued {
		if record.Observation.State != string(want[i]) || record.ObservedAt.IsZero() {
			t.Fatalf("offline record %d = %+v, want %s with observed time", i, record, want[i])
		}
	}

	r.dropNextAck.Store(true)
	r.online.Store(true)
	lost := receiveObserver(t, r.lost, "main commit followed by lost HTTP acknowledgement")
	if lost.batch.Producer != r.initial.batch.Producer || lost.ack.Cursor != lost.batch.Cursor {
		t.Fatalf("lost acknowledgement %+v, initial producer %s", lost, r.initial.batch.Producer)
	}
	for _, record := range queued {
		sent := sampleIn(lost.batch, record.Observation.UpdatedAt)
		if sent == nil || sent.Cursor <= r.initial.ack.Cursor {
			t.Fatalf("offline sample missing from lost batch %+v", record)
		}
		record.Cursor = sent.Cursor
		if !reflect.DeepEqual(record, *sent) {
			t.Fatalf("worker changed offline sample on delivery: queued %+v, sent %+v", record, *sent)
		}
	}
	disk = r.outbox()
	if disk.Acked >= lost.ack.Cursor || disk.Cursor != lost.ack.Cursor || len(disk.Sealed) == 0 {
		t.Fatalf("worker discarded unacknowledged batch at cursor %d: %+v", lost.ack.Cursor, disk)
	}
	r.taskIn(db.StateDone)
	beforeRetry := r.effects()
	close(r.allowRetry)
	retry := r.delivered(done.UpdatedAt)
	if !bytes.Equal(lost.body, retry.body) || retry.ack != lost.ack {
		t.Fatalf("retried batch changed: lost %s ack %+v, retried %s ack %+v", lost.body, lost.ack, retry.body, retry.ack)
	}
	r.waitDrained(retry.ack.Cursor)
	afterRetry := r.effects()
	if !reflect.DeepEqual(beforeRetry, afterRetry) {
		t.Fatalf("retry changed committed effects: before %+v, after %+v", beforeRetry, afterRetry)
	}
	var times []time.Time
	for _, record := range queued {
		times = append(times, record.ObservedAt.Truncate(time.Microsecond))
	}
	r.assertEffects(afterRetry, want, times)
	finished := r.taskIn(db.StateDone).FinishedAt
	if finished == nil || !finished.Equal(times[2]) {
		t.Fatalf("finish time %v, want worker observation time %v", finished, times[2])
	}
	r.assertObservation(done)
}

func TestWorkerObservationChannelAcknowledgesRetiredSamples(t *testing.T) {
	r := newObserverChannel(t)
	r.disconnect()
	done := r.push(r.stream, db.StateDone, time.Second)
	if err := r.store.RetireConnection(t.Context(), done.Session.ConnectionID, 0); err != nil {
		t.Fatal(err)
	}

	r.online.Store(true)
	batch := r.delivered(done.UpdatedAt)
	r.waitDrained(batch.ack.Cursor)
	if task := r.taskIn(db.StateRunning); task.ConnectionActive {
		t.Fatal("task still shows a retired connection as active")
	}
	r.assertEffects(r.effects(), nil, nil)
	r.assertObservation(r.adapter.baseline)
	var saved struct{ Watch observe.Watch }
	readObserverJSON(t, filepath.Join(worker.Dir(r.config), "watch.json"), &saved)
	if len(saved.Watch.Connections) != 0 {
		t.Fatalf("retired connection remains watched: %+v", saved.Watch)
	}
}

func TestWorkerObservationChannelRearmsInactiveTask(t *testing.T) {
	r := openObserverChannel(t, db.StateFailed)
	waiting := r.adapter.sample(db.StateNeedsInput, 2*time.Second)
	if first := sampleIn(r.initial.batch, waiting.UpdatedAt); first == nil || !first.Attention || !first.Quiet {
		t.Fatalf("first sample did not come from the initial attention pass: %+v", r.initial.batch)
	}
	r.taskIn(db.StateNeedsInput)
	r.assertObservation(waiting)
	receiveObserver(t, r.adapter.streams, "worker subscription for the rearmed watch")

	done := r.adapter.sample(db.StateDone, 3*time.Second)
	r.adapter.snapshot.Store(&done)
	batch := r.delivered(done.UpdatedAt)
	if record := sampleIn(batch.batch, done.UpdatedAt); record == nil || record.Attention {
		t.Fatalf("rearmed task was not polled: %+v", batch.batch)
	}
	r.taskIn(db.StateDone)
	r.assertObservation(done)
}

func TestWorkerObservationChannelRecoversReleasedCursor(t *testing.T) {
	r := newObserverChannel(t)
	r.disconnect()
	// Model a restored main receipt with the worker outbox still intact.
	if err := r.storage.Write(t.Context(), func(tx store.Tx) error {
		return tx.ObservationProducers().SetCursor(t.Context(), r.machine, r.initial.batch.Producer, 0)
	}); err != nil {
		t.Fatal(err)
	}
	done := r.push(r.stream, db.StateDone, time.Second)
	r.online.Store(true)
	batch := r.delivered(done.UpdatedAt)
	if batch.batch.Producer != r.initial.batch.Producer || batch.ack.Cursor <= r.initial.ack.Cursor {
		t.Fatalf("delivery did not resume the original producer: %+v", batch)
	}
	var lost string
	if err := r.storage.QueryRowContext(t.Context(), `SELECT lost FROM observation_dispositions
		WHERE machine_id=? AND producer=? AND cursor=? AND code='receipt_rollback'`,
		r.machine, batch.batch.Producer, r.initial.ack.Cursor).Scan(&lost); err != nil || lost != strconv.FormatInt(r.initial.ack.Cursor, 10) {
		t.Fatalf("durable rollback disposition lost %q records: %v", lost, err)
	}
	waitObserver(t, "worker to receive a resample watch", func() bool {
		var saved struct{ Watch observe.Watch }
		readObserverJSON(t, filepath.Join(worker.Dir(r.config), "watch.json"), &saved)
		return saved.Watch.Resample
	})
	r.waitDrained(batch.ack.Cursor)
	r.taskIn(db.StateDone)
	r.assertObservation(done)
}

func TestWorkerObservationChannelSharesOrderingWithBridge(t *testing.T) {
	r := newObserverChannel(t)
	r.disconnect()
	stale := r.push(r.stream, db.StateFailed, 2*time.Second)
	done := r.push(r.stream, db.StateDone, 3*time.Second)
	bridge := r.adapter.sample(db.StateNeedsInput, 2*time.Second)

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(map[string][]execution.Observation{"observations": {bridge}}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/api/connections/%d/observations", r.server.URL, bridge.Session.ConnectionID)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, path, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var reply struct{ Results []api.ObservationResult }
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil || resp.StatusCode != http.StatusOK ||
		len(reply.Results) != 1 || reply.Results[0].Status != "ingested" {
		t.Fatalf("bridge status %d, reply %+v, err %v", resp.StatusCode, reply, err)
	}
	r.taskIn(db.StateNeedsInput)
	r.assertObservation(bridge)

	r.online.Store(true)
	batch := r.delivered(done.UpdatedAt)
	if sampleIn(batch.batch, stale.UpdatedAt) == nil {
		t.Fatalf("stale sample did not reach the main server: %+v", batch.batch)
	}
	r.waitDrained(batch.ack.Cursor)
	observedDone := sampleIn(batch.batch, done.UpdatedAt).ObservedAt.Truncate(time.Microsecond)
	r.assertEffects(r.effects(), []db.TaskState{db.StateNeedsInput, db.StateDone}, []time.Time{bridge.UpdatedAt, observedDone})
	r.taskIn(db.StateDone)
	r.assertObservation(done)
}

type observerSubscription struct {
	ctx  context.Context
	sink execution.Sink
}

type channelObservationAdapter struct {
	documentHarness
	baseline execution.Observation
	streams  chan observerSubscription
	snapshot atomic.Pointer[execution.Observation]
}

func (a *channelObservationAdapter) Attention(context.Context) ([]execution.SessionRef, error) {
	if current := a.snapshot.Load(); current != nil && current.State == string(db.StateNeedsInput) {
		return []execution.SessionRef{current.Session}, nil
	}
	return nil, nil
}

func (a *channelObservationAdapter) Launch(context.Context, execution.Intent, execution.Checkpoint, execution.SaveCheckpoint) (execution.Observation, error) {
	return a.baseline, nil
}

func (a *channelObservationAdapter) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if ref != a.baseline.Session {
		return execution.Observation{}, execution.ErrNotFound
	}
	if current := a.snapshot.Load(); current != nil {
		return *current, nil
	}
	// The snapshot endpoint lags the stream throughout this fixture.
	return a.baseline, nil
}

func (a *channelObservationAdapter) Events(ctx context.Context, sink execution.Sink) error {
	select {
	case a.streams <- observerSubscription{ctx: ctx, sink: sink}:
	case <-ctx.Done():
	}
	<-ctx.Done()
	return ctx.Err()
}

func (a *channelObservationAdapter) sample(state db.TaskState, since time.Duration) execution.Observation {
	o := a.baseline
	o.State, o.Active = string(state), state == db.StateRunning || state == db.StateNeedsInput
	o.UpdatedAt = o.UpdatedAt.Add(since)
	return o
}

type observerHost struct {
	*remote.Host
	observationRPCs *atomic.Int64
}

func (h observerHost) Handlers() map[string]workerchan.WorkerHandler {
	handlers := h.Host.Handlers()
	for _, kind := range []string{remote.KindObserve, remote.KindEvents, remote.KindAttention} {
		handle := handlers[kind]
		handlers[kind] = func(ctx context.Context, env workerchan.Envelope, up workerchan.Upcall) (any, error) {
			h.observationRPCs.Add(1)
			return handle(ctx, env, up)
		}
	}
	return handlers
}

type observerBatch struct {
	body  []byte
	batch observe.Batch
	ack   observe.BatchAck
}

type observerChannel struct {
	t                   *testing.T
	store               *db.Store
	storage             *sqlite.DB
	hub                 *workerchan.Hub
	server              *httptest.Server
	log                 *slog.Logger
	task                db.Task
	machine             int64
	config              string
	adapter             *channelObservationAdapter
	stream              observerSubscription
	initial             observerBatch
	listen              []listen.Addr
	stop                context.CancelFunc
	workerDone          chan error
	online, dropNextAck atomic.Bool
	ackDropped          atomic.Bool
	observationRPCs     atomic.Int64
	refused             chan struct{}
	allowRetry          chan struct{}
	lost, accepted      chan observerBatch
}

func newObserverChannel(t *testing.T) *observerChannel {
	t.Helper()
	return openObserverChannel(t, db.StateRunning)
}

func openObserverChannel(t *testing.T, initialState db.TaskState) *observerChannel {
	t.Helper()
	ctx := t.Context()
	var test dbtest.DB
	store := db.NewStore(test.Fresh(t))
	pairing, err := store.CreatePairing(ctx, "test worker")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.RedeemPairing(ctx, pairing.Code, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _ := documentHarnesses.Lookup("document")
	conn, err := store.SwitchInstance(ctx, grant.MachineID, descriptor, "documents-local", "Documents", 0)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.InsertProject(ctx, db.Project{Title: "Stage collection", Bindings: []db.ProjectBinding{
		{ConnectionID: conn.New, ExternalProjectID: "collection-z", Workspace: "volume://checkout"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, db.NewTask{ProjectID: project.ID, ConnectionID: conn.New, Title: "Write", Prompt: "Write the document"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventNeedsInput, db.EventDone, db.EventFailed}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	profile := harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}
	machines := machine.MustSet(machine.NewLocal(db.LocalMachine, profile, nil))
	eng := engine.NewForMachines(store, documentHarnesses, machines, log)
	inbox := notify.NewHub(ctx)
	notifier := notify.New(store, notify.FallbackSubject, log)
	notifier.Inbox = inbox
	eng.Observe.RecordNotification = notifier.RecordObservation
	eng.Observe.PollEvery = 20 * time.Millisecond
	hub := workerchan.NewHub(ctx, workerchan.Options{Version: "test"})
	eng.ServeWorkers(hub)
	hosts := remote.NewHosts(machines, hub, func(id int64) []int64 {
		if id == grant.MachineID && store.ConnectionActive(conn.New) {
			return []int64{conn.New}
		}
		return nil
	}, eng.Observe.Kick, log)
	hosts.SetSetup(remote.SetupHooks{Kinds: []string{descriptor.Kind}})
	hosts.Join(grant.MachineID)
	routes := api.NewRouter(api.Deps{Store: store, Engine: eng, Channels: hub, Machines: hosts, Harnesses: documentHarnesses,
		Logger: log, Inbox: inbox})
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if initialState != db.StateRunning {
		now = now.Add(-time.Hour)
	}
	adapter := &channelObservationAdapter{
		baseline: execution.Observation{Session: execution.SessionRef{ConnectionID: conn.New, ExternalSessionID: "document:1"},
			Project: "collection-z", Title: "Document", State: string(db.StateRunning), Active: true,
			ExecutionID: "write:1", CreatedAt: now, UpdatedAt: now},
		streams: make(chan observerSubscription, 8),
	}
	r := &observerChannel{t: t, store: store, storage: test.Pool, hub: hub, machine: grant.MachineID, log: log, task: task, adapter: adapter,
		refused: make(chan struct{}, 1), allowRetry: make(chan struct{}),
		lost: make(chan observerBatch, 1), accepted: make(chan observerBatch, 32)}
	r.online.Store(true)
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.serve(w, req, routes) }))
	t.Cleanup(r.server.Close)
	// Keep the gateway socket path short even under a long test name.
	short, err := os.MkdirTemp("", "stage-observe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	if r.listen, err = listen.ParseList("unix:" + filepath.Join(short, "gw.sock")); err != nil {
		t.Fatal(err)
	}
	r.config = filepath.Join(t.TempDir(), worker.ConfigFile)
	if err := worker.SaveConfig(r.config, worker.Config{Schema: worker.ConfigSchema, URL: r.server.URL, MachineID: grant.MachineID,
		Label: grant.Label, Token: grant.Token, JoinedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.stopWorker)
	r.startWorker()
	waitObserver(t, "remote worker ready", func() bool { return hub.Presence(grant.MachineID).Online })
	started, err := eng.Coordinator.StartTask(ctx, task.ID)
	if err != nil || started.State != db.StateRunning || started.Session == nil || *started.Session != adapter.baseline.Session {
		t.Fatalf("launch task: %+v %v", started, err)
	}
	initial := adapter.baseline
	if initialState != db.StateRunning {
		ageObservationTask(t, test.Pool, store, task.ID, adapter.sample(initialState, time.Second))
		initial = adapter.sample(db.StateNeedsInput, 2*time.Second)
		adapter.snapshot.Store(&initial)
	}
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		eng.Observe.Run(ctx)
	}()
	t.Cleanup(func() {
		receiveObserver(t, runDone, "main observation loop to stop")
		if n := r.observationRPCs.Load(); n != 0 {
			t.Errorf("main sent %d adapter.observe, adapter.events or adapter.attention RPCs", n)
		}
	})
	r.stream = receiveObserver(t, r.adapter.streams, "initial worker subscription")
	r.initial = r.delivered(initial.UpdatedAt)
	r.waitDrained(r.initial.ack.Cursor)
	return r
}

func (r *observerChannel) serve(w http.ResponseWriter, req *http.Request, routes http.Handler) {
	if req.URL.Path != "/api/worker/channel" {
		routes.ServeHTTP(w, req)
		return
	}
	if !r.online.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		if req.Method == http.MethodGet {
			select {
			case r.refused <- struct{}{}:
			default:
			}
		}
		return
	}
	if req.Method != http.MethodPost {
		routes.ServeHTTP(w, req)
		return
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var env workerchan.Envelope
	var post observerBatch
	if json.Unmarshal(raw, &env) != nil || env.Kind != observe.KindBatch || json.Unmarshal(env.Body, &post.batch) != nil {
		routes.ServeHTTP(w, req)
		return
	}
	post.body = env.Body
	if r.ackDropped.Load() {
		select {
		case <-r.allowRetry:
		case <-req.Context().Done():
			return
		}
	}
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	var result workerchan.Envelope
	if rec.Code == http.StatusOK && json.Unmarshal(rec.Body.Bytes(), &result) == nil && result.Error == nil &&
		json.Unmarshal(result.Body, &post.ack) == nil && post.ack.Cursor == post.batch.Cursor {
		if r.dropNextAck.CompareAndSwap(true, false) {
			r.ackDropped.Store(true)
			r.lost <- post
			panic(http.ErrAbortHandler)
		}
		select {
		case r.accepted <- post:
		case <-req.Context().Done():
			return
		}
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

func (r *observerChannel) startWorker() {
	t := r.t
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r.stop = cancel
	done := make(chan error, 1)
	r.workerDone = done
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{ConfigPath: r.config, HTTP: r.server.Client(), Logger: r.log, Listen: r.listen,
			MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
			Host: func(machineID int64, _ workerchan.Upcall) worker.Host {
				h := remote.NewHost(machine.NewLocal(machineID, harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, machine.LocalOps{}))
				id := r.adapter.baseline.Session.ConnectionID
				h.Local().Install(id, id, r.adapter)
				h.Local().SetConnector("document", contractConnector{})
				return observerHost{Host: h, observationRPCs: &r.observationRPCs}
			},
		})
	}()
}

func (r *observerChannel) stopWorker() {
	r.t.Helper()
	if r.stop == nil {
		return
	}
	r.stop()
	r.stop = nil
	if err := receiveObserver(r.t, r.workerDone, "worker to stop"); err != nil {
		r.t.Errorf("worker exited: %v", err)
	}
}

func (r *observerChannel) disconnect() {
	r.t.Helper()
	r.online.Store(false)
	r.hub.Close(r.machine)
	receiveObserver(r.t, r.refused, "worker HTTP reconnect to fail")
}

func (r *observerChannel) push(stream observerSubscription, state db.TaskState, since time.Duration) execution.Observation {
	r.t.Helper()
	sample := r.adapter.sample(state, since)
	if err := stream.sink(stream.ctx, sample); err != nil {
		r.t.Fatalf("worker observe %s: %v", state, err)
	}
	return sample
}

func sampleIn(batch observe.Batch, updatedAt time.Time) *observe.Record {
	for i, record := range batch.Records {
		if record.Observation != nil && record.Observation.UpdatedAt.Equal(updatedAt) {
			return &batch.Records[i]
		}
	}
	return nil
}

func (r *observerChannel) delivered(updatedAt time.Time) observerBatch {
	r.t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case post := <-r.accepted:
			if sampleIn(post.batch, updatedAt) != nil {
				return post
			}
		case <-timer.C:
			r.t.Fatalf("no successful HTTP acknowledgement for sample at %v", updatedAt)
		}
	}
}

type observerOutbox struct {
	Acked, Cursor   int64
	Lost            uint64
	Gap, SealedGap  *observe.Gap
	Sealed, Pending []struct{ Record observe.Record }
}

func (r *observerChannel) outbox() observerOutbox {
	r.t.Helper()
	var state observerOutbox
	readObserverJSON(r.t, filepath.Join(worker.Dir(r.config), "outbox", "state.json"), &state)
	return state
}

func (r *observerChannel) waitDrained(cursor int64) {
	r.t.Helper()
	waitObserver(r.t, "worker to durably drain the outbox", func() bool {
		state := r.outbox()
		return state.Acked >= cursor && state.Acked == state.Cursor && len(state.Sealed)+len(state.Pending) == 0 &&
			state.Gap == nil && state.SealedGap == nil && state.Lost == 0
	})
}

func (r *observerChannel) taskIn(state db.TaskState) db.Task {
	r.t.Helper()
	task, err := r.store.GetTask(r.t.Context(), r.task.ID)
	if err != nil || task.State != state {
		r.t.Fatalf("task state %s, want %s: %v", task.State, state, err)
	}
	return task
}

func (r *observerChannel) assertObservation(want execution.Observation) {
	r.t.Helper()
	got, err := r.store.Observation(r.t.Context(), want.Session)
	if err != nil || got.State != want.State || !got.UpdatedAt.Equal(want.UpdatedAt) {
		r.t.Fatalf("stored observation %+v, want %s at %v: %v", got, want.State, want.UpdatedAt, err)
	}
}

type observerEffects struct {
	events  []db.TaskEvent
	journal []db.JournalEntry
	inbox   []db.Notification
}

func (r *observerChannel) effects() observerEffects {
	t := r.t
	t.Helper()
	ctx := t.Context()
	events, err := r.store.TaskEvents(ctx, r.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.store.ReadJournal(ctx, db.JournalOwner{TaskID: &r.task.ID}, db.JournalQuery{Kind: "events", Q: "execution observed", Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := r.store.ListNotifications(ctx, 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(page.Entries)
	slices.Reverse(inbox)
	return observerEffects{events: slices.DeleteFunc(events, func(e db.TaskEvent) bool {
		return !strings.HasPrefix(e.Detail, "execution observed ")
	}), journal: page.Entries, inbox: inbox}
}

func (r *observerChannel) assertEffects(got observerEffects, states []db.TaskState, times []time.Time) {
	t := r.t
	t.Helper()
	if len(got.events) != len(states) || len(got.journal) != len(states) {
		t.Fatalf("observation effects %+v, want transitions %v", got, states)
	}
	inbox := got.inbox
	from := db.StateRunning
	for i, state := range states {
		event, journal := got.events[i], got.journal[i]
		if event.FromState == nil || *event.FromState != from || event.ToState != state || !event.At.Equal(times[i]) {
			t.Fatalf("event %+v, want %s to %s at %v", event, from, state, times[i])
		}
		if journal.EventType != "task_state_changed" || journal.Text != fmt.Sprintf("execution observed %s (%s -> %s)", state, from, state) ||
			journal.SourceSession == nil || *journal.SourceSession != r.adapter.baseline.Session || !journal.CreatedAt.Equal(times[i]) {
			t.Fatalf("journal entry %+v, want %s to %s at %v", journal, from, state, times[i])
		}
		// A task's finished run records no inbox row; needs input does.
		if state == db.StateNeedsInput {
			if len(inbox) == 0 || inbox[0].Event != db.NotificationEvent(state) || inbox[0].TaskID == nil || *inbox[0].TaskID != r.task.ID ||
				!inbox[0].CreatedAt.Equal(times[i]) {
				t.Fatalf("inbox %+v, want %s for task %d at %v", inbox, state, r.task.ID, times[i])
			}
			inbox = inbox[1:]
		}
		from = state
	}
	if len(inbox) != 0 {
		t.Fatalf("unexpected inbox entries %+v", inbox)
	}
}

func readObserverJSON(t *testing.T, path string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
}

func waitObserver(t *testing.T, what string, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func receiveObserver[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}
