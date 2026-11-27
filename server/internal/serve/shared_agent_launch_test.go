package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type reportBackT3 struct {
	*t3.Fake
	mu     sync.Mutex
	prompt string
}

func (f *reportBackT3) LaunchThread(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompt = in.Message
	return f.Fake.LaunchThread(ctx, in)
}

func TestSharedLocalHarnessLaunchAndReportBack(t *testing.T) {
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	c := cfg(t, filepath.Join(short, "data"), upstream, "unix")
	c.PublicURL = proxy.URL
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("STAGE_DATA_DIR", c.DataDir)
	for _, key := range []string{
		"STAGE_URL", "STAGE_AGENT_URL", agentctx.CallerEnv, "T3CODE_THREAD_ID", "CODEX_THREAD_ID",
		"OPENCODE_SESSION_ID", claudecode.SessionEnv, "CLAUDE_CODE_SESSION_KIND", "CLAUDE_CODE_ENTRYPOINT", "STAGE_AGENT_CREDENTIAL_FILE",
	} {
		t.Setenv(key, "")
	}

	t3Fake := &reportBackT3{Fake: t3.NewFake()}
	t3Dir := t.TempDir()
	t3Project := t3Fake.AddProject("T3 project", t3Dir)
	claudeFake := claudetest.New(t)
	codexFake := codextest.New(t)
	codexDir := t.TempDir()
	codexFake.Trust(codexDir, "trusted")
	openCodeDir := t.TempDir()
	openCodeURL, openCodePrompt := openCodeLaunchFixture(t, openCodeDir)

	output := &syncBuffer{}
	c.MultiUser = new(true)
	i, _ := startSharedOwner(t, serve.Options{
		Config: c, BootstrapOutput: output,
		Profile: harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true},
		HarnessOverrides: map[string]any{
			t3.Kind: t3Fake,
			claudecode.Kind: claudecodesetup.Options{
				Home: claudeFake.Home, Environ: claudeFake.Environ(),
				LookPath: func(string) (string, error) { return claudeFake.Bin, nil },
			},
			codex.Kind: codexsetup.Options{Daemon: func(string) codex.Daemon {
				return &codextest.Daemon{Srv: codexFake, T: t}
			}},
		},
	})
	agentPath, err := config.AgentSocket(os.Getenv, i.ready.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	agentURL := "unix://" + agentPath
	if i.ready.AgentURL != agentURL {
		t.Fatalf("ready agent URL %q, want %q", i.ready.AgentURL, agentURL)
	}
	for _, l := range i.ready.Listeners {
		if l.URL() == agentURL {
			t.Fatal("agent socket advertised as a product listener")
		}
	}
	info, err := os.Lstat(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("agent socket mode %s, want owner-only Unix socket", info.Mode())
	}
	_, holder, err := instance.ReadHolder(i.ready.DataDir)
	if err != nil || holder == nil || strings.Contains(strings.Join(holder.Listeners, "\n"), agentPath) {
		t.Fatalf("product lock reports the wrong listeners: %+v, %v", holder, err)
	}
	agent := client.New(agentURL)
	agent.HTTP.Timeout = 10 * time.Second
	(transportRequest{method: "GET", path: "/api/tasks"}).send(t, agent.HTTP, "http://stage", 403, nil)
	owner := browserClient(t, proxy)
	headers := claimBrowser(t, owner, proxy.URL, output.String())

	t.Run("transport scope", func(t *testing.T) {
		(transportRequest{method: "GET", path: "/api/tasks"}).send(t, agent.HTTP, "http://stage", 200, nil)
		for _, path := range []string{"/api/me", "/api/admin/accounts"} {
			(transportRequest{method: "GET", path: path}).send(t, agent.HTTP, "http://stage", 403, nil)
		}
		for _, path := range []string{"/", "/api/worker/machine", api.GatewayPrefix + "/api/tasks"} {
			(transportRequest{method: "GET", path: path}).send(t, agent.HTTP, "http://stage", 404, nil)
		}
		(transportRequest{method: "GET", path: "/api/tasks", origin: proxy.URL}).send(t, agent.HTTP, "http://stage", 403, nil)
		cookies := (&http.Response{Header: headers}).Cookies()
		if len(cookies) != 1 {
			t.Fatal("owner claim did not issue a browser session")
		}
		(transportRequest{method: "GET", path: "/api/tasks", headers: http.Header{"Cookie": {cookies[0].Name + "=" + cookies[0].Value}}}).send(t, agent.HTTP, "http://stage", 403, nil)
		claims := http.Header{agentctx.CallerHeader: {"1:untrusted"}, "Stage-Action-Agent": {"1"}}
		(transportRequest{method: "GET", path: "/api/tasks", headers: claims}).send(t, proxy.Client(), proxy.URL, 401, nil)
		(transportRequest{method: "POST", path: "/mcp", headers: claims, body: "{}"}).send(t, proxy.Client(), proxy.URL, 401, nil)
		product := clientFor(i.ready.Listeners[1])
		(transportRequest{method: "GET", path: "/api/tasks", headers: claims}).send(t, product.HTTP, "http://stage", 401, nil)
	})

	for _, tc := range []struct {
		kind, external, workspace, callerEnv, connect, address string
		prompt                                                 func(*testing.T, string) string
	}{
		{kind: t3.Kind, external: t3Project.ID, workspace: t3Dir, callerEnv: "T3CODE_THREAD_ID",
			prompt: func(*testing.T, string) string {
				t3Fake.mu.Lock()
				defer t3Fake.mu.Unlock()
				return t3Fake.prompt
			}},
		{kind: claudecode.Kind, external: claudeFake.Workspace, workspace: claudeFake.Workspace,
			callerEnv: claudecode.SessionEnv, connect: claudecode.ActionConnect, address: "env",
			prompt: func(t *testing.T, _ string) string {
				launches := claudeFake.Launches()
				if len(launches) != 1 {
					t.Fatalf("Claude launched %d times", len(launches))
				}
				args := launches[0].Args
				return args[len(args)-1]
			}},
		{kind: codex.Kind, external: codexDir, workspace: codexDir,
			callerEnv: "CODEX_THREAD_ID", connect: codex.ActionConnect, address: "flag",
			prompt: func(t *testing.T, session string) string {
				thread := codexFake.Thread(session)
				if thread == nil || len(thread.Turns) != 1 {
					t.Fatalf("Codex launch thread %+v, want one turn", thread)
				}
				return thread.Turns[0].Prompt
			}},
		{kind: opencode.Kind, external: "test-project", workspace: openCodeDir, callerEnv: "OPENCODE_SESSION_ID", prompt: openCodePrompt},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			harnessPath := "/api/harnesses/" + tc.kind
			if tc.connect != "" {
				browserPost(t, owner, proxy.URL, harnessPath+"/actions/"+tc.connect, "{}", 200, nil)
			}
			if tc.kind == opencode.Kind {
				(transportRequest{method: "PUT", path: harnessPath + "/settings", origin: proxy.URL,
					body: fmt.Sprintf(`{"endpoint":%q}`, openCodeURL)}).send(t, owner, proxy.URL, 200, nil)
			}
			var status client.HarnessStatus
			(transportRequest{method: "GET", path: harnessPath + "/status"}).send(t, owner, proxy.URL, 200, &status)
			if status.ConnectionID <= 0 || !status.Connected {
				t.Fatalf("harness connection: %+v", status)
			}
			workspace := fixtureWorkspace(t, owner, proxy.URL, status.ConnectionID, tc.external, tc.workspace)
			var project db.Project
			browserPost(t, owner, proxy.URL, "/api/projects", fmt.Sprintf(`{"title":%q}`, tc.kind+" project"), http.StatusCreated, &project)
			const prompt = "Run the integration test and record its outcome."
			var task client.Task
			browserPost(t, owner, proxy.URL, "/api/tasks",
				fmt.Sprintf(`{"projectId":%d,"connectionId":%d,"workspaceId":%q,"title":"Report back","prompt":%q,"start":true}`, project.ID, status.ConnectionID, workspace.WorkspaceID, prompt), 201, &task)
			if task.ExternalSessionID == nil || task.ConnectionID != status.ConnectionID || task.State != "running" {
				if task.LastError != nil {
					t.Logf("launch error: %s", *task.LastError)
				}
				t.Fatalf("launched task: %+v", task)
			}
			ref := db.SessionRef{ConnectionID: task.ConnectionID, ExternalSessionID: *task.ExternalSessionID}
			delivered := tc.prompt(t, ref.ExternalSessionID)
			for _, want := range []string{prompt, fmt.Sprintf("Stage taskId: %d.", task.ID), tc.callerEnv, agentURL, "STAGE_AGENT_URL", "--url", "curl --unix-socket"} {
				if !strings.Contains(delivered, want) {
					t.Errorf("launch prompt missing %q: %s", want, delivered)
				}
			}
			if strings.Contains(delivered, proxy.URL) || strings.Contains(delivered, "--human") {
				t.Errorf("launch prompt points to a human transport: %s", delivered)
			}

			t.Setenv(tc.callerEnv, ref.ExternalSessionID)
			if tc.kind == claudecode.Kind {
				t.Setenv("CLAUDE_CODE_SESSION_KIND", "bg")
			}
			var flags []string
			switch tc.address {
			case "env":
				t.Setenv("STAGE_AGENT_URL", agentURL)
			case "flag":
				t.Setenv("STAGE_AGENT_URL", i.ready.Listeners[1].URL())
				flags = []string{"--url", agentURL}
			}
			assertLaunchReportBack(t, owner, proxy.URL, agent, flags, task, ref, tc.callerEnv)
		})
	}
	if err := i.stop(t); err != nil {
		t.Fatal(err)
	}
	gone(t, agentPath)
}

func assertLaunchReportBack(t *testing.T, owner *http.Client, base string, agent *client.Client, flags []string, task client.Task, ref db.SessionRef, callerEnv string) {
	t.Helper()
	scoped, err := agent.SessionContext(t.Context(), ref)
	if err != nil || scoped.Session != ref || scoped.Task == nil || scoped.Task.ID != task.ID ||
		scoped.Project == nil || scoped.Project.ID != task.ProjectID {
		t.Fatalf("HTTP session context %+v: %v", scoped, err)
	}
	runCLI := func(out any, args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		env := cli.OSEnv()
		env.Stdout, env.Stderr = &stdout, &stderr
		if code := cli.Run(t.Context(), append(args, flags...), env); code != 0 {
			t.Fatalf("stage %v: exit %d, stderr %s", args, code, &stderr)
		}
		if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
			t.Fatalf("stage %v: %v, stdout %s", args, err, &stdout)
		}
	}
	checkContext := func(where string, out agenttools.ContextOut) {
		t.Helper()
		if out.Session == nil || *out.Session != ref || out.CallerTaskID == nil || *out.CallerTaskID != task.ID ||
			out.Project == nil || out.Project.ID != task.ProjectID || out.SessionSource != callerEnv || out.APIURL != agent.BaseURL {
			t.Fatalf("%s: caller context %+v, want session %+v, task %d, project %d, source %s, URL %s",
				where, out, ref, task.ID, task.ProjectID, callerEnv, agent.BaseURL)
		}
	}
	checkNote := func(note db.JournalEntry, text string) {
		t.Helper()
		if note.ID == 0 || note.TaskID == nil || *note.TaskID != task.ID || note.Text != text ||
			note.SourceSession == nil || *note.SourceSession != ref || note.SourceMachineID == nil || *note.SourceMachineID != db.LocalMachine {
			t.Fatalf("note %+v, want %q from local session %+v on task %d", note, text, ref, task.ID)
		}
		if note.Actor.Kind != "agent" || note.Actor.UserID != 0 || note.Actor.CredentialAccountID != 0 || note.Actor.InitiatorUserID != 0 {
			t.Fatalf("local report-back actor %+v, want an agent without a human identity", note.Actor)
		}
	}
	var selected agenttools.ContextOut
	runCLI(&selected, "context", "--json")
	checkContext("CLI", selected)
	const cliText = "Report from the launch CLI"
	var cliNote db.JournalEntry
	runCLI(&cliNote, "task", "notes", fmt.Sprint(task.ID), "add", "--text", cliText, "--request-id", "cli", "--json")
	checkNote(cliNote, cliText)

	const httpText = "Report from HTTP MCP"
	httpMCP := connectMCP(t, agent.HTTP, "http://stage/mcp", "")
	var httpNote db.JournalEntry
	launchMCPCall(t, httpMCP, "append_note", map[string]any{
		"taskId": task.ID, "text": httpText, "requestId": "http-mcp", "callerSession": ref,
	}, &httpNote)
	checkNote(httpNote, httpText)

	cmd := exec.CommandContext(t.Context(), os.Args[0], append([]string{"mcp"}, flags...)...)
	cmd.Env = append(os.Environ(), "SERVE_TEST_STAGE_CLI=1")
	m := mcp.NewClient(&mcp.Implementation{Name: "shared launch stdio", Version: "1"}, nil)
	stdio, err := m.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect stdio MCP: %v", err)
	}
	defer stdio.Close()
	var fromStdio agenttools.ContextOut
	launchMCPCall(t, stdio, "get_context", nil, &fromStdio)
	checkContext("stdio MCP", fromStdio)
	const stdioText = "Report from stdio MCP"
	var stdioNote db.JournalEntry
	launchMCPCall(t, stdio, "append_note", map[string]any{
		"taskId": task.ID, "text": stdioText, "requestId": "stdio-mcp",
	}, &stdioNote)
	checkNote(stdioNote, stdioText)

	var journal db.JournalPage
	(transportRequest{method: "GET", path: fmt.Sprintf("/api/tasks/%d/journal", task.ID)}).send(t, owner, base, 200, &journal)
	if len(journal.Entries) != 3 {
		t.Fatalf("owner journal contains %d notes, want 3", len(journal.Entries))
	}
	wantNotes := map[string]bool{cliText: true, httpText: true, stdioText: true}
	for _, entry := range journal.Entries {
		if !wantNotes[entry.Text] {
			t.Fatalf("unexpected note in owner journal: %+v", entry)
		}
		delete(wantNotes, entry.Text)
		checkNote(entry, entry.Text)
	}
}

func launchMCPCall(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("MCP %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("MCP %s: %s", name, mcpText(result))
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("MCP %s: %v; result %s", name, err, data)
	}
}

// The shared OpenCode fixture serves the setup contract. This handler adds the
// workspace and session endpoints used by a real adapter launch.
func openCodeLaunchFixture(t *testing.T, directory string) (string, func(*testing.T, string) string) {
	t.Helper()
	const project = "test-project"
	setup := opencodetest.New(t, "")
	setupURL, err := url.Parse(setup.URL)
	if err != nil {
		t.Fatal(err)
	}
	fallback := httputil.NewSingleHostReverseProxy(setupURL)
	var mu sync.Mutex
	var session struct {
		ID, Title string
		Location  struct{ Directory string }
		Metadata  map[string]string
	}
	var prompt string
	stamp := time.Now().UnixMilli()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		sessionPath := "/api/session/" + session.ID
		sessionJSON := func() {
			fmt.Fprintf(w, `{"data":{"id":%q,"title":%q,"projectId":%q,"location":{"directory":%q},"time":{"created":%d,"updated":%d,"busy":%d},"metadata":{"stageAttempt":%q}}}`,
				session.ID, session.Title, project, session.Location.Directory, stamp, stamp, stamp, session.Metadata["stageAttempt"])
		}
		switch {
		case r.URL.Path == "/api/project":
			fmt.Fprintf(w, `[{"id":%q,"canonical":%q,"time":{"created":%d,"updated":%d}}]`, project, directory, stamp, stamp)
		case r.URL.Path == "/api/location":
			fmt.Fprintf(w, `{"directory":%q,"project":{"id":%q,"directory":%q}}`, directory, project, directory)
		case r.URL.Path == "/api/model" || r.URL.Path == "/api/agent":
			fmt.Fprintf(w, `{"location":{"directory":%q},"data":[]}`, directory)
		case r.Method == "POST" && r.URL.Path == "/api/session":
			if err := json.NewDecoder(r.Body).Decode(&session); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			sessionJSON()
		case r.URL.Path == "/api/session/active":
			if prompt == "" {
				fmt.Fprint(w, `{"data":{}}`)
			} else {
				fmt.Fprintf(w, `{"data":{%q:{"type":"running"}}}`, session.ID)
			}
		case r.URL.Path == "/api/debug/location":
			fmt.Fprint(w, `[]`)
		case session.ID != "" && r.URL.Path == sessionPath:
			sessionJSON()
		case session.ID != "" && r.URL.Path == sessionPath+"/prompt" && r.Method == "POST":
			var in struct {
				ID, Text string
				Metadata map[string]string
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			prompt = in.Text
			fmt.Fprintf(w, `{"data":{"id":%q,"type":"user","sessionId":%q,"time":{"created":%d},"payload":{"text":%q,"metadata":{"stageAttempt":%q}}}}`,
				in.ID, session.ID, stamp, prompt, in.Metadata["stageAttempt"])
		case session.ID != "" && (r.URL.Path == sessionPath+"/permission" || r.URL.Path == sessionPath+"/form" || r.URL.Path == sessionPath+"/message"):
			fmt.Fprint(w, `{"data":[]}`)
		default:
			fallback.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func(t *testing.T, id string) string {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if session.ID != id || prompt == "" {
			t.Fatalf("OpenCode launched session %q with prompt %q, want session %q with a prompt", session.ID, prompt, id)
		}
		return prompt
	}
}
