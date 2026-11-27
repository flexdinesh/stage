package agenttools_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type exactWorkspaceHarness struct{ callerHarness }

func (*exactWorkspaceHarness) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{
		{ID: "external", Title: "Root one", WorkspaceRoot: "volume://remote/one"},
		{ID: "external", Title: "Root two", WorkspaceRoot: "volume://remote/two"},
	}, nil
}

func (*exactWorkspaceHarness) FeaturesForWorkspace(_ context.Context, _, root string) (execution.Capabilities, error) {
	model := strings.TrimPrefix(root, "volume://remote/")
	return execution.Capabilities{Models: []execution.Model{{Choice: execution.Choice{
		ID: model, Availability: execution.Availability{Available: true},
	}}}}, nil
}

func TestMCPExactDestinations(t *testing.T) {
	e := harnessEnv(t, &exactWorkspaceHarness{})
	p := e.project("/w/origin")
	caller := e.t3(e.session(p, "Planner"))
	if _, err := e.client.Workspaces(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, &agenttools.Ops{Client: e.client})
	var catalog agenttools.ProjectsOut
	mustCall(t, cs, "list_projects", nil, &catalog)
	workspace := map[string]string{}
	for _, d := range catalog.Destinations {
		if d.ConnectionID != 7 {
			continue
		}
		if !d.Available || d.ProjectID != nil || d.WorkspaceID == "" {
			t.Fatalf("expected an unbound destination, got %+v", d)
		}
		workspace[d.Title] = d.WorkspaceID
	}
	one, two := workspace["Root one"], workspace["Root two"]
	if one == "" || two == "" || one == two {
		t.Fatalf("same native ID needs distinct exact selectors: %+v", workspace)
	}
	for selector, want := range map[string]string{one: "one", two: "two"} {
		var models agenttools.ModelsOut
		mustCall(t, cs, "list_models", map[string]any{"connectionId": 7, "workspaceId": selector}, &models)
		if models.Capabilities == nil || len(models.Capabilities.Models) != 1 || models.Capabilities.Models[0].ID != want {
			t.Fatalf("list_models for %s = %+v, want %s", selector, models.Capabilities, want)
		}
	}

	var root client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": fmt.Sprint(p.ID), "title": "Root", "prompt": "plan"}, &root)
	var tree client.TreeDetail
	mustCall(t, cs, "create_tree", map[string]any{"title": "Plan", "tasks": []map[string]any{{"id": root.ID}}}, &tree)
	args := map[string]any{
		"workspaceId": "", "callerSession": caller, "title": "Exact member", "prompt": "work", "treeId": tree.ID,
	}
	if msg := call(t, cs, "create_task", args, nil); !strings.Contains(msg, "workspaceId is empty") {
		t.Fatalf("an empty selector fell back to caller context: %s", msg)
	}
	args["workspaceId"] = one
	if msg := call(t, cs, "create_task", args, nil); !strings.Contains(msg, "workspaceId requires a positive connectionId") {
		t.Fatalf("a workspace selector used the caller's connection: %s", msg)
	}
	args["connectionId"] = 1
	if msg := call(t, cs, "create_task", args, nil); !strings.Contains(msg, "workspace is unavailable") {
		t.Fatalf("the caller connection admitted another connection's workspace: %s", msg)
	}
	args["connectionId"] = 7
	args["executionConfig"] = map[string]any{"model": "one"}
	var task client.Task
	mustCall(t, cs, "create_task", args, &task)
	if task.ConnectionID != 7 || task.WorkspaceID != one || task.ProjectID == 0 || task.ProjectID == p.ID ||
		task.TreeID == nil || *task.TreeID != tree.ID || task.OriginSession == nil || *task.OriginSession != *caller {
		t.Fatalf("exact member: %+v", task)
	}
	var listed agenttools.TasksOut
	mustCall(t, cs, "list_tasks", nil, &listed)
	if !slices.ContainsFunc(listed.Tasks, func(row agenttools.TaskRow) bool { return row.ID == task.ID && row.WorkspaceID == one }) {
		t.Fatalf("list_tasks lost the exact destination: %+v", listed.Tasks)
	}
	var schedule db.Schedule
	mustCall(t, cs, "create_schedule", map[string]any{
		"connectionId": 7, "workspaceId": one, "callerSession": caller, "title": "Exact schedule", "prompt": "work", "cron": "@daily", "tz": "UTC",
	}, &schedule)
	if schedule.WorkspaceID != one || schedule.ProjectID != task.ProjectID || schedule.OriginSession == nil || *schedule.OriginSession != *caller {
		t.Fatalf("exact schedule: %+v", schedule)
	}

	for tool, id := range map[string]int64{"update_task": task.ID, "update_schedule": schedule.ID} {
		var updated struct {
			WorkspaceID string `json:"workspaceId"`
			ProjectID   int64  `json:"projectId"`
		}
		mustCall(t, cs, tool, map[string]any{
			"id": id, "connectionId": 7, "workspaceId": two, "executionConfig": map[string]any{"model": "two"},
		}, &updated)
		if updated.WorkspaceID != two || updated.ProjectID != task.ProjectID {
			t.Fatalf("%s reselect: %+v", tool, updated)
		}
		mustCall(t, cs, tool, map[string]any{"id": id, "title": "Renamed"}, &updated)
		if updated.WorkspaceID != two {
			t.Fatalf("%s lost the pin on an unrelated edit: %+v", tool, updated)
		}
		updated.WorkspaceID = ""
		mustCall(t, cs, tool, map[string]any{"id": id, "workspaceId": nil, "executionConfig": map[string]any{}}, &updated)
		if updated.WorkspaceID != "" || updated.ProjectID != task.ProjectID {
			t.Fatalf("%s clear: %+v", tool, updated)
		}
	}
}
