package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestRemovedProjectRoutes(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ method, path, replacement string }{
		{"GET", "/api/shared-paths", "GET /api/connections/{connectionId}/workspaces"},
		{"PUT", "/api/shared-paths", "PUT /api/projects/{id}/binding"},
		{"GET", "/api/connections/1/projects", "GET /api/connections/{connectionId}/workspaces"},
		{"POST", "/api/projects/1/bindings/1/match", "POST /api/connections/{connectionId}/resolve"},
		{"DELETE", "/api/projects/1/binding", "DELETE /api/projects/{id}/bindings/{bindingId}"},
	} {
		var out struct{ Code, Error string }
		e.must(tc.method, tc.path, nil, http.StatusGone, &out)
		if out.Code != "removed" || !strings.Contains(out.Error, tc.replacement) {
			t.Fatalf("%s %s: %+v", tc.method, tc.path, out)
		}
	}
}

func TestWorkspaceInventoryReviewAndResolve(t *testing.T) {
	e := newEnv(t)
	const inventory = "/api/connections/1/workspaces"
	const resolve = "/api/connections/1/resolve"
	var listed struct{ Workspaces []db.Workspace }
	e.must("GET", inventory, nil, 200, &listed)
	if listed.Workspaces == nil || len(listed.Workspaces) != 0 {
		t.Fatalf("empty inventory: %+v", listed)
	}

	first := e.fake.AddProject("A", "/w/a")
	var review db.WorkspaceReview
	e.must("GET", "/api/workspaces/review", nil, 200, &review)
	if len(review.Workspaces) != 0 {
		t.Fatalf("review refreshed the harness: %+v", review)
	}
	e.must("GET", inventory, nil, 200, &listed)
	if len(listed.Workspaces) != 1 || listed.Workspaces[0].ExternalID != first.ID || listed.Workspaces[0].ProjectID != nil {
		t.Fatalf("live inventory imported a workspace: %+v", listed)
	}
	e.must("GET", "/api/workspaces/review", nil, 200, &review)
	if review.Counts.New != 1 || review.Attention != 0 {
		t.Fatalf("stored review: %+v", review)
	}
	ref := map[string]any{"externalId": first.ID, "workspace": first.WorkspaceRoot}
	e.must("POST", inventory+"/decline", ref, 204, nil)
	e.must("GET", "/api/workspaces/review", nil, 200, &review)
	if review.Counts.Declined != 1 || review.Attention != 0 {
		t.Fatalf("declined review: %+v", review)
	}
	e.must("POST", inventory+"/undecline", ref, 204, nil)
	e.must("POST", resolve, map[string]any{"apply": true}, 400, nil)

	var preview db.ResolveResult
	e.must("POST", resolve, map[string]any{"apply": false}, 200, &preview)
	if preview.Applied || preview.PreviewToken == "" || len(preview.Placements) != 1 {
		t.Fatalf("resolve preview: %+v", preview)
	}
	second := e.fake.AddProject("B", "/w/b")
	var changed struct {
		Code    string
		Preview db.ResolveResult
	}
	e.must("POST", resolve, map[string]any{"apply": true, "previewToken": preview.PreviewToken}, 409, &changed)
	if changed.Code != "preview_changed" || changed.Preview.Applied || changed.Preview.PreviewToken == "" ||
		changed.Preview.PreviewToken == preview.PreviewToken || len(changed.Preview.Placements) != 2 {
		t.Fatalf("fresh resolve preview: %+v", changed)
	}
	e.must("GET", "/api/workspaces/review", nil, 200, &review)
	if len(review.Workspaces) != 1 || review.Workspaces[0].ExternalID != first.ID {
		t.Fatalf("stale confirmation committed its inventory: %+v", review)
	}
	e.must("GET", inventory, nil, 200, &listed)
	if slices.ContainsFunc(listed.Workspaces, func(w db.Workspace) bool { return w.ProjectID != nil }) {
		t.Fatalf("stale confirmation imported workspaces: %+v", listed)
	}

	var applied db.ResolveResult
	e.must("POST", resolve, map[string]any{"apply": true, "previewToken": changed.Preview.PreviewToken}, 200, &applied)
	if !applied.Applied || applied.Created != 2 {
		t.Fatalf("applied resolution: %+v", applied)
	}
	e.must("GET", inventory, nil, 200, &listed)
	for _, id := range []string{first.ID, second.ID} {
		if !slices.ContainsFunc(listed.Workspaces, func(w db.Workspace) bool {
			return w.ExternalID == id && w.Available && w.ProjectID != nil && w.BindingID != nil && w.Primary
		}) {
			t.Fatalf("workspace %s has no primary binding: %+v", id, listed)
		}
	}
}

func TestProjectBindingRoutesValidateLiveInventoryAndManagePrimary(t *testing.T) {
	e := newEnv(t)
	first := e.fake.AddProject("A", "/w/a")
	pathless := e.fake.AddProject("B", "")
	workspace := func(p t3.Project) map[string]any {
		return map[string]any{"connectionId": 1, "externalProjectId": p.ID, "workspace": p.WorkspaceRoot}
	}

	stale := workspace(pathless)
	stale["workspace"] = "/w/stale"
	e.must("POST", "/api/projects", map[string]any{"title": "Stale", "bindings": []any{workspace(first), stale}}, 409, nil)
	var list struct{ Projects []db.Project }
	e.must("GET", "/api/projects", nil, 200, &list)
	if len(list.Projects) != 0 {
		t.Fatalf("invalid binding left a project: %+v", list)
	}

	var project db.Project
	e.must("POST", "/api/projects", map[string]any{"title": "Both", "bindings": []any{workspace(first), workspace(pathless)}}, 201, &project)
	binding := func(externalID string) db.ProjectBinding {
		t.Helper()
		i := slices.IndexFunc(project.Bindings, func(b db.ProjectBinding) bool { return b.ExternalProjectID == externalID })
		if i < 0 {
			t.Fatalf("missing binding %q in %+v", externalID, project.Bindings)
		}
		return project.Bindings[i]
	}
	a, b := binding(first.ID), binding(pathless.ID)
	if !a.Primary || b.Primary || b.Workspace != "" {
		t.Fatalf("initial bindings: %+v", project.Bindings)
	}
	path := fmt.Sprintf("/api/projects/%d", project.ID)
	e.must("PUT", path+"/binding", stale, 409, nil)
	e.must("PUT", fmt.Sprintf("%s/bindings/%d/primary", path, b.ID), nil, 200, &project)
	if binding(first.ID).Primary || !binding(pathless.ID).Primary {
		t.Fatalf("primary change: %+v", project.Bindings)
	}

	replacement := e.fake.AddProject("C", "/w/c")
	var refused struct{ Code string }
	duplicate := map[string]any{"title": "Duplicate", "bindings": []any{workspace(replacement), workspace(first)}}
	e.must("POST", "/api/projects", duplicate, 409, &refused)
	if refused.Code != "workspace_bound" {
		t.Fatalf("duplicate workspace: %+v", refused)
	}
	e.must("GET", "/api/projects", nil, 200, &list)
	if len(list.Projects) != 1 {
		t.Fatalf("failed create left a project: %+v", list)
	}
	bind := workspace(replacement)
	bind["replaces"] = b.ID
	e.must("PUT", path+"/binding", bind, 200, &project)
	c := binding(replacement.ID)
	if len(project.Bindings) != 2 || !c.Primary || binding(first.ID).Primary {
		t.Fatalf("replacement lost the primary: %+v", project.Bindings)
	}
	e.must("DELETE", fmt.Sprintf("%s/bindings/%d", path, c.ID), nil, 200, &project)
	if len(project.Bindings) != 1 || !binding(first.ID).Primary {
		t.Fatalf("removing primary did not promote the existing binding: %+v", project.Bindings)
	}
	var listed struct{ Workspaces []db.Workspace }
	e.must("GET", "/api/connections/1/workspaces", nil, 200, &listed)
	if !slices.ContainsFunc(listed.Workspaces, func(w db.Workspace) bool {
		return w.ExternalID == replacement.ID && w.Declined && w.BindingID == nil
	}) {
		t.Fatalf("unbound workspace was not declined: %+v", listed)
	}
}

func TestProjectMergePreviewAliasesAndSplit(t *testing.T) {
	e := newEnv(t)
	into := e.project("/w/into")
	from := e.project("/w/from")
	path := fmt.Sprintf("/api/projects/%d", into.ID)
	alias := fmt.Sprintf("/api/projects/%d", from.ID)
	e.must("POST", path+"/merge-dismissals", map[string]any{"otherId": from.ID}, 204, nil)
	e.must("POST", path+"/split", map[string]any{"bindingId": into.Bindings[0].ID}, 409, nil)

	var preview db.MergeResult
	e.must("POST", path+"/merge", map[string]any{"from": from.ID}, 200, &preview)
	if !preview.Preview || preview.Project.ID != into.ID || preview.From.ID != from.ID ||
		preview.Moves.Workspaces != 1 || preview.Blocked != nil || preview.PreviewToken == "" {
		t.Fatalf("merge preview: %+v", preview)
	}
	e.must("POST", path+"/merge", map[string]any{"from": from.ID, "confirm": true}, 400, nil)

	member := e.task(from.ID, "Move this task", nil)
	var tree db.Tree
	e.must("POST", "/api/trees", map[string]any{
		"title": "Move this tree member", "startMode": "manual", "tasks": []db.TreeMember{{TaskID: member.ID}},
	}, 201, &tree)
	var sn db.Snippet
	e.must("POST", "/api/snippets", map[string]any{"name": "Move this snippet", "body": "body", "projectId": from.ID}, 201, &sn)
	var changed struct {
		Code    string
		Preview db.MergeResult
	}
	e.must("POST", path+"/merge", map[string]any{"from": from.ID, "confirm": true, "previewToken": preview.PreviewToken}, 409, &changed)
	if changed.Code != "preview_changed" || !changed.Preview.Preview || changed.Preview.PreviewToken == "" ||
		changed.Preview.PreviewToken == preview.PreviewToken || changed.Preview.Moves.Tasks != 1 || changed.Preview.Moves.Snippets != 1 {
		t.Fatalf("fresh merge preview: %+v", changed)
	}
	var project db.Project
	e.must("GET", alias, nil, 200, &project)
	if project.ID != from.ID {
		t.Fatalf("stale confirmation merged the project: %+v", project)
	}

	var merged db.MergeResult
	e.must("POST", path+"/merge", map[string]any{"from": from.ID, "confirm": true, "previewToken": changed.Preview.PreviewToken}, 200, &merged)
	if merged.Preview || merged.Project.ID != into.ID || len(merged.Project.Bindings) != 2 || len(merged.Project.Aliases) != 1 || merged.Project.Aliases[0].ID != from.ID {
		t.Fatalf("merged project: %+v", merged)
	}
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

	var split db.SplitResult
	e.must("POST", alias+"/split", map[string]any{"bindingId": from.Bindings[0].ID, "title": "Split workspace"}, 201, &split)
	if split.Source.ID != into.ID || len(split.Source.Bindings) != 1 ||
		split.Project.ID == into.ID || split.Project.Title != "Split workspace" ||
		len(split.Project.Bindings) != 1 || split.Project.Bindings[0].ExternalProjectID != from.T3ProjectID {
		t.Fatalf("split result: %+v", split)
	}
	var events struct{ Events []db.ProjectEvent }
	e.must("GET", alias+"/events", nil, 200, &events)
	for _, kind := range []string{"project_merged", "project_split"} {
		if !slices.ContainsFunc(events.Events, func(e db.ProjectEvent) bool { return e.Type == kind }) {
			t.Fatalf("missing %s event for survivor: %+v", kind, events)
		}
	}
}
