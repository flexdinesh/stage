package api_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestTaskWorkspaceChoiceAndLaunchSnapshot(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/primary")
	alt := e.fake.AddProject("Alternative", "/w/alternative")
	e.must("PUT", fmt.Sprintf("/api/projects/%d/binding", p.ID),
		map[string]any{"connectionId": 1, "externalProjectId": alt.ID, "workspace": alt.WorkspaceRoot}, 200, &p)
	idx := slices.IndexFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.ExternalProjectID == alt.ID })
	if idx < 0 {
		t.Fatalf("alternative binding missing: %+v", p.Bindings)
	}
	chosen := p.Bindings[idx].ID
	body := map[string]any{"projectId": p.ID, "title": "Launch", "prompt": "Reply OK", "start": true, "bindingId": chosen}
	var started db.Task
	e.must("POST", "/api/tasks", body, 201, &started)
	if started.BindingID == nil || *started.BindingID != chosen || started.Session == nil ||
		started.Workspace == nil || started.Workspace.BindingID != chosen || started.Workspace.Path != alt.WorkspaceRoot ||
		started.Workspace.Source != "pinned" || started.Workspace.ExternalID != alt.ID {
		t.Fatalf("started task %+v, workspace %+v", started, started.Workspace)
	}
	dest, err := e.store.LaunchDestination(t.Context(), started.ID, started.LaunchAttempt)
	if err != nil || dest.ExternalProjectID != alt.ID || dest.Workspace != alt.WorkspaceRoot {
		t.Fatalf("launch destination %+v, %v", dest, err)
	}
	var listed struct{ Tasks []db.Task }
	e.must("GET", fmt.Sprintf("/api/tasks?project=%d", p.ID), nil, 200, &listed)
	if len(listed.Tasks) != 1 || listed.Tasks[0].Workspace == nil || *listed.Tasks[0].Workspace != *started.Workspace {
		t.Fatalf("task list %+v", listed.Tasks)
	}
}

func TestExactUnboundWorkspaceSurvivesEditAndDuplicate(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/group")
	unbound := e.fake.AddProject("Unbound", "/w/unbound")
	var catalog workspaceInventory
	e.must("GET", "/api/connections/1/workspaces", nil, 200, &catalog)
	selector := db.WorkspaceID(1, unbound.ID, unbound.WorkspaceRoot)
	var draft db.Task
	e.must("POST", "/api/tasks", map[string]any{"connectionId": 1, "workspaceId": selector, "projectId": p.ID,
		"title": "Draft", "prompt": "Reply OK"}, 201, &draft)
	if draft.ExecutionConfig == nil || draft.ExecutionConfig.Options == nil {
		t.Fatal("default execution options must serialize as an array")
	}
	var detail db.Task
	e.must("GET", taskPath(draft.ID, ""), nil, 200, &detail)
	if detail.ExecutionConfig == nil || detail.ExecutionConfig.Options == nil {
		t.Fatal("task detail must return an array for default execution options")
	}
	e.must("PATCH", taskPath(draft.ID, ""), map[string]string{"title": "Ready to start"}, 200, &draft)
	var duplicate db.Task
	e.must("POST", taskPath(draft.ID, "/duplicate"), nil, 201, &duplicate)
	if draft.WorkspaceID != selector || duplicate.WorkspaceID != selector || duplicate.BindingID != nil {
		t.Fatalf("draft and duplicate did not preserve the exact unbound choice: %+v, %+v", draft, duplicate)
	}
	if err := e.fake.SetProject(unbound.ID, func(w *execution.WorkspaceInfo) { w.WorkspaceRoot = "/w/moved" }); err != nil {
		t.Fatal(err)
	}
	e.must("POST", taskPath(duplicate.ID, "/start"), nil, 409, nil)
	if len(e.t3.launchRequestIDs()) != 0 {
		t.Fatal("an unavailable selection fell back to the group workspace")
	}
	reselected := db.WorkspaceID(1, unbound.ID, "/w/moved")
	e.must("PATCH", taskPath(duplicate.ID, ""), map[string]any{"workspaceId": reselected}, 200, &duplicate)
	e.must("POST", taskPath(duplicate.ID, "/start"), nil, 200, &duplicate)
	frozen, err := e.store.LaunchDestination(t.Context(), duplicate.ID, duplicate.LaunchAttempt)
	if err != nil || duplicate.WorkspaceID != reselected || frozen.BindingID != 0 || frozen.Workspace != "/w/moved" {
		t.Fatalf("reselected launch destination %+v, %v", frozen, err)
	}
}

func TestExplicitUnresolvedBacklogDoesNotInheritThePrimary(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/primary")
	body := map[string]any{"connectionId": 1, "projectId": p.ID, "workspaceId": nil, "title": "Plan", "prompt": "p"}
	var planned db.Task
	e.must("POST", "/api/tasks", body, 201, &planned)
	if planned.WorkspaceID != "" || planned.BindingID != nil || planned.ProjectID != p.ID || planned.State != db.StateBacklog {
		t.Fatalf("unresolved task inherited a destination: %+v", planned)
	}
	e.must("POST", taskPath(planned.ID, "/start"), nil, 409, nil)
	body["start"] = true
	e.must("POST", "/api/tasks", body, 409, nil)
	delete(body, "start")
	body["cron"], body["tz"] = "0 2 * * *", "UTC"
	e.must("POST", "/api/schedules", body, 409, nil)
	if len(e.t3.launchRequestIDs()) != 0 {
		t.Fatal("planning work launched without a destination")
	}
}

func TestDuplicateDraftPinsVisibleWorkspaceAndCopiesSelectedImages(t *testing.T) {
	e, _, connection, _, projectID := imagesEnv(t)
	var source db.Task
	a, b, staged := e.mustUpload("keep.png"), e.mustUpload("leave.png"), e.mustUpload("new.png")
	input := map[string]any{"connectionId": connection, "projectId": projectID, "title": "Original", "prompt": "p", "attachmentIds": []int64{a.ID, b.ID}}
	e.must("POST", "/api/tasks", input, 201, &source)
	input["workspaceId"] = db.WorkspaceID(connection, "repo", hostedWorkspace)
	input["title"], input["prompt"] = "Edited copy", "New prompt"
	input["attachmentIds"] = []int64{a.ID, staged.ID}
	var duplicate db.Task
	e.must("POST", taskPath(source.ID, "/duplicate"), input, 201, &duplicate)
	if duplicate.Title != "Edited copy" || duplicate.Prompt != "New prompt" || duplicate.WorkspaceID != input["workspaceId"] || duplicate.BindingID != nil || duplicate.ID == source.ID {
		t.Fatalf("duplicate did not save the draft: %+v", duplicate)
	}
	images, err := e.store.TaskAttachments(t.Context(), duplicate.ID)
	if err != nil || len(images) != 2 || !slices.ContainsFunc(images, func(image db.AttachmentData) bool {
		return image.Name == a.Name && image.ID != a.ID
	}) || !slices.ContainsFunc(images, func(image db.AttachmentData) bool { return image.ID == staged.ID }) {
		t.Fatalf("duplicate images = %+v, %v", images, err)
	}
	originalImages, err := e.store.TaskAttachments(t.Context(), source.ID)
	if err != nil || len(originalImages) != 2 || originalImages[0].ID != a.ID || originalImages[1].ID != b.ID {
		t.Fatalf("source images changed: %+v, %v", originalImages, err)
	}
	input["attachmentIds"] = []int64{staged.ID}
	e.must("POST", taskPath(source.ID, "/duplicate"), input, 400, nil)
}

func TestLegacyProjectCannotBorrowSharedDefault(t *testing.T) {
	e, _, preferred, other, projectID := imagesEnv(t)
	if _, err := e.store.SetDefaultConnection(t.Context(), &preferred); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"projectId": projectID, "title": "Choose a destination", "prompt": "p"}
	var refused errOut
	e.must("POST", "/api/tasks", in, 409, &refused)
	if refused.Code != "destination_required" {
		t.Fatalf("legacy selector repair = %+v", refused)
	}
	in["connectionId"] = other
	in["workspaceId"] = db.WorkspaceID(preferred, "repo", hostedWorkspace)
	e.must("POST", "/api/tasks", in, 409, &refused)
	if refused.Code != "destination_unavailable" {
		t.Fatalf("foreign connection selector = %+v", refused)
	}
}

const alternateWorkspace = "/home/u/alternate"

type scopedWorkspaceAdapter struct{ hostedAdapter }

func (scopedWorkspaceAdapter) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{
		{ID: "repo", WorkspaceRoot: hostedWorkspace},
		{ID: "alternate", WorkspaceRoot: alternateWorkspace},
	}, nil
}

func (scopedWorkspaceAdapter) FeaturesForWorkspace(_ context.Context, external, directory string) (execution.Capabilities, error) {
	return execution.Capabilities{
		Operations: map[execution.Op]execution.Availability{
			"images": {Available: directory == hostedWorkspace, Reason: "This workspace does not read images."},
		},
		Models: []execution.Model{{Choice: execution.Choice{ID: external, Availability: execution.Availability{Available: true}}}},
	}, nil
}

func TestCapabilitiesAndConfigFollowWorkspaceChoice(t *testing.T) {
	e, local, connection, otherConnection, projectID := imagesEnv(t)
	local.Install(connection, connection, scopedWorkspaceAdapter{})
	var p db.Project
	e.must("PUT", fmt.Sprintf("/api/projects/%d/binding", projectID),
		map[string]any{"connectionId": connection, "externalProjectId": "alternate", "workspace": alternateWorkspace}, 200, &p)
	bindingAt := func(path string, connection int64) int64 {
		t.Helper()
		idx := slices.IndexFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.Workspace == path && b.ConnectionID == connection })
		if idx < 0 {
			t.Fatalf("binding for %s on %d missing: %+v", path, connection, p.Bindings)
		}
		return p.Bindings[idx].ID
	}
	alternate := bindingAt(alternateWorkspace, connection)
	other := bindingAt(hostedWorkspace, otherConnection)

	var alias db.Project
	e.must("POST", "/api/projects", map[string]any{"title": "Old project link"}, 201, &alias)
	mergePath := fmt.Sprintf("/api/projects/%d/merge", p.ID)
	var preview db.MergeResult
	e.must("POST", mergePath, map[string]any{"from": alias.ID}, 200, &preview)
	e.must("POST", mergePath, map[string]any{"from": alias.ID, "confirm": true, "previewToken": preview.PreviewToken}, 200, nil)

	capsPath := fmt.Sprintf("/api/connections/%d/capabilities?projectId=%d", connection, alias.ID)
	assertModel := func(url, model string) {
		t.Helper()
		var caps execution.Capabilities
		e.must("GET", url, nil, 200, &caps)
		if len(caps.Models) != 1 || caps.Models[0].ID != model {
			t.Fatalf("models on %s: %+v", url, caps.Models)
		}
	}
	assertModel(capsPath, "repo")
	assertModel(fmt.Sprintf("%s&bindingId=%d", capsPath, alternate), "alternate")
	const invalid = "bindingId must be an active workspace of this project on the chosen connection"
	if msg := e.refused("GET", fmt.Sprintf("%s&bindingId=%d", capsPath, other), nil, 400); msg != invalid {
		t.Fatalf("foreign connection binding: %q", msg)
	}

	create := map[string]any{"projectId": alias.ID, "connectionId": connection, "bindingId": alternate, "title": "Look", "prompt": "p"}
	img := e.mustUpload("workspace.png")
	create["attachmentIds"] = []int64{img.ID}
	if msg := e.refused("POST", "/api/tasks", create, 400); msg != "Images are unavailable on this connection: This workspace does not read images. Remove them or choose another connection." {
		t.Fatalf("chosen workspace image check: %q", msg)
	}
	delete(create, "attachmentIds")
	create["executionConfig"] = map[string]any{"model": "repo"}
	e.must("POST", "/api/tasks", create, 400, nil)
	create["executionConfig"] = map[string]any{"model": "alternate"}
	var task db.Task
	e.must("POST", "/api/tasks", create, 201, &task)
	if task.ProjectID != p.ID || task.BindingID == nil || *task.BindingID != alternate || task.Workspace == nil || task.Workspace.Source != "chosen" {
		t.Fatalf("created task %+v, workspace %+v", task, task.Workspace)
	}
	taskPath := fmt.Sprintf("/api/tasks/%d", task.ID)
	e.must("PATCH", taskPath, map[string]any{"projectId": alias.ID}, 200, &task)
	if task.BindingID == nil || *task.BindingID != alternate {
		t.Fatalf("same project alias cleared binding: %+v", task)
	}
	e.must("PATCH", taskPath, map[string]any{"bindingId": nil, "executionConfig": map[string]any{"model": "repo"}}, 200, &task)
	if task.BindingID != nil || task.Workspace == nil || task.Workspace.Source != "primary" {
		t.Fatalf("cleared task choice %+v, workspace %+v", task, task.Workspace)
	}
	e.must("PATCH", taskPath, map[string]any{"attachmentIds": []int64{img.ID}}, 200, nil)
	e.must("PATCH", taskPath, map[string]any{"bindingId": alternate}, 400, nil)

	create["cron"], create["tz"] = "0 2 * * *", "UTC"
	var schedule db.Schedule
	e.must("POST", "/api/schedules", create, 201, &schedule)
	var schedules struct{ Schedules []db.Schedule }
	e.must("GET", fmt.Sprintf("/api/schedules?project=%d", alias.ID), nil, 200, &schedules)
	if len(schedules.Schedules) != 1 || schedules.Schedules[0].ID != schedule.ID ||
		schedule.ProjectID != p.ID || schedule.BindingID == nil || *schedule.BindingID != alternate {
		t.Fatalf("schedule %+v, list %+v", schedule, schedules.Schedules)
	}
	var tasks struct{ Tasks []db.Task }
	e.must("GET", fmt.Sprintf("/api/tasks?project=%d", alias.ID), nil, 200, &tasks)
	if len(tasks.Tasks) != 1 || tasks.Tasks[0].ID != task.ID {
		t.Fatalf("tasks listed by alias: %+v", tasks.Tasks)
	}
}
