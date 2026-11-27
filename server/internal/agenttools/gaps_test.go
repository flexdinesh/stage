package agenttools_test

import (
	"encoding/base64"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// httpMCP connects to the HTTP /mcp handler (no local file access).
func httpMCP(t *testing.T, e *env) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(agenttools.HTTPHandler(&agenttools.Ops{Client: e.client}))
	t.Cleanup(srv.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func attachmentIDs(as []db.Attachment) []int64 {
	out := []int64{}
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func TestInlineImagesOverHTTPMCP(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/inline")
	workspace := e.workspaceID(p.Bindings[0])
	cs := httpMCP(t, e)
	data := base64.StdEncoding.EncodeToString(pngBytes)
	named := map[string]any{"name": "shot.png", "mimeType": "image/png", "data": data}
	unnamed := map[string]any{"mimeType": "image/png", "data": data}
	base := func(extra map[string]any) map[string]any {
		args := map[string]any{"project": p.Title, "connectionId": dbtest.T3Connection, "title": "i", "prompt": "see image"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	var tk client.Task
	mustCall(t, cs, "create_task", base(map[string]any{"images": []any{named}}), &tk)
	if len(tk.Attachments) != 1 || tk.Attachments[0].Name != "shot.png" || tk.Attachments[0].MimeType != "image/png" {
		t.Fatalf("create attachments %+v", tk.Attachments)
	}
	stored, err := e.store.GetAttachment(t.Context(), tk.Attachments[0].ID)
	if err != nil || string(stored.Data) != string(pngBytes) {
		t.Fatalf("stored bytes %v %v", stored.Data, err)
	}
	first := tk.Attachments[0].ID

	// images without attachmentIds are added after the current images.
	mustCall(t, cs, "update_task", map[string]any{"id": tk.ID, "images": []any{unnamed}}, &tk)
	if len(tk.Attachments) != 2 || tk.Attachments[0].ID != first || tk.Attachments[1].Name != "image.png" {
		t.Fatalf("appended attachments %+v", tk.Attachments)
	}
	second := tk.Attachments[1].ID
	// attachmentIds is the exact set kept; images follow it.
	mustCall(t, cs, "update_task", map[string]any{"id": tk.ID, "attachmentIds": []int64{second}, "images": []any{named}}, &tk)
	if got := attachmentIDs(tk.Attachments); len(got) != 2 || got[0] != second || slices.Contains(got, first) {
		t.Fatalf("replaced attachments %+v", tk.Attachments)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": tk.ID, "attachmentIds": []int64{}}, &tk)
	if len(tk.Attachments) != 0 {
		t.Fatalf("cleared attachments %+v", tk.Attachments)
	}
	// Staged uploads by id on create.
	att, err := e.client.Upload(t.Context(), "staged.png", pngBytes)
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, cs, "create_task", base(map[string]any{"attachmentIds": []int64{att.ID}, "images": []any{unnamed}}), &tk)
	if got := attachmentIDs(tk.Attachments); len(got) != 2 || got[0] != att.ID {
		t.Fatalf("create with attachmentIds %+v", tk.Attachments)
	}

	nine := make([]any, 9)
	for i := range nine {
		nine[i] = unnamed
	}
	for _, c := range []struct {
		image any
		want  string
	}{
		{map[string]any{"mimeType": "image/png", "data": "not base64!"}, "not valid base64"},
		{map[string]any{"mimeType": "image/jpeg", "data": data}, "not mimeType image/jpeg"},
		{map[string]any{"mimeType": "image/bmp", "data": data}, "unsupported image mimeType"},
		{map[string]any{"mimeType": "image/png", "data": ""}, "image is empty"},
	} {
		if msg := call(t, cs, "create_task", base(map[string]any{"images": []any{c.image}}), nil); !strings.Contains(msg, c.want) {
			t.Fatalf("image %v: %q, want %q", c.image, msg, c.want)
		}
	}
	if msg := call(t, cs, "create_task", base(map[string]any{"images": nine}), nil); !strings.Contains(msg, "at most 8 images") {
		t.Fatalf("nine images: %q", msg)
	}
	if msg := call(t, cs, "update_task", map[string]any{"id": tk.ID, "images": nine[:7]}, nil); !strings.Contains(msg, "at most 8 images") {
		t.Fatalf("2 kept + 7 images: %q", msg)
	}
	if msg := call(t, cs, "update_task", map[string]any{"id": tk.ID, "imagePaths": []string{"/tmp/x.png"}}, nil); !strings.Contains(msg, "local file access") {
		t.Fatalf("imagePaths over HTTP: %q", msg)
	}

	var sc db.Schedule
	mustCall(t, cs, "create_schedule", base(map[string]any{"workspaceId": workspace, "cron": "0 2 * * *", "tz": "UTC", "images": []any{named}}), &sc)
	if len(sc.Attachments) != 1 || sc.Attachments[0].Name != "shot.png" {
		t.Fatalf("schedule attachments %+v", sc.Attachments)
	}
	mustCall(t, cs, "update_schedule", map[string]any{"id": sc.ID, "images": []any{unnamed}}, &sc)
	if len(sc.Attachments) != 2 || sc.Attachments[1].Name != "image.png" {
		t.Fatalf("schedule appended %+v", sc.Attachments)
	}
	mustCall(t, cs, "update_schedule", map[string]any{"id": sc.ID, "attachmentIds": []int64{sc.Attachments[1].ID}}, &sc)
	if len(sc.Attachments) != 1 || sc.Attachments[0].Name != "image.png" {
		t.Fatalf("schedule replaced %+v", sc.Attachments)
	}
}

func TestUpdateClearsExecutionConfig(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/clear")
	workspace := e.workspaceID(p.Bindings[0])
	cs := connect(t, &agenttools.Ops{Client: e.client})
	config := map[string]any{"options": []map[string]any{{"id": "worktree", "value": true}}}

	var tk client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "connectionId": dbtest.T3Connection, "title": "c", "prompt": "x", "executionConfig": config}, &tk)
	mustCall(t, cs, "update_task", map[string]any{"id": tk.ID, "title": "renamed"}, &tk)
	if tk.ExecutionConfig == nil || !tk.ExecutionConfig.Bool("worktree") {
		t.Fatalf("omitted executionConfig changed it: %+v", tk.ExecutionConfig)
	}
	mustCall(t, cs, "update_task", map[string]any{"id": tk.ID, "executionConfig": nil}, &tk)
	if c := tk.ExecutionConfig; c != nil && (c.Bool("worktree") || len(c.Options) != 0) {
		t.Fatalf("null executionConfig kept %+v", c)
	}

	var sc db.Schedule
	mustCall(t, cs, "create_schedule", map[string]any{"project": "1", "connectionId": dbtest.T3Connection, "workspaceId": workspace, "title": "n", "prompt": "x", "cron": "@daily", "tz": "UTC", "executionConfig": config}, &sc)
	if sc.ExecutionConfig == nil || !sc.ExecutionConfig.Bool("worktree") {
		t.Fatalf("schedule config %+v", sc.ExecutionConfig)
	}
	mustCall(t, cs, "update_schedule", map[string]any{"id": sc.ID, "executionConfig": nil}, &sc)
	if c := sc.ExecutionConfig; c != nil && (c.Bool("worktree") || len(c.Options) != 0) {
		t.Fatalf("schedule null executionConfig kept %+v", c)
	}
}

func TestListFilterTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/filters")
	workspace := e.workspaceID(p.Bindings[0])
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var sc db.Schedule
	mustCall(t, cs, "create_schedule", map[string]any{"project": "1", "connectionId": dbtest.T3Connection, "workspaceId": workspace, "title": "nightly", "prompt": "x", "cron": "0 2 * * *", "tz": "UTC"}, &sc)
	var run, plain client.Task
	mustCall(t, cs, "run_schedule", map[string]any{"id": sc.ID}, &run)
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "connectionId": dbtest.T3Connection, "title": "plain", "prompt": "x"}, &plain)

	tasks := func(args map[string]any) []int64 {
		t.Helper()
		var out agenttools.TasksOut
		mustCall(t, cs, "list_tasks", args, &out)
		ids := []int64{}
		for _, row := range out.Tasks {
			ids = append(ids, row.ID)
		}
		slices.Sort(ids)
		return ids
	}
	if got := tasks(map[string]any{"scheduleId": sc.ID}); !slices.Equal(got, []int64{run.ID}) {
		t.Fatalf("scheduleId %v, want [%d]", got, run.ID)
	}
	if got := tasks(map[string]any{"from": "2000-01-01", "to": "2999-01-01T00:00"}); len(got) != 2 {
		t.Fatalf("from/to %v", got)
	}
	if got := tasks(map[string]any{"from": "2999-01-01T00:00:00Z"}); len(got) != 0 {
		t.Fatalf("future from %v", got)
	}
	if got := tasks(map[string]any{"to": "2000-01-01"}); len(got) != 0 {
		t.Fatalf("past to %v", got)
	}
	if got := tasks(map[string]any{"completedFrom": "2000-01-01"}); len(got) != 0 {
		t.Fatalf("completedFrom without acceptance %v", got)
	}
	if msg := call(t, cs, "list_tasks", map[string]any{"from": "yesterday"}, nil); !strings.Contains(msg, "invalid from") {
		t.Fatalf("bad from: %q", msg)
	}
	if msg := call(t, cs, "list_trees", map[string]any{"completedFrom": "soon"}, nil); !strings.Contains(msg, "invalid completedFrom") {
		t.Fatalf("bad tree completedFrom: %q", msg)
	}
	mustCall(t, cs, "list_trees", map[string]any{"completedFrom": "2000-01-01"}, nil)

	schedules := func(dueWithin string) int {
		t.Helper()
		var out agenttools.SchedulesOut
		mustCall(t, cs, "list_schedules", map[string]any{"dueWithin": dueWithin}, &out)
		return len(out.Schedules)
	}
	if n := schedules("48h"); n != 1 {
		t.Fatalf("dueWithin 48h: %d", n)
	}
	if n := schedules("0s"); n != 0 {
		t.Fatalf("dueWithin 0s: %d", n)
	}
	if msg := call(t, cs, "list_schedules", map[string]any{"dueWithin": "soon"}, nil); !strings.Contains(msg, "invalid dueWithin") {
		t.Fatalf("bad dueWithin: %q", msg)
	}

	projects := func(archived string) agenttools.ProjectsOut {
		t.Helper()
		var out agenttools.ProjectsOut
		mustCall(t, cs, "list_projects", map[string]any{"archived": archived}, &out)
		return out
	}
	if out := projects("all"); len(out.Groups) != 1 {
		t.Fatalf("archived all %+v", out.Groups)
	}
	if out := projects("true"); len(out.Groups) != 0 || len(out.Projects) != 0 {
		t.Fatalf("archived true %+v", out)
	}
	if msg := call(t, cs, "list_projects", map[string]any{"archived": "nope"}, nil); !strings.Contains(msg, "invalid archived") {
		t.Fatalf("bad archived: %q", msg)
	}
}
