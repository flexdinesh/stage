package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWorkerEnv(t *testing.T) {
	vals, err := ParseWorkerEnv(strings.NewReader(WorkerEnvTemplate + "STAGE_WORKER_CONFIG='/x/w.json'\n"))
	if err != nil || vals[WorkerConfigEnv] != "/x/w.json" {
		t.Fatalf("%v %v", vals, err)
	}
	for _, bad := range []string{"STAGE_LISTEN=unix\n", "STAGE_WORKER_CONFIG=rel/w.json\n", "STAGE_WORKER_CONFIG=/a\nSTAGE_WORKER_CONFIG=/b\n", "STAGE_DATA_DIR=/d\n"} {
		if _, err := ParseWorkerEnv(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), WorkerEnvFile) {
			t.Errorf("accepted %q (%v)", bad, err)
		}
	}
}

func TestWorkerListen(t *testing.T) {
	for v, want := range map[string]int{"": 1, "unix": 1, "unix, 127.0.0.1:7381": 2} {
		addrs, err := ParseWorkerListen(v)
		if err != nil || len(addrs) != want || !addrs[0].Private || addrs[0].Address != "" {
			t.Errorf("%q: %+v %v", v, addrs, err)
		}
	}
	for _, bad := range []string{"127.0.0.1:7381", "unix:/tmp/x.sock", "unix,unix", "nope"} {
		if _, err := ParseWorkerListen(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	vals, err := ParseWorkerEnv(strings.NewReader("STAGE_WORKER_LISTEN=unix,127.0.0.1:7381\n"))
	if err != nil || vals[WorkerListenEnv] != "unix,127.0.0.1:7381" {
		t.Fatalf("%v %v", vals, err)
	}
	if _, err := ParseWorkerEnv(strings.NewReader("STAGE_WORKER_LISTEN=127.0.0.1:7381\n")); err == nil || !strings.Contains(err.Error(), "must include unix") {
		t.Fatalf("gateway without unix accepted: %v", err)
	}
}

func TestReadWorkerEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), WorkerEnvFile)
	if vals, err := ReadWorkerEnv(path); err != nil || len(vals) != 0 {
		t.Fatalf("missing: %v %v", vals, err)
	}
	if err := os.WriteFile(path, []byte("STAGE_WORKER_CONFIG=/w.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkerEnv(path); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("group-readable accepted: %v", err)
	}
	_ = os.Chmod(path, 0o600)
	if vals, err := ReadWorkerEnv(path); err != nil || vals[WorkerConfigEnv] != "/w.json" {
		t.Fatalf("%v %v", vals, err)
	}
	if p, _ := WorkerEnvPath(func(k string) string { return map[string]string{"HOME": "/h"}[k] }, "linux"); p != "/h/.config/stage/worker.env" {
		t.Fatalf("path %s", p)
	}
}
