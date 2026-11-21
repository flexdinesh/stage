package sync_test

import (
	"context"
	"errors"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
)

// Failures only a remote machine has: the worker channel loses a reply, the
// worker restarts mid-launch, the machine is offline. Each leaves the
// launch unconfirmed (never failed or rejected) and the replay reconciles
// from the saved checkpoint: the harness creates and starts exactly once.

var remotePlacement = placement{name: "remote", remote: true}

func remoteSetup(t *testing.T, a execution.Adapter) (*db.Store, *remotetest.Rig, *coordinator.Service, db.Task) {
	t.Helper()
	store, task := contractTask(t, remotePlacement)
	rig := remotetest.New(t, remoteMachine, map[int64]execution.Adapter{2: a})
	svc := coordinator.NewForMachines(store, rig.Set(), nil)
	svc.Harnesses = documentHarnesses
	return store, rig, svc, task
}

func TestRemoteLostLaunchReplyReplaysOnce(t *testing.T) {
	fake := &documentHarness{}
	store, rig, svc, task := remoteSetup(t, fake)
	ctx := t.Context()
	svc.LaunchAttemptTimeout = 1500 * time.Millisecond
	rig.LoseResult(remote.KindLaunch, 1)
	got, err := svc.StartTask(ctx, task.ID)
	if err == nil || got.State != db.StateLaunching || got.Session != nil {
		t.Fatalf("lost reply %+v %v", got, err)
	}
	var rejected *execution.Rejected
	if errors.As(err, &rejected) {
		t.Fatal("lost reply rejected the launch", err)
	}
	if fake.created != 1 || fake.started != 1 {
		t.Fatalf("worker launch ran %d/%d", fake.created, fake.started)
	}
	attempt, err := store.Attempt(ctx, task.ID, 1)
	if err != nil || attempt.Phase != "unconfirmed" || attempt.Checkpoint.Version != 7 {
		t.Fatalf("attempt %+v %v", attempt, err)
	}
	clearRetry(t)
	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetTask(ctx, task.ID)
	if err != nil || got.State != db.StateRunning || got.Session == nil || got.Session.ExternalSessionID != fake.document || fake.created != 1 || fake.started != 1 {
		t.Fatalf("replay %+v %v created %d started %d", got, err, fake.created, fake.started)
	}
}

// stalling stops its first launch after its first external effect (the
// document exists), before the next checkpoint reaches the main server,
// until its worker dies: the replay must start the document, not create
// another.
type stalling struct {
	*documentHarness
	once    gosync.Once
	stalled chan struct{}
}

func (s *stalling) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	first := false
	s.once.Do(func() { first = true })
	if !first {
		return s.documentHarness.Launch(ctx, in, cp, save)
	}
	saves := 0
	return s.documentHarness.Launch(ctx, in, cp, func(ctx context.Context, c execution.Checkpoint) error {
		if saves++; saves == 1 {
			return save(ctx, c)
		}
		close(s.stalled)
		<-ctx.Done()
		return ctx.Err()
	})
}

func TestRemoteWorkerRestartMidLaunchReplaysFromCheckpoint(t *testing.T) {
	fake := &stalling{documentHarness: &documentHarness{}, stalled: make(chan struct{})}
	store, rig, svc, task := remoteSetup(t, fake)
	ctx := t.Context()
	type result struct {
		task db.Task
		err  error
	}
	started := make(chan result, 1)
	go func() {
		got, err := svc.StartTask(ctx, task.ID)
		started <- result{got, err}
	}()
	select {
	case <-fake.stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("launch never reached its second checkpoint")
	}
	rig.StopWorker()
	var r result
	select {
	case r = <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("start did not return after the worker died")
	}
	if r.err == nil || r.task.State != db.StateLaunching || r.task.Session != nil {
		t.Fatalf("worker died mid-launch %+v %v", r.task, r.err)
	}
	if fake.created != 1 || fake.started != 0 {
		t.Fatalf("worker died after creating: %d/%d", fake.created, fake.started)
	}
	attempt, err := store.Attempt(ctx, task.ID, 1)
	if err != nil || attempt.Phase != "unconfirmed" || attempt.Checkpoint.Version != 7 || !strings.Contains(string(attempt.Checkpoint.Data), "doc:") {
		t.Fatalf("checkpoint not kept %+v %v", attempt, err)
	}
	// Offline: recovery waits, nothing changes.
	clearRetry(t)
	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetTask(ctx, task.ID); got.State != db.StateLaunching {
		t.Fatal("offline worker changed the launch", got.State)
	}
	rig.StartWorker()
	clearRetry(t)
	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask(ctx, task.ID)
	if err != nil || got.State != db.StateRunning || got.Session == nil || fake.created != 1 || fake.started != 1 || got.Session.ExternalSessionID != fake.document {
		t.Fatalf("replay after restart %+v %v created %d started %d", got, err, fake.created, fake.started)
	}
}

func TestRemoteOfflineMachineIsAnOfflineHarness(t *testing.T) {
	fake := &documentHarness{}
	store, rig, svc, task := remoteSetup(t, fake)
	ctx := t.Context()
	rig.StopWorker()
	var ce *db.ConflictError
	if _, err := svc.StartTask(ctx, task.ID); !errors.As(err, &ce) || ce.Code != "connection_unavailable" || !strings.Contains(ce.Msg, "offline") {
		t.Fatal("offline launch", err)
	}
	if got, _ := store.GetTask(ctx, task.ID); got.State != db.StateBacklog || got.LaunchAttempt != 0 || fake.created != 0 {
		t.Fatal("offline preflight dispatched work", got)
	}
	rig.StartWorker()
	running, err := svc.StartTask(ctx, task.ID)
	if err != nil || running.State != db.StateRunning {
		t.Fatal(running, err)
	}
	rig.StopWorker()
	fake.phase = "published"
	if err := svc.PollTask(ctx, running); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("offline observe", err)
	}
	if err := svc.Stream(ctx, 2); err != nil {
		t.Fatal("offline stream", err)
	}
	if got, _ := store.GetTask(ctx, task.ID); got.State != db.StateRunning {
		t.Fatal("offline worker implied an outcome", got.State)
	}
	rig.StartWorker()
	if err := svc.PollTask(ctx, running); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetTask(ctx, task.ID); got.State != db.StateDone {
		t.Fatal("reconnected worker not observed", got.State)
	}
}
