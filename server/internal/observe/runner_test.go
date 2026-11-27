package observe

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

type runnerAdapter struct {
	execution.Adapter
	observe       func(context.Context, execution.SessionRef) (execution.Observation, error)
	attention     []execution.SessionRef
	attentionRefs func() []execution.SessionRef
}

func (a runnerAdapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if a.observe != nil {
		return a.observe(ctx, ref)
	}
	return testObservation(ref), nil
}

func (a runnerAdapter) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: true}}
}

func (a runnerAdapter) Attention(context.Context) ([]execution.SessionRef, error) {
	if a.attentionRefs != nil {
		return a.attentionRefs(), nil
	}
	return a.attention, nil
}

type runnerEventAdapter struct {
	runnerAdapter
	events func(context.Context, execution.Sink) error
}

func (a runnerEventAdapter) Events(ctx context.Context, sink execution.Sink) error {
	return a.events(ctx, sink)
}

func testObservation(ref execution.SessionRef) execution.Observation {
	return execution.Observation{Session: ref, Project: "project", State: "running", Active: true}
}

func recordedRunner(adapters map[int64]execution.Adapter) (*Runner, chan Record) {
	records := make(chan Record, 128)
	runner := NewRunner(func(id int64) (execution.Adapter, error) {
		if adapter := adapters[id]; adapter != nil {
			return adapter, nil
		}
		return nil, execution.ErrUnavailable
	}, func(_ context.Context, r Record) error {
		records <- r
		return nil
	}, RunnerOptions{})
	return runner, records
}

// runnerRecords is called after the bubble waits for the runner to block.
func runnerRecords(records chan Record) []Record {
	var got []Record
	for {
		select {
		case r := <-records:
			got = append(got, r)
		default:
			return got
		}
	}
}

func TestRunnerFollowsDiscoveredActiveSessionAfterItLeavesAttention(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "discovered"}
		var finished atomic.Bool
		a := runnerAdapter{attentionRefs: func() []execution.SessionRef {
			if finished.Load() {
				return nil
			}
			return []execution.SessionRef{ref}
		}}
		a.observe = func(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
			o := testObservation(ref)
			o.State = "needs_input"
			if finished.Load() {
				o.State, o.Active = "done", false
			}
			return o, nil
		}
		r, records := recordedRunner(map[int64]execution.Adapter{1: a})
		r.Replace(Watch{Epoch: "epoch", Generation: 1, Connections: []WatchConnection{{ID: 1, Attention: true}}})
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go r.Run(ctx)
		synctest.Wait()
		runnerRecords(records)
		finished.Store(true)
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if got := runnerRecords(records); !slices.ContainsFunc(got, func(r Record) bool {
			return r.Attention && !r.Quiet && r.Observation != nil && r.Observation.State == "done"
		}) {
			t.Fatalf("discovered outcome missing: %+v", got)
		}
	})
}

func TestRunnerReplaceFencesBlockedReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		a := runnerAdapter{observe: func(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
			if ref.ExternalSessionID == "old" {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release // A return after cancellation must not reach the sink.
			}
			return testObservation(ref), nil
		}}
		r, records := recordedRunner(map[int64]execution.Adapter{1: a})
		watch := Watch{Epoch: "epoch", Generation: 4, Connections: []WatchConnection{{ID: 1, Sessions: []WatchSession{{ID: "old"}}}}}
		if !r.Replace(watch) || !r.Replace(watch) {
			t.Fatal("initial watch or replay rejected")
		}
		watch.Connections[0].Sessions[0].ID = "new"
		if r.Replace(watch) {
			t.Fatal("conflicting watch at the same generation accepted")
		}
		watch.Generation--
		if r.Replace(watch) {
			t.Fatal("older watch accepted")
		}
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go r.Run(ctx)
		synctest.Wait()
		select {
		case <-started:
		default:
			t.Fatal("initial read did not start")
		}
		watch.Generation = 5
		if !r.Replace(watch) {
			t.Fatal("newer watch rejected")
		}
		synctest.Wait()
		select {
		case <-canceled:
		default:
			t.Fatal("replacement did not cancel the blocked read")
		}
		got := runnerRecords(records)
		if !slices.ContainsFunc(got, func(r Record) bool { return r.ExternalSessionID == "new" && r.Observation != nil }) {
			t.Fatalf("new read did not finish while the old read was blocked: %+v", got)
		}
		unblock()
		synctest.Wait()
		if got := runnerRecords(records); len(got) != 0 {
			t.Fatalf("canceled read emitted %+v", got)
		}
	})
}

func TestRunnerOnlyRestartsChangedConnections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type streamCall struct {
			ctx  context.Context
			sink execution.Sink
		}
		streamAdapter := func(calls chan streamCall) execution.Adapter {
			return runnerEventAdapter{events: func(ctx context.Context, sink execution.Sink) error {
				calls <- streamCall{ctx, sink}
				<-ctx.Done()
				return ctx.Err()
			}}
		}
		one, two := make(chan streamCall, 10), make(chan streamCall, 10)
		r, records := recordedRunner(map[int64]execution.Adapter{1: streamAdapter(one), 2: streamAdapter(two)})
		watch := Watch{Epoch: "epoch", Generation: 2, Connections: []WatchConnection{{ID: 1}, {ID: 2}}}
		if !r.Replace(watch) {
			t.Fatal("initial watch rejected")
		}
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go r.Run(ctx)
		synctest.Wait()
		oldOne, oldTwo := <-one, <-two

		watch.Generation++
		if !r.Replace(watch) {
			t.Fatal("new generation with unchanged entries rejected")
		}
		synctest.Wait()
		if oldOne.ctx.Err() != nil || oldTwo.ctx.Err() != nil || len(one) != 0 || len(two) != 0 {
			t.Fatal("a new generation with unchanged work should preserve both streams")
		}

		watch.Generation++
		watch.Connections[0].Sessions = []WatchSession{{ID: "added"}}
		if !r.Replace(watch) {
			t.Fatal("changed watch rejected")
		}
		synctest.Wait()
		newOne := <-one
		if oldOne.ctx.Err() == nil || oldTwo.ctx.Err() != nil || len(two) != 0 {
			t.Fatal("only the stream for the changed watch entry should restart")
		}

		r.Reset(1)
		synctest.Wait()
		resetOne := <-one
		if newOne.ctx.Err() == nil || oldTwo.ctx.Err() != nil || len(two) != 0 {
			t.Fatal("reset should restart the selected stream only")
		}
		ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "unlisted"}
		if err := resetOne.sink(t.Context(), testObservation(ref)); err != nil {
			t.Fatalf("active stream callback failed: %v", err)
		}
		if err := oldOne.sink(t.Context(), testObservation(ref)); !errors.Is(err, context.Canceled) {
			t.Fatalf("callback from a replaced stream returned %v", err)
		}
		if count := len(slices.DeleteFunc(runnerRecords(records), func(r Record) bool { return r.ExternalSessionID != "unlisted" })); count != 1 {
			t.Fatalf("got %d emitted samples, want 1 from the active stream", count)
		}

		watch.Epoch = "another-epoch"
		watch.Generation = 1
		watch.Connections = watch.Connections[1:]
		if !r.Replace(watch) {
			t.Fatal("new epoch rejected")
		}
		synctest.Wait()
		if resetOne.ctx.Err() == nil || oldTwo.ctx.Err() == nil || len(one) != 0 || len(two) != 1 {
			t.Fatal("new epoch should stop removed connections and restart retained ones")
		}
		if err := resetOne.sink(t.Context(), testObservation(ref)); !errors.Is(err, context.Canceled) {
			t.Fatalf("callback from a removed connection returned %v", err)
		}
	})
}

func TestRunnerPollsWhenStreamFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "owned"}
		streamAttempts := 0
		reads := 0
		a := runnerEventAdapter{
			runnerAdapter: runnerAdapter{observe: func(context.Context, execution.SessionRef) (execution.Observation, error) {
				reads++
				if reads == 1 {
					return execution.Observation{}, execution.ErrUnavailable
				}
				return testObservation(ref), nil
			}},
			events: func(ctx context.Context, sink execution.Sink) error {
				streamAttempts++
				if streamAttempts <= 2 {
					return errors.New("stream disconnected")
				}
				o := testObservation(ref)
				o.State = "needs_input"
				if err := sink(ctx, o); err != nil {
					return err
				}
				<-ctx.Done()
				return ctx.Err()
			},
		}
		r, records := recordedRunner(map[int64]execution.Adapter{1: a})
		r.Replace(Watch{Epoch: "epoch", Generation: 1, Connections: []WatchConnection{{ID: 1, Sessions: []WatchSession{{ID: "owned"}}}}})
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go r.Run(ctx)
		synctest.Wait()
		got := runnerRecords(records)
		if !slices.ContainsFunc(got, func(r Record) bool { return r.StreamError != nil && *r.StreamError == "stream disconnected" }) ||
			!slices.ContainsFunc(got, func(r Record) bool { return r.ExternalSessionID == "owned" && r.Unavailable && r.Observation == nil }) {
			t.Fatalf("want stream failure and read unavailability, got %+v", got)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		got = runnerRecords(records)
		if len(got) != 1 || got[0].Observation == nil || got[0].Observation.State != "running" {
			t.Fatalf("want poll while stream is still disconnected, got %+v", got)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		got = runnerRecords(records)
		for _, state := range []string{"running", "needs_input"} {
			if !slices.ContainsFunc(got, func(r Record) bool {
				return r.Observation != nil && r.Observation.State == state && r.ObservedAt.Equal(time.Now())
			}) {
				t.Fatalf("missing %s sample at source time in %+v", state, got)
			}
		}
		if !slices.ContainsFunc(got, func(r Record) bool { return r.StreamError != nil && *r.StreamError == "" }) {
			t.Fatalf("missing stream recovery in %+v", got)
		}
	})
}

func TestRunnerReadTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := runnerAdapter{observe: func(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
			<-ctx.Done()
			return testObservation(ref), nil
		}}
		r, records := recordedRunner(map[int64]execution.Adapter{1: a})
		r.Replace(Watch{Epoch: "epoch", Generation: 1, Connections: []WatchConnection{{ID: 1, Sessions: []WatchSession{{ID: "owned"}}}}})
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go r.Run(ctx)
		time.Sleep(19 * time.Second)
		synctest.Wait()
		for _, r := range runnerRecords(records) {
			if r.ExternalSessionID != "" {
				t.Fatalf("read was cut short: %+v", r)
			}
		}
		time.Sleep(time.Second)
		synctest.Wait()
		got := runnerRecords(records)
		if len(got) != 1 || got[0].ExternalSessionID != "owned" || !got[0].Unavailable || got[0].Observation != nil {
			t.Fatalf("want unavailability at deadline, got %+v", got)
		}
	})
}

func TestRunnerPollCadenceAndAttention(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ref := func(id string) execution.SessionRef {
			return execution.SessionRef{ConnectionID: 1, ExternalSessionID: id}
		}
		reads := map[string]int{}
		a := runnerAdapter{
			attention: []execution.SessionRef{ref("active"), ref("found"), ref("found"), ref("child"), ref("mismatch"), {ConnectionID: 2, ExternalSessionID: "foreign"}},
			observe: func(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
				reads[ref.ExternalSessionID]++
				o := testObservation(ref)
				switch ref.ExternalSessionID {
				case "child":
					o.Child = true
				case "mismatch":
					o.Session.ExternalSessionID = "another"
				}
				return o, nil
			},
		}
		r, records := recordedRunner(map[int64]execution.Adapter{1: a})
		r.Replace(Watch{Epoch: "epoch", Generation: 1, Connections: []WatchConnection{{ID: 1, Attention: true, Sessions: []WatchSession{
			{ID: "active"}, {ID: "stale", Stale: true}, {ID: "retained", Attention: true},
		}}}})
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go r.Run(ctx)
		assertPass := func(quiet, stale bool) {
			t.Helper()
			synctest.Wait()
			ids := []string{}
			for _, r := range runnerRecords(records) {
				if r.Observation == nil {
					continue
				}
				id := r.ExternalSessionID
				ids = append(ids, id)
				attention := id == "found" || id == "retained"
				if r.Attention != attention || r.Quiet != (attention && quiet) {
					t.Errorf("unexpected attention flags for %s: attention=%t quiet=%t", id, r.Attention, r.Quiet)
				}
			}
			want := []string{"active", "found", "retained"}
			if stale {
				want = append(want, "stale")
			}
			slices.Sort(ids)
			if !slices.Equal(ids, want) {
				t.Fatalf("observed %v, want %v", ids, want)
			}
		}
		assertPass(true, true)
		for i := 1; i <= 12; i++ {
			time.Sleep(5 * time.Second)
			assertPass(false, i == 12)
		}
		if reads["active"] != 13 || reads["found"] != 13 || reads["stale"] != 2 || reads["foreign"] != 0 {
			t.Fatalf("unexpected read counts: %v", reads)
		}
	})
}
