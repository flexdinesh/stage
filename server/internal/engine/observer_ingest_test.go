package engine_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
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
	if _, err := store.SetNotifyEvents(t.Context(), []db.NotificationEvent{db.EventNeedsInput, db.EventFailed, db.EventDone}); err != nil {
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
		if c.Actor.Kind != "system" || c.Actor.UserID != 0 || c.SourceMachine == nil || *c.SourceMachine != remoteMachine ||
			c.Cause == nil || c.Cause.Session == nil || *c.Cause.Session != ref {
			t.Fatalf("observer attribution = %+v", c.Attribution)
		}
		after, err := n.RecordObservation(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		if c.Task.To == db.StateNeedsInput && fail {
			return nil, abort
		}
		if after == nil {
			return nil, nil
		}
		return func() {
			published++
			after()
		}, nil
	}
	if err := svc.Observe.BindWorkerProducer(ctx, remoteMachine, observe.WatchAck{Producer: "producer"}); err != nil {
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
	events, err := store.TaskEvents(ctx, task.ID)
	if err != nil || len(events) == 0 || events[len(events)-1].ToState != db.StateRunning {
		t.Fatalf("uncommitted actor event: %+v, %v", events, err)
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
	// A finished run records no task notification; a failed one does.
	done := sample(ref, db.StateFailed, at.Add(3*time.Second))
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
	if err != nil || got.State != db.StateFailed || got.FinishedAt == nil || !got.FinishedAt.Equal(done.UpdatedAt) {
		t.Fatalf("bridge state = %s finished %v, %v", got.State, got.FinishedAt, err)
	}
	rows, err := store.ListNotifications(ctx, 10, nil)
	if err != nil || len(rows) != 2 || published != 2 || !rows[0].CreatedAt.Equal(done.UpdatedAt) || !rows[1].CreatedAt.Equal(waiting.UpdatedAt) {
		t.Fatalf("notifications = %+v, published %d, err %v", rows, published, err)
	}
}

func TestObservationBatchOrdersEqualHarnessUpdatesByCapture(t *testing.T) {
	ctx := t.Context()
	store, task, ref := observationTask(t, remotePlacement)
	svc := engine.NewWithAdapters(store, documentHarnesses, nil, nil)
	const producer = "producer"
	if err := svc.Observe.BindWorkerProducer(ctx, remoteMachine, observe.WatchAck{Producer: producer}); err != nil {
		t.Fatal(err)
	}
	updated := time.Now().UTC().Add(-time.Minute)
	earlier := updated.Add(time.Second)
	later := earlier.Add(time.Nanosecond)
	waiting := sample(ref, db.StateNeedsInput, updated)
	done := sample(ref, db.StateDone, updated)
	commit := func(cursor int64, at time.Time, o execution.Observation) {
		t.Helper()
		batch := observe.Batch{Producer: producer, Cursor: cursor, Records: []observe.Record{
			{Cursor: cursor, ConnectionID: ref.ConnectionID, ExternalSessionID: ref.ExternalSessionID, ObservedAt: at, Observation: &o},
		}}
		if ack, err := svc.Observe.CommitBatch(ctx, remoteMachine, batch); err != nil || ack.Cursor != cursor {
			t.Fatalf("commit at %v: ack %+v, err %v", at, ack, err)
		}
	}
	commit(1, earlier, waiting)
	svc = engine.NewWithAdapters(store, documentHarnesses, nil, nil)
	commit(2, later, done)
	commit(3, earlier, waiting)
	got, err := store.GetTask(ctx, task.ID)
	if err != nil || got.State != db.StateDone {
		t.Fatalf("task after equal harness updates = %s, %v", got.State, err)
	}
	snapshot, err := store.ObservationSnapshot(ctx, ref)
	if err != nil || snapshot.State != string(db.StateDone) || !snapshot.CapturedAt.Equal(later) || !snapshot.UpdatedAt.Equal(updated) {
		t.Fatalf("durable snapshot = %+v, %v", snapshot, err)
	}
}

type localObservationAdapter struct {
	execution.Adapter
	sample    atomic.Pointer[execution.Observation]
	attention []execution.SessionRef
}

func (a *localObservationAdapter) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: true}}
}
func (a *localObservationAdapter) Attention(context.Context) ([]execution.SessionRef, error) {
	return a.attention, nil
}
func (a *localObservationAdapter) Observe(context.Context, execution.SessionRef) (execution.Observation, error) {
	return *a.sample.Load(), nil
}

func ageObservationTask(t *testing.T, pool *sqlite.DB, store *db.Store, id int64, inactive execution.Observation) {
	t.Helper()
	ctx := t.Context()
	if _, err := store.TransitionTask(ctx, db.Transition{TaskID: id, To: db.TaskState(inactive.State)}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(ctx, inactive); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, "UPDATE tasks SET updated_at=$1 WHERE id=$2", inactive.UpdatedAt.Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.PollableTasks(ctx, true)
	if err != nil || slices.ContainsFunc(tasks, func(task db.Task) bool { return task.ID == id }) {
		t.Fatalf("inactive task %d must be outside the poll watch: %+v, %v", id, tasks, err)
	}
}

func TestInitialAttentionRearmsInactiveTask(t *testing.T) {
	for _, state := range []db.TaskState{db.StateFailed, db.StateInterrupted, db.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			ctx := t.Context()
			store, task, ref := observationTask(t, placement{name: "local"})
			at := time.Now().UTC().Truncate(time.Second)
			ageObservationTask(t, testDB.Pool, store, task.ID, sample(ref, state, at.Add(-time.Hour)))

			adapter := &localObservationAdapter{attention: []execution.SessionRef{ref}}
			waiting := sample(ref, db.StateNeedsInput, at)
			adapter.sample.Store(&waiting)
			svc := engine.NewWithAdapters(store, documentHarnesses, map[int64]execution.Adapter{ref.ConnectionID: adapter}, nil)
			n := notify.New(store, "https://stage.invalid", nil)
			svc.Observe.RecordNotification = n.RecordObservation
			if err := svc.Observe.WatchAttention(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetTask(ctx, task.ID)
			if err != nil || got.State != db.StateNeedsInput {
				t.Fatalf("first active attention sample: task state %s, want needs_input: %v", got.State, err)
			}
			rows, err := store.ListNotifications(ctx, 10, nil)
			if err != nil || len(rows) != 1 || rows[0].Event != db.EventNeedsInput || rows[0].TaskID == nil || *rows[0].TaskID != task.ID {
				t.Fatalf("notifications = %+v, %v", rows, err)
			}
		})
	}
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
	// Needs input notifies; the finished run does not.
	notifications, err := store.ListNotifications(ctx, 10, nil)
	if err != nil || len(notifications) != 1 || notifications[0].Event != db.EventNeedsInput {
		t.Fatalf("notifications = %+v, %v", notifications, err)
	}
}
