package coordinator_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Two more fake harnesses prove the optional contract without T3 or
// OpenCode. assigned: the harness assigns session ids and creation is not
// idempotent, so launches are correlated. hosted: its sessions require
// local processes; it pushes events, can lose its owner and answers interaction.

var (
	assignedDescriptor = harness.Descriptor{Kind: "assigned", Name: "Assigned", Sessions: harness.SessionsCloud, Launch: harness.LaunchCorrelated, StateReasons: []string{"preempted"}}
	hostedDescriptor   = harness.Descriptor{Kind: "hosted", Name: "Hosted", Sessions: harness.SessionsMachine, LocalProcesses: true, StateReasons: []string{execution.ReasonOwnerExited}}
	v2Harnesses        = harness.MustRegistry(assignedDescriptor, hostedDescriptor)
)

// fakeBase answers the operations the v2 tests do not exercise.
type fakeBase struct{ workspace string }

func (f fakeBase) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: true}, Identity: "local"}
}
func (f fakeBase) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{{ID: "proj", Title: "Project", WorkspaceRoot: f.workspace}}, nil
}
func (f fakeBase) Attention(context.Context) ([]execution.SessionRef, error) { return nil, nil }
func (f fakeBase) Opening(context.Context, execution.SessionRef, bool) (*execution.Reply, error) {
	return nil, execution.ErrUnavailable
}
func (f fakeBase) Stop(context.Context, execution.SessionRef) error { return nil }

type assignedHarness struct {
	fakeBase
	mu                          gosync.Mutex
	prompts                     map[string]string // session id: prompt
	created, correlations       int
	loseReply, failBeforeCreate bool
}

func (f *assignedHarness) Features(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{Operations: map[execution.Op]execution.Availability{execution.OpLaunch: {Available: true}}, Models: []execution.Model{{Choice: execution.Choice{ID: "m", Availability: execution.Availability{Available: true}}}}}, nil
}
func (f *assignedHarness) observation(conn int64, id string) execution.Observation {
	now := time.Now().UTC()
	return execution.Observation{Session: execution.SessionRef{ConnectionID: conn, ExternalSessionID: id}, Project: "proj", Title: "Assigned", State: "running", Active: true, CreatedAt: now, UpdatedAt: now}
}
func (f *assignedHarness) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.prompts[ref.ExternalSessionID]; !ok {
		return execution.Observation{}, execution.ErrNotFound
	}
	return f.observation(ref.ConnectionID, ref.ExternalSessionID), nil
}

// Launch creates a new session on every call: never safe to replay.
func (f *assignedHarness) Launch(_ context.Context, in execution.Intent, _ execution.Checkpoint, _ execution.SaveCheckpoint) (execution.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failBeforeCreate {
		return execution.Observation{}, errors.New("connection reset")
	}
	f.created++
	id := fmt.Sprintf("srv-%d", f.created)
	if f.prompts == nil {
		f.prompts = map[string]string{}
	}
	f.prompts[id] = in.Prompt
	if f.loseReply {
		return execution.Observation{}, errors.New("acknowledgement lost")
	}
	return f.observation(in.ConnectionID, id), nil
}
func (f *assignedHarness) Correlate(_ context.Context, in execution.Intent) (execution.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.correlations++
	for id, p := range f.prompts {
		if strings.Contains(p, in.Marker) {
			return f.observation(in.ConnectionID, id), nil
		}
	}
	return execution.Observation{}, execution.ErrNotFound
}

type hostedHarness struct {
	fakeBase
	mu         gosync.Mutex
	state      string
	reason     string
	observed   int
	pushes     []execution.Observation
	requests   []execution.Request
	responses  map[string]execution.Response
	messages   []string
	respondOff string // reason respond is unavailable
}

func (f *hostedHarness) Features(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{
		Operations: map[execution.Op]execution.Availability{
			execution.OpLaunch:  {Available: true},
			execution.OpRespond: {Available: f.respondOff == "", Reason: f.respondOff},
			execution.OpSend:    {Available: true},
		},
		Models: []execution.Model{{Choice: execution.Choice{ID: "m", Availability: execution.Availability{Available: true}}}},
	}, nil
}
func (f *hostedHarness) snapshot(ref execution.SessionRef) execution.Observation {
	now := time.Now().UTC()
	state := f.state
	if state == "" {
		state = "running"
	}
	return execution.Observation{Session: ref, Project: "proj", Title: "Hosted", State: state, StateReason: f.reason, Active: state == "running" || state == "needs_input", CreatedAt: now, UpdatedAt: now}
}
func (f *hostedHarness) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observed++
	return f.snapshot(ref), nil
}
func (f *hostedHarness) Launch(_ context.Context, in execution.Intent, _ execution.Checkpoint, _ execution.SaveCheckpoint) (execution.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot(execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: "run:" + in.RequestID}), nil
}

// Events drains the queued pushes, then ends the stream.
func (f *hostedHarness) Events(ctx context.Context, sink execution.Sink) error {
	f.mu.Lock()
	pushes := f.pushes
	f.pushes = nil
	f.mu.Unlock()
	var errs []error
	for _, o := range pushes {
		errs = append(errs, sink(ctx, o))
	}
	return errors.Join(errs...)
}
func (f *hostedHarness) Requests(context.Context, execution.SessionRef) ([]execution.Request, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, nil
}
func (f *hostedHarness) Respond(_ context.Context, _ execution.SessionRef, id string, r execution.Response) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Option == "bad" {
		return &execution.Invalid{Reason: "pick allow or deny"}
	}
	for i, q := range f.requests {
		if q.ID == id {
			f.requests = append(f.requests[:i], f.requests[i+1:]...)
			if f.responses == nil {
				f.responses = map[string]execution.Response{}
			}
			f.responses[id] = r
			return nil
		}
	}
	return execution.ErrNotFound
}
func (f *hostedHarness) Send(_ context.Context, _ execution.SessionRef, m execution.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, m.Text)
	return nil
}

// v2Setup registers connection 3 of kind with a, one bound project and one
// backlog task.
func v2Setup(t *testing.T, p placement, kind string, a execution.Adapter) (*db.Store, *coordinator.Service, db.Task) {
	t.Helper()
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness,machine_id) VALUES(3,$1,$2)", kind, p.machine(t, pool)); err != nil {
		t.Fatal(err)
	}
	project, err := store.InsertProject(t.Context(), db.Project{Title: "V2", Bindings: []db.ProjectBinding{{ConnectionID: 3, ExternalProjectID: "proj", Workspace: "volume://v2"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: project.ID, ConnectionID: 3, Title: "Work", Prompt: "Do it", ExecutionConfig: &execution.Config{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := p.coordinator(t, store, v2Harnesses, map[int64]execution.Adapter{3: a}, nil)
	return store, svc, task
}

func clearRetry(t *testing.T) {
	t.Helper()
	if _, err := testDB.Pool.ExecContext(t.Context(), "UPDATE execution_attempts SET retry_at=NULL"); err != nil {
		t.Fatal(err)
	}
}

func TestContractCorrelatedLaunchDispatchesOnce(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := &assignedHarness{fakeBase: fakeBase{workspace: "volume://v2"}, loseReply: true}
		store, svc, task := v2Setup(t, p, "assigned", f)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err == nil || got.State != db.StateLaunching || f.created != 1 {
			t.Fatalf("lost reply %+v %v created %d", got, err, f.created)
		}
		a, err := store.Attempt(ctx, task.ID, 1)
		if err != nil || a.Intent == nil || a.Intent.Marker != execution.LaunchMarker(a.Intent.RequestID) || !strings.HasSuffix(a.Intent.Prompt, "\n\n"+a.Intent.Marker) || a.Phase != "unconfirmed" {
			t.Fatalf("correlated intent %+v %v", a, err)
		}
		f.loseReply = false
		clearRetry(t)
		if err := svc.PollActive(ctx); err != nil {
			t.Fatal(err)
		}
		got, err = store.GetTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil || got.Session.ExternalSessionID != "srv-1" || f.created != 1 || f.correlations != 1 {
			t.Fatalf("correlated recovery %+v %v created %d correlations %d", got, err, f.created, f.correlations)
		}
	})
}

func TestContractCorrelatedLaunchNotFoundStaysUnconfirmed(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := &assignedHarness{fakeBase: fakeBase{workspace: "volume://v2"}, failBeforeCreate: true}
		store, svc, task := v2Setup(t, p, "assigned", f)
		ctx := t.Context()
		if _, err := svc.StartTask(ctx, task.ID); err == nil {
			t.Fatal("uncertain launch reported success")
		}
		f.failBeforeCreate = false
		for range 2 {
			clearRetry(t)
			if err := svc.PollActive(ctx); err != nil {
				t.Fatal(err)
			}
		}
		got, err := store.GetTask(ctx, task.ID)
		if err != nil || got.State != db.StateLaunching || got.Session != nil || f.created != 0 || f.correlations != 2 || got.LastError == nil || !strings.Contains(*got.LastError, "link the session") {
			t.Fatalf("not found must wait for linking, never relaunch: %+v %v created %d correlations %d", got, err, f.created, f.correlations)
		}
	})
}

func TestContractCorrelatedLaunchNeedsCorrelator(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, task := contractTask(t, p)
		d := documentHarnesses.All()[0]
		d.Launch = harness.LaunchCorrelated
		svc := p.coordinator(t, store, harness.MustRegistry(d), map[int64]execution.Adapter{2: &documentHarness{}}, nil)
		if _, err := svc.StartTask(t.Context(), task.ID); err == nil || !strings.Contains(err.Error(), "cannot reconcile") {
			t.Fatal("correlated launch without a correlator", err)
		}
		got, _ := store.GetTask(t.Context(), task.ID)
		if got.State != db.StateBacklog || got.LaunchAttempt != 0 {
			t.Fatal("preflight dispatched work", got)
		}
	})
}

func TestContractPushIngest(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := &hostedHarness{fakeBase: fakeBase{workspace: "volume://v2"}}
		store, svc, task := v2Setup(t, p, "hosted", f)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil {
			t.Fatal(got, err)
		}
		before := f.observed
		done := f.snapshot(*got.Session)
		done.State, done.Active = "done", false
		done.UpdatedAt = done.UpdatedAt.Add(time.Second)
		unowned := f.snapshot(execution.SessionRef{ConnectionID: 3, ExternalSessionID: "elsewhere"})
		f.pushes = []execution.Observation{unowned, done}
		if err := svc.Stream(ctx, 3); err != nil {
			t.Fatal(err)
		}
		got, _ = store.GetTask(ctx, task.ID)
		if got.State != db.StateDone || f.observed != before {
			t.Fatalf("push not ingested without polling: %s observed %d->%d", got.State, before, f.observed)
		}
		other := done
		other.Session.ConnectionID = 2
		f.pushes = []execution.Observation{other}
		if err := svc.Stream(ctx, 3); err == nil || !strings.Contains(err.Error(), "another connection") {
			t.Fatal("pushed observation crossed connections", err)
		}
	})
}

func TestContractObservationStateReasons(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := &hostedHarness{fakeBase: fakeBase{workspace: "volume://v2"}}
		store, svc, task := v2Setup(t, p, "hosted", f)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		f.state, f.reason = "interrupted", execution.ReasonOwnerExited
		if err := svc.PollTask(ctx, got); err != nil {
			t.Fatal(err)
		}
		if got, _ = store.GetTask(ctx, task.ID); got.State != db.StateInterrupted {
			t.Fatal("hosted owner exit", got.State)
		}

		// The coordinator accepts extra declared reasons, with the usual
		// activity checks. owner_exited always requires interrupted.
		o := execution.Observation{Session: execution.SessionRef{ConnectionID: 3, ExternalSessionID: "a"}, Project: "proj", State: "failed", StateReason: "preempted"}
		if err := svc.CheckObservation(assignedDescriptor.Kind, o); err != nil {
			t.Fatal("declared extra reason", err)
		}
		if err := svc.CheckObservation(hostedDescriptor.Kind, o); err == nil || !strings.Contains(err.Error(), o.StateReason) {
			t.Fatal("undeclared extra reason", err)
		}
		o.StateReason = execution.ReasonOwnerExited
		if err := svc.CheckObservation(hostedDescriptor.Kind, o); err == nil || !strings.Contains(err.Error(), "failed") {
			t.Fatal("owner_exited must interrupt", err)
		}

		// The document harness has no declared reasons.
		dstore, doc, dsvc, dtask := contractSetup(t, p)
		running, err := dsvc.StartTask(ctx, dtask.ID)
		if err != nil {
			t.Fatal(err)
		}
		dsvc2 := p.coordinator(t, dstore, documentHarnesses, map[int64]execution.Adapter{2: lostOwner{doc}}, nil)
		if err := dsvc2.PollTask(ctx, running); err == nil || !strings.Contains(err.Error(), execution.ReasonOwnerExited) {
			t.Fatal("undeclared owner_exited was accepted", err)
		}
		if got, _ := dstore.GetTask(ctx, dtask.ID); got.State != db.StateRunning {
			t.Fatal("malformed owner exit changed execution", got.State)
		}
	})
}

// lostOwner claims the process owner exited.
type lostOwner struct{ *documentHarness }

func (l lostOwner) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	o, err := l.documentHarness.Observe(ctx, ref)
	o.State, o.Active, o.StateReason = "interrupted", false, execution.ReasonOwnerExited
	o.UpdatedAt = o.UpdatedAt.Add(time.Second)
	return o, err
}

func TestContractInteraction(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := &hostedHarness{fakeBase: fakeBase{workspace: "volume://v2"}, requests: []execution.Request{
			{ID: "q1", Kind: "approval", Title: "Run tests?", Preview: "go test ./...", TextOption: "no", Options: []execution.Choice{{ID: "yes"}, {ID: "no"}}},
			{ID: "q2", Kind: "question", Title: "Pick", Questions: []execution.Question{{ID: "0", Header: "Color", Prompt: "Which color?", Options: []execution.QuestionOption{{Label: "Red", Description: "warm"}}, Multi: true, Custom: true}}},
		}}
		_, svc, task := v2Setup(t, p, "hosted", f)
		ctx := t.Context()
		if _, err := svc.Requests(ctx, task.ID); err == nil {
			t.Fatal("requests without a session")
		}
		if _, err := svc.StartTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		rs, err := svc.Requests(ctx, task.ID)
		if err != nil || len(rs) != 2 || rs[0].ID != "q1" || rs[0].Preview != "go test ./..." || rs[0].TextOption != "no" {
			t.Fatal(rs, err)
		}
		if q := rs[1].Questions; len(q) != 1 || q[0].Prompt != "Which color?" || !q[0].Multi || !q[0].Custom || q[0].Options[0].Description != "warm" {
			t.Fatal("question round trip", rs[1])
		}
		var invalid *db.InvalidError
		if _, err := svc.Respond(ctx, task.ID, "q1", execution.Response{}); !errors.As(err, &invalid) {
			t.Fatal("empty response accepted", err)
		}
		if _, err := svc.Respond(ctx, task.ID, "missing", execution.Response{Option: "yes"}); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("unknown request", err)
		}
		if _, err := svc.Respond(ctx, task.ID, "q1", execution.Response{Option: "bad"}); !errors.As(err, &invalid) || invalid.Msg != "pick allow or deny" {
			t.Fatal("invalid answer", err)
		}
		if _, err := svc.Respond(ctx, task.ID, "q1", execution.Response{Option: "yes"}); err != nil || f.responses["q1"].Option != "yes" {
			t.Fatal(err, f.responses)
		}
		if _, err := svc.Respond(ctx, task.ID, "q2", execution.Response{Answers: map[string][]string{"0": {"Red", "teal"}}}); err != nil || len(f.responses["q2"].Answers["0"]) != 2 {
			t.Fatal("answers", err, f.responses)
		}
		if _, err := svc.Send(ctx, task.ID, execution.Message{Text: "also update docs"}); err != nil || len(f.messages) != 1 {
			t.Fatal(err, f.messages)
		}
		f.respondOff = "read-only session"
		var ce *db.ConflictError
		if _, err := svc.Requests(ctx, task.ID); !errors.As(err, &ce) || ce.Code != "operation_unavailable" || !strings.Contains(ce.Msg, "read-only session") {
			t.Fatal("unavailable operation", err)
		}

		// A harness without the interface: operation_unavailable, nothing else limited.
		_, _, dsvc, dtask := contractSetup(t, p)
		if _, err := dsvc.StartTask(ctx, dtask.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := dsvc.Send(ctx, dtask.ID, execution.Message{Text: "hi"}); !errors.As(err, &ce) || ce.Code != "operation_unavailable" {
			t.Fatal("non-interactive harness", err)
		}
	})
}
