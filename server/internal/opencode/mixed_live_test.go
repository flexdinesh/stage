package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

// T3's upstream mobile behavior fixture supplies a scripted Fx provider. Both
// services still create and run their own sessions using their real protocols.
func isolatedT3(t *testing.T, dir, binary, fx string, offline *atomic.Bool) *t3.Adapter {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, "t3")
	settingsDir := filepath.Join(base, "userdata")
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "fx")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec node \"$STAGE_TEST_FX\" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]interface{}{"providerInstances": map[string]interface{}{
		"evalbot": map[string]interface{}{"driver": "fx", "displayName": "Stage fixture", "config": map[string]interface{}{"enabled": true, "binaryPath": launcher}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{"HOME=" + home, "XDG_RUNTIME_DIR=" + home, "TMPDIR=" + home, "T3CODE_HOME=" + base, "STAGE_TEST_FX=" + fx, "PATH=" + os.Getenv("PATH")}
	cmd := exec.Command(binary, "serve", "--base-dir", base, "--port", port, "--host", "127.0.0.1", "--no-browser", dir)
	cmd.Env, cmd.Dir = environment, dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() })
	url := "http://" + address
	client := &http.Client{Timeout: 3 * time.Second, Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if offline.Load() {
			return nil, errors.New("fixture T3 transport disconnected")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	deadline := time.Now().Add(30 * time.Second)
	for !t3.ProbeURL(t.Context(), client, url).Reachable {
		if time.Now().After(deadline) {
			t.Fatal("isolated T3 did not publish its environment")
		}
		time.Sleep(100 * time.Millisecond)
	}
	mint := exec.CommandContext(t.Context(), binary, append(t3setup.PairingArgs("stage fixture"), "--base-dir", base)...)
	mint.Dir, mint.Env = dir, environment
	raw, err := mint.Output()
	if err != nil {
		t.Fatal("isolated pairing failed", err)
	}
	var code struct{ Credential string }
	if err := json.Unmarshal(raw, &code); err != nil {
		t.Fatal("invalid pairing response", err)
	}
	mcp := t3.NewMCPClient(t3.Options{BaseURL: url, PublicURL: url, ClientName: "stage fixture", HTTPClient: client})
	t.Cleanup(mcp.Close)
	if err := mcp.Pair(t.Context(), code.Credential); err != nil {
		t.Fatal(err)
	}
	return t3.NewAdapter(mcp)
}

func TestLiveMixedManualTree(t *testing.T) {
	binary, fx := os.Getenv("STAGE_TEST_T3"), os.Getenv("STAGE_TEST_FX")
	if binary == "" || fx == "" {
		t.Skip("set STAGE_TEST_T3 and STAGE_TEST_FX for real T3 execution")
	}
	f := newFixture(t)
	var disconnected atomic.Bool
	first := isolatedT3(t, f.dir, binary, fx, &disconnected)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	project, err := first.Client.CreateProject(ctx, t3.CreateProjectInput{Title: "Stage fixture", WorkspaceRoot: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	var temp dbtest.DB
	pool := temp.Reset(t)
	if _, err := pool.ExecContext(ctx, "INSERT INTO harness_connections(id,harness) VALUES(2,'opencode') ON CONFLICT DO NOTHING"); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(pool)
	p, err := store.InsertProject(ctx, db.Project{Title: "Mixed", Bindings: []db.ProjectBinding{
		{ConnectionID: 1, ExternalProjectID: project.ID, Workspace: f.dir},
		{ConnectionID: 2, ExternalProjectID: f.project, Workspace: f.dir},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := coordinator.NewWithAdapters(store, nil, map[int64]execution.Adapter{1: first, 2: f.a}, nil)
	var tasks []db.Task
	for _, harness := range []int64{1, 2, 1} {
		model := t3.ModelKey("evalbot", "eval/scripted")
		if harness == 2 {
			model = "fixture/test"
		}
		task, err := store.CreateTask(ctx, db.NewTask{ConnectionID: harness, ProjectID: p.ID, Title: "Mixed handoff", Prompt: "Report fixture result", ExecutionConfig: &execution.Config{Model: model}})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	_, err = store.CreateTree(ctx, db.NewTree{Title: "Mixed manual chain", StartMode: "manual", Tasks: []db.TreeMember{
		{TaskID: tasks[0].ID}, {TaskID: tasks[1].ID, Needs: []int64{tasks[0].ID}}, {TaskID: tasks[2].ID, Needs: []int64{tasks[1].ID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var acceptedReply string
	for i, task := range tasks {
		started, err := service.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("start step %d: %v", i+1, err)
		}
		if i > 0 {
			h, err := store.GetHandoffs(ctx, task.ID, started.LaunchAttempt)
			if err != nil || len(h) != 1 || h[0].Status != db.HandoffSent || !strings.Contains(h[0].Text, acceptedReply) {
				t.Fatalf("incorrect offline handoff on step %d: %+v %v", i+1, h, err)
			}
		}
		deadline := time.Now().Add(40 * time.Second)
		for started.State != db.StateDone {
			pollErr := service.PollTask(ctx, started)
			if time.Now().After(deadline) {
				t.Fatalf("step %d still %s; last observation error: %v", i+1, started.State, pollErr)
			}
			started, err = store.GetTask(ctx, task.ID)
			if err != nil || started.State == db.StateFailed {
				t.Fatalf("step %d failed: %+v %v", i+1, started, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if i+1 < len(tasks) {
			if _, err := service.StartTask(ctx, tasks[i+1].ID); err == nil {
				t.Fatal("run finish released successor before acceptance")
			}
		}
		switch i {
		case 0:
			disconnected.Store(true)
		case 1:
			f.stop()
			disconnected.Store(false)
		}
		preview, err := store.CompletionPreview(ctx, task.ID)
		if err != nil || preview.Evidence.Reply == nil || *preview.Evidence.Reply == "" {
			t.Fatalf("missing cached result on step %d: %+v %v", i+1, preview, err)
		}
		acceptedReply = *preview.Evidence.Reply
		_, err = store.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{RequestID: "accept", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, false)
		if err != nil {
			t.Fatal(err)
		}
		if i+1 < len(tasks) {
			next, err := store.GetTask(ctx, tasks[i+1].ID)
			if err != nil || next.State != db.StateBacklog || next.Readiness == nil || *next.Readiness != "ready" || next.Session != nil {
				t.Fatalf("manual successor: %+v %v", next, err)
			}
		}
	}
}
