package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/observe/outbox"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

type samplingAdapter struct {
	execution.Adapter
	reads chan execution.SessionRef
}

func (a *samplingAdapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	select {
	case a.reads <- ref:
	case <-ctx.Done():
		return execution.Observation{}, ctx.Err()
	}
	return execution.Observation{Session: ref, State: "working"}, nil
}

func TestObservationRestoresAndReconcilesWatch(t *testing.T) {
	w := &runner{dir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	local := machine.NewLocal(2, harness.Profile{}, nil)
	first := &samplingAdapter{reads: make(chan execution.SessionRef, 1)}
	local.Install(4, 4, first)
	o := newObservationWorker(w, local, "join")
	watch := observe.Watch{Epoch: "main", Generation: 4, Connections: []observe.WatchConnection{{
		ID: 4, Sessions: []observe.WatchSession{{ID: "offline"}},
	}}}
	if err := o.saveWatch(watch); err != nil {
		t.Fatal(err)
	}
	o.restore(true, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.work.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
		o.pause()
	}()
	read := func(a *samplingAdapter, want string) {
		t.Helper()
		select {
		case ref := <-a.reads:
			if ref != (execution.SessionRef{ConnectionID: 4, ExternalSessionID: want}) {
				t.Fatalf("read %+v, want connection 4 session %q", ref, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("adapter did not read %q", want)
		}
	}
	read(first, "offline")

	o.reconcile()
	o.detach(&channel{ctx: ctx}, true)
	read(first, "offline") // the attachment ended inside setup

	o.reconcile()
	second := &samplingAdapter{reads: make(chan execution.SessionRef, 1)}
	local.Install(4, 4, second)
	c := &channel{ctx: ctx, serverKinds: []string{observe.KindBatch}}
	o.attach(c)
	accept := func(watch observe.Watch) (watchReceipt, error) {
		t.Helper()
		body, err := json.Marshal(watch)
		if err != nil {
			t.Fatal(err)
		}
		return o.accept(c, ctx, workerchan.Envelope{Body: body})
	}
	watch.Generation--
	if _, err := accept(watch); err == nil {
		t.Fatal("reconciliation accepted a stale watch generation")
	}
	watch.Generation += 2
	watch.Connections[0].Sessions[0].ID = "authoritative"
	receipt, err := accept(watch)
	if err != nil || receipt.ack.Producer != o.queue.Producer() {
		t.Fatalf("watch receipt %+v, err %v", receipt, err)
	}
	saved, err := o.readWatch()
	if err != nil || !reflect.DeepEqual(saved, watch) {
		t.Fatalf("watch on disk %+v, err %v", saved, err)
	}
	o.acknowledged(c, receipt)
	if got, _ := o.deliveryTarget(); got != nil {
		t.Fatal("watch result alone enabled delivery before ready")
	}
	o.ready(c)
	if got, _ := o.deliveryTarget(); got != c {
		t.Fatal("ready attachment with an acknowledged watch cannot send")
	}
	read(second, "authoritative")
}

func TestObservationReplaysUnacknowledgedBatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), outboxDir)
	q, err := outbox.Open(dir, "join", outbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	record := observe.Record{
		ConnectionID: 4, ExternalSessionID: "session", ObservedAt: time.Now().UTC(), Unavailable: true,
	}
	if err := q.Enqueue(record); err != nil {
		t.Fatal(err)
	}
	requests := make(chan observe.Batch, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env workerchan.Envelope
		var batch observe.Batch
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(env.Body, &batch); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- batch
		if r.Header.Get(workerchan.SessionHeader) == "before" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ack, err := json.Marshal(observe.BatchAck{Cursor: batch.Cursor})
		if err != nil {
			t.Error(err)
			return
		}
		if err := json.NewEncoder(w).Encode(workerchan.Envelope{ID: env.ID, Kind: workerchan.KindResult, Body: ack}); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	w := &runner{api: newAPI(srv.URL, "token", srv.Client())}
	c := &channel{w: w, ctx: t.Context(), id: "before", serverKinds: []string{observe.KindBatch}}
	if sent, err := sendObservations(c, q); err == nil || sent {
		t.Fatalf("failed upload returned sent=%v, err=%v", sent, err)
	}
	q, err = outbox.Open(dir, "join", outbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	c.id = "after"
	if sent, err := sendObservations(c, q); err != nil || !sent {
		t.Fatalf("replay returned sent=%v, err=%v", sent, err)
	}
	first, second := <-requests, <-requests
	if !reflect.DeepEqual(first, second) || len(second.Records) != 1 {
		t.Fatalf("replay changed batch: first %+v, second %+v", first, second)
	}
	q, err = outbox.Open(dir, "join", outbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if batch, err := q.Seal(); err != nil || len(batch.Records) != 0 {
		t.Fatalf("acknowledged records survived reopen: %+v, err %v", batch, err)
	}
}

func TestObservationCorruptionKeepsIndependentState(t *testing.T) {
	w := &runner{dir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	local := machine.NewLocal(2, harness.Profile{}, nil)
	o := newObservationWorker(w, local, "join")
	o.restore(false, nil, nil)
	if got := w.state.Observation; got != observationWaitingSetup || o.queue == nil {
		t.Fatalf("missing setup reported %q", got)
	}
	producer := o.queue.Producer()
	if err := os.WriteFile(filepath.Join(w.dir, watchFile), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	o.restore(true, nil, nil)
	if got := w.state.Observation; got != observationRecovery || o.queue.Producer() != producer {
		t.Fatalf("corrupt watch reported %q and changed producer to %q", got, o.queue.Producer())
	}
	o.reconcile()
	o.detach(&channel{ctx: t.Context()}, true)
	if got := w.state.Observation; got != observationRecovery {
		t.Fatalf("failed attachment resumed a corrupt watch: %q", got)
	}
	if err := os.WriteFile(filepath.Join(w.dir, outboxDir, "state.json"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	o = newObservationWorker(w, local, "join")
	o.restore(true, nil, nil)
	if got := w.state.Observation; got != observationRecovery || o.queue == nil || o.queue.Producer() == producer {
		t.Fatalf("corrupt outbox reported %q and failed to create a fresh producer", got)
	}
}

func TestObservationPersistsLossUntilResampleWatch(t *testing.T) {
	t.Run("explicit resample", func(t *testing.T) { testObservationResample(t, false) })
	t.Run("new server epoch", func(t *testing.T) { testObservationResample(t, true) })
}

func testObservationResample(t *testing.T, restart bool) {
	t.Helper()
	w := &runner{dir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	local := machine.NewLocal(2, harness.Profile{}, nil)
	o := newObservationWorker(w, local, "join")
	watch := observe.Watch{Epoch: "main", Generation: 1}
	if err := o.saveWatch(watch); err != nil {
		t.Fatal(err)
	}
	q, err := outbox.Open(filepath.Join(w.dir, outboxDir), "join", outbox.Limits{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	o.queue = q
	o.resume()
	record := observe.Record{
		ConnectionID: 4, ExternalSessionID: strings.Repeat("x", 1024), ObservedAt: time.Now().UTC(), Unavailable: true,
	}
	if err := o.enqueue(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	requireState := func(want string) *State {
		t.Helper()
		state, err := ReadState(w.dir)
		if err != nil || state == nil || state.Observation != want || state.ObservationLost != 1 {
			t.Fatalf("expected %q with one durable loss, got %+v, err %v", want, state, err)
		}
		return state
	}
	previous := requireState(observationDegraded)
	o = newObservationWorker(w, local, "join")
	o.restore(true, nil, previous)
	requireState(observationDegraded)

	c := &channel{ctx: t.Context(), serverKinds: []string{observe.KindBatch}}
	o.reconcile()
	o.attach(c)
	o.ready(c)
	watch.Generation++
	if restart {
		watch.Epoch = "restarted-main"
		watch.Generation = 1
	} else {
		watch.Resample = true
	}
	body, err := json.Marshal(watch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.accept(c, t.Context(), workerchan.Envelope{Body: body}); err != nil {
		t.Fatal(err)
	}
	previous = requireState(observationSampling)
	o = newObservationWorker(w, local, "join")
	o.restore(true, nil, previous)
	requireState(observationSampling)
}
