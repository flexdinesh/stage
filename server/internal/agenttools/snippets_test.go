package agenttools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestSnippetTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/snippets")
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var sn db.Snippet
	mustCall(t, cs, "create_snippet", map[string]any{"name": "Review", "body": "review the diff", "projectId": p.ID}, &sn)
	if sn.ID == 0 || sn.Name != "Review" || sn.ProjectID == nil || *sn.ProjectID != p.ID {
		t.Fatalf("create_snippet %+v", sn)
	}
	if msg := call(t, cs, "create_snippet", map[string]any{"name": "Review", "body": "again"}, nil); msg == "" {
		t.Fatal("duplicate name accepted")
	}
	mustCall(t, cs, "update_snippet", map[string]any{"id": sn.ID, "body": "review it"}, &sn)
	if sn.Body != "review it" || sn.Name != "Review" || sn.ProjectID == nil {
		t.Fatalf("update body keeps the rest: %+v", sn)
	}
	mustCall(t, cs, "update_snippet", map[string]any{"id": sn.ID, "projectId": nil}, &sn)
	if sn.ProjectID != nil {
		t.Fatalf("null projectId makes it global: %+v", sn)
	}
	var got db.Snippet
	mustCall(t, cs, "get_snippet", map[string]any{"id": sn.ID}, &got)
	if got.Body != "review it" {
		t.Fatalf("get_snippet %+v", got)
	}
	var list agenttools.SnippetsOut
	mustCall(t, cs, "list_snippets", nil, &list)
	if len(list.Snippets) != 1 || list.Snippets[0].ID != sn.ID {
		t.Fatalf("list_snippets %+v", list)
	}
	var deleted agenttools.DeletedOut
	mustCall(t, cs, "delete_snippet", map[string]any{"id": sn.ID}, &deleted)
	if !deleted.Deleted || deleted.ID != sn.ID {
		t.Fatalf("delete_snippet %+v", deleted)
	}
	if msg := call(t, cs, "get_snippet", map[string]any{"id": sn.ID}, nil); msg == "" {
		t.Fatal("deleted snippet still readable")
	}
	mustCall(t, cs, "list_snippets", nil, &list)
	if list.Snippets == nil || len(list.Snippets) != 0 {
		t.Fatalf("empty list %+v", list)
	}
}

func TestDeleteAttachmentTool(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, &agenttools.Ops{Client: e.client})
	a, err := e.client.Upload(context.Background(), "shot.png", pngBytes)
	if err != nil {
		t.Fatal(err)
	}
	var deleted agenttools.DeletedOut
	mustCall(t, cs, "delete_attachment", map[string]any{"id": a.ID}, &deleted)
	if !deleted.Deleted || deleted.ID != a.ID {
		t.Fatalf("delete_attachment %+v", deleted)
	}
	if _, err := e.store.GetAttachment(context.Background(), a.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("attachment still stored: %v", err)
	}
	if msg := call(t, cs, "delete_attachment", map[string]any{"id": a.ID}, nil); !strings.Contains(strings.ToLower(msg), "not found") {
		t.Fatalf("second delete: %q", msg)
	}
}
