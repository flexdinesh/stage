// Package remotetest runs adapters on a remote machine behind a real worker
// channel, for tests: the main server's hub and HTTP handlers on a loopback
// server, and a real `stage worker run` loop (worker.Run, temp worker dir)
// whose host serves the adapters. The worker can be stopped and restarted
// (a new process with fresh adapter wrappers over the same harness) and a
// call's result can be lost in transit.
package remotetest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Rig is one remote machine with its worker.
type Rig struct {
	t          *testing.T
	machineID  int64
	hub        *workerchan.Hub
	set        *machine.Set
	hosts      *remote.Hosts
	srv        *httptest.Server
	cfgPath    string
	listen     []listen.Addr // the worker's agent gateway: a temp socket
	adapters   map[int64]execution.Adapter
	connectors map[string]harness.Connector
	// profile is what the worker reports (SetProfile).
	profile      harness.Profile
	missingKinds []string

	mu    sync.Mutex
	kinds map[string]string // request id -> kind, from the down stream
	drop  map[string]int    // kind -> results still to lose
	stop  context.CancelFunc
	done  chan error
}

// New hosts adapters (by connection) on remote machine machineID, beside
// an empty local machine 1, and starts its worker. The main server's
// record of the machine's active connections is adapters' keys. The
// worker allows local harnesses (`stage worker run --local-harnesses`).
func New(t *testing.T, machineID int64, adapters map[int64]execution.Adapter) *Rig {
	return NewWithConnectors(t, machineID, adapters, nil)
}

// NewWithConnectors also runs connection setup on the real worker.
func NewWithConnectors(t *testing.T, machineID int64, adapters map[int64]execution.Adapter, connectors map[string]harness.Connector) *Rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &Rig{t: t, machineID: machineID, adapters: adapters, kinds: map[string]string{}, drop: map[string]int{},
		profile: harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, connectors: connectors}
	r.hub = workerchan.NewHub(ctx, workerchan.Options{Version: "test"})
	r.set = machine.MustSet(machine.NewLocal(1, harness.Profile{}, nil))
	conns := func(id int64) []int64 {
		if id != machineID {
			return nil
		}
		ids := slices.Collect(maps.Keys(adapters))
		slices.Sort(ids)
		return ids
	}
	r.hosts = remote.NewHosts(r.set, r.hub, conns, nil, quiet())
	r.hosts.SetSetup(remote.SetupHooks{Kinds: slices.Sorted(maps.Keys(connectors))})
	r.hosts.Join(machineID)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/worker/channel", func(w http.ResponseWriter, req *http.Request) {
		r.hub.ServeStream(&recorder{ResponseWriter: w, rig: r}, req, machineID, "test", quiet())
	})
	mux.HandleFunc("POST /api/worker/channel", func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var env workerchan.Envelope
		if json.Unmarshal(body, &env) == nil && env.Kind == workerchan.KindResult && r.lose(env.ID) {
			w.WriteHeader(http.StatusNoContent) // lost in transit
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		r.hub.ServePost(w, req, machineID)
	})
	r.srv = httptest.NewServer(mux)
	r.cfgPath = filepath.Join(t.TempDir(), "worker", worker.ConfigFile)
	// A short dir: socket paths are length-limited.
	short, err := os.MkdirTemp("", "rg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	if r.listen, err = listen.ParseList("unix:" + filepath.Join(short, "gw.sock")); err != nil {
		t.Fatal(err)
	}
	if err := worker.SaveConfig(r.cfgPath, worker.Config{Schema: worker.ConfigSchema, URL: r.srv.URL, MachineID: machineID, Label: "remote", Token: "stage_mt_test", JoinedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.StopWorker()
		cancel()
		r.srv.Close()
	})
	r.StartWorker()
	return r
}

// Set is the main server's machines: local machine 1 and the remote one.
func (r *Rig) Set() *machine.Set { return r.set }

// Machine is the remote machine as the main server hosts it.
func (r *Rig) Machine() *remote.Machine {
	m, ok := r.hosts.Machine(r.machineID)
	if !ok {
		r.t.Fatal("remote machine not hosted")
	}
	return m
}

// Adapter is the main server's proxy of connection's adapter.
func (r *Rig) Adapter(connection int64) execution.Adapter {
	a, ok := r.Machine().Adapter(connection)
	if !ok {
		r.t.Fatalf("connection %d not hosted", connection)
	}
	return a
}

// SetProfile sets the profile the next worker StartWorker starts reports
// (stop the running one first).
func (r *Rig) SetProfile(p harness.Profile) { r.profile = p }

// SetMissingRequestKinds omits kinds from the next worker's handlers and ready advertisement.
func (r *Rig) SetMissingRequestKinds(kinds ...string) { r.missingKinds = kinds }

type host struct {
	*remote.Host
	missing []string
}

func (h host) Handlers() map[string]workerchan.WorkerHandler {
	handlers := h.Host.Handlers()
	for _, kind := range h.missing {
		delete(handlers, kind)
	}
	return handlers
}

// StartWorker starts a worker process (a fresh host over the same
// adapters) and waits until the main server has described it.
func (r *Rig) StartWorker() {
	r.t.Helper()
	r.mu.Lock()
	if r.stop != nil {
		r.mu.Unlock()
		r.t.Fatal("worker already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.done = cancel, make(chan error, 1)
	r.mu.Unlock()
	profile := r.profile
	makeHost := func(id int64, _ workerchan.Upcall) worker.Host {
		h := remote.NewHost(machine.NewLocal(id, profile, machine.LocalOps{}))
		for conn, a := range r.adapters {
			h.Local().Install(conn, conn, a)
		}
		for kind, c := range r.connectors {
			h.Local().SetConnector(kind, c)
		}
		return host{Host: h, missing: r.missingKinds}
	}
	done := r.done
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{ConfigPath: r.cfgPath, Logger: quiet(), HTTP: r.srv.Client(), Listen: r.listen,
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, Host: makeHost})
	}()
	r.wait("worker ready", r.Machine().Online)
}

// StopWorker stops the worker process (its calls in flight end with it)
// and waits until the main server sees the machine offline.
func (r *Rig) StopWorker() {
	r.mu.Lock()
	stop, done := r.stop, r.done
	r.stop = nil
	r.mu.Unlock()
	if stop == nil {
		return
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			r.t.Errorf("worker: %v", err)
		}
	case <-time.After(10 * time.Second):
		r.t.Error("worker did not stop")
	}
	r.wait("machine offline", func() bool { return !r.Machine().Online() })
}

// LoseResult loses the next n results of calls of kind in transit: the
// worker answered, the main server never hears it.
func (r *Rig) LoseResult(kind string, n int) {
	r.mu.Lock()
	r.drop[kind] += n
	r.mu.Unlock()
}

func (r *Rig) lose(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	kind := r.kinds[id]
	if r.drop[kind] <= 0 {
		return false
	}
	r.drop[kind]--
	return true
}

func (r *Rig) wait(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// recorder notes each request's kind as the down stream writes it.
type recorder struct {
	http.ResponseWriter
	rig *Rig
	buf []byte
}

func (w *recorder) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.Index(w.buf, []byte("\n\n"))
		if i < 0 {
			break
		}
		event := string(w.buf[:i])
		w.buf = w.buf[i+2:]
		if !strings.HasPrefix(event, "event: "+workerchan.EventRPC+"\n") {
			continue
		}
		var env workerchan.Envelope
		if json.Unmarshal([]byte(strings.TrimPrefix(event, "event: "+workerchan.EventRPC+"\ndata: ")), &env) == nil && env.ID != "" {
			w.rig.mu.Lock()
			w.rig.kinds[env.ID] = env.Kind
			w.rig.mu.Unlock()
		}
	}
	return w.ResponseWriter.Write(p)
}

func (w *recorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
