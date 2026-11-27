package api_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Two fake hosted harnesses: one offers the images operation, one does not.
var (
	imageKit       = harness.Descriptor{Kind: "image-kit", Name: "Image kit", Sessions: harness.SessionsMachine, LocalProcesses: true}
	textKit        = harness.Descriptor{Kind: "text-kit", Name: "Text kit", Sessions: harness.SessionsMachine, LocalProcesses: true}
	imageHarnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, imageKit, textKit)
)

const textKitReason = "Text kit reads no images."

// imageAdapter is the hosted fake with its images operation (or a
// capabilities error: the harness is offline).
type imageAdapter struct {
	hostedAdapter
	images execution.Availability
	err    error
}

func (a imageAdapter) Features(ctx context.Context) (execution.Capabilities, error) {
	if a.err != nil {
		return execution.Capabilities{}, a.err
	}
	c, err := a.hostedAdapter.Features(ctx)
	c.Operations["images"] = a.images
	return c, err
}

// imagesEnv hosts both kits on the local machine, connected, and a project
// bound to each.
func imagesEnv(t *testing.T) (e *env, local *machine.Local, imageConn, textConn, projectID int64) {
	t.Helper()
	e = newEnv(t, func(d *api.Deps) {
		local = machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
		set := machine.MustSet(local)
		svc := engine.NewForMachines(d.Store, imageHarnesses, set, nil)
		local.OnChange(svc.ConnectionsChanged)
		d.Harnesses, d.Engine = imageHarnesses, svc
	})
	ports := machine.NewStorePorts(e.store.OnMachine(db.LocalMachine), imageHarnesses)
	connect := func(kind string, a execution.Adapter) int64 {
		if _, err := machine.Connect(t.Context(), ports, local.Install, kind, "/home/u/."+kind, "u@"+kind, func(int64) execution.Adapter { return a }); err != nil {
			t.Fatal(err)
		}
		return e.store.ActiveID(db.LocalMachine, kind)
	}
	imageConn = connect(imageKit.Kind, imageAdapter{images: execution.Availability{Available: true}})
	textConn = connect(textKit.Kind, imageAdapter{images: execution.Availability{Reason: textKitReason}})
	p, err := e.store.InsertProject(t.Context(), db.Project{Title: "Kit", Bindings: []db.ProjectBinding{
		{ConnectionID: imageConn, ExternalProjectID: "repo", Workspace: hostedWorkspace},
		{ConnectionID: textConn, ExternalProjectID: "repo", Workspace: hostedWorkspace},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range []int64{imageConn, textConn} {
		if _, err := e.store.RefreshWorkspaces(t.Context(), connection, []execution.WorkspaceInfo{{ID: "repo", WorkspaceRoot: hostedWorkspace}}); err != nil {
			t.Fatal(err)
		}
	}
	return e, local, imageConn, textConn, p.ID
}

// refused sends a request that must fail with want and returns its error.
func (e *env) refused(method, path string, body any, want int) string {
	e.t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if code := e.do(method, path, body, &out); code != want {
		e.t.Fatalf("%s %s: status %d (%q), want %d", method, path, code, out.Error, want)
	}
	return out.Error
}

// Images follow the connection's images operation, not its harness: the
// API takes them where the capabilities offer images and refuses them
// with the operation's reason elsewhere, on create, edit and connection
// change, for tasks and schedules alike.
func TestImagesFollowTheImagesOperation(t *testing.T) {
	e, _, imageConn, textConn, projectID := imagesEnv(t)
	body := func(conn int64, extra map[string]any) map[string]any {
		b := map[string]any{"projectId": projectID, "connectionId": conn, "workspaceId": db.WorkspaceID(conn, "repo", hostedWorkspace), "title": "Look", "prompt": "Describe it", "executionConfig": map[string]any{"model": "m"}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	refusal := func(msg string) {
		t.Helper()
		if !strings.Contains(msg, textKitReason) || strings.Contains(msg, "OpenCode") || strings.Contains(msg, "T3") {
			t.Fatalf("refusal %q does not carry the operation's reason", msg)
		}
	}

	var tk task
	e.must("POST", "/api/tasks", body(imageConn, map[string]any{"attachmentIds": []int64{e.mustUpload("a.png").ID}}), 201, &tk)
	if len(tk.Attachments) != 1 {
		t.Fatalf("attachments %+v", tk.Attachments)
	}
	refusal(e.refused("POST", "/api/tasks", body(textConn, map[string]any{"attachmentIds": []int64{e.mustUpload("b.png").ID}}), 400))

	var sc struct {
		ID          int64        `json:"id"`
		Attachments []attachment `json:"attachments"`
	}
	e.must("POST", "/api/schedules", body(imageConn, map[string]any{"cron": "0 2 * * *", "tz": "UTC", "attachmentIds": []int64{e.mustUpload("c.png").ID}}), 201, &sc)
	if len(sc.Attachments) != 1 {
		t.Fatalf("schedule attachments %+v", sc.Attachments)
	}
	refusal(e.refused("POST", "/api/schedules", body(textConn, map[string]any{"cron": "0 2 * * *", "tz": "UTC", "attachmentIds": []int64{e.mustUpload("d.png").ID}}), 400))

	// Moving images to a connection without the operation is refused; moving
	// without them is not.
	path := fmt.Sprintf("/api/tasks/%d", tk.ID)
	move := map[string]any{"connectionId": textConn, "workspaceId": db.WorkspaceID(textConn, "repo", hostedWorkspace), "executionConfig": map[string]any{"model": "m"}}
	refusal(e.refused("PATCH", path, move, 400))
	refusal(e.refused("PATCH", fmt.Sprintf("/api/schedules/%d", sc.ID), move, 400))
	move["attachmentIds"] = []int64{}
	var moved task
	e.must("PATCH", path, move, 200, &moved)
	if len(moved.Attachments) != 0 {
		t.Fatalf("moved attachments %+v", moved.Attachments)
	}

	// Adding an image where the operation is unavailable is refused.
	refusal(e.refused("PATCH", path, map[string]any{"attachmentIds": []int64{e.mustUpload("e.png").ID}}, 400))
}

// Capabilities that can't be read refuse images, as MCP does: the harness
// error, not a guess. A task without images is still created.
func TestImagesRefusedWhileCapabilitiesAreUnreadable(t *testing.T) {
	e, local, _, textConn, projectID := imagesEnv(t)
	local.Install(textConn, textConn, imageAdapter{err: errors.New("text kit is offline")})
	body := map[string]any{"projectId": projectID, "connectionId": textConn, "title": "Look", "prompt": "Describe it"}
	e.must("POST", "/api/tasks", body, 201, nil)
	body["attachmentIds"] = []int64{e.mustUpload("a.png").ID}
	if msg := e.refused("POST", "/api/tasks", body, 502); !strings.Contains(msg, "text kit is offline") {
		t.Fatalf("refusal %q", msg)
	}
}
