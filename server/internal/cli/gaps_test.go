package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

// pngBytes is a valid 1x1 PNG.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0bIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func TestEditImagesConfigAndListFilters(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-gaps")
	workspace := h.workspaceID(p)
	h.caller = agentctx.Caller{Cwd: "/w/cli-gaps"}
	img := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(img, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun := func(args ...string) string {
		t.Helper()
		code, stdout, stderr := h.run("", args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, stderr)
		}
		return stdout
	}

	tk := decode[client.Task](t, mustRun("task", "add", "--project", "1", "--connection", "1", "--title", "a", "--prompt", "x",
		"--image", img, "--execution-config", `{"options":[{"id":"worktree","value":true}]}`, "--json"))
	if len(tk.Attachments) != 1 || tk.ExecutionConfig == nil || !tk.ExecutionConfig.Bool("worktree") {
		t.Fatalf("add %+v", tk)
	}
	tk = decode[client.Task](t, mustRun("task", "edit", fmt.Sprint(tk.ID), "--image", img, "--json"))
	if len(tk.Attachments) != 2 || tk.Attachments[1].Name != "shot.png" {
		t.Fatalf("edit --image appends: %+v", tk.Attachments)
	}
	tk = decode[client.Task](t, mustRun("task", "edit", fmt.Sprint(tk.ID), "--execution-config", "null", "--json"))
	if c := tk.ExecutionConfig; c != nil && c.Bool("worktree") {
		t.Fatalf("--execution-config null kept %+v", c)
	}
	if code, _, _ := h.run("", "task", "add", "--project", "1", "--connection", "1", "--title", "b", "--prompt", "x", "--execution-config", "null"); code != 2 {
		t.Fatalf("add --execution-config null: exit %d", code)
	}

	sc := decode[db.Schedule](t, mustRun("schedule", "add", "--connection", "1", "--workspace-id", workspace, "--title", "s", "--prompt", "x",
		"--cron", "0 2 * * *", "--tz", "UTC", "--image", img, "--json"))
	sc = decode[db.Schedule](t, mustRun("schedule", "edit", fmt.Sprint(sc.ID), "--image", img, "--json"))
	if len(sc.Attachments) != 2 {
		t.Fatalf("schedule edit --image: %+v", sc.Attachments)
	}
	run := decode[client.Task](t, mustRun("schedule", "run", fmt.Sprint(sc.ID), "--json"))

	rows := decode[agenttools.TasksOut](t, mustRun("task", "ls", "--schedule", fmt.Sprint(sc.ID), "--json"))
	if rows.Total != 1 || rows.Tasks[0].ID != run.ID {
		t.Fatalf("ls --schedule %+v", rows)
	}
	if rows := decode[agenttools.TasksOut](t, mustRun("task", "ls", "--from", "2999-01-01", "--json")); rows.Total != 0 {
		t.Fatalf("ls --from %+v", rows)
	}
	if rows := decode[agenttools.TasksOut](t, mustRun("task", "ls", "--to", "2999-01-01", "--completed-from", "2000-01-01", "--json")); rows.Total != 0 {
		t.Fatalf("ls --completed-from %+v", rows)
	}
	if code, _, _ := h.run("", "tree", "ls", "--completed-from", "soon"); code != 1 {
		t.Fatalf("tree ls bad --completed-from: exit %d", code)
	}
	if out := decode[agenttools.SchedulesOut](t, mustRun("schedule", "ls", "--due-within", "48h", "--json")); len(out.Schedules) != 1 {
		t.Fatalf("schedule ls --due-within %+v", out)
	}
	if out := decode[agenttools.ProjectsOut](t, mustRun("projects", "ls", "--archived", "true", "--json")); len(out.Groups) != 0 {
		t.Fatalf("projects ls --archived true %+v", out.Groups)
	}
}
