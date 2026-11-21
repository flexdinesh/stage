package cli_test

import (
	"net/http"
	"net/http/httptest"
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
	if code != 0 || !strings.Contains(out, "renamed to work laptop") {
		t.Fatalf("rename %d %q", code, out)
	}
	code, out, _ = h.run("", "machine", "rm", id)
	if code != 0 || !strings.Contains(out, "removed") {
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
