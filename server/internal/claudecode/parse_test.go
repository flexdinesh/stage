package claudecode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
)

func roster(t *testing.T, name string) []claudecode.Entry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "roster", name))
	if err != nil {
		t.Fatal(err)
	}
	es, err := claudecode.ParseRoster(b)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

// Every captured session list maps by ADR 0005 decision 4: background
// sessions by state, interactive ones by status.
func TestRosterFixturesMapStates(t *testing.T) {
	want := map[string]map[string]string{
		"all-working.json":            {"working": "running", "done": "done"},
		"all-blocked-permission.json": {"blocked": "needs_input"},
		"all-blocked-question.json":   {"blocked": "needs_input"},
		"all-after-stop.json":         {"stopped": "interrupted", "done": "done"},
		"all-failed.json":             {"failed": "failed"},
		"all-reaped.json":             {"done": "done", "blocked": "needs_input", "stopped": "interrupted"},
	}
	for file, states := range want {
		t.Run(file, func(t *testing.T) {
			seen := map[string]bool{}
			for _, e := range roster(t, file) {
				got, err := claudecode.MapState(e)
				if err != nil {
					t.Fatalf("%+v: %v", e, err)
				}
				if !e.Background() {
					if e.ID != "" || e.State != "" {
						t.Fatalf("interactive entry with background fields %+v", e)
					}
					if got != map[string]string{"busy": "running", "waiting": "needs_input", "idle": "idle", "": "idle"}[e.Status] {
						t.Fatalf("interactive %q mapped %q", e.Status, got)
					}
					continue
				}
				if w, ok := states[e.State]; ok {
					seen[e.State] = true
					if got != w {
						t.Fatalf("%s mapped %q, want %q", e.State, got, w)
					}
				}
				if len(e.ID) != 8 || !strings.HasPrefix(e.SessionID, e.ID) {
					t.Fatalf("short id %q of %q", e.ID, e.SessionID)
				}
			}
			for s := range states {
				if !seen[s] {
					t.Fatalf("fixture has no %s session", s)
				}
			}
		})
	}
	// A blocked permission prompt and a turn that ended asking the user
	// look different in the list but map the same.
	for _, e := range roster(t, "all-blocked-permission.json") {
		if e.State == "blocked" && (e.Status != "waiting" || e.WaitingFor != "permission prompt") {
			t.Fatalf("permission prompt entry %+v", e)
		}
	}
	// Reaped sessions keep their state with no process.
	reaped := 0
	for _, e := range roster(t, "all-reaped.json") {
		if e.Background() && e.PID == 0 && e.Status == "" && (e.State == "done" || e.State == "blocked") {
			reaped++
		}
	}
	if reaped == 0 {
		t.Fatal("no reaped session in the fixture")
	}
	if _, err := claudecode.MapState(claudecode.Entry{Kind: claudecode.KindBackground, State: "paused"}); err == nil {
		t.Fatal("unknown state mapped")
	}
	if _, err := claudecode.ParseRoster([]byte("not json")); err == nil {
		t.Fatal("malformed list parsed")
	}
}

// The ADR's reply table (E4): the last assistant text of the current turn,
// whether the turn is over, and the first prompt's entrypoint.
func TestTranscriptFixtures(t *testing.T) {
	cases := []struct {
		file, entrypoint, reply, branch string
		over                            bool
	}{
		{"done-text", "cli", "**The Keeper of Hollow Point**", "", true},
		{"blocked-question", "cli", "Blocked: the harness rejected", "", true},
		{"blocked-permission", "cli", "", "", false},
		{"working", "cli", "", "", false},
		{"worktree", "cli", "wt ok", "worktree-spikewt", true},
		{"print-then-bg", "sdk-cli", "r2 bg", "", true},
		{"t3-code", "t3-code", "Reply text.", "", false},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join("testdata", "transcripts", c.file+".jsonl")
			tr, err := claudecode.ReadTranscript(path)
			if err != nil {
				t.Fatal(err)
			}
			if tr.Entrypoint != c.entrypoint || tr.TurnOver != c.over || !strings.HasPrefix(tr.Reply, c.reply) || (c.reply == "") != (tr.Reply == "") {
				t.Fatalf("entrypoint %q over %v reply %.60q", tr.Entrypoint, tr.TurnOver, tr.Reply)
			}
			if c.branch != "" && tr.Branch != c.branch {
				t.Fatalf("branch %q", tr.Branch)
			}
			if tr.SessionID == "" || tr.Cwd == "" || tr.FirstPrompt == "" || tr.FirstAt.IsZero() || tr.LastAt.Before(tr.FirstAt) || tr.LastPromptID == "" {
				t.Fatalf("identity %+v", tr)
			}
			head, err := claudecode.ReadHead(path)
			if err != nil || head.FirstPrompt != tr.FirstPrompt || head.Entrypoint != tr.Entrypoint || head.Cwd != tr.Cwd {
				t.Fatalf("head %+v %v", head, err)
			}
		})
	}
	// Titles: the user's title wins over Claude's.
	tr, _ := claudecode.ReadTranscript(filepath.Join("testdata", "transcripts", "done-text.jsonl"))
	if tr.Title != "Stage working probe 2" || tr.Model != "claude-haiku-5-5" {
		t.Fatalf("title %q model %q", tr.Title, tr.Model)
	}
	// The launch marker is in the first prompt.
	if !strings.Contains(tr.FirstPrompt, "stage-launch:") {
		t.Fatalf("marker missing from %q", tr.FirstPrompt)
	}
}

func TestEncodeProject(t *testing.T) {
	for in, want := range map[string]string{
		"/tmp/x/.claude/worktrees/w": "-tmp-x--claude-worktrees-w",
		"/home/user/src/spike":       "-home-user-src-spike",
		"/a/b-c.d":                   "-a-b-c-d",
	} {
		if got := claudecode.EncodeProject(in); got != want {
			t.Fatalf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestParseBackgroundedAndWorktreeName(t *testing.T) {
	if s, ok := claudecode.ParseBackgrounded("backgrounded · b4889af7 · Stage task 999\n\n  claude agents"); !ok || s != "b4889af7" {
		t.Fatal(s, ok)
	}
	if _, ok := claudecode.ParseBackgrounded("Starting background service…"); ok {
		t.Fatal("no id parsed one")
	}
	if got := claudecode.WorktreeName("Req_12:ab-C"); got != "stage-req12ab-c" {
		t.Fatal(got)
	}
}
