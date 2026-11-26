package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// This harness has documents and phases, not T3 threads, providers or runs.
// Its two-step launch records a document before starting the worker.
// documentHarnesses registers the fake harness kind: neither T3 nor OpenCode,
// so the coordinator can only use what the descriptor declares.
var documentHarnesses = harness.MustRegistry(harness.Descriptor{
	Kind: "document", Name: "Document", Sessions: harness.SessionsMachine,
	Closures: []harness.Closure{{Signal: execution.ClosedArchived, Provenance: "harness_archived", Event: "Completed from document archive."}},
})

type documentHarness struct {
	offline, loseReply, malformed, disallow bool
	created, started                        int
	document                                string
	phase                                   string
	closed                                  string // closure signal, "" for none
	links                                   *execution.Links
	input                                   execution.Intent
}

func (f *documentHarness) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: !f.offline, Reason: "Reconnect documents"}, Identity: "documents-local"}
}
func (f *documentHarness) Features(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{Operations: map[execution.Op]execution.Availability{execution.OpLaunch: {Available: !f.disallow, Reason: "Read-only document service"}, execution.OpOpening: {Reason: "Documents have no opening prompt"}}, Models: []execution.Model{{Choice: execution.Choice{ID: "brain-7", Label: "Brain seven", Availability: execution.Availability{Available: true}}, Permissions: []execution.Choice{{ID: "reviewed"}}}}}, nil
}
func (f *documentHarness) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	if f.offline {
		return nil, execution.ErrUnavailable
	}
	return []execution.WorkspaceInfo{{ID: "collection-z", Title: "Collection", WorkspaceRoot: "volume://checkout"}}, nil
}
func (f *documentHarness) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if f.offline {
		return execution.Observation{}, execution.ErrUnavailable
	}
	if ref.ExternalSessionID != f.document {
		return execution.Observation{}, execution.ErrNotFound
	}
	state := "running"
	switch f.phase {
	case "published":
		state = "done"
	case "withdrawn": // the service no longer knows the outcome
		state = "idle"
	}
	if f.malformed {
		state = "unrecognized vendor state"
	}
	now := time.Now().UTC()
	o := execution.Observation{Session: ref, Project: "collection-z", Title: "Document", State: state, Active: state == "running", CreatedAt: now, UpdatedAt: now, ExecutionID: "worker:99", Config: f.input.Config, Links: execution.Links{Web: "https://documents.invalid/view/" + ref.ExternalSessionID}}
	if f.links != nil {
		o.Links = *f.links
	}
	if state == "done" {
		o.Reply = &execution.Reply{Text: "Saved result", ItemID: "paragraph:3", RunID: "worker:99", UpdatedAt: now}
	}
	if f.closed != "" {
		o.Closed = &execution.Closure{Signal: f.closed}
	}
	return o, nil
}
func (f *documentHarness) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	if f.offline {
		return execution.Observation{}, execution.ErrUnavailable
	}
	var record struct {
		Document string
		Started  bool
	}
	if cp.Version != 0 {
		if err := json.Unmarshal(cp.Data, &record); err != nil {
			return execution.Observation{}, err
		}
	}
	persist := func() error {
		raw, _ := json.Marshal(record)
		return save(ctx, execution.Checkpoint{Version: 7, Data: raw})
	}
	if record.Document == "" {
		record.Document = "doc:" + in.RequestID
		if err := persist(); err != nil {
			return execution.Observation{}, err
		}
		f.created++
		f.document = record.Document
		f.input = in
	}
	if !record.Started {
		record.Started = true
		if err := persist(); err != nil {
			return execution.Observation{}, err
		}
		f.started++
		f.phase = "drafting"
	}
	if f.loseReply {
		return execution.Observation{}, errors.New("acknowledgement lost")
	}
	return f.Observe(ctx, execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: record.Document})
}
func (f *documentHarness) Stop(context.Context, execution.SessionRef) error {
	f.phase = "published"
	return nil
}
func (f *documentHarness) Attention(context.Context) ([]execution.SessionRef, error) { return nil, nil }
func (f *documentHarness) Opening(context.Context, execution.SessionRef, bool) (*execution.Reply, error) {
	return nil, execution.ErrUnavailable
}
func contractSetup(t *testing.T, p placement) (*db.Store, *documentHarness, *engine.Engine, db.Task) {
	t.Helper()
	store, task := contractTask(t, p)
	fake := &documentHarness{}
	svc := p.engine(t, store, documentHarnesses, map[int64]execution.Adapter{2: fake}, nil)
	return store, fake, svc, task
}

// contractTask is a store with document connection 2 on p's machine, one
// bound project and one backlog task.
func contractTask(t *testing.T, p placement) (*db.Store, db.Task) {
	t.Helper()
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness,machine_id) VALUES(2,'document',$1)", p.machine(t, pool)); err != nil {
		t.Fatal(err)
	}
	project, err := store.InsertProject(t.Context(), db.Project{Title: "Stage collection", Bindings: []db.ProjectBinding{{ConnectionID: 2, ExternalProjectID: "collection-z", Workspace: "volume://checkout"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: project.ID, ConnectionID: 2, Title: "Write", Prompt: "Keep this intent", ExecutionConfig: &execution.Config{Model: "brain-7", Permission: "reviewed", Mode: "compose"}})
	if err != nil {
		t.Fatal(err)
	}
	return store, task
}
func TestContractRestartReconcilesFrozenTwoStepIntent(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, fake, svc, task := contractSetup(t, p)
		ctx := t.Context()
		fake.loseReply = true
		unknown, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err == nil || unknown.State != db.StateLaunching || unknown.Session != nil {
			t.Fatalf("unknown %+v %v", unknown, err)
		}
		if fake.created != 1 || fake.started != 1 || fake.input.Config.Model != "brain-7" || fake.input.Config.Permission != "reviewed" {
			t.Fatalf("duplicate or translated launch %+v", fake)
		}
		attempt, err := store.Attempt(ctx, task.ID, 1)
		if err != nil || attempt.Intent == nil || attempt.Checkpoint.Version != 7 {
			t.Fatalf("missing durable launch %+v %v", attempt, err)
		}
		fake.loseReply = false
		restarted := p.engine(t, store, documentHarnesses, map[int64]execution.Adapter{2: fake}, nil)
		restarted.Coordinator.RecoverLaunching(ctx)
		// The periodic recovery path, without a second Start action.
		if _, err := testDB.Pool.ExecContext(ctx, "UPDATE execution_attempts SET retry_at=NULL"); err != nil {
			t.Fatal(err)
		}
		if err := restarted.Coordinator.PollActive(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil || got.Session.ConnectionID != 2 || got.LaunchAttempt != 1 || fake.created != 1 || fake.started != 1 {
			t.Fatalf("recovered %+v %v", got, err)
		}
		fake.offline = true
		_ = restarted.Coordinator.PollTask(ctx, got)
		offline, _ := store.GetTask(ctx, task.ID)
		if offline.State != db.StateRunning {
			t.Fatal("offline changed execution", offline.State)
		}
		fake.offline = false
		fake.malformed = true
		if err := restarted.Coordinator.PollTask(ctx, got); err == nil {
			t.Fatal("malformed state accepted")
		}
		malformed, _ := store.GetTask(ctx, task.ID)
		if malformed.State != db.StateRunning {
			t.Fatal("malformed reply changed execution")
		}
		fake.malformed = false
		fake.phase = "published"
		if err := restarted.Coordinator.PollTask(ctx, got); err != nil {
			t.Fatal(err)
		}
		preview, err := store.CompletionPreview(ctx, task.ID)
		if err != nil || preview.BlockedReason != "" || preview.Evidence.Reply == nil || *preview.Evidence.Reply != "Saved result" {
			t.Fatalf("evidence %+v %v", preview, err)
		}
		fake.offline = true
		accepted, err := store.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{RequestID: "accept-doc", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, false)
		if err != nil || accepted.AcceptanceID == nil {
			t.Fatal("offline acceptance failed", err)
		}
		evidence, err := store.Acceptances(ctx, task.ID)
		if err != nil || evidence[0].Evidence.Session.ConnectionID != 2 || *evidence[0].Evidence.Reply != "Saved result" {
			t.Fatal("offline evidence lost", err)
		}
	})
}
func TestContractArchivedSessionInfersCompletion(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, fake, svc, task := contractSetup(t, p)
		ctx := t.Context()
		released := 0
		svc.Coordinator.OnAccepted = func() { released++ }
		got, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		fake.closed = execution.ClosedArchived
		if err = svc.Coordinator.PollTask(ctx, got); err != nil {
			t.Fatal(err)
		}
		if evidence, _ := store.Acceptances(ctx, task.ID); len(evidence) != 0 || released != 0 {
			t.Fatal("running archived session completed")
		}
		got, _ = store.GetTask(ctx, task.ID)
		fake.phase = "published"
		for range 2 {
			if err = svc.Coordinator.PollTask(ctx, got); err != nil {
				t.Fatal(err)
			}
			got, _ = store.GetTask(ctx, task.ID)
		}
		evidence, err := store.Acceptances(ctx, task.ID)
		if err != nil || len(evidence) != 1 || evidence[0].Provenance != "harness_archived" || *evidence[0].Evidence.Reply != "Saved result" || released != 1 || got.AcceptanceID == nil {
			t.Fatalf("archive inference %+v %v released %d", evidence, err, released)
		}
	})
}

// An idle observation keeps the task's state but may carry a closure: it
// completes a task Stage recorded done, never one still running.
func TestContractIdleClosureInfersFromStoredDone(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, fake, svc, task := contractSetup(t, p)
		ctx := t.Context()
		got, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		poll := func(want db.TaskState) db.Task {
			t.Helper()
			if err := svc.Coordinator.PollTask(ctx, got); err != nil {
				t.Fatal(err)
			}
			got, err = store.GetTask(ctx, task.ID)
			if err != nil || got.State != want {
				t.Fatalf("want %s: %+v %v", want, got, err)
			}
			return got
		}
		fake.phase, fake.closed = "withdrawn", execution.ClosedArchived
		if got = poll(db.StateRunning); got.AcceptanceID != nil {
			t.Fatal("an idle closure completed running work")
		}
		fake.phase, fake.closed = "published", ""
		poll(db.StateDone)
		fake.phase, fake.closed = "withdrawn", execution.ClosedArchived
		got = poll(db.StateDone)
		evidence, err := store.Acceptances(ctx, task.ID)
		if err != nil || got.AcceptanceID == nil || len(evidence) != 1 || evidence[0].Provenance != "harness_archived" || evidence[0].Evidence.Reply == nil || *evidence[0].Evidence.Reply != "Saved result" {
			t.Fatalf("idle closure %+v %+v %v", got, evidence, err)
		}
	})
}

func TestContractOptionalCapabilitiesAndOfflineDoNotClaim(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, fake, svc, task := contractSetup(t, p)
		fake.disallow = true
		if _, err := svc.Coordinator.StartTask(t.Context(), task.ID); err == nil {
			t.Fatal("unsupported launch allowed")
		}
		fake.disallow = false
		fake.offline = true
		if _, err := svc.Coordinator.StartTask(t.Context(), task.ID); err == nil {
			t.Fatal("offline launch allowed")
		}
		got, _ := store.GetTask(t.Context(), task.ID)
		if got.State != db.StateBacklog || got.LaunchAttempt != 0 || fake.created != 0 {
			t.Fatal("preflight dispatched work")
		}
	})
}

func TestContractSchedulesAndHandoffPreserveScopedConfiguration(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, fake, svc, source := contractSetup(t, p)
		ctx := t.Context()
		launched, err := svc.Coordinator.StartTask(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		fake.phase = "published"
		if err = svc.Coordinator.PollTask(ctx, launched); err != nil {
			t.Fatal(err)
		}
		preview, err := store.CompletionPreview(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ChangeAcceptance(ctx, source.ID, db.AcceptanceRequest{RequestID: "accepted", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, false); err != nil {
			t.Fatal(err)
		}
		accepted, err := store.Acceptances(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if text := coordinator.ComposeAcceptedHandoff(accepted[0], 4000); !strings.Contains(text, "connection 2") || !strings.Contains(text, "Saved result") {
			t.Fatal(text)
		}
		sc, err := store.CreateSchedule(ctx, db.NewSchedule{ConnectionID: 2, ProjectID: source.ProjectID, Title: "Scheduled document", Prompt: "scheduled", ExecutionConfig: source.ExecutionConfig, Cron: "0 * * * *", Tz: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		spawned, err := store.RunScheduleNow(ctx, sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if spawned.ConnectionID != 2 || spawned.ExecutionConfig == nil || spawned.ExecutionConfig.Model != "brain-7" {
			t.Fatalf("schedule lost contract: %+v", spawned)
		}
		if _, err = svc.Coordinator.StartTask(ctx, spawned.ID); err != nil {
			t.Fatal(err)
		}
		if fake.input.Config.Permission != "reviewed" {
			t.Fatal("schedule translated model options")
		}
		ref := accepted[0].Evidence.Session
		if err = store.RecordHandoff(ctx, spawned.ID, db.Handoff{Session: ref, Attempt: 1, Status: db.HandoffSent, SourceTaskID: &source.ID, Text: "Saved result"}); err != nil {
			t.Fatal(err)
		}
		handoffs, err := store.GetHandoffs(ctx, spawned.ID, 1)
		if err != nil || len(handoffs) != 1 || handoffs[0].Session == nil || handoffs[0].Session.ConnectionID != 2 {
			t.Fatal("handoff lost scope", handoffs, err)
		}
	})
}

func TestContractMalformedLaunchRemainsUnconfirmed(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, fake, svc, task := contractSetup(t, p)
		fake.malformed = true
		got, err := svc.Coordinator.StartTask(t.Context(), task.ID)
		if err == nil || got.State != db.StateLaunching || got.Session != nil {
			t.Fatal(got, err)
		}
		fake.malformed = false
		got, err = svc.Coordinator.StartTask(t.Context(), task.ID)
		if err != nil || got.Session == nil || got.LaunchAttempt != 1 || fake.created != 1 || fake.started != 1 {
			t.Fatal(got, err)
		}
		current, err := store.Observation(t.Context(), *got.Session)
		if err != nil {
			t.Fatal(err)
		}
		old := current
		old.UpdatedAt = current.UpdatedAt.Add(-time.Hour)
		old.State = "done"
		old.Active = false
		old.Reply = &execution.Reply{Text: "stale"}
		if err = store.PutObservation(t.Context(), old); err != nil {
			t.Fatal(err)
		}
		retained, err := store.Observation(t.Context(), *got.Session)
		if err != nil || retained.State != "running" || retained.Reply != nil {
			t.Fatal("stale observation replaced current", retained, err)
		}
	})
}
