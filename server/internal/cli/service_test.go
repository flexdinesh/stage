package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/service"
)

// noManager is a Runner with no reachable service manager.
type noManager struct{ calls int }

func (n *noManager) Run(context.Context, service.Cmd) (service.Result, error) {
	n.calls++
	return service.Result{Code: 1, Stderr: "Failed to connect to bus: No medium found\n"}, nil
}

func runService(t *testing.T, r service.Runner, args ...string) (int, string, string) {
	t.Helper()
	home := t.TempDir()
	var out, errb bytes.Buffer
	env := cli.Env{
		Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return map[string]string{"HOME": home, "STAGE_URL": "http://remote:7380"}[k] },
		Service: func() service.Deps {
			return service.Deps{
				GOOS: "linux", UID: 1000, Getenv: func(k string) string { return map[string]string{"HOME": home, "XDG_RUNTIME_DIR": "/run/user/1000"}[k] },
				Runner: r, Self: func() string { return "/usr/bin/stage" },
				Health: func(context.Context, string) (client.Health, error) { return client.Health{}, context.Canceled },
			}
		},
	}
	code := cli.Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func TestServiceRejectsURL(t *testing.T) {
	r := &noManager{}
	if code, _, errOut := runService(t, r, "service", "start", "--url", "http://x"); code != 2 || !strings.Contains(errOut, "--url/STAGE_URL do not apply") {
		t.Fatalf("--url: %d %q", code, errOut)
	}
	if code, _, _ := runService(t, r, "service", "bogus"); code != 2 {
		t.Fatalf("bogus verb: %d", code)
	}
	if r.calls != 0 {
		t.Fatal("manager called on a usage error")
	}
}

func TestServiceNoManager(t *testing.T) {
	code, out, _ := runService(t, &noManager{}, "service", "start", "--json")
	var rep service.Report
	if code != 6 || json.Unmarshal([]byte(out), &rep) != nil || rep.OK || rep.Code != 6 || !strings.Contains(rep.Fix, "enable-linger") {
		t.Fatalf("start: %d %s", code, out)
	}
	code, out, _ = runService(t, &noManager{}, "service", "status", "--json")
	var st service.Status
	if code != 6 || json.Unmarshal([]byte(out), &st) != nil || st.State != "unknown" || st.Manager.Reachable {
		t.Fatalf("status: %d %s", code, out)
	}
	code, out, _ = runService(t, &noManager{}, "service", "status")
	if code != 6 || !strings.Contains(out, "✗ stage service (systemd stage.service): unknown") || !strings.Contains(out, "✗ No systemd user manager") {
		t.Fatalf("status text: %d %s", code, out)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := runService(t, &noManager{}, "version", "--json")
	var bi service.BuildInfo
	if code != 0 || json.Unmarshal([]byte(out), &bi) != nil || bi.Identity != "stage" || bi.APIVersion != 7 || bi.ServiceUnit != "stage.service" || bi.ServiceLabel != "io.github.flexdinesh.stage" || bi.Schema != sqlite.MaxVersion() {
		t.Fatalf("version: %d %s", code, out)
	}
}
