package codex

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// Real codex-cli 0.171.0 replies (testdata, sanitized) decode into the
// wire shapes and map to Stage's observation.
func TestRecordedArchivedThreadMapsToClosure(t *testing.T) {
	var msg struct{ Result threadResponse }
	fixture(t, "g2-thread-read-archived.json", &msg)
	th := msg.Result.Thread
	if !threadID.MatchString(th.ID) || th.Status.Type != statusNotLoaded || th.Cwd == "" || th.CreatedAt <= 0 {
		t.Fatalf("decoded %+v", th)
	}
	var done struct {
		Msg struct {
			Params struct{ Turn turn } `json:"params"`
		} `json:"msg"`
	}
	var transcript []json.RawMessage
	fixture(t, "g4-transcript.json", &transcript)
	for _, m := range transcript {
		if strings.Contains(string(m), `"turn/completed"`) {
			if err := json.Unmarshal(m, &done); err != nil {
				t.Fatal(err)
			}
		}
	}
	tu := done.Msg.Params.Turn
	a := New(nil, 4, "")
	o, err := a.observation(th, &tu, true)
	if err != nil {
		t.Fatal(err)
	}
	if o.State != "done" || o.Reply == nil || o.Reply.Text != "OK" || o.Closed == nil || o.Closed.Signal != "archived" || !o.Archived || o.EndedAt == nil || o.Branch != "main" {
		t.Fatalf("observation %+v reply %+v", o, o.Reply)
	}
	if o.Links.Command != "cd /home/u/work/g1 && codex resume "+th.ID {
		t.Fatalf("resume command %q", o.Links.Command)
	}
}

func TestRecordedErrorsClassify(t *testing.T) {
	var unknown, missing struct{ Error RPCError }
	fixture(t, "misc-unknown-method-error.json", &unknown)
	fixture(t, "g6-thread-read-unknown.json", &missing)
	unknown.Error.Method = "foo/bar"
	if !unsupported(&unknown.Error) || notLoaded(&unknown.Error) {
		t.Fatal("unknown method", unknown.Error)
	}
	missing.Error.Method = "thread/read"
	if !notLoaded(&missing.Error) || unsupported(&missing.Error) {
		t.Fatal("unknown thread", missing.Error)
	}
}

func TestRecordedDaemonVersion(t *testing.T) {
	var rec struct{ Stdout DaemonInfo }
	fixture(t, "g7-daemon-version.json", &rec)
	if !rec.Stdout.Up() || rec.Stdout.CLIVersion != "0.171.0" || !strings.HasSuffix(rec.Stdout.SocketPath, "app-server-control.sock") {
		t.Fatalf("%+v", rec.Stdout)
	}
	var started struct{ Stdout DaemonInfo }
	fixture(t, "g7-daemon-start-already-running.json", &started)
	if started.Stdout.Status != "alreadyRunning" || !started.Stdout.Up() {
		t.Fatalf("%+v", started.Stdout)
	}
	if (DaemonInfo{Status: "notRunning", SocketPath: "/s"}).Up() || (DaemonInfo{Status: "running"}).Up() {
		t.Fatal("down daemon reported up")
	}
}

func TestEnvironDropsSessionVariables(t *testing.T) {
	got := Environ([]string{"HOME=/h", "CODEX_HOME=/c", "CODEX_THREAD_ID=x", "T3CODE_THREAD_ID=t", "STAGE_CALLER_SESSION=1:x", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=T3 Code", "PATH=/bin"})
	want := []string{"HOME=/h", "CODEX_HOME=/c", "PATH=/bin"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%v", got)
	}
}

func TestShellQuoteAndResume(t *testing.T) {
	a := New(nil, 1, "/opt/my codex/codex")
	if got := a.resumeIn("/w/it's", "id"); got != `cd '/w/it'\''s' && '/opt/my codex/codex' resume id` {
		t.Fatal(got)
	}
	if got := New(nil, 1, "").resumeIn("", "id"); got != "codex resume id" {
		t.Fatal(got)
	}
}

func TestObservationNeverInfersAnOutcomeFromUnknownState(t *testing.T) {
	a := New(nil, 1, "")
	th := thread{ID: "019a0000-0000-7000-8000-000000000001", Cwd: "/w", CreatedAt: 1, Status: threadStatus{Type: statusNotLoaded}}
	running := &turn{ID: "t", Status: turnInProgress}
	if _, err := a.observation(th, running, false); err == nil {
		t.Fatal("turn in progress on an unloaded thread mapped to a state")
	}
	th.Status.Type = statusSystemError
	if _, err := a.observation(th, &turn{ID: "t", Status: turnCompleted}, false); err == nil {
		t.Fatal("system error mapped to a state")
	}
	th.Status = threadStatus{Type: statusActive, ActiveFlags: []string{"waitingOnUserInput"}}
	if o, err := a.observation(th, running, false); err != nil || o.State != "needs_input" || !o.Active {
		t.Fatal(o, err)
	}
	th.Status = threadStatus{Type: statusIdle}
	if o, err := a.observation(th, nil, false); err != nil || o.State != "idle" {
		t.Fatal(o, err)
	}
	ended := int64(2)
	for status, want := range map[string]string{turnFailed: "failed", turnInterrupted: "interrupted"} {
		if o, err := a.observation(th, &turn{ID: "t", Status: status, CompletedAt: &ended}, false); err != nil || o.State != want || o.StateReason != "" {
			t.Fatal(status, o, err)
		}
	}
	// Interrupted without completedAt on an idle thread: a turn still
	// settling after turn/start, no evidence yet (Observe rereads it).
	if o, err := a.observation(th, &turn{ID: "t", Status: turnInterrupted}, false); !errors.Is(err, errUnclosed) || o.State != "interrupted" {
		t.Fatal(o, err)
	}
}

func TestFinalReplyPrefersFinalAnswerAndBounds(t *testing.T) {
	final, commentary := phaseFinalAnswer, "commentary"
	long := strings.Repeat("é", replyLimit+5)
	r := finalReply(turn{ID: "t", Items: []item{
		{Type: itemAgentMessage, ID: "a", Text: long, Phase: &final},
		{Type: itemAgentMessage, ID: "b", Text: "later commentary", Phase: &commentary},
	}})
	if r == nil || r.ItemID != "a" || !r.Truncated || len([]rune(r.Text)) != replyLimit {
		t.Fatalf("%+v", r)
	}
	if finalReply(turn{ID: "t"}) != nil {
		t.Fatal("reply from no messages")
	}
}
