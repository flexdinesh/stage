package agenttools_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestProjectLifecycleTools(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var p db.ProjectSummary
	mustCall(t, cs, "create_project", map[string]any{"title": " Atlas "}, &p)
	if p.ID == 0 || p.Title != "Atlas" || p.ArchivedAt != nil {
		t.Fatalf("create_project %+v", p)
	}
	if msg := call(t, cs, "create_project", map[string]any{"title": ""}, nil); !strings.Contains(msg, "title is required") {
		t.Fatalf("empty title: %q", msg)
	}
	mustCall(t, cs, "update_project", map[string]any{"id": p.ID, "archived": true}, &p)
	if p.Title != "Atlas" || p.ArchivedAt == nil {
		t.Fatalf("archive keeps the title: %+v", p)
	}
	mustCall(t, cs, "update_project", map[string]any{"id": p.ID, "title": "Atlas 2"}, &p)
	if p.Title != "Atlas 2" || p.ArchivedAt == nil {
		t.Fatalf("rename keeps it archived: %+v", p)
	}
	mustCall(t, cs, "update_project", map[string]any{"id": p.ID, "archived": false}, &p)
	if p.ArchivedAt != nil {
		t.Fatalf("unarchive %+v", p)
	}
	if msg := call(t, cs, "update_project", map[string]any{"id": p.ID}, nil); !strings.Contains(msg, "nothing to change") {
		t.Fatalf("empty update: %q", msg)
	}

	var events agenttools.ProjectEventsOut
	mustCall(t, cs, "get_project_events", map[string]any{"id": p.ID}, &events)
	if events.Events == nil || len(events.Events) != 0 {
		t.Fatalf("new project events %+v", events)
	}
	from, err := e.store.InsertProject(context.Background(), db.Project{Title: "Older Atlas"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.MergeProjects(context.Background(), p.ID, from.ID, true); err != nil {
		t.Fatal(err)
	}
	mustCall(t, cs, "get_project_events", map[string]any{"id": from.ID}, &events)
	if len(events.Events) != 1 || events.Events[0].Type != "project_merged" {
		t.Fatalf("merged project events (via alias) %+v", events)
	}
}

func TestMembershipToolsMoveAWorkspace(t *testing.T) {
	e := newEnv(t)
	a := e.project("/w/alpha")
	e.project("/w/beta")
	cs := connect(t, &agenttools.Ops{Client: e.client})
	workspace := e.workspaceID(a.Bindings[0])

	groupOf := func() int64 {
		t.Helper()
		var out agenttools.ProjectsOut
		mustCall(t, cs, "list_projects", nil, &out)
		for _, d := range out.Destinations {
			if d.WorkspaceID == workspace && d.GroupProjectID != nil {
				return *d.GroupProjectID
			}
		}
		t.Fatalf("workspace %s missing: %+v", workspace, out)
		return 0
	}
	source := groupOf()
	var target db.ProjectSummary
	mustCall(t, cs, "create_project", map[string]any{"title": "Shared"}, &target)

	move := map[string]any{"connectionId": a.Bindings[0].ConnectionID, "workspaceId": workspace, "targetProjectId": target.ID}
	var preview db.GroupEdit
	mustCall(t, cs, "preview_membership", move, &preview)
	if preview.Applied || preview.PreviewToken == "" || preview.Source.ID != source || preview.Target == nil || preview.Target.ID != target.ID ||
		preview.Blocked != nil || preview.Workspace.Path != "" {
		t.Fatalf("preview %+v", preview)
	}
	if groupOf() != source {
		t.Fatal("preview changed membership")
	}
	if msg := call(t, cs, "set_membership", move, nil); !strings.Contains(msg, "previewToken") {
		t.Fatalf("missing token: %q", msg)
	}
	stale := map[string]any{"connectionId": a.Bindings[0].ConnectionID, "workspaceId": workspace, "targetProjectId": target.ID, "previewToken": "stale"}
	if msg := call(t, cs, "set_membership", stale, nil); !strings.Contains(msg, "preview changed") {
		t.Fatalf("stale token: %q", msg)
	}
	move["previewToken"] = preview.PreviewToken
	var applied db.GroupEdit
	mustCall(t, cs, "set_membership", move, &applied)
	if !applied.Applied || applied.Target == nil || applied.Target.ID != target.ID || applied.Workspace.Path != "" {
		t.Fatalf("applied %+v", applied)
	}
	if got := groupOf(); got != target.ID {
		t.Fatalf("workspace in group %d, want %d", got, target.ID)
	}

	// Omitting the target separates it into a new group.
	separate := map[string]any{"connectionId": a.Bindings[0].ConnectionID, "workspaceId": workspace}
	mustCall(t, cs, "preview_membership", separate, &preview)
	if preview.Target != nil || preview.NewTitle == "" {
		t.Fatalf("separate preview %+v", preview)
	}
	separate["previewToken"] = preview.PreviewToken
	mustCall(t, cs, "set_membership", separate, &applied)
	if got := groupOf(); !applied.Applied || applied.Target == nil || got != applied.Target.ID || slices.Contains([]int64{source, target.ID}, got) {
		t.Fatalf("separated into %d: %+v", got, applied)
	}
}
