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
// whether the turn is over, the first prompt's entrypoint, and whether the
// last turn ran in a background session (a `-p` seed then `--bg --resume`
// is background from its second turn).
func TestTranscriptFixtures(t *testing.T) {
	cases := []struct {
		file, entrypoint, reply, branch string
		over, background                bool
	}{
		{"done-text", "cli", "**The Keeper of Hollow Point**", "", true, true},
		{"blocked-question", "cli", "Blocked: the harness rejected", "", true, true},
		{"blocked-permission", "cli", "", "", false, true},
		{"working", "cli", "", "", false, true},
		{"worktree", "cli", "wt ok", "worktree-spikewt", true, true},
		{"print-then-bg", "sdk-cli", "r2 bg", "", true, true},
		{"t3-code", "t3-code", "Reply text.", "", false, false},
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
			if tr.Background != c.background {
				t.Fatalf("background %v", tr.Background)
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

// Usage from the captured transcripts: each assistant message counted once
// although its records repeat it (blocked-question has three messages over
// five records), synthetic and usage-less records skipped, turns are typed
// prompts and working time sums turn_duration (finished turns only).
func TestTranscriptUsageFixtures(t *testing.T) {
	cases := []struct {
		file    string
		turns   int
		working int64
		tokens  claudecode.Tokens
	}{
		{"done-text", 1, 23015, claudecode.Tokens{Input: 2, CacheRead: 27618, CacheWrite: 16652, Output: 4875}},
		{"blocked-question", 1, 7827, claudecode.Tokens{Input: 6, CacheRead: 119137, CacheWrite: 19926, Output: 665}},
		{"blocked-permission", 1, 0, claudecode.Tokens{Input: 2, CacheRead: 25662, CacheWrite: 16554, Output: 88}},
		{"working", 1, 0, claudecode.Tokens{}},
		{"print-then-bg", 2, 3163, claudecode.Tokens{Input: 4, CacheRead: 13430, CacheWrite: 53336, Output: 14}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			tr, err := claudecode.ReadTranscript(filepath.Join("testdata", "transcripts", c.file+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if tr.Turns != c.turns || tr.WorkingMs != c.working || tr.Tokens != c.tokens {
				t.Fatalf("turns %d working %d tokens %+v, want %d %d %+v", tr.Turns, tr.WorkingMs, tr.Tokens, c.turns, c.working, c.tokens)
			}
		})
	}
}

// A message streamed over several records counts its largest usage once;
// sidechain messages count, synthetic ones do not, and a record with
// neither a message nor a request id counts on its own. A prompt with no
// origin (-p, SDK) or a human one is a turn; a subagent's notification and
// a peer's message are not.
func TestTranscriptUsageDedupe(t *testing.T) {
	lines := []string{
		`{"type":"user","sessionId":"s","uuid":"u1","timestamp":"2026-11-25T03:00:00Z","entrypoint":"cli","cwd":"/w","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","requestId":"r1","timestamp":"2026-11-25T03:00:01Z","message":{"id":"m1","model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":3,"cache_read_input_tokens":100,"cache_creation_input_tokens":10,"output_tokens":1}}}`,
		`{"type":"assistant","requestId":"r1","timestamp":"2026-11-25T03:00:02Z","message":{"id":"m1","model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":3,"cache_read_input_tokens":100,"cache_creation_input_tokens":10,"output_tokens":40}}}`,
		`{"type":"assistant","isSidechain":true,"requestId":"r2","timestamp":"2026-11-25T03:00:03Z","message":{"id":"m2","model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":5}}}`,
		`{"type":"assistant","timestamp":"2026-11-25T03:00:04Z","message":{"model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`,
		`{"type":"assistant","timestamp":"2026-11-25T03:00:05Z","message":{"model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`,
		`{"type":"assistant","timestamp":"2026-11-25T03:00:06Z","message":{"id":"m3","model":"<synthetic>","content":[],"usage":{"input_tokens":0,"output_tokens":999}}}`,
		`{"type":"system","subtype":"turn_duration","isSidechain":true,"durationMs":500,"timestamp":"2026-11-25T03:00:07Z"}`,
		`{"type":"system","subtype":"turn_duration","durationMs":7000,"timestamp":"2026-11-25T03:00:08Z"}`,
		`{"type":"user","sessionId":"s","uuid":"u2","timestamp":"2026-11-25T03:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]}}`,
		`{"type":"user","sessionId":"s","uuid":"u3","timestamp":"2026-11-25T03:02:00Z","origin":{"kind":"human"},"message":{"role":"user","content":[{"type":"text","text":"again"}]}}`,
		`{"type":"user","sessionId":"s","uuid":"u4","timestamp":"2026-11-25T03:03:00Z","origin":{"kind":"task-notification"},"message":{"role":"user","content":"<task-notification>done</task-notification>"}}`,
		`{"type":"user","sessionId":"s","uuid":"u5","timestamp":"2026-11-25T03:04:00Z","origin":{"kind":"peer"},"message":{"role":"user","content":"from another session"}}`,
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := claudecode.ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	want := claudecode.Tokens{Input: 3 + 1 + 1 + 1, CacheRead: 100, CacheWrite: 10, Output: 40 + 5 + 2 + 2}
	if tr.Tokens != want || tr.Turns != 2 || tr.WorkingMs != 7000 {
		t.Fatalf("tokens %+v turns %d working %d, want %+v 2 7000", tr.Tokens, tr.Turns, tr.WorkingMs, want)
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
