package api_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestTaskWorkspaceChoiceAndLaunchSnapshot(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/primary")
	alt := e.fake.AddProject("Alternative", "/w/alternative")
	bound, err := e.store.BindProject(t.Context(), db.ProjectBinding{
		ProjectID: p.ID, ConnectionID: 1, ExternalProjectID: alt.ID, Workspace: alt.WorkspaceRoot,
	})
	bound = mustValue(t, bound, err)
	idx := slices.IndexFunc(bound.Bindings, func(b db.ProjectBinding) bool { return b.ExternalProjectID == alt.ID })
	if idx < 0 {
		t.Fatalf("alternative fixture missing: %+v", bound.Bindings)
	}
	chosen := bound.Bindings[idx].ID
	e.must("GET", "/api/connections/1/workspaces", nil, 200, nil)
	selector := db.WorkspaceID(1, alt.ID, alt.WorkspaceRoot)
	body := map[string]any{"projectId": p.ID, "connectionId": 1, "workspaceId": selector, "title": "Launch", "prompt": "Reply OK", "start": true}
	var started db.Task
	e.must("POST", "/api/tasks", body, 201, &started)
	if started.BindingID != nil || started.WorkspaceID != selector || started.Session == nil ||
		started.Workspace == nil || started.Workspace.BindingID != chosen || started.Workspace.Path != alt.WorkspaceRoot ||
		started.Workspace.Source != "pinned" || started.Workspace.ExternalID != alt.ID {
		t.Fatalf("started task %+v, workspace %+v", started, started.Workspace)
	}
	dest, err := e.store.LaunchDestination(t.Context(), started.ID, started.LaunchAttempt)
	if err != nil || dest.ExternalProjectID != alt.ID || dest.Workspace != alt.WorkspaceRoot || dest.BindingID != chosen {
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
	var schedule db.Schedule
	e.must("POST", "/api/schedules", map[string]any{"connectionId": 1, "workspaceId": selector, "projectId": p.ID,
		"title": "Run unbound", "prompt": "Reply OK", "cron": "0 2 * * *", "tz": "UTC"}, 201, &schedule)
	var duplicate db.Task
	e.must("POST", taskPath(draft.ID, "/duplicate"), nil, 201, &duplicate)
	if draft.WorkspaceID != selector || duplicate.WorkspaceID != selector || duplicate.BindingID != nil {
		t.Fatalf("draft and duplicate did not preserve the exact unbound choice: %+v, %+v", draft, duplicate)
	}
	if err := e.fake.SetProject(unbound.ID, func(w *execution.WorkspaceInfo) { w.WorkspaceRoot = "/w/moved" }); err != nil {
		t.Fatal(err)
	}
	e.must("POST", taskPath(duplicate.ID, "/start"), nil, 409, nil)
	schedulePath := fmt.Sprintf("/api/schedules/%d", schedule.ID)
	var refused errOut
	e.must("POST", schedulePath+"/run", nil, 409, &refused)
	if refused.Code != "destination_unavailable" {
		t.Fatalf("schedule with an unavailable workspace: %+v", refused)
	}
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

func TestUnresolvedBacklogRequiresExactWorkspaceBeforeLaunch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/primary")
	for _, tc := range []struct {
		name string
		null bool
	}{{"omitted", false}, {"null", true}} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"connectionId": 1, "projectId": p.ID, "title": "Plan " + tc.name, "prompt": "p"}
			if tc.null {
				body["workspaceId"] = nil
			}
			var planned db.Task
			e.must("POST", "/api/tasks", body, 201, &planned)
			if planned.WorkspaceID != "" || planned.BindingID != nil || planned.ProjectID != p.ID ||
				planned.ConnectionID != 1 || planned.State != db.StateBacklog {
				t.Fatalf("unresolved task inherited a destination: %+v", planned)
			}
			checkRequired := func(path string, body any) {
				t.Helper()
				var out errOut
				e.must("POST", path, body, 409, &out)
				if out.Code != "destination_required" || !strings.Contains(out.Error, "Choose a connection and workspace") {
					t.Fatalf("destination guidance: %+v", out)
				}
			}
			checkRequired(taskPath(planned.ID, "/start"), nil)
			body["start"] = true
			checkRequired("/api/tasks", body)
			delete(body, "start")
			body["startLocal"], body["startTz"] = "2999-01-01T00:00", "UTC"
			checkRequired("/api/tasks", body)
			delete(body, "startLocal")
			delete(body, "startTz")
			body["cron"], body["tz"] = "0 2 * * *", "UTC"
			checkRequired("/api/schedules", body)
			if got := e.getTask(planned.ID); got.State != "backlog" || got.LaunchAttempt != 0 {
				t.Fatalf("refused launch changed the task: %+v", got)
			}
		})
	}
	if len(e.t3.launchRequestIDs()) != 0 {
		t.Fatal("planning work launched without a destination")
	}

	var planned db.Task
	e.must("POST", "/api/tasks", map[string]any{"connectionId": 1, "projectId": p.ID, "title": "Repair", "prompt": "p"}, 201, &planned)
	path := taskPath(planned.ID, "")
	e.must("PATCH", path, map[string]any{"startLocal": "2999-01-01T00:00", "startTz": "UTC"}, 409, nil)
	e.must("PATCH", path, map[string]any{"workspaceId": p.workspaceID()}, 200, &planned)
	e.must("POST", path+"/start", nil, 200, &planned)
	if planned.WorkspaceID != p.workspaceID() || planned.State != db.StateRunning || planned.Session == nil {
		t.Fatalf("repaired task did not launch: %+v", planned)
	}
	e.must("PATCH", path, map[string]any{"workspaceId": nil}, 409, nil)
}

func TestConnectionPatchRequiresWorkspaceSelection(t *testing.T) {
	e, _, first, second, projectID := imagesEnv(t)
	firstWorkspace := db.WorkspaceID(first, "repo", hostedWorkspace)
	secondWorkspace := db.WorkspaceID(second, "repo", hostedWorkspace)
	body := map[string]any{"connectionId": first, "workspaceId": firstWorkspace, "projectId": projectID, "title": "Move", "prompt": "p"}
	var draft db.Task
	e.must("POST", "/api/tasks", body, 201, &draft)
	body["cron"], body["tz"] = "0 2 * * *", "UTC"
	var schedule db.Schedule
	e.must("POST", "/api/schedules", body, 201, &schedule)

	draftPath := taskPath(draft.ID, "")
	schedulePath := fmt.Sprintf("/api/schedules/%d", schedule.ID)
	checkPatch := func(path string, body map[string]any, want string) {
		t.Helper()
		var out errOut
		e.must("PATCH", path, body, 409, &out)
		if out.Code != want {
			t.Fatalf("PATCH %s: %+v, want %s", path, out, want)
		}
	}
	config := map[string]any{}
	for _, path := range []string{draftPath, schedulePath} {
		checkPatch(path, map[string]any{"connectionId": second, "executionConfig": config}, "destination_required")
		checkPatch(path, map[string]any{"connectionId": second, "workspaceId": firstWorkspace, "executionConfig": config}, "destination_unavailable")
	}
	checkPatch(schedulePath, map[string]any{"connectionId": second, "workspaceId": nil, "executionConfig": config}, "destination_required")
	e.must("GET", schedulePath, nil, 200, &schedule)
	if schedule.ConnectionID != first || schedule.WorkspaceID != firstWorkspace {
		t.Fatalf("rejected patch moved schedule: %+v", schedule)
	}
	var cleared db.Task
	e.must("PATCH", draftPath, map[string]any{"connectionId": second, "workspaceId": nil, "executionConfig": config}, 200, &cleared)
	if cleared.ConnectionID != second || cleared.WorkspaceID != "" || cleared.BindingID != nil {
		t.Fatalf("connection change populated an omitted workspace: %+v", cleared)
	}
	e.must("POST", draftPath+"/start", nil, 409, nil)
	e.must("PATCH", schedulePath, map[string]any{"connectionId": second, "workspaceId": secondWorkspace, "executionConfig": config}, 200, &schedule)
	if schedule.ConnectionID != second || schedule.WorkspaceID != secondWorkspace || schedule.BindingID != nil {
		t.Fatalf("schedule did not save the exact pair: %+v", schedule)
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
	e.must("GET", fmt.Sprintf("/api/connections/%d/workspaces", connection), nil, 200, nil)
	repo := db.WorkspaceID(connection, "repo", hostedWorkspace)
	alternate := db.WorkspaceID(connection, "alternate", alternateWorkspace)
	other := db.WorkspaceID(otherConnection, "repo", hostedWorkspace)

	alias, err := e.store.InsertProject(t.Context(), db.Project{Title: "Old project link"})
	alias = mustValue(t, alias, err)
	_, err = e.store.MergeProjects(t.Context(), projectID, alias.ID, true)
	mustValue(t, struct{}{}, err)

	capsPath := fmt.Sprintf("/api/connections/%d/capabilities", connection)
	assertModel := func(selector, model string) {
		t.Helper()
		var caps execution.Capabilities
		e.must("GET", capsPath+"?workspaceId="+selector, nil, 200, &caps)
		if len(caps.Models) != 1 || caps.Models[0].ID != model {
			t.Fatalf("models for %s: %+v", selector, caps.Models)
		}
	}
	assertModel(repo, "repo")
	assertModel(alternate, "alternate")
	var unavailable errOut
	e.must("GET", capsPath+"?workspaceId="+other, nil, 409, &unavailable)
	if unavailable.Code != "destination_unavailable" {
		t.Fatalf("foreign capability selector: %+v", unavailable)
	}
	for _, query := range []string{fmt.Sprintf("projectId=%d", alias.ID), "bindingId=1"} {
		msg := e.refused("GET", capsPath+"?workspaceId="+repo+"&"+query, nil, 400)
		if !strings.Contains(msg, "workspaceId") {
			t.Fatalf("retired capability selector %s: %q", query, msg)
		}
	}

	create := map[string]any{"projectId": alias.ID, "connectionId": connection, "workspaceId": alternate, "title": "Look", "prompt": "p"}
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
	if task.ProjectID != projectID || task.BindingID != nil || task.WorkspaceID != alternate {
		t.Fatalf("created task lost the exact workspace: %+v", task)
	}
	taskPath := fmt.Sprintf("/api/tasks/%d", task.ID)
	e.must("PATCH", taskPath, map[string]any{"projectId": alias.ID}, 200, &task)
	if task.ProjectID != projectID || task.WorkspaceID != alternate {
		t.Fatalf("same project alias changed workspace: %+v", task)
	}
	e.must("PATCH", taskPath, map[string]any{"workspaceId": repo, "executionConfig": map[string]any{"model": "repo"}}, 200, &task)
	if task.BindingID != nil || task.WorkspaceID != repo {
		t.Fatalf("reselected task lost the exact workspace: %+v", task)
	}
	e.must("PATCH", taskPath, map[string]any{"attachmentIds": []int64{img.ID}}, 200, nil)
	e.must("PATCH", taskPath, map[string]any{"workspaceId": alternate, "attachmentIds": []int64{img.ID}}, 400, nil)

	create["cron"], create["tz"] = "0 2 * * *", "UTC"
	var schedule db.Schedule
	e.must("POST", "/api/schedules", create, 201, &schedule)
	var schedules struct{ Schedules []db.Schedule }
	e.must("GET", fmt.Sprintf("/api/schedules?project=%d", alias.ID), nil, 200, &schedules)
	if len(schedules.Schedules) != 1 || schedules.Schedules[0].ID != schedule.ID ||
		schedule.ProjectID != projectID || schedule.BindingID != nil || schedule.WorkspaceID != alternate {
		t.Fatalf("schedule %+v, list %+v", schedule, schedules.Schedules)
	}
	var tasks struct{ Tasks []db.Task }
	e.must("GET", fmt.Sprintf("/api/tasks?project=%d", alias.ID), nil, 200, &tasks)
	if len(tasks.Tasks) != 1 || tasks.Tasks[0].ID != task.ID {
		t.Fatalf("tasks listed by alias: %+v", tasks.Tasks)
	}
}

func TestBindingIDRejectedForTaskAndScheduleWrites(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/retired")
	draft := e.task(p.ID, "Draft", nil)
	create := e.taskBody(p.ID, "Other work", nil)
	scheduleBody := e.taskBody(p.ID, "Schedule", map[string]any{"cron": "0 2 * * *", "tz": "UTC"})
	var schedule db.Schedule
	e.must("POST", "/api/schedules", scheduleBody, 201, &schedule)
	for _, req := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/api/tasks", create},
		{"PATCH", taskPath(draft.ID, ""), map[string]any{}},
		{"POST", "/api/schedules", scheduleBody},
		{"PATCH", fmt.Sprintf("/api/schedules/%d", schedule.ID), map[string]any{}},
	} {
		for _, value := range []any{p.Bindings[0].ID, nil} {
			req.body["bindingId"] = value
			msg := e.refused(req.method, req.path, req.body, 400)
			if !strings.Contains(msg, "bindingId") || !strings.Contains(msg, "workspaceId") {
				t.Fatalf("%s %s with bindingId %v: %q", req.method, req.path, value, msg)
			}
		}
	}
}
