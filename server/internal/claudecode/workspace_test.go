package claudecode_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

func TestWorkspaceEvidenceUsesStateAndExactListedFolders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override string
	}{
		{name: "default"},
		{name: "custom", override: "custom-config"},
		{name: "explicit default directory", override: ".claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := claudetest.New(t)
			f.GitInit()
			cli := f.CLI()
			config, stateDir := f.ConfigDir(), f.Home
			if tc.override != "" {
				config = filepath.Join(f.Home, tc.override)
				stateDir = config
				cli.Environ = append(cli.Environ, "CLAUDE_CONFIG_DIR="+config)
				f.WriteClaudeJSON(filepath.Join(f.Home, "wrong-state-file"))
			}
			projectsDir := filepath.Join(config, "projects")
			nested := filepath.Join(f.Workspace, "app", "src")
			plain := filepath.Join(f.Home, "notes_a-b with space")
			unlisted := filepath.Join(f.Home, "unlisted")
			managed := filepath.Join(f.Workspace, ".claude", "worktrees", "branch")
			for _, dir := range []string{nested, plain, unlisted, managed} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// The state contains the checkout top. Listed sessions also
			// attest to nested and plain folders missing from the state.
			f.Update(func(s *fakestate.State) {
				s.ConfigDir, s.ProjectsDir = config, projectsDir
				for i, cwd := range []string{plain, f.Workspace, nested, nested + "/.", managed, "relative"} {
					e := fakestate.Entry{Cwd: cwd, Kind: "interactive", SessionID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)}
					if cwd == plain {
						e.Kind, e.State = "background", "done"
					}
					s.Sessions = append(s.Sessions, fakestate.Session{Entry: e})
				}
			})
			if err := fakestate.AppendUser(projectsDir, unlisted, "11111111-1111-4111-8111-111111111111", "cli", "", "unlisted transcript", time.Now()); err != nil {
				t.Fatal(err)
			}
			state, err := json.Marshal(map[string]map[string]struct{}{
				"projects": {f.Workspace: {}, f.Workspace + "/.": {}, managed: {}, "relative": {}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, ".claude.json"), state, 0o600); err != nil {
				t.Fatal(err)
			}

			a := claudecode.New(claudecode.Options{Connection: conn, CLI: cli, Identity: config})
			ws, err := a.Workspaces(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := []string{f.Workspace, nested, plain}
			slices.Sort(want)
			got := make([]string, 0, len(ws))
			for _, w := range ws {
				got = append(got, w.ID)
				if w.WorkspaceRoot != w.ID || w.Title != filepath.Base(w.ID) || !w.Local || w.ImportIf != execution.ImportCheckoutTop {
					t.Fatalf("workspace identity or import rule: %+v", w)
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("workspace paths %q, want %q", got, want)
			}
			withEvidence, err := (machine.LocalOps{}).Inventory(t.Context(), ws)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range withEvidence {
				if w.Evidence == nil || w.Evidence.CanonicalPath != w.ID || w.Importable != (w.ID == f.Workspace) {
					t.Fatalf("filesystem evidence or effective import policy: %+v, evidence %+v", w, w.Evidence)
				}
			}
			in := intent(f, "untrusted-listed-folder")
			in.Project, in.Directory = plain, plain
			var rejected *execution.Rejected
			if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); !errors.As(err, &rejected) || len(f.Launches()) != 1 {
				t.Fatalf("listed folder must go through Claude's launch trust check: %v", err)
			}
		})
	}
}

func TestWorkspaceEvidenceFailsIncompleteReadsAndRecovers(t *testing.T) {
	f := claudetest.New(t)
	a := claudecode.New(claudecode.Options{Connection: conn, CLI: f.CLI(), Identity: f.ConfigDir(), RosterTTL: time.Hour})
	listed := filepath.Join(f.Home, "listed")
	f.Update(func(s *fakestate.State) {
		s.Sessions = []fakestate.Session{{Entry: fakestate.Entry{
			Cwd: listed, Kind: "interactive", SessionID: "11111111-1111-4111-8111-111111111111",
		}}}
	})
	if ws, err := a.Workspaces(t.Context()); err != nil || len(ws) != 2 {
		t.Fatalf("initial inventory: %+v %v", ws, err)
	}
	f.Update(func(s *fakestate.State) { s.ConfigDir = filepath.Join(f.Home, "other-config") })
	if ws, err := a.Workspaces(t.Context()); ws != nil || err == nil || !strings.Contains(err.Error(), "now uses") {
		t.Fatalf("inventory after a config switch: %+v %v", ws, err)
	}
	f.Update(func(s *fakestate.State) { s.ConfigDir = f.ConfigDir() })

	path := filepath.Join(f.Home, ".claude.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{`, `null`, `{"projects":null}`, `{"projects":[]}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		if ws, err := a.Workspaces(t.Context()); ws != nil || err == nil {
			t.Fatalf("inventory with a malformed state %q: %+v %v", data, ws, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if ws, err := a.Workspaces(t.Context()); ws != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inventory with absent state: %+v %v", ws, err)
	}
	// A freshly initialized state can omit projects.
	if err := os.WriteFile(path, []byte(`{"firstStartVersion":"2.1.332"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.Update(func(s *fakestate.State) { s.FailAgents = true })
	if ws, err := a.Workspaces(t.Context()); ws != nil || err == nil {
		t.Fatalf("inventory with unavailable roster: %+v %v", ws, err)
	}
	f.Update(func(s *fakestate.State) { s.FailAgents = false })
	if ws, err := a.Workspaces(t.Context()); err != nil || len(ws) != 1 || ws[0].ID != listed {
		t.Fatalf("recovered inventory: %+v %v", ws, err)
	}
	f.Update(func(s *fakestate.State) { s.Sessions = nil })
	if ws, err := a.Workspaces(t.Context()); err != nil || ws == nil || len(ws) != 0 {
		t.Fatalf("authoritative empty inventory: %+v %v", ws, err)
	}
}

// Untrusted folders stay in inventory with Claude's trust rule, so machine
// ops mark them not launchable; trust follows hasTrustDialogAccepted.
func TestWorkspacesCarryClaudeTrust(t *testing.T) {
	f := claudetest.New(t)
	f.GitInit()
	trustedParent := filepath.Join(f.Home, "work")
	inherited := filepath.Join(trustedParent, "notes")
	declined := filepath.Join(f.Home, "lab")
	for _, dir := range []string{inherited, declined} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := `{"projects":{
		"` + f.Home + `":{"hasTrustDialogAccepted":false},
		"` + f.Workspace + `":{"hasTrustDialogAccepted":true},
		"` + trustedParent + `":{"hasTrustDialogAccepted":true},
		"` + declined + `/":{"hasTrustDialogAccepted":true},
		"` + inherited + `":{"hasTrustDialogAccepted":false},
		"` + declined + `":{},
		"/odd":{"hasTrustDialogAccepted":"true"},
		"/broken":[]
	}}`
	if err := os.WriteFile(filepath.Join(f.Home, ".claude.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	f.Update(func(s *fakestate.State) { s.Sessions = nil })
	cli := f.CLI()
	ws, err := claudecode.New(claudecode.Options{Connection: conn, CLI: cli, Identity: f.ConfigDir()}).Workspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	records := []string{declined + "/", trustedParent, f.Workspace}
	slices.Sort(records)
	rows := map[string]execution.WorkspaceInfo{}
	for _, w := range ws {
		rows[w.ID] = w
		if w.Trust == nil || !slices.Equal(w.Trust.Records, records) || w.BlockedReason != "" {
			t.Fatalf("trust rule for %s: %+v", w.ID, w.Trust)
		}
	}
	if got := rows[f.Home].Trust.Reason; !strings.Contains(got, "home folder one session at a time") {
		t.Fatalf("home reason %q", got)
	}
	if got := rows[declined].Trust.Reason; got != "Not trusted in Claude Code. Use Trust in Claude Code, or run claude in this folder once and accept the trust prompt." {
		t.Fatalf("untrusted reason %q", got)
	}

	withEvidence, err := machine.LocalOps{}.Inventory(t.Context(), ws)
	if err != nil {
		t.Fatal(err)
	}
	blocked := map[string]bool{}
	for _, w := range withEvidence {
		blocked[w.ID] = w.BlockedReason != ""
	}
	for dir, want := range map[string]bool{f.Workspace: false, trustedParent: false, inherited: false, declined: true, f.Home: true} {
		if blocked[dir] != want {
			t.Errorf("%s blocked = %v, want %v", dir, blocked[dir], want)
		}
	}

	cli.Environ = append(cli.Environ, "CLAUDE_CODE_SANDBOXED= Yes ")
	ws, err = claudecode.New(claudecode.Options{Connection: conn, CLI: cli, Identity: f.ConfigDir()}).Workspaces(t.Context())
	if err != nil || slices.ContainsFunc(ws, func(w execution.WorkspaceInfo) bool { return w.Trust != nil }) {
		t.Fatalf("sandboxed Claude trusts every workspace: %+v %v", ws, err)
	}
}
