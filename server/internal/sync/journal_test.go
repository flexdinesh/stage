package sync

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

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
			[]string{"Stage taskId: 7. TreeId at launch: 4. ConnectionId: 2.\n", "or the Stage server's HTTP API.", "direct OpenCode MCP supplies it per call", "CLI: pass --caller-connection and --caller-session."},
			[]string{"T3CODE_THREAD_ID", "/mcp)", "http"}},
		{"t3 with public url", "https://stage.example/", t3,
			[]string{"Stage taskId: 8. TreeId at launch: none. ConnectionId: 1.\n", "or the HTTP API at https://stage.example/api (MCP at https://stage.example/mcp).", "MCP: pass callerThreadId from T3CODE_THREAD_ID", "CLI: reads T3CODE_THREAD_ID, or pass --caller-connection"},
			[]string{"OpenCode", "the Stage server's HTTP API", "example//"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := journalInstructions(tc.task, "T3CODE_THREAD_ID", tc.publicURL, "")
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
	got := journalInstructions(db.Task{ID: 1, ConnectionID: 1, Harness: db.HarnessOpenCode}, "", "", "stage-dev")
	want := "the stage-dev CLI (stage-dev context, stage-dev task show, stage-dev task|tree notes), or the Stage server's HTTP API."
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	if strings.Contains(got, "%!") {
		t.Errorf("format error in:\n%s", got)
	}
}
