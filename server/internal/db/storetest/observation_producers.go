package storetest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func init() {
	register("ObservationProducerCursor", caseObservationProducerCursor)
	register("ObservationWriteTransaction", caseObservationWriteTransaction)
}

func caseObservationProducerCursor(t *testing.T, open Factory) {
	ctx := t.Context()
	engine := open(t)
	s, restarted := db.NewStore(engine), db.NewStore(engine)
	machine := pairMachine(t, s, ctx, "worker").MachineID
	other := pairMachine(t, s, ctx, "other").MachineID
	const producer = "epoch-one"
	bind := func(id int64, epoch string) {
		t.Helper()
		if err := s.BindObservationProducer(ctx, id, epoch); err != nil {
			t.Fatal(err)
		}
	}
	unexpected := errors.New("callback should not run")
	neverApply := func(*db.Store) (db.ObservationDisposition, error) {
		return db.ObservationDisposition{}, unexpected
	}
	if _, err := s.CommitObservationRecord(ctx, machine, producer, 1, neverApply); !errors.Is(err, db.ErrObservationProducer) {
		t.Fatalf("unbound producer: %v", err)
	}
	bind(machine, producer)
	for _, cursor := range []int64{0, 2} {
		if _, err := s.CommitObservationRecord(ctx, machine, producer, cursor, neverApply); !errors.Is(err, db.ErrObservationCursor) {
			t.Fatalf("cursor %d: %v", cursor, err)
		}
	}
	if _, err := s.CommitObservationRecord(ctx, other, producer, 1, neverApply); !errors.Is(err, db.ErrObservationProducer) {
		t.Fatalf("producer on another machine: %v", err)
	}

	// Racing deliveries acknowledge the same cursor. Only one applies it.
	stores := []*db.Store{s, restarted}
	results := make([]db.ObservationCommit, len(stores))
	for _, err := range race(len(stores), func(i int) error {
		var err error
		results[i], err = stores[i].CommitObservationRecord(ctx, machine, producer, 1, func(*db.Store) (db.ObservationDisposition, error) {
			return db.ObservationDisposition{Code: "invalid_record"}, nil
		})
		return err
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].Cursor != 1 || results[1].Cursor != 1 || results[0].Applied == results[1].Applied {
		t.Fatalf("racing cursor results: %+v", results)
	}
	bind(machine, producer)
	seen := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	got, err := restarted.CommitObservationRecord(ctx, machine, producer, 2, func(*db.Store) (db.ObservationDisposition, error) {
		return db.ObservationDisposition{Code: "gap", Lost: ^uint64(0), From: seen, To: seen.Add(time.Second)}, nil
	})
	if err != nil || got != (db.ObservationCommit{Cursor: 2, Applied: true}) {
		t.Fatalf("gap disposition: %+v %v", got, err)
	}
	got, err = restarted.CommitObservationRecord(ctx, machine, producer, 1, neverApply)
	if err != nil || got != (db.ObservationCommit{Cursor: 2}) {
		t.Fatalf("replay: %+v %v", got, err)
	}

	bind(other, producer)
	bind(machine, "epoch-two")
	if err := restarted.BindObservationProducer(ctx, machine, producer); !errors.Is(err, db.ErrObservationProducer) {
		t.Fatalf("retired producer rebound: %v", err)
	}
	if _, err := restarted.CommitObservationRecord(ctx, machine, producer, 2, neverApply); !errors.Is(err, db.ErrObservationProducer) {
		t.Fatalf("retired producer replay: %v", err)
	}
	if _, err := restarted.CommitObservationRecord(ctx, machine, "epoch-two", 2, neverApply); !errors.Is(err, db.ErrObservationCursor) {
		t.Fatalf("new epoch inherited cursor: %v", err)
	}
	got, err = restarted.CommitObservationRecord(ctx, other, producer, 1, func(*db.Store) (db.ObservationDisposition, error) {
		return db.ObservationDisposition{}, nil
	})
	if err != nil || got != (db.ObservationCommit{Cursor: 1, Applied: true}) {
		t.Fatalf("same producer on another machine: %+v %v", got, err)
	}
	if err := s.RemoveMachine(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CommitObservationRecord(ctx, other, producer, 1, neverApply); !errors.Is(err, db.ErrObservationProducer) {
		t.Fatalf("removed producer replay: %v", err)
	}
}

func caseObservationWriteTransaction(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "obs-project")
	task := mustTask(t, s, ctx, p.ID, "Observed task", nil)
	ref := session("obs-session")
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, Session: &ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoComplete(ctx, ref.ConnectionID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.BindObservationProducer(ctx, db.LocalMachine, "producer"); err != nil {
		t.Fatal(err)
	}

	seen := time.Date(2026, 11, 1, 12, 0, 0, 123456000, time.FixedZone("worker", 3600))
	o := execution.Observation{
		Session: ref, Project: "obs-project", State: "done", CreatedAt: seen.Add(-time.Minute), UpdatedAt: seen,
		ExecutionID: "obs-execution", Closed: &execution.Closure{Signal: execution.ClosedSettled},
	}
	const detail = "harness reported completion"
	apply := func(tx *db.Store) error {
		if err := tx.LockObservationConnection(ctx, ref.ConnectionID); err != nil {
			return err
		}
		if _, err := tx.GetHarnessConnection(ctx, ref.ConnectionID); err != nil {
			return err
		}
		tracked, err := tx.TaskBySession(ctx, ref)
		if err != nil {
			return err
		}
		if err := tx.PutObservation(ctx, o); err != nil {
			return err
		}
		if saved, err := tx.Observation(ctx, ref); err != nil || saved.State != o.State {
			return fmt.Errorf("scoped observation %+v: %v", saved, err)
		}
		if _, err := tx.TransitionTask(ctx, db.Transition{TaskID: tracked.ID, To: db.StateDone, Detail: detail, ObservedAt: &seen}); err != nil {
			return err
		}
		inferred, err := tx.InferCompletion(ctx, tracked.ID, o, harness.Closure{
			Signal: execution.ClosedSettled, Provenance: "harness_settled", Event: "Task completed from harness closure",
		})
		if err != nil || !inferred {
			return fmt.Errorf("scoped inference %t: %v", inferred, err)
		}
		_, err = tx.InsertNotification(ctx, db.NewNotification{
			Event: db.StateDone, TaskID: &tracked.ID, Session: &ref, Title: tracked.Title, ObservedAt: &seen,
		})
		return err
	}
	rollback := errors.New("abort observation")
	failAfterApplying := func(tx *db.Store) error {
		if err := apply(tx); err != nil {
			return err
		}
		return rollback
	}
	assertRolledBack := func() {
		t.Helper()
		if _, err := s.Observation(ctx, ref); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("rolled back observation: %v", err)
		}
		got, err := s.GetTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.FinishedAt != nil || got.AcceptanceID != nil {
			t.Fatalf("rolled back task: %+v %v", got, err)
		}
		ns, err := s.ListNotifications(ctx, 5, nil)
		if err != nil || len(ns) != 0 {
			t.Fatalf("rolled back notifications: %+v %v", ns, err)
		}
	}
	if err := s.WriteObservation(ctx, failAfterApplying); !errors.Is(err, rollback) {
		t.Fatalf("local observation rollback: %v", err)
	}
	assertRolledBack()
	got, err := s.CommitObservationRecord(ctx, db.LocalMachine, "producer", 1, func(tx *db.Store) (db.ObservationDisposition, error) {
		return db.ObservationDisposition{}, failAfterApplying(tx)
	})
	if got.Applied || !errors.Is(err, rollback) {
		t.Fatalf("producer observation rollback: %+v %v", got, err)
	}
	assertRolledBack()

	got, err = s.CommitObservationRecord(ctx, db.LocalMachine, "producer", 1, func(tx *db.Store) (db.ObservationDisposition, error) {
		return db.ObservationDisposition{}, apply(tx)
	})
	if err != nil || got != (db.ObservationCommit{Cursor: 1, Applied: true}) {
		t.Fatalf("retry observation: %+v %v", got, err)
	}
	saved, err := s.GetTask(ctx, task.ID)
	if err != nil || saved.State != db.StateDone || saved.AcceptanceID == nil || saved.FinishedAt == nil || !saved.FinishedAt.Equal(seen) {
		t.Fatalf("observed finish: %+v %v", saved, err)
	}
	events, err := s.TaskEvents(ctx, task.ID)
	if err != nil || len(events) != 3 {
		t.Fatalf("task events: %+v %v", events, err)
	}
	event := slices.IndexFunc(events, func(e db.TaskEvent) bool { return e.ToState == db.StateDone })
	if event < 0 || events[event].Detail != detail || !events[event].At.Equal(seen) {
		t.Fatalf("observed task event: %+v", events)
	}
	journal, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &task.ID}, db.JournalQuery{Kind: "events"})
	if err != nil || len(journal.Entries) != 4 {
		t.Fatalf("journal events: %+v %v", journal, err)
	}
	entry := slices.IndexFunc(journal.Entries, func(e db.JournalEntry) bool { return strings.HasPrefix(e.Text, detail) })
	if entry < 0 || !journal.Entries[entry].CreatedAt.Equal(seen) {
		t.Fatalf("observed journal event: %+v", journal.Entries)
	}
	ns, err := s.ListNotifications(ctx, 5, nil)
	if err != nil || len(ns) != 1 || !ns[0].CreatedAt.Equal(seen) {
		t.Fatalf("observed notification: %+v %v", ns, err)
	}
}
