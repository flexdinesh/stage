package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// archiveFixture serves contract and PATCH from the fake and a session
// snapshot reflecting the fake's archive state.
func archiveFixture(t *testing.T, archive bool) (*Adapter, *opencodetest.Server) {
	t.Helper()
	fake := opencodetest.New(t, "")
	if !archive {
		fake.WithoutSessionUpdate()
	}
	fake.AddSession("ses_test")
	base := fake.Config.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/info" || r.URL.Path == "/openapi.json" || r.Method == http.MethodPatch:
			base.ServeHTTP(w, r)
		case r.URL.Path == "/api/session/ses_test":
			var archived int64
			if fake.Archived("ses_test") {
				archived = 60
			}
			_ = json.NewEncoder(w).Encode(envelope[session]{Data: session{ID: "ses_test", Time: wireTime{Created: 10, Updated: 50 + archived, Busy: 30, Idle: 40, Archived: archived}, Outcome: "succeeded", ProjectID: "project", Location: location{Directory: "/repo"}}})
		case r.URL.Path == "/api/session/active":
			fmt.Fprint(w, `{"data":{}}`)
		case r.URL.Path == "/api/session/ses_test/permission", r.URL.Path == "/api/session/ses_test/form", r.URL.Path == "/api/session/ses_test/message":
			fmt.Fprint(w, `{"data":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, err := New(2, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func archiveDeclaration(t *testing.T, a *Adapter) execution.Action {
	t.Helper()
	c, err := a.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeclaresOperations(); err != nil {
		t.Fatal(err)
	}
	if err := execution.ValidateActions(c.Actions, execution.TargetSession); err != nil || len(c.Actions) != 1 {
		t.Fatalf("actions %+v: %v", c.Actions, err)
	}
	return c.Actions[0]
}

func TestArchiveActionArchivesTheSessionInOpenCode(t *testing.T) {
	a, fake := archiveFixture(t, true)
	ref := execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_test"}
	action := archiveDeclaration(t, a)
	if action.ID != ActionArchive || action.Label != "Archive in OpenCode" || !action.Availability.Available ||
		action.Destructive || !action.Allows(execution.CallerAgent) || action.Confirm == "" {
		t.Fatalf("declaration %+v", action)
	}
	o, err := a.Observe(t.Context(), ref)
	if err != nil || o.Closed != nil || len(o.Actions) != 0 {
		t.Fatalf("before archive: %+v, %v", o, err)
	}
	res, err := a.Act(t.Context(), execution.ActionRequest{ID: ActionArchive, Target: execution.TargetSession, Session: &ref, Input: json.RawMessage(`{}`)})
	if err != nil || res.Message != "Archived in OpenCode." || !fake.Archived("ses_test") {
		t.Fatalf("Act = %+v, %v (archived %v)", res, err, fake.Archived("ses_test"))
	}
	o, err = a.Observe(t.Context(), ref)
	if err != nil || o.Closed == nil || o.Closed.Signal != execution.ClosedArchived {
		t.Fatalf("after archive: %+v, %v", o, err)
	}
	got := execution.ApplyActionStates([]execution.Action{action}, o.Actions)
	if got[0].Availability.Available || got[0].Availability.Reason != "Already archived in OpenCode." {
		t.Fatalf("archived session availability %+v", got[0].Availability)
	}
	// Repeating is a no-op in OpenCode.
	if _, err := a.Act(t.Context(), execution.ActionRequest{ID: ActionArchive, Target: execution.TargetSession, Session: &ref}); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveActionRejectsWhatOpenCodeCannotRun(t *testing.T) {
	a, fake := archiveFixture(t, true)
	ref := execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_test"}
	var invalid *execution.Invalid
	other := execution.SessionRef{ConnectionID: 3, ExternalSessionID: "ses_test"}
	for _, r := range []execution.ActionRequest{
		{ID: "settle", Target: execution.TargetSession, Session: &ref},
		{ID: ActionArchive, Target: execution.TargetSession, Session: &other},
		{ID: ActionArchive, Target: execution.TargetProject, Project: &execution.ActionWorkspace{Directory: "/repo"}},
	} {
		if _, err := a.Act(t.Context(), r); !errors.As(err, &invalid) {
			t.Errorf("Act(%+v) = %v, want invalid", r, err)
		}
	}
	gone := execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_gone"}
	if _, err := a.Act(t.Context(), execution.ActionRequest{ID: ActionArchive, Target: execution.TargetSession, Session: &gone}); !errors.Is(err, execution.ErrNotFound) {
		t.Errorf("missing session: %v", err)
	}
	if fake.Archived("ses_test") {
		t.Fatal("a rejected request archived the session")
	}
}

func TestArchiveActionUnavailableWithoutSessionUpdate(t *testing.T) {
	a, fake := archiveFixture(t, false)
	action := archiveDeclaration(t, a)
	if action.Availability.Available || action.Availability.Reason != "This OpenCode version cannot archive sessions." {
		t.Fatalf("declaration %+v", action)
	}
	ref := execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_test"}
	if _, err := a.Act(t.Context(), execution.ActionRequest{ID: ActionArchive, Target: execution.TargetSession, Session: &ref}); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("Act without session.update: %v", err)
	}
	if fake.Archived("ses_test") {
		t.Fatal("archived without a declared session.update")
	}
}
