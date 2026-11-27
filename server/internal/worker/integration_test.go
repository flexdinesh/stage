package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// isolate points every path Stage derives at temp dirs (never the user's
// data dir or runtime dir) and returns a short dir for socket paths.
func isolate(t *testing.T) string {
	t.Helper()
	short, err := os.MkdirTemp("", "wk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("STAGE_DATA_DIR", "")
	t.Setenv(config.WorkerConfigEnv, "")
	rt := filepath.Join(short, "rt")
	if err := os.Mkdir(rt, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("NOTIFY_SOCKET", "")
	t.Setenv("T3_FAKE", "")
	return short
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// mainServer is a foreground main server on a temp data dir, a temp socket
// and a loopback TCP port (its STAGE_PUBLIC_URL).
type mainServer struct {
	t      *testing.T
	cfg    config.Config
	url    string
	cancel context.CancelFunc
	done   chan error
}

func newMain(t *testing.T, short string) *mainServer {
	t.Helper()
	addr := freePort(t)
	ls, err := listen.ParseList("unix:" + filepath.Join(short, "main.sock") + "," + addr)
	if err != nil {
		t.Fatal(err)
	}
	m := &mainServer{t: t, url: "http://" + addr, cfg: config.Config{
		Listen: ls, DataDir: filepath.Join(t.TempDir(), "main"), PublicURL: "http://" + addr,
	}}
	m.start()
	t.Cleanup(func() { m.stop() })
	return m
}

func (m *mainServer) start() {
	m.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	m.cancel, m.done = cancel, make(chan error, 1)
	go func() {
		m.done <- serve.Run(ctx, serve.Options{Config: m.cfg, HarnessOverrides: map[string]any{t3.Kind: t3.NewFake()}, Logger: quiet(), ShutdownTimeout: 10 * time.Second,
			Ready: func(serve.Ready) { close(ready) }})
	}()
	select {
	case <-ready:
	case err := <-m.done:
		m.t.Fatalf("main server did not start: %v", err)
	case <-time.After(20 * time.Second):
		m.t.Fatal("main server did not start in time")
	}
}

func (m *mainServer) stop() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	m.cancel = nil
	select {
	case err := <-m.done:
		if err != nil {
			m.t.Errorf("main server: %v", err)
		}
	case <-time.After(30 * time.Second):
		m.t.Error("main server did not stop")
	}
}

func (m *mainServer) client() *client.Client { return client.New(m.url) }

// machine is GET /api/machines/{id} through the list.
func (m *mainServer) machine(id int64) client.Machine {
	m.t.Helper()
	ms, err := m.client().Machines(context.Background())
	if err != nil {
		m.t.Fatal(err)
	}
	for _, x := range ms {
		if x.ID == id {
			return x
		}
	}
	m.t.Fatalf("machine %d not listed", id)
	return client.Machine{}
}

// eventually polls cond for up to 15s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMainAndWorker pairs a foreground main server and a worker on temp
// dirs, connects them, survives a main restart and shows the machine
// online, then revokes it.
func TestMainAndWorker(t *testing.T) {
	short := isolate(t)
	m := newMain(t, short)
	ctx := context.Background()

	p, err := m.client().PairMachine(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "w", worker.DirName, worker.ConfigFile)
	c, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath})
	if err != nil {
		t.Fatal(err)
	}
	if c.Label != "laptop" || c.URL != m.url || c.MachineID <= 1 {
		t.Fatalf("joined %+v", c)
	}
	if st, err := os.Stat(cfgPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("worker config mode: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Dir(cfgPath)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("worker dir mode: %v %v", st, err)
	}
	if got := m.machine(c.MachineID); got.Online || got.Status != "offline" {
		t.Fatalf("before run: %+v", got)
	}
	// Joining twice is refused.
	var aj *worker.AlreadyJoinedError
	if _, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath}); !errors.As(err, &aj) {
		t.Fatalf("second join: %v", err)
	}

	states := make(chan worker.State, 64)
	composing, finishSetup := make(chan struct{}, 1), make(chan struct{})
	runCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(runCtx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet(), MinBackoff: 20 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
			HTTP: &http.Client{Transport: channelAPISkew{}},
			Host: func(id int64, call workerchan.Upcall) worker.Host {
				h := remote.NewHost(machine.NewLocal(id, harness.Profile{}, nil))
				h.OnAttached(func(life context.Context, kinds []string) error {
					if !slices.Contains(kinds, t3.Kind) {
						return fmt.Errorf("T3 missing from advertised harnesses: %v", kinds)
					}
					if _, err := call(life, remote.KindSettingsGet, remote.KindRequest{Kind: t3.Kind}); err != nil {
						return err
					}
					if _, err := call(life, remote.KindStatusError, remote.StatusErrorRequest{Kind: t3.Kind, Message: "initializing"}); err != nil {
						return err
					}
					_, err := call(life, remote.KindSettingsSave, remote.SettingsRequest{Kind: t3.Kind, Patch: json.RawMessage(`{"url":"http://worker:3773"}`)})
					var re *workerchan.RemoteError
					if !errors.As(err, &re) || re.Code != workerchan.CodeOperationUnavailable || re.Message != workerchan.ErrNegotiating.Error() {
						return fmt.Errorf("settings write during negotiation: %v", err)
					}
					_, err = call(life, "setup.t3.get", nil)
					if !errors.As(err, &re) || re.Code != workerchan.CodeOperationUnavailable || re.Message == workerchan.ErrNegotiating.Error() {
						return fmt.Errorf("obsolete setup call was advertised: %v", err)
					}
					select {
					case composing <- struct{}{}:
					default:
					}
					select {
					case <-finishSetup:
						return nil
					case <-life.Done():
						return life.Err()
					}
				})
				return h
			},
			OnState: func(s worker.State) { states <- s }})
	}()
	waitState := func(want string) {
		t.Helper()
		timeout := time.After(20 * time.Second)
		for {
			select {
			case s := <-states:
				if s.State == want {
					return
				}
			case err := <-done:
				t.Fatalf("worker exited waiting for %s: %v", want, err)
			case <-timeout:
				t.Fatalf("worker never reached %s", want)
			}
		}
	}
	select {
	case <-composing:
	case <-time.After(10 * time.Second):
		t.Fatal("worker setup callback did not complete during negotiation")
	}
	if got := m.machine(c.MachineID); got.Online || got.Status != "negotiating" || got.StatusReason == "" {
		t.Fatalf("machine before composition %+v", got)
	}
	close(finishSetup)
	waitState(worker.StateOnline)
	eventually(t, "machine online", func() bool { x := m.machine(c.MachineID); return x.Online && x.Status == "online" })
	ping, err := m.client().PingMachine(ctx, c.MachineID)
	if err != nil || ping.MachineID != c.MachineID {
		t.Fatalf("ping: %+v %v", ping, err)
	}

	// One worker per directory.
	var le *worker.LockedError
	if err := worker.Run(ctx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet()}); !errors.As(err, &le) {
		t.Fatalf("second worker on the dir: %v", err)
	}
	st, code := worker.ReadStatus(ctx, cfgPath, nil)
	if code != worker.StatusOnline || st.Process == nil || st.Process.State != worker.StateOnline || st.Server == nil || st.Server.Status != "online" {
		t.Fatalf("status %d %+v", code, st)
	}

	// The main server restarts: the worker reconnects on its own.
	m.stop()
	waitState(worker.StateConnecting)
	if _, code := worker.ReadStatus(ctx, cfgPath, nil); code != worker.StatusNotOnline {
		t.Fatalf("status while main is down: %d", code)
	}
	m.start()
	waitState(worker.StateOnline)
	eventually(t, "machine online after restart", func() bool { return m.machine(c.MachineID).Online })
	if x := m.machine(c.MachineID); x.LastSeenAt == nil || x.Version == nil {
		t.Fatalf("presence not recorded: %+v", x)
	}
	if _, err := m.client().PingMachine(ctx, c.MachineID); err != nil {
		t.Fatalf("ping after restart: %v", err)
	}

	// Removing the machine closes the channel at once; the worker stops,
	// revoked, without a retry loop.
	if err := m.client().RemoveMachine(ctx, c.MachineID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var ie *config.InvalidError
		if !errors.As(err, &ie) || !errors.Is(err, worker.ErrRevoked) {
			t.Fatalf("revoked worker: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not stop after revocation")
	}
	s, err := worker.ReadState(filepath.Dir(cfgPath))
	if err != nil || s == nil || s.State != worker.StateRevoked {
		t.Fatalf("final state %+v %v", s, err)
	}
	if _, code := worker.ReadStatus(ctx, cfgPath, nil); code != worker.StatusRevoked {
		t.Fatalf("status after revoke: %d", code)
	}
	// Leave with a revoked token deletes the local config.
	res, err := worker.Leave(ctx, worker.LeaveOptions{ConfigPath: cfgPath})
	if err != nil || !res.AlreadyRemoved {
		t.Fatalf("leave: %+v %v", res, err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config kept: %v", err)
	}
	// Joining again clears the old machine's final state.
	p2, err := m.client().PairMachine(ctx, "again")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Join(ctx, worker.JoinOptions{Link: p2.Link, ConfigPath: cfgPath}); err != nil {
		t.Fatal(err)
	}
	if s, err := worker.ReadState(filepath.Dir(cfgPath)); err != nil || s != nil {
		t.Fatalf("state after rejoin %+v %v", s, err)
	}
}

// Only the channel requests differ from the server's application API version.
type channelAPISkew struct{}

func (channelAPISkew) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/api/worker/channel" {
		r = r.Clone(r.Context())
		r.Header.Set(buildinfo.APIVersionHeader, "42")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// TestLeaveRemovesMachine: leave removes the machine on the server, and a
// running worker stops.
func TestLeaveRemovesMachine(t *testing.T) {
	short := isolate(t)
	m := newMain(t, short)
	ctx := context.Background()
	p, err := m.client().PairMachine(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), worker.ConfigFile)
	c, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath, Label: "desk"})
	if err != nil {
		t.Fatal(err)
	}
	online := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet(), MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
			OnState: func(s worker.State) {
				if s.State == worker.StateOnline {
					select {
					case online <- struct{}{}:
					default:
					}
				}
			}})
	}()
	select {
	case <-online:
	case err := <-done:
		t.Fatalf("worker exited: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("worker not online")
	}
	res, err := worker.Leave(ctx, worker.LeaveOptions{ConfigPath: cfgPath})
	if err != nil || !res.Removed {
		t.Fatalf("leave: %+v %v", res, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, worker.ErrRevoked) {
			t.Fatalf("worker after leave: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker kept running after leave")
	}
	ms, err := m.client().Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ms {
		if x.ID == c.MachineID {
			t.Fatalf("machine %d still listed after leave", x.ID)
		}
	}
}

// TestAgentThroughGateway: an agent on the worker machine uses the CLI
// (default socket resolution, no --url) and HTTP MCP (the opt-in TCP
// listener) unchanged; the main server records the forwarding machine and
// the caller session.
func TestAgentThroughGateway(t *testing.T) {
	short := isolate(t)
	t.Setenv("STAGE_URL", "")
	m := newMain(t, short)
	ctx := context.Background()
	// A fresh install has no T3 connection: the refreshed T3 status stamps
	// the fake's environment, creating the one the caller sessions run on.
	mainJSON(t, m, "GET", "/api/harnesses/t3/status", "", 200, nil)
	h, err := m.client().Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var conn int64
	for _, x := range h.Harnesses {
		if x.Kind == "t3" {
			conn = x.ConnectionID
		}
	}
	if conn == 0 {
		t.Fatalf("no T3 connection: %+v", h.Harnesses)
	}
	t.Setenv(agentctx.CallerEnv, fmt.Sprintf("%d:agent-cli", conn))
	p, err := m.client().PairMachine(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), worker.ConfigFile)
	c, err := worker.Join(ctx, worker.JoinOptions{Link: p.Link, ConfigPath: cfgPath})
	if err != nil {
		t.Fatal(err)
	}
	tcp := freePort(t)
	addrs, err := config.ParseWorkerListen("unix," + tcp)
	if err != nil {
		t.Fatal(err)
	}
	online := make(chan worker.State, 1)
	runCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(runCtx, worker.RunOptions{ConfigPath: cfgPath, Logger: quiet(), Listen: addrs, MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
			OnState: func(s worker.State) {
				if s.State == worker.StateOnline {
					select {
					case online <- s:
					default:
					}
				}
			}})
	}()
	t.Cleanup(func() {
		stopWorker()
		<-done
	})
	var st worker.State
	select {
	case st = <-online:
	case err := <-done:
		t.Fatalf("worker exited: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("worker not online")
	}
	sock, err := config.DefaultSocket(os.Getenv, mustDataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Gateway) != 2 || st.Gateway[0] != "unix://"+sock || st.Gateway[1] != "http://"+tcp {
		t.Fatalf("gateway %v, want the default socket %s and %s", st.Gateway, sock, tcp)
	}

	// The project lives on the main server.
	var proj struct{ ID int64 }
	mainJSON(t, m, "POST", "/api/projects", `{"title":"Remote work"}`, 201, &proj)

	// CLI: no --url and no STAGE_URL, so it resolves this machine's default
	// socket, which the gateway owns.
	if r := client.Resolve("", os.Getenv); r.Source != "default socket" || r.URL != "unix://"+sock {
		t.Fatalf("resolution %+v", r)
	}
	stage := func(args ...string) string {
		t.Helper()
		var out, errb strings.Builder
		env := cli.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, Getenv: os.Getenv, Caller: agentctx.FromEnv(), Home: os.Getenv("HOME")}
		if code := cli.Run(ctx, args, env); code != 0 {
			t.Fatalf("stage %v: exit %d: %s", args, code, errb.String())
		}
		return out.String()
	}
	_ = stage("task", "add", "--project", fmt.Sprint(proj.ID), "--connection", fmt.Sprint(conn), "--title", "from the laptop", "--prompt", "p")
	tasks, err := m.client().Tasks(ctx, client.TaskQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var taskID int64
	for _, x := range tasks {
		if x.Title == "from the laptop" {
			taskID = x.ID
		}
	}
	if taskID == 0 {
		t.Fatalf("task not created on main: %+v", tasks)
	}
	var task struct {
		ConnectionID    int64  `json:"connectionId"`
		SourceMachineID *int64 `json:"sourceMachineId"`
		CreatedBy       int64  `json:"createdBy"`
	}
	mainJSON(t, m, "GET", fmt.Sprintf("/api/tasks/%d", taskID), "", 200, &task)
	if task.ConnectionID != conn || task.SourceMachineID == nil || *task.SourceMachineID != c.MachineID || task.CreatedBy != 1 {
		t.Fatalf("task %+v, want source machine %d", task, c.MachineID)
	}
	_ = stage("task", "notes", fmt.Sprint(taskID), "add", "--text", "cli note", "--request-id", "cli-1")

	// HTTP MCP on the opt-in TCP listener, caller by header.
	tr := &mcp.StreamableClientTransport{Endpoint: "http://" + tcp + "/mcp", HTTPClient: &http.Client{Transport: callerHeader{fmt.Sprintf("%d:agent-mcp", conn)}}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil).Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "append_note", Arguments: map[string]any{"taskId": taskID, "text": "mcp note", "requestId": "mcp-1"}})
	if err != nil || res.IsError {
		t.Fatalf("append_note: %v %+v", err, res)
	}

	var page struct {
		Entries []struct {
			Text            string `json:"text"`
			SourceMachineID *int64 `json:"sourceMachineId"`
			SourceSession   *struct {
				ConnectionID      int64  `json:"connectionId"`
				ExternalSessionID string `json:"externalSessionId"`
			} `json:"sourceSession"`
		} `json:"entries"`
	}
	mainJSON(t, m, "GET", fmt.Sprintf("/api/tasks/%d/journal?kind=notes", taskID), "", 200, &page)
	want := map[string]string{"cli note": "agent-cli", "mcp note": "agent-mcp"}
	for _, e := range page.Entries {
		session, ok := want[e.Text]
		if !ok {
			continue
		}
		delete(want, e.Text)
		if e.SourceMachineID == nil || *e.SourceMachineID != c.MachineID || e.SourceSession == nil || e.SourceSession.ConnectionID != conn || e.SourceSession.ExternalSessionID != session {
			t.Errorf("%q: machine %v session %+v", e.Text, e.SourceMachineID, e.SourceSession)
		}
	}
	if len(want) != 0 {
		t.Fatalf("notes missing %v in %+v", want, page.Entries)
	}

	// A second worker under this identity cannot take the gateway socket.
	p2, err := m.client().PairMachine(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := filepath.Join(t.TempDir(), worker.ConfigFile)
	if _, err := worker.Join(ctx, worker.JoinOptions{Link: p2.Link, ConfigPath: cfg2}); err != nil {
		t.Fatal(err)
	}
	var ue *listen.InUseError
	if err := worker.Run(ctx, worker.RunOptions{ConfigPath: cfg2, Logger: quiet()}); !errors.As(err, &ue) {
		t.Fatalf("second gateway on the default socket: %v", err)
	}
}

func mustDataDir(t *testing.T) string {
	t.Helper()
	d, err := config.DataDirFrom(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// mainJSON calls the main server directly (not through a gateway).
func mainJSON(t *testing.T, m *mainServer, method, path, body string, want int, out any) {
	t.Helper()
	req, _ := http.NewRequest(method, m.url+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

// callerHeader is an HTTP MCP client naming its session.
type callerHeader struct{ value string }

func (c callerHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(agentctx.CallerHeader, c.value)
	return http.DefaultTransport.RoundTrip(r)
}
