package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
)

// An agent completes another task through its worker gateway: the
// acceptance is attributed to the agent (its caller session and forwarding
// machine) and releases the auto dependent. Completing or reopening its own
// task is refused, directly or through the gateway, before anything else.
func TestAgentCompletesOtherTaskNotOwn(t *testing.T) {
	e := gatewayEnv(t)
	ctx := t.Context()
	j := e.join("laptop")
	p := e.project("/w/agent-accept")
	a := e.task(p.ID, "source", nil)
	b := e.task(p.ID, "dependent", map[string]any{"needs": []int64{a.ID}, "startMode": "auto"})
	worker := e.task(p.ID, "worker", map[string]any{"start": true})
	st, err := e.store.GetTask(ctx, worker.ID)
	if err != nil || st.Session == nil {
		t.Fatalf("worker session: %v", err)
	}
	caller := fmt.Sprintf("%d:%s", st.Session.ConnectionID, st.Session.ExternalSessionID)
	headers := workerHeaders(j.Token)
	headers[agentctx.CallerHeader] = caller

	var errOut struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	var own db.CompletionPreview
	if rec := raw(t, e.h, "GET", fmt.Sprintf("%s/api/tasks/%d/completion-preview", api.GatewayPrefix, worker.ID), headers, nil, &own); rec.Code != 200 {
		t.Fatalf("own preview: %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		action, msg string
		body        db.AcceptanceRequest
	}{
		{"complete", "complete its own task", db.AcceptanceRequest{RequestID: "self", Revision: own.Revision, EvidenceVersion: own.EvidenceVersion}},
		{"reopen", "reopen its own task", db.AcceptanceRequest{RequestID: "self-reopen", Revision: own.Revision}},
	} {
		path := fmt.Sprintf("/api/tasks/%d/%s", worker.ID, c.action)
		if rec := raw(t, e.h, "POST", api.GatewayPrefix+path, headers, c.body, &errOut); rec.Code != 403 || errOut.Code != "self_action_forbidden" || !strings.Contains(errOut.Error, c.msg) {
			t.Fatalf("gateway self %s: %d %s", c.action, rec.Code, rec.Body)
		}
		direct := map[string]string{agentctx.CallerHeader: caller}
		if rec := raw(t, e.h, "POST", path, direct, c.body, &errOut); rec.Code != 403 || errOut.Code != "self_action_forbidden" {
			t.Fatalf("direct self %s: %d %s", c.action, rec.Code, rec.Body)
		}
	}

	var preview db.CompletionPreview
	if rec := raw(t, e.h, "GET", fmt.Sprintf("%s/api/tasks/%d/completion-preview", api.GatewayPrefix, a.ID), headers, nil, &preview); rec.Code != 200 {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	stale := db.AcceptanceRequest{RequestID: "stale", Revision: preview.Revision, EvidenceVersion: "stale"}
	if rec := raw(t, e.h, "POST", fmt.Sprintf("%s/api/tasks/%d/complete", api.GatewayPrefix, a.ID), headers, stale, &errOut); rec.Code != 409 || !strings.Contains(errOut.Error, "Evidence changed") {
		t.Fatalf("stale evidence: %d %s", rec.Code, rec.Body)
	}
	var result db.AcceptanceResult
	request := db.AcceptanceRequest{RequestID: "agent-accept", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}
	if rec := raw(t, e.h, "POST", fmt.Sprintf("%s/api/tasks/%d/complete", api.GatewayPrefix, a.ID), headers, request, &result); rec.Code != 200 || result.AcceptanceID == nil {
		t.Fatalf("agent complete: %d %s", rec.Code, rec.Body)
	}
	acceptances, err := e.store.Acceptances(ctx, a.ID)
	if err != nil || len(acceptances) != 1 {
		t.Fatalf("acceptances %+v %v", acceptances, err)
	}
	stamp := acceptances[0].Attribution
	if stamp.Actor.Kind != "agent" || !machineIs(stamp.SourceMachine, j.MachineID) || stamp.Cause == nil ||
		stamp.Cause.Session == nil || *stamp.Cause.Session != *st.Session || stamp.Cause.RequestID != "agent-accept" {
		t.Fatalf("acceptance attribution %+v", stamp)
	}
	if err := e.runner.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.getTask(b.ID); got.LaunchAttempt != 1 {
		t.Fatalf("agent acceptance did not release the auto dependent: %+v", got)
	}
}
