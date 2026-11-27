package engine_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

func observationTask(t *testing.T, placement placement) (*db.Store, db.Task, execution.SessionRef) {
	t.Helper()
	store, task := contractTask(t, placement)
	ref := execution.SessionRef{ConnectionID: task.ConnectionID, ExternalSessionID: "observed-session"}
	if _, err := store.TransitionTask(t.Context(), db.Transition{TaskID: task.ID, To: db.StateRunning, Session: &ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetNotifyEvents(t.Context(), []db.TaskState{db.StateNeedsInput, db.StateDone}); err != nil {
		t.Fatal(err)
	}
	return store, task, ref
}

func sample(ref execution.SessionRef, state db.TaskState, at time.Time) execution.Observation {
	return execution.Observation{
		Session: ref, Project: "collection-z", Title: "Document", State: string(state),
		Active: state.Active(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at, ExecutionID: "run-1",
	}
}

func TestObservationBatchRollbackReplayAndBridgeOrdering(t *testing.T) {
	ctx := t.Context()
	store, task, ref := observationTask(t, remotePlacement)
	svc := engine.NewWithAdapters(store, documentHarnesses, nil, nil)
	n := notify.New(store, "https://stage.invalid", nil)
	abort := errors.New("abort before committing notification")
	fail := true
	var published int
	svc.Observe.RecordNotification = func(ctx context.Context, tx *db.Store, c workevent.ObservationChange) (func(), error) {
		after, err := n.RecordObservation(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		if c.Task.To == db.StateNeedsInput && fail {
			return nil, abort
		}
		return func() {
			published++
			after()
		}, nil
	}
	if err := svc.Observe.BindWorkerProducer(ctx, remoteMachine, "producer"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	running, waiting := sample(ref, db.StateRunning, at), sample(ref, db.StateNeedsInput, at.Add(time.Second))
	batch := observe.Batch{Producer: "producer", Cursor: 2, Records: []observe.Record{
		{Cursor: 1, ConnectionID: ref.ConnectionID, ExternalSessionID: ref.ExternalSessionID, ObservedAt: at, Observation: &running},
		{Cursor: 2, ConnectionID: ref.ConnectionID, ExternalSessionID: ref.ExternalSessionID, ObservedAt: waiting.UpdatedAt, Observation: &waiting},
	}}
	if ack, err := svc.Observe.CommitBatch(ctx, remoteMachine, batch); !errors.Is(err, abort) || ack.Cursor != 1 {
		t.Fatalf("failed commit ack = %+v, %v", ack, err)
	}
	if got, err := store.GetTask(ctx, task.ID); err != nil || got.State != db.StateRunning {
		t.Fatalf("uncommitted state = %s, %v", got.State, err)
	}
	if got, err := store.ListNotifications(ctx, 10, nil); err != nil || len(got) != 0 || published != 0 {
		t.Fatalf("uncommitted notifications = %+v, published %d, err %v", got, published, err)
	}
	fail = false
	for range 2 {
		if ack, err := svc.Observe.CommitBatch(ctx, remoteMachine, batch); err != nil || ack.Cursor != 2 {
			t.Fatalf("retry ack = %+v, %v", ack, err)
		}
	}
	done := sample(ref, db.StateDone, at.Add(3*time.Second))
	if _, err := svc.Observe.Ingest(ctx, done); err != nil {
		t.Fatal(err)
	}
	older := sample(ref, db.StateRunning, at.Add(2*time.Second))
	batch.Cursor = 4
	batch.Records = []observe.Record{
		{Cursor: 3, ConnectionID: ref.ConnectionID, ObservedAt: at}, // invalid, permanently consumed
		{Cursor: 4, ConnectionID: ref.ConnectionID, ExternalSessionID: ref.ExternalSessionID, ObservedAt: older.UpdatedAt, Observation: &older},
	}
	if ack, err := svc.Observe.CommitBatch(ctx, remoteMachine, batch); err != nil || ack.Cursor != 4 {
		t.Fatalf("permanent rejection ack = %+v, %v", ack, err)
	}
	foreign := store.ActiveID(db.LocalMachine, "t3")
	waitAgain := sample(ref, db.StateNeedsInput, at.Add(4*time.Second))
	batch.Cursor = 6
	batch.Records = []observe.Record{
		{Cursor: 5, ConnectionID: ref.ConnectionID, ExternalSessionID: ref.ExternalSessionID, ObservedAt: waitAgain.UpdatedAt, Observation: &waitAgain},
		{Cursor: 6, ConnectionID: foreign, ObservedAt: at},
	}
	if ack, err := svc.Observe.CommitBatch(ctx, remoteMachine, batch); !errors.Is(err, observe.ErrWrongMachine) || ack.Cursor != 0 {
		t.Fatalf("cross-machine batch ack = %+v, %v", ack, err)
	}
	got, err := store.GetTask(ctx, task.ID)
	if err != nil || got.State != db.StateDone || got.FinishedAt == nil || !got.FinishedAt.Equal(done.UpdatedAt) {
		t.Fatalf("bridge state = %s finished %v, %v", got.State, got.FinishedAt, err)
	}
	rows, err := store.ListNotifications(ctx, 10, nil)
	if err != nil || len(rows) != 2 || published != 2 || !rows[0].CreatedAt.Equal(done.UpdatedAt) || !rows[1].CreatedAt.Equal(waiting.UpdatedAt) {
		t.Fatalf("notifications = %+v, published %d, err %v", rows, published, err)
	}
}

type localObservationAdapter struct {
	execution.Adapter
	sample atomic.Pointer[execution.Observation]
}

func (a *localObservationAdapter) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: true}}
}
func (a *localObservationAdapter) Attention(context.Context) ([]execution.SessionRef, error) {
	return nil, nil
}
func (a *localObservationAdapter) Observe(context.Context, execution.SessionRef) (execution.Observation, error) {
	return *a.sample.Load(), nil
}

func TestObservationRunnerLocalUsesTransactionalIngest(t *testing.T) {
	store, task, ref := observationTask(t, placement{name: "local"})
	adapter := &localObservationAdapter{}
	running := sample(ref, db.StateRunning, time.Now().UTC())
	adapter.sample.Store(&running)
	svc := engine.NewWithAdapters(store, documentHarnesses, map[int64]execution.Adapter{ref.ConnectionID: adapter}, nil)
	n := notify.New(store, "https://stage.invalid", nil)
	svc.Observe.RecordNotification = n.RecordObservation
	svc.Observe.PollEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Observe.Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	waitFor(t, "initial observation", func() bool {
		_, err := store.Observation(ctx, ref)
		return err == nil
	})
	for _, state := range []db.TaskState{db.StateNeedsInput, db.StateRunning, db.StateDone} {
		at := time.Now().UTC()
		o := sample(ref, state, at)
		adapter.sample.Store(&o)
		waitFor(t, string(state), func() bool {
			got, err := store.GetTask(ctx, task.ID)
			return err == nil && got.State == state
		})
	}
	events, err := store.TaskEvents(ctx, task.ID)
	if n := len(events); err != nil || n != 5 || events[n-3].ToState != db.StateNeedsInput || events[n-2].ToState != db.StateRunning || events[n-1].ToState != db.StateDone {
		t.Fatalf("task events = %+v, %v", events, err)
	}
	notifications, err := store.ListNotifications(ctx, 10, nil)
	if err != nil || len(notifications) != 2 {
		t.Fatalf("notifications = %+v, %v", notifications, err)
	}
}
