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
		"Stage's start mode (auto or manual) and this message's delivery path only decide when and how this session started.",
		"The user watches this session and answers in it",
		"including asking the user when they call for it. Don't treat this launch as unattended.",
	}
	cases := []struct {
		name, publicURL string
		task            db.Task
		want, not       []string
		remote          bool
		gateway         []string
	}{
		{name: "remote machine names its tcp gateway, never the public url", publicURL: "https://stage.example", task: t3, remote: true,
			gateway: []string{"unix:///run/user/1000/stage/stage.sock", "http://127.0.0.1:7381"},
			want:    []string{"or the HTTP API at http://127.0.0.1:7381/api (MCP at http://127.0.0.1:7381/mcp) on this machine's Stage agent gateway."},
			not:     []string{"stage.example", "unix-socket"}},
		{name: "remote machine with only the gateway socket", publicURL: "https://stage.example", task: t3, remote: true,
			gateway: []string{"unix:///run/user/1000/stage/stage.sock"},
			want:    []string{"or the HTTP API on this machine's Stage agent gateway socket /run/user/1000/stage/stage.sock (curl --unix-socket /run/user/1000/stage/stage.sock http://stage/api/...)."},
			not:     []string{"stage.example", "unix://"}},
		{name: "remote machine with no gateway known", publicURL: "https://stage.example", task: t3, remote: true,
			want: []string{"or the HTTP API through this machine's Stage agent gateway (the CLI and its mcp command find it)."},
			not:  []string{"stage.example"}},
		{"opencode without public url", "", openCode,
			[]string{"Stage taskId: 7. TreeId at launch: 4. ConnectionId: 2.\n", "or the Stage server's HTTP API.", "direct OpenCode MCP supplies it per call", "CLI: export STAGE_CALLER_SESSION=2:<session id>, or pass --caller-connection 2 --caller-session ID."},
			[]string{"T3CODE_THREAD_ID", "/mcp)", "http", "connectionId: {connectionId}", "{connectionId}:"}, false, nil},
		{"t3 with public url", "https://stage.example/", t3,
			[]string{"Stage taskId: 8. TreeId at launch: none. ConnectionId: 1.\n", "or the HTTP API at https://stage.example/api (MCP at https://stage.example/mcp).", "MCP: pass callerSession {connectionId: 1, externalSessionId: $T3CODE_THREAD_ID}", "Stage-Caller-Session: 1:$T3CODE_THREAD_ID", "CLI: export STAGE_CALLER_SESSION=1:$T3CODE_THREAD_ID"},
			[]string{"OpenCode", "the Stage server's HTTP API", "example//", "callerThreadId", "t3-session-env", "gateway"}, false, nil},
		{"generic hint for a harness without one", "", db.Task{ID: 9, ConnectionID: 5, Harness: "document"},
			[]string{"MCP: pass callerSession {connectionId: 5, externalSessionId: <your session id>}", "Stage-Caller-Session: 5:<your session id>", "STAGE_CALLER_SESSION=5:<your session id>"},
			[]string{"T3CODE_THREAD_ID", "OpenCode", "connectionId: {connectionId}", "{connectionId}:"}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := journalInstructions(tc.task, testAttribution[tc.task.Harness], httpAPI(tc.publicURL, tc.remote, tc.gateway), "")
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
	got := journalInstructions(db.Task{ID: 1, ConnectionID: 1, Harness: db.HarnessOpenCode}, opencode.Descriptor.Attribution, httpAPI("", false, nil), "stage-dev")
	want := "the stage-dev CLI (stage-dev context, stage-dev task show, stage-dev task|tree notes), or the Stage server's HTTP API."
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	if strings.Contains(got, "%!") {
		t.Errorf("format error in:\n%s", got)
	}
}
