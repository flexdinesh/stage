package sync

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testAttribution = map[string]harness.Attribution{t3.Kind: t3.Descriptor.Attribution, opencode.Kind: opencode.Descriptor.Attribution}

func TestJournalInstructionsNameEveryInterface(t *testing.T) {
	tree := int64(4)
	openCode := db.Task{ID: 7, TreeID: &tree, ConnectionID: 2, Harness: db.HarnessOpenCode}
	t3 := db.Task{ID: 8, ConnectionID: 1, Harness: db.HarnessT3}
	both := []string{
		"<stage-journal>\n",
		"Use whichever Stage interface you have: Stage MCP tools (names below), the stage CLI",
		"If none is reachable, say so and continue; never install one.",
		"get_context is GET /api/connections/{connectionId}/sessions/{externalSessionId}/context",
		"GET and POST /api/tasks/{id}/journal or /api/trees/{id}/journal",
		"HTTP: pass sourceSession {connectionId, externalSessionId} in the journal POST body.",
		"Before read_journal, use get_context or get_task",
	}
	cases := []struct {
		name, publicURL string
		task            db.Task
		want, not       []string
	}{
		{"opencode without public url", "", openCode,
			[]string{"Stage taskId: 7. TreeId at launch: 4. ConnectionId: 2.\n", "or the Stage server's HTTP API.", "direct OpenCode MCP supplies it per call", "CLI: export STAGE_CALLER_SESSION=2:<session id>, or pass --caller-connection 2 --caller-session ID."},
			[]string{"T3CODE_THREAD_ID", "/mcp)", "http", "connectionId: {connectionId}", "{connectionId}:"}},
		{"t3 with public url", "https://stage.example/", t3,
			[]string{"Stage taskId: 8. TreeId at launch: none. ConnectionId: 1.\n", "or the HTTP API at https://stage.example/api (MCP at https://stage.example/mcp).", "MCP: pass callerSession {connectionId: 1, externalSessionId: $T3CODE_THREAD_ID}", "Stage-Caller-Session: 1:$T3CODE_THREAD_ID", "CLI: export STAGE_CALLER_SESSION=1:$T3CODE_THREAD_ID"},
			[]string{"OpenCode", "the Stage server's HTTP API", "example//", "callerThreadId", "t3-session-env"}},
		{"generic hint for a harness without one", "", db.Task{ID: 9, ConnectionID: 5, Harness: "document"},
			[]string{"MCP: pass callerSession {connectionId: 5, externalSessionId: <your session id>}", "Stage-Caller-Session: 5:<your session id>", "STAGE_CALLER_SESSION=5:<your session id>"},
			[]string{"T3CODE_THREAD_ID", "OpenCode", "connectionId: {connectionId}", "{connectionId}:"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := journalInstructions(tc.task, testAttribution[tc.task.Harness], tc.publicURL, "")
			for _, w := range append(both, tc.want...) {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("unexpected %q in:\n%s", n, got)
				}
			}
			if strings.Contains(got, "—") {
				t.Error("em dash in journal instructions")
			}
		})
	}
}

func TestJournalInstructionsNameInstalledCLI(t *testing.T) {
	got := journalInstructions(db.Task{ID: 1, ConnectionID: 1, Harness: db.HarnessOpenCode}, opencode.Descriptor.Attribution, "", "stage-dev")
	want := "the stage-dev CLI (stage-dev context, stage-dev task show, stage-dev task|tree notes), or the Stage server's HTTP API."
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	if strings.Contains(got, "%!") {
		t.Errorf("format error in:\n%s", got)
	}
}
