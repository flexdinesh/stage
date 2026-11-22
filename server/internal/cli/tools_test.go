package cli_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stage tools lists exactly the MCP tools, without a server.
func TestToolsListsTheMCPTools(t *testing.T) {
	h := &harness{t: t, url: "http://127.0.0.1:1"}
	code, stdout, stderr := h.run("", "tools", "--json")
	if code != 0 {
		t.Fatalf("tools: %d %s", code, stderr)
	}
	var names []string
	for _, tool := range decode[[]mcp.Tool](t, stdout) {
		names = append(names, tool.Name)
	}
	server, clientT := mcp.NewInMemoryTransports()
	if _, err := agenttools.NewServer(&agenttools.Ops{Client: client.New(h.url)}).Connect(t.Context(), server, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, tool := range res.Tools {
		want = append(want, tool.Name)
	}
	if !slices.Equal(names, want) || !slices.Contains(names, "append_note") {
		t.Fatalf("stage tools %v, MCP tools %v", names, want)
	}
	code, stdout, _ = h.run("", "tools", "append_note")
	if code != 0 || !strings.Contains(stdout, `"requestId"`) || !strings.Contains(stdout, "Append an immutable note") {
		t.Fatalf("tools append_note: %d %s", code, stdout)
	}
	if code, _, stderr = h.run("", "tools", "nope"); code != 2 || !strings.Contains(stderr, `unknown tool "nope"`) {
		t.Fatalf("unknown tool: %d %s", code, stderr)
	}
}

// stage call runs a tool through the MCP server with the CLI's caller:
// here derived from the harness session variable (T3CODE_THREAD_ID).
func TestCallRunsAToolWithTheHarnessCaller(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/call")
	h.env = map[string]string{"T3CODE_THREAD_ID": "thread-call"}
	code, stdout, stderr := h.run("", "call", "get_context")
	if cx := decode[agenttools.ContextOut](t, stdout); code != 0 || cx.Session == nil || *cx.Session != *t3Session("thread-call") || cx.SessionSource != "T3CODE_THREAD_ID" {
		t.Fatalf("call get_context: %d %s %s", code, stdout, stderr)
	}
	args := fmt.Sprintf(`{"project": "%d", "title": "via call", "prompt": "p"}`, p.ID)
	code, stdout, stderr = h.run(args, "call", "create_task", "-")
	task := decode[client.Task](t, stdout)
	if code != 0 || task.Title != "via call" || task.OriginSession == nil || *task.OriginSession != *t3Session("thread-call") {
		t.Fatalf("call create_task: %d %s %s", code, stdout, stderr)
	}
	// The CLI's own commands derive it too, and explicit flags win.
	code, stdout, _ = h.run("", "context", "--json")
	if cx := decode[agenttools.ContextOut](t, stdout); code != 0 || cx.Session == nil || cx.Session.ExternalSessionID != "thread-call" {
		t.Fatalf("context: %s", stdout)
	}
	code, stdout, _ = h.run("", "context", "--json", "--caller-connection", "1", "--caller-session", "explicit")
	if cx := decode[agenttools.ContextOut](t, stdout); code != 0 || cx.Session == nil || cx.Session.ExternalSessionID != "explicit" || cx.SessionSource != "argument" {
		t.Fatalf("explicit context: %s", stdout)
	}
	// A tool error exits 1 with its message; bad arguments are usage errors.
	if code, _, stderr = h.run("", "call", "get_task", `{"id": 999999}`); code != 1 || !strings.Contains(stderr, "not found") {
		t.Fatalf("tool error: %d %s", code, stderr)
	}
	if code, _, _ = h.run("", "call", "get_task", `[1]`); code != 2 {
		t.Fatalf("non-object arguments: %d", code)
	}
	if code, _, _ = h.run("", "call"); code != 2 {
		t.Fatalf("no tool: %d", code)
	}
}
