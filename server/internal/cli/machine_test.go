package cli_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
)

func TestMachineCommands(t *testing.T) {
	h := newHarness(t, func(d *api.Deps) { d.Config.PublicURL = "https://stage.example.test" })
	code, out, _ := h.run("", "machine")
	if code != 0 || !strings.Contains(out, "ID") || !strings.Contains(out, "local") || !strings.Contains(out, "online") {
		t.Fatalf("ls %d %q", code, out)
	}
	code, out, errb := h.run("", "machine", "pair", "--label", "desk", "--json")
	if code != 0 {
		t.Fatalf("pair %d %q", code, errb)
	}
	p := decode[client.Pairing](t, out)
	if p.Label != "desk" || p.Link != "https://stage.example.test/join#"+p.Code || p.Command != "stage worker join "+p.Link {
		t.Fatalf("pairing %+v", p)
	}
	code, out, _ = h.run("", "machine", "pair")
	if code != 0 || !strings.Contains(out, "stage worker join https://stage.example.test/join#stage_pc_") || !strings.Contains(out, "works once") {
		t.Fatalf("pair human %d %q", code, out)
	}

	req := httptest.NewRequest("POST", "/api/worker/join", strings.NewReader(`{"code":"`+p.Code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(buildinfo.APIVersionHeader, strconv.Itoa(buildinfo.APIVersion))
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("join %d %s", rec.Code, rec.Body)
	}

	code, out, _ = h.run("", "machines", "ls", "--json")
	ms := decode[[]client.Machine](t, out)
	if code != 0 || len(ms) != 2 || ms[1].Label != "desk" || ms[1].Kind != "remote" || ms[1].Online {
		t.Fatalf("ls json %d %+v", code, ms)
	}
	id := strconv.FormatInt(ms[1].ID, 10)
	code, out, _ = h.run("", "machine", "rename", id, "work", "laptop")
	if code != 0 || !strings.Contains(out, "✓ Renamed machine "+id+" to work laptop") {
		t.Fatalf("rename %d %q", code, out)
	}
	code, out, _ = h.run("", "machine", "rm", id)
	if code != 0 || !strings.Contains(out, "✓ Removed machine "+id) {
		t.Fatalf("rm %d %q", code, out)
	}
	if code, _, errb := h.run("", "machine", "rm", "1"); code != 1 || !strings.Contains(errb, "cannot be removed") {
		t.Fatalf("rm local %d %q", code, errb)
	}
	if code, _, _ := h.run("", "machine", "rename", id); code != 2 {
		t.Fatalf("rename without label: %d", code)
	}
	if code, _, _ := h.run("", "machine", "nope"); code != 2 {
		t.Fatalf("unknown verb: %d", code)
	}
}

func TestMachinePairNeedsPublicURL(t *testing.T) {
	h := newHarness(t)
	if code, _, errb := h.run("", "machine", "pair"); code != 1 || !strings.Contains(errb, "STAGE_PUBLIC_URL") {
		t.Fatalf("pair %d %q", code, errb)
	}
}

func TestMachinePing(t *testing.T) {
	h := newHarness(t)
	code, out, errb := h.run("", "machine", "ping", "1")
	if code != 0 || !strings.Contains(out, "✓ Machine 1 answered in") {
		t.Fatalf("ping local %d %q %q", code, out, errb)
	}
	if code, _, errb := h.run("", "machine", "ping", "99"); code != 1 || !strings.Contains(errb, "not found") {
		t.Fatalf("ping unknown %d %q", code, errb)
	}
	if code, _, _ := h.run("", "machine", "ping"); code != 2 {
		t.Fatalf("ping without id: %d", code)
	}
}

func TestWorkerCommandsWithoutJoin(t *testing.T) {
	h := newHarness(t)
	cfg := t.TempDir() + "/worker.json"
	if code, _, _ := h.run("", "worker"); code != 2 {
		t.Fatalf("worker without verb: %d", code)
	}
	if code, _, _ := h.run("", "worker", "nope"); code != 2 {
		t.Fatalf("unknown verb: %d", code)
	}
	if code, _, _ := h.run("", "worker", "join", "--config", cfg); code != 2 {
		t.Fatalf("join without link: %d", code)
	}
	if code, _, _ := h.run("", "worker", "run", "--managed", "--config", cfg); code != 2 {
		t.Fatalf("run --managed --config: %d", code)
	}
	code, out, _ := h.run("", "worker", "status", "--config", cfg)
	if code != 4 || !strings.Contains(out, "! Not joined") || !strings.Contains(out, "join with   stage worker join <link>") {
		t.Fatalf("status %d %q", code, out)
	}
	if code, _, errb := h.run("", "worker", "run", "--config", cfg); code != 78 || !strings.Contains(errb, "not joined") {
		t.Fatalf("run not joined %d %q", code, errb)
	}
	if code, _, errb := h.run("", "worker", "join", "--config", cfg, "https://h.example/other#c"); code == 0 || errb == "" {
		t.Fatalf("join bad link %d %q", code, errb)
	}
}

// TestWorkerJoinOutput: join prints the outcome, the config path and the
// next steps under this install's CLI name.
func TestWorkerJoinOutput(t *testing.T) {
	h := newHarness(t, func(d *api.Deps) { d.Config.PublicURL = "https://stage.example.test" })
	h.home = t.TempDir()
	rec := filepath.Join(h.home, ".local", "lib", buildinfo.Identity, "install.json")
	if err := os.MkdirAll(filepath.Dir(rec), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, []byte(`{"schema":1,"cliName":"stage-dev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errb := h.run("", "machine", "pair", "--label", "box", "--json")
	if code != 0 {
		t.Fatalf("pair %d %q", code, errb)
	}
	p := decode[client.Pairing](t, out)
	cfg := filepath.Join(t.TempDir(), "worker.json")
	code, out, errb = h.run("", "worker", "join", "--config", cfg, h.url+"/join#"+p.Code)
	want := "✓ Joined " + h.url + " as machine 2 (box)\n" +
		"  config      " + cfg + "\n" +
		"\nNext:\n" +
		"  stage-dev worker service start   run in the background, restarted by the OS\n" +
		"  stage-dev worker run             run in the foreground\n"
	if code != 0 || out != want {
		t.Fatalf("join %d %q\nwant %q\nstderr %q", code, out, want, errb)
	}
}
