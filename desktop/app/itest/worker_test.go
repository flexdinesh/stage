package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// workerShim runs the desktop's CLI calls with the real stage binary in
// the worker's isolated environment, except the service manager parts the
// isolated environment has no systemd for: `worker service start|restart`
// runs a foreground `stage worker run` instead, `worker service status`
// is derived from `stage worker status`, and `service uninstall`,
// `worker service uninstall|enable|disable` only report success (after
// stopping the foreground worker for uninstall).
type workerShim struct {
	t   *testing.T
	bin string
	env []string

	mu   sync.Mutex
	proc *server
}

func (w *workerShim) exec(ctx context.Context, args ...string) launch.Result {
	cmd := exec.CommandContext(ctx, w.bin, args...)
	cmd.Env = w.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := launch.Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.Code = exit.ExitCode()
	default:
		r.Code, r.Err = -1, err
	}
	return r
}

func ok(msg string) launch.Result {
	b, _ := json.Marshal(map[string]any{"schema": 1, "code": 0, "ok": true, "message": msg})
	return launch.Result{Stdout: b}
}

func (w *workerShim) run(ctx context.Context, _ string, args ...string) launch.Result {
	switch {
	case len(args) >= 3 && args[0] == "worker" && args[1] == "service":
		switch args[2] {
		case "start", "restart":
			w.stop()
			w.start()
			return ok("started")
		case "uninstall", "stop":
			w.stop()
			return ok("uninstalled")
		case "status":
			return w.status(ctx)
		default:
			return ok(args[2])
		}
	case len(args) >= 2 && args[0] == "service" && args[1] == "uninstall":
		return ok("uninstalled")
	}
	return w.exec(ctx, args...)
}

func (w *workerShim) start() {
	cmd := exec.Command(w.bin, "worker", "run")
	cmd.Env = w.env
	s := &server{cmd: cmd, done: make(chan struct{}), out: &syncBuffer{}}
	cmd.Stdout, cmd.Stderr = s.out, s.out
	if err := cmd.Start(); err != nil {
		w.t.Errorf("worker run: %v", err)
		return
	}
	go func() { _ = cmd.Wait(); close(s.done) }()
	w.mu.Lock()
	w.proc = s
	w.mu.Unlock()
	waitUntil(w.t, "worker holds its dir", 15*time.Second, func() bool {
		var st struct {
			Process *struct{ PID int } `json:"process"`
		}
		res := w.exec(context.Background(), "worker", "status", "--json")
		return json.Unmarshal(res.Stdout, &st) == nil && st.Process != nil
	})
}

func (w *workerShim) stop() {
	w.mu.Lock()
	p := w.proc
	w.proc = nil
	w.mu.Unlock()
	if p != nil {
		p.kill(syscall.SIGTERM)
		if w.t.Failed() {
			w.t.Logf("stage worker run output:\n%s", p.out.String())
		}
	}
}

// status is `worker service status --json` as the launcher reads it, from
// `stage worker status --json`.
func (w *workerShim) status(ctx context.Context) launch.Result {
	var st struct {
		Config struct {
			Joined    bool   `json:"joined"`
			URL       string `json:"url"`
			MachineID int64  `json:"machineId"`
		} `json:"config"`
		Process *struct {
			State     string `json:"state"`
			LastError string `json:"lastError"`
		} `json:"process"`
		Server *struct {
			Status string `json:"status"`
		} `json:"server"`
	}
	res := w.exec(ctx, "worker", "status", "--json")
	if err := json.Unmarshal(res.Stdout, &st); err != nil {
		return launch.Result{Code: 1, Stderr: []byte("worker status: " + err.Error())}
	}
	worker := map[string]any{"joined": st.Config.Joined, "url": st.Config.URL, "machineId": st.Config.MachineID}
	state, ready := "not-installed", false
	if st.Config.Joined {
		state = "stopped"
		if st.Server != nil && st.Server.Status == "revoked" {
			worker["state"] = "revoked"
		}
	}
	if st.Process != nil {
		state, ready = "running", true
		worker["ownedByService"] = true
		worker["state"], worker["lastError"] = st.Process.State, st.Process.LastError
	}
	b, _ := json.Marshal(map[string]any{"schema": 1, "role": "worker", "state": state, "ready": ready,
		"definition": map[string]any{"present": st.Config.Joined}, "worker": worker, "problems": []any{}})
	return launch.Result{Stdout: b}
}

// remoteMachine is GET <main>/api/machines' entry for id, nil when not
// listed.
func remoteMachine(t *testing.T, mainURL string, id int64) map[string]any {
	t.Helper()
	res, err := http.Get(mainURL + "/api/machines")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, m := range body.Machines {
		if n, _ := m["id"].(float64); int64(n) == id {
			return m
		}
	}
	return nil
}

// TestDesktopWorker drives the desktop joining this machine to a real
// remote main server as a worker: the page's pairing call (POST
// /api/machines/pairings on the remote), the launcher's real `stage worker
// join`, a real worker process (foreground, standing in for the worker
// service), its state as online then offline then online again, the main
// server's view of the machine, and a real `stage worker leave` that
// removes it there.
func TestDesktopWorker(t *testing.T) {
	bin := stage(t)
	re := newEnv(t)
	mainURL, _ := re.serveTCPWith(t, bin, true)
	we := newEnv(t)
	shim := &workerShim{t: t, bin: bin, env: we.vars}
	t.Cleanup(shim.stop)

	store, err := target.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := store.Add("Box", mainURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActive(box.ID); err != nil {
		t.Fatal(err)
	}
	timing := launch.DefaultTiming()
	timing.WorkerEvery = 200 * time.Millisecond
	var joinedMu sync.Mutex
	var joined []bool
	l := launch.New(launch.Config{
		CLI: bin, AppName: "Stage Test", AppVersion: "itest", TempDirs: []string{},
		Run: shim.run, Servers: store, Timing: timing,
		OnWorker: func(v bool) { joinedMu.Lock(); joined = append(joined, v); joinedMu.Unlock() },
		Logf:     t.Logf,
	})
	t.Cleanup(l.Close)
	l.Start()
	waitUntil(t, "ready on the remote", 30*time.Second, func() bool { s := l.State(); return s.Phase == launch.PhaseReady && s.Target.ID == box.ID })
	if w := l.Worker(); w != nil {
		t.Fatalf("worker before join: %+v", w)
	}

	// The page creates the pairing on the active server, then the host joins.
	res, err := http.Post(mainURL+"/api/machines/pairings", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var pairing struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(res.Body).Decode(&pairing); err != nil || res.StatusCode != http.StatusCreated {
		t.Fatalf("pairing %d %v", res.StatusCode, err)
	}
	_ = res.Body.Close()
	if err := l.JoinWorker(context.Background(), pairing.Link); err != nil {
		t.Fatal(err)
	}
	var w *launch.Worker
	waitUntil(t, "worker online", 30*time.Second, func() bool { w = l.Worker(); return w != nil && w.Status == launch.WorkerOnline })
	if w.ServerID == nil || *w.ServerID != box.ID || w.ServerName != "Box" || w.MachineID < 2 {
		t.Fatalf("worker %+v", w)
	}
	waitUntil(t, "main sees the machine online", 15*time.Second, func() bool {
		m := remoteMachine(t, mainURL, w.MachineID)
		return m != nil && m["status"] == "online"
	})

	// The worker process stops: offline; Restart worker brings it back.
	shim.stop()
	waitUntil(t, "worker offline", 15*time.Second, func() bool { w := l.Worker(); return w != nil && w.Status == launch.WorkerOffline })
	if err := l.RestartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worker online again", 30*time.Second, func() bool { w := l.Worker(); return w != nil && w.Status == launch.WorkerOnline })

	// Leave: the main server removes the machine; this machine is no worker.
	if err := l.LeaveWorker(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if w := l.Worker(); w != nil {
		t.Fatalf("worker after leave: %+v", w)
	}
	if m := remoteMachine(t, mainURL, w.MachineID); m != nil {
		t.Fatalf("main still lists the machine: %v", m)
	}
	joinedMu.Lock()
	defer joinedMu.Unlock()
	if len(joined) != 2 || !joined[0] || joined[1] {
		t.Fatalf("OnWorker %v", joined)
	}
}
