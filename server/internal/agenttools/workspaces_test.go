package agenttools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// folderAdder adds one kind of workspace: an existing folder.
type folderAdder struct {
	execution.Adapter
	rows     []execution.WorkspaceInfo
	requests []execution.WorkspaceAddRequest
}

func (a *folderAdder) Features(ctx context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{
		Operations: map[execution.Op]execution.Availability{execution.OpWorkspaceAdd: {Available: true}},
		WorkspaceSources: []execution.WorkspaceSource{{ID: "folder", Label: "Existing folder", Availability: execution.Availability{Available: true},
			Input: []execution.Field{{ID: "path", Label: "Folder", Kind: execution.FieldPath, Required: true}}}},
	}, nil
}

func (a *folderAdder) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) { return a.rows, nil }

func (a *folderAdder) AddWorkspace(_ context.Context, r execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	a.requests = append(a.requests, r)
	var in struct{ Path string }
	_ = json.Unmarshal(r.Input, &in)
	info := execution.WorkspaceInfo{ID: in.Path, Title: "added", WorkspaceRoot: in.Path}
	a.rows = append(a.rows, info)
	return execution.WorkspaceAdded{Workspace: info, Message: "added " + in.Path}, nil
}

func TestWorkspaceTools(t *testing.T) {
	adder := &folderAdder{Adapter: t3.NewAdapter(t3.NewFake())}
	e := harnessEnv(t, adder)
	if _, err := testDB.Pool.ExecContext(t.Context(), `UPDATE harness_connections SET active=true WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	// A connection write reloads the store's connection snapshot.
	if err := e.store.SetConnectionIdentity(t.Context(), 7, "opencode-test", "OpenCode"); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, &agenttools.Ops{Client: e.client.AsToolTransport()})

	var sources agenttools.WorkspaceSourcesOut
	mustCall(t, cs, "list_workspace_sources", map[string]any{"connectionId": 7}, &sources)
	if !sources.Availability.Available || len(sources.Sources) != 1 || sources.Sources[0].Input[0].Kind != execution.FieldPath {
		t.Fatalf("sources %+v", sources)
	}
	mustCall(t, cs, "list_workspace_sources", map[string]any{"connectionId": 1}, &sources)
	if !sources.Availability.Available || len(sources.Sources) != 3 || sources.Sources[0].ID != t3.SourceFolder || sources.Sources[1].ID != t3.SourceClone || sources.Sources[2].ID != t3.SourceNew {
		t.Fatalf("T3 sources %+v", sources)
	}

	args := map[string]any{"connectionId": 7, "sourceId": "folder", "input": map[string]string{"path": "/w/added"}}
	if msg := call(t, cs, "add_workspace", args, nil); !strings.Contains(msg, "requestId") {
		t.Fatalf("missing requestId: %q", msg)
	}
	args["requestId"] = "mcp-1"
	args["callerSession"] = map[string]any{"connectionId": 7, "externalSessionId": "caller"}
	var added client.AddedWorkspace
	mustCall(t, cs, "add_workspace", args, &added)
	if added.Workspace.Title != "added" || added.Workspace.Path != "" || added.Message != "" || added.Group.ID == 0 || added.Existing {
		t.Fatalf("add_workspace %+v", added)
	}
	if len(adder.requests) != 1 || adder.requests[0].RequestID != "mcp-1" {
		t.Fatalf("relayed %+v", adder.requests)
	}
	var events agenttools.ProjectEventsOut
	mustCall(t, cs, "get_project_events", map[string]any{"id": added.Group.ID}, &events)
	if len(events.Events) != 1 || events.Events[0].Type != "workspace_added" || events.Events[0].Cause == nil ||
		events.Events[0].Cause.Session == nil || events.Events[0].Cause.Session.ExternalSessionID != "caller" {
		t.Fatalf("events %+v", events.Events)
	}
	if strings.Contains(events.Events[0].Text, "/w/added") {
		t.Fatalf("event text exposes the path: %q", events.Events[0].Text)
	}
}
