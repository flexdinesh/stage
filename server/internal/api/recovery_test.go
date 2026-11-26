package api_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestLegacyUnconfirmedLaunchLinksOnlyAfterPreview(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/repair")
	pending := e.task(p.ID, "Repair", nil)
	if _, err := e.store.TransitionTask(t.Context(), db.Transition{TaskID: pending.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	// A legacy attempt has no safe replay identity.
	if _, err := testDB.Pool.ExecContext(t.Context(), "UPDATE execution_attempts SET phase='unconfirmed' WHERE task_id=?", pending.ID); err != nil {
		t.Fatal(err)
	}
	existing, err := e.fake.LaunchThread(t.Context(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "Existing work"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(e.fake.Launched)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", pending.ID), nil, 409, nil)
	endpoint := fmt.Sprintf("/api/tasks/%d/launch-link", pending.ID)
	link := t3.AppThreadURL(t3.FakeEnvironmentID, existing.ThreadID)
	var preview coordinator.LinkPreview
	e.must("POST", endpoint+"/preview", map[string]any{"link": link}, 200, &preview)
	if preview.Observation.Title != "Existing work" || preview.Attempt != 1 {
		t.Fatalf("preview %+v", preview)
	}
	if got := e.getTask(pending.ID); got.State != "launching" || got.ExternalSessionID != nil {
		t.Fatal("preview attached")
	}
	body := map[string]any{"link": link, "attempt": preview.Attempt, "revision": preview.Revision + 1, "environment": preview.Environment}
	e.must("POST", endpoint, body, 409, nil)
	body["revision"] = preview.Revision
	var attached task
	e.must("POST", endpoint, body, 200, &attached)
	if attached.State != "running" || attached.ExternalSessionID == nil || *attached.ExternalSessionID != existing.ThreadID || attached.LaunchAttempt != 1 || len(e.fake.Launched) != before {
		t.Fatalf("link launched or changed identity %+v", attached)
	}
	e.must("POST", endpoint, body, 409, nil)
}
func TestRecoveryPreviewRejectsForeignOwnedAndUnverifiedSessions(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/repair")
	pending := e.task(p.ID, "Repair", nil)
	if _, err := e.store.TransitionTask(t.Context(), db.Transition{TaskID: pending.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("/api/tasks/%d/launch-link/preview", pending.ID)
	owned := e.task(p.ID, "Owned", map[string]any{"start": true})
	e.must("POST", endpoint, map[string]any{"link": t3.AppThreadURL(t3.FakeEnvironmentID, *owned.ExternalSessionID)}, 409, nil)
	other := e.project("/w/other")
	foreign, err := e.fake.LaunchThread(t.Context(), t3.LaunchInput{ProjectID: other.T3ProjectID, Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	e.must("POST", endpoint, map[string]any{"link": t3.AppThreadURL(t3.FakeEnvironmentID, foreign.ThreadID)}, 409, nil)
	e.must("POST", endpoint, map[string]any{"link": t3.AppThreadURL("other-environment", foreign.ThreadID)}, 400, nil)
}

// A launch whose session Stage saw but could not record, on a connection
// whose adapter resolves no destination (a remote machine's) and with no
// stored observation, still offers the resume command the attempt kept.
func TestUnrecordedLaunchOffersTheKeptResumeCommand(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	p := e.project("/w/resume")
	pending := e.task(p.ID, "Resume", nil)
	if _, err := e.store.TransitionTask(ctx, db.Transition{TaskID: pending.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	// A connection this server hosts no adapter for.
	if _, err := testDB.Pool.ExecContext(ctx, "INSERT INTO harness_connections(id,harness,machine_id) VALUES(77,'hosted-elsewhere',1)"); err != nil {
		t.Fatal(err)
	}
	a, err := e.store.Attempt(ctx, pending.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	seen := db.Observed(execution.Observation{Session: execution.SessionRef{ConnectionID: 77, ExternalSessionID: "s-1"}, Title: "Resume", State: "running",
		Links: execution.Links{Command: "claude attach s1"}})
	if err := e.store.Unconfirmed(ctx, a, errors.New("record failed"), seen); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Launch struct {
			Session struct {
				Links links `json:"links"`
			} `json:"session"`
		} `json:"launch"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d", pending.ID), nil, 200, &got)
	if got.Launch.Session.Links.Command != "claude attach s1" {
		t.Fatalf("launch session links %+v", got.Launch.Session.Links)
	}
}
