package t3

import (
	"errors"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRenameThreadToolArgs(t *testing.T) {
	f := newFakeT3(t)
	var calls []map[string]any
	f.tool("t3_thread_update", func(args map[string]any) (*mcp.CallToolResult, error) {
		calls = append(calls, args)
		return structured(`{"threadId":"t1","action":"rename","title":"New"}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	if err := c.RenameThread(ctxT(t), "t1", "  New  "); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0]["threadId"] != "t1" || calls[0]["action"] != "rename" || calls[0]["title"] != "New" {
		t.Fatalf("calls = %v", calls)
	}
	id, _ := calls[0]["clientRequestId"].(string)
	if !strings.HasPrefix(id, "stage-rename:") || !retrySafe("t3_thread_update", calls[0]) || retrySafe("t3_thread_update", map[string]any{"action": "rename"}) {
		t.Fatalf("idempotency key %q and retry policy", id)
	}
	if err := c.RenameThread(ctxT(t), "t1", " "); err == nil || len(calls) != 1 {
		t.Fatalf("empty title: %v after %d calls", err, len(calls))
	}
}

func TestAdapterRenamesTheThread(t *testing.T) {
	f := NewFake()
	p := f.AddProject("app", "/w/app")
	launched, err := f.LaunchThread(t.Context(), LaunchInput{ProjectID: p.ID, Title: "Old"})
	if err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(f)
	c, err := a.Features(t.Context())
	if err != nil || !c.Operations[execution.OpSessionRename].Available || c.DeclaresOperations() != nil {
		t.Fatalf("rename declaration: %+v, %v", c.Operations[execution.OpSessionRename], err)
	}
	if _, ok := execution.As[execution.SessionNamer](a); !ok {
		t.Fatal("T3 adapter is no session namer")
	}
	ref := execution.SessionRef{ConnectionID: a.ConnectionID, ExternalSessionID: launched.ThreadID}
	if err := a.RenameSession(t.Context(), ref, "New"); err != nil {
		t.Fatal(err)
	}
	if d, err := f.ReadThread(t.Context(), launched.ThreadID); err != nil || d.Title != "New" {
		t.Fatalf("thread after rename: %q, %v", d.Title, err)
	}
	if err := a.RenameSession(t.Context(), execution.SessionRef{ConnectionID: a.ConnectionID + 1, ExternalSessionID: launched.ThreadID}, "x"); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("another connection's thread: %v", err)
	}
	if len(f.Renamed) != 1 {
		t.Fatalf("renames %+v", f.Renamed)
	}
}
