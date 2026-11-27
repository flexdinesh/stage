package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

var retiredProjectRoutes = []struct{ method, path, replacement string }{
	{"GET", "/api/shared-paths", "GET /api/connections/{connectionId}/workspaces"},
	{"PUT", "/api/shared-paths", "POST /api/project-groups/membership"},
	{"GET", "/api/connections/1/projects", "GET /api/connections/{connectionId}/workspaces"},
	{"POST", "/api/projects/1/bindings/1/match", "GET /api/connections/{connectionId}/workspaces"},
	{"PUT", "/api/projects/1/binding", "POST /api/project-groups/membership"},
	{"DELETE", "/api/projects/1/binding", "POST /api/project-groups/membership"},
	{"DELETE", "/api/projects/1/bindings/1", "POST /api/project-groups/membership"},
	{"PUT", "/api/projects/1/bindings/1/primary", "connectionId and workspaceId"},
	{"POST", "/api/projects/1/merge", "POST /api/project-groups/membership"},
	{"POST", "/api/projects/1/merge-dismissals", "GET /api/project-groups"},
	{"POST", "/api/projects/1/split", "POST /api/project-groups/membership"},
	{"GET", "/api/workspaces/review", "GET /api/project-groups"},
	{"POST", "/api/connections/1/resolve", "GET /api/connections/{connectionId}/workspaces"},
	{"POST", "/api/connections/1/workspaces/decline", "POST /api/project-groups/membership"},
	{"POST", "/api/connections/1/workspaces/undecline", "POST /api/project-groups/membership"},
}

func TestRemovedProjectRoutes(t *testing.T) {
	e := newEnv(t)
	for _, route := range retiredProjectRoutes {
		var out errOut
		e.must(route.method, route.path, nil, http.StatusGone, &out)
		if out.Code != "removed" || !strings.Contains(out.Error, route.replacement) {
			t.Fatalf("%s %s: %+v", route.method, route.path, out)
		}
	}
}

func TestCreateProjectRejectsBindings(t *testing.T) {
	e := newEnv(t)
	for _, legacy := range []any{nil, []any{}, []any{map[string]any{"connectionId": 1, "externalProjectId": "old", "workspace": "/w/old"}}} {
		msg := e.refused("POST", "/api/projects", map[string]any{"title": "Retired", "bindings": legacy}, http.StatusBadRequest)
		if !strings.Contains(msg, "POST /api/project-groups/membership") {
			t.Fatalf("project binding rejection: %s", msg)
		}
	}
	var projects struct{ Projects []db.Project }
	e.must("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) != 0 {
		t.Fatalf("rejected project create wrote projects: %+v", projects)
	}

	var p db.Project
	e.must("POST", "/api/projects", map[string]string{"title": "New group"}, 201, &p)
	if p.Title != "New group" || len(p.Bindings) != 0 {
		t.Fatalf("title-only project: %+v", p)
	}
	var group db.ProjectGroup
	e.must("GET", fmt.Sprintf("/api/project-groups/%d", p.ID), nil, 200, &group)
	if group.State != "empty" || len(group.Members) != 0 {
		t.Fatalf("title-only group: %+v", group)
	}
}

func TestWorkspaceInventoryProjectsGroupMembershipWithoutBindings(t *testing.T) {
	e := newEnv(t)
	const inventory = "/api/connections/1/workspaces"
	var listed workspaceInventory
	e.must("GET", inventory, nil, 200, &listed)
	if listed.Workspaces == nil || len(listed.Workspaces) != 0 {
		t.Fatalf("empty inventory: %+v", listed)
	}

	first := e.fake.AddProject("A", "/w/a")
	e.must("GET", inventory, nil, 200, &listed)
	selector := db.WorkspaceID(dbtest.T3Connection, first.ID, first.WorkspaceRoot)
	if len(listed.Workspaces) != 1 || listed.Workspaces[0].WorkspaceID != selector ||
		listed.Workspaces[0].BindingID != nil || listed.Workspaces[0].GroupProjectID == nil {
		t.Fatalf("workspace inventory: %+v", listed)
	}
	original := *listed.Workspaces[0].GroupProjectID
	var target db.Project
	e.must("POST", "/api/projects", map[string]string{"title": "New group"}, 201, &target)
	input := db.GroupEditInput{ConnectionID: dbtest.T3Connection, WorkspaceID: selector, TargetProjectID: &target.ID}
	var preview db.GroupEdit
	e.must("POST", "/api/project-groups/membership", input, 200, &preview)
	if preview.Applied || preview.Source.ID != original || preview.PreviewToken == "" {
		t.Fatalf("group preview: %+v", preview)
	}
	input.PreviewToken = preview.PreviewToken
	var applied db.GroupEdit
	e.must("POST", "/api/project-groups/membership", input, 200, &applied)
	if !applied.Applied || applied.Target == nil || applied.Target.ID != target.ID {
		t.Fatalf("group edit: %+v", applied)
	}
	e.must("GET", inventory, nil, 200, &listed)
	if len(listed.Workspaces) != 1 || listed.Workspaces[0].WorkspaceID != selector || listed.Workspaces[0].BindingID != nil ||
		listed.Workspaces[0].GroupProjectID == nil || *listed.Workspaces[0].GroupProjectID != target.ID {
		t.Fatalf("regrouping changed the workspace or created a binding: %+v", listed)
	}
}

func TestLegacyProjectAliasResolvesRelatedAPIRequests(t *testing.T) {
	e := newEnv(t)
	into := e.project("/w/into")
	from := e.project("/w/from")
	alias := fmt.Sprintf("/api/projects/%d", from.ID)
	member := e.task(from.ID, "Existing work", nil)
	var tree db.Tree
	e.must("POST", "/api/trees", map[string]any{
		"title": "Move this tree member", "startMode": "manual", "tasks": []db.TreeMember{{TaskID: member.ID}},
	}, 201, &tree)
	var sn db.Snippet
	e.must("POST", "/api/snippets", map[string]any{"name": "Move this snippet", "body": "body", "projectId": from.ID}, 201, &sn)
	_, err := e.store.MergeProjects(t.Context(), into.ID, from.ID, true)
	mustValue(t, struct{}{}, err)
	var project db.Project
	e.must("GET", alias, nil, 200, &project)
	if project.ID != into.ID {
		t.Fatalf("alias returned %+v", project)
	}
	e.must("GET", fmt.Sprintf("/api/snippets/%d", sn.ID), nil, 200, &sn)
	if sn.ProjectID == nil || *sn.ProjectID != into.ID {
		t.Fatalf("snippet stayed on the merged project: %+v", sn)
	}
	e.must("POST", "/api/snippets", map[string]any{"name": "Using the alias", "body": "body", "projectId": from.ID}, 201, &sn)
	if sn.ProjectID == nil || *sn.ProjectID != into.ID {
		t.Fatalf("alias snippet: %+v", sn)
	}
	var trees struct{ Trees []db.TreeSummary }
	e.must("GET", fmt.Sprintf("/api/trees?all=1&project=%d", from.ID), nil, 200, &trees)
	if len(trees.Trees) != 1 || trees.Trees[0].ID != tree.ID {
		t.Fatalf("alias tree filter: %+v", trees)
	}
	session := launchSession(t, e, from.T3ProjectID, "Adopt from merged workspace")
	var adopted agentTask
	e.must("POST", "/api/connections/1/sessions/"+session+"/adopt", map[string]any{"projectId": from.ID}, 201, &adopted)
	if adopted.ProjectID != into.ID {
		t.Fatalf("alias adoption: %+v", adopted)
	}

	var events struct{ Events []db.ProjectEvent }
	e.must("GET", alias+"/events", nil, 200, &events)
	if !slices.ContainsFunc(events.Events, func(e db.ProjectEvent) bool { return e.Type == "project_merged" }) {
		t.Fatalf("missing merge event on alias: %+v", events)
	}
}
