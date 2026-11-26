package claudecode_test

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// resumes are the fake's `--bg --resume` calls.
func resumes(f *claudetest.Fake) []fakestate.Call {
	var out []fakestate.Call
	for _, c := range f.Read().Calls {
		if len(c.Args) > 1 && c.Args[0] == "--bg" && c.Args[1] == "--resume" {
			out = append(out, c)
		}
	}
	return out
}

func transcriptHas(t *testing.T, f *claudetest.Fake, id, text string) bool {
	t.Helper()
	b, err := os.ReadFile(fakestate.TranscriptPath(f.ProjectsDir(), f.Session(id).Cwd, id))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), text)
}

// A finished session whose process lives is stopped, then woken under its
// own id with the message after "--" and no flags (a flag, or a live
// process, would start a copy): it runs again.
func TestSendWakesFinishedSession(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-send"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.Reply(id, "Done.", true)
	f.SetState(id, "done", "idle", "")
	if err := a.Send(t.Context(), ref, execution.Message{Text: "-now add tests"}); err != nil {
		t.Fatal(err)
	}
	short := f.Session(id).ID
	calls := f.Read().Calls
	stopAt := slices.IndexFunc(calls, func(c fakestate.Call) bool { return slices.Equal(c.Args, []string{"stop", short}) })
	rs := resumes(f)
	if stopAt < 0 || len(rs) != 1 {
		t.Fatalf("stop at %d, resumes %d", stopAt, len(rs))
	}
	if want := []string{"--bg", "--resume", id, "--", "-now add tests"}; !slices.Equal(rs[0].Args, want) || rs[0].Dir != f.Workspace {
		t.Fatalf("resume %q in %s", rs[0].Args, rs[0].Dir)
	}
	if n := len(f.Read().Sessions); n != 1 {
		t.Fatalf("a copy started: %d sessions", n)
	}
	if !transcriptHas(t, f, id, "-now add tests") {
		t.Fatal("message not in the transcript")
	}
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "running" {
		t.Fatalf("%+v %v", o, err)
	}
}

// A session whose process already exited (reaped, stopped while done) is
// woken without a stop; one blocked on a finished turn (it asked its user)
// takes the message as its answer.
func TestSendWithoutProcessAndToAQuestion(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-q"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.Reply(id, "Which color?", true)
	f.Update(func(s *fakestate.State) {
		s.Sessions[0].State, s.Sessions[0].Status, s.Sessions[0].PID = "blocked", "", 0
	})
	if err := a.Send(t.Context(), ref, execution.Message{Text: "teal"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Read().Calls {
		if len(c.Args) > 0 && c.Args[0] == "stop" {
			t.Fatalf("stopped an exited session: %q", c.Args)
		}
	}
	if s := f.Session(id); s.State != "working" || !transcriptHas(t, f, id, "teal") {
		t.Fatalf("%+v", s)
	}
}

// Busy: working, a permission prompt or question (also a reaped one whose
// turn is still open), a terminal session, a removed one, or a folder that
// is gone; nothing is stopped or sent.
func TestSendBusy(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-b"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	busy := func(name, want string) {
		t.Helper()
		var b *execution.Busy
		if err := a.Send(t.Context(), ref, execution.Message{Text: "hi"}); !errors.As(err, &b) || !strings.Contains(b.Reason, want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	busy("working", "is working")
	f.SetState(id, "blocked", "waiting", "permission prompt")
	busy("permission prompt", "permission prompt or question")
	f.SetState(id, "blocked", "idle", "input needed")
	busy("question", "permission prompt or question")
	f.Reply(id, "", false)
	f.Update(func(s *fakestate.State) { s.Sessions[0].State, s.Sessions[0].Status, s.Sessions[0].PID = "blocked", "", 0 })
	busy("reaped mid-turn", "permission prompt or question")
	f.SetState(id, "stopped", "", "")
	busy("stopped", "claude attach")
	f.SetState(id, "done", "idle", "")
	if err := os.Rename(f.Workspace, f.Workspace+".gone"); err != nil {
		t.Fatal(err)
	}
	busy("folder gone", "is gone")
	if err := os.Rename(f.Workspace+".gone", f.Workspace); err != nil {
		t.Fatal(err)
	}
	f.Remove(id)
	busy("removed", "claude --resume "+id)
	inter := "22222222-2222-4222-8222-222222222222"
	f.AddInteractive(inter, f.Workspace, "cli", "hi")
	var b *execution.Busy
	if err := a.Send(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: inter}, execution.Message{Text: "hi"}); !errors.As(err, &b) {
		t.Fatalf("interactive: %v", err)
	}
	for _, c := range f.Read().Calls {
		if len(c.Args) > 0 && (c.Args[0] == "stop" || slices.Contains(c.Args, "--resume")) {
			t.Fatalf("busy session touched: %q", c.Args)
		}
	}
}

// A copy (the session got busy between the check and the resume) is
// stopped and removed, and the send is Busy; a failed wake is an error.
func TestSendCopyAndFailedWake(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-c"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.Reply(id, "Done.", true)
	f.SetState(id, "done", "idle", "")
	f.Update(func(s *fakestate.State) { s.CopyOnResume = true })
	var b *execution.Busy
	if err := a.Send(t.Context(), ref, execution.Message{Text: "again"}); !errors.As(err, &b) || !strings.Contains(b.Reason, "copy") {
		t.Fatalf("copy: %v", err)
	}
	var listed []string
	for _, s := range f.Read().Sessions {
		if !s.Removed {
			listed = append(listed, s.SessionID)
		}
	}
	if !slices.Equal(listed, []string{id}) {
		t.Fatalf("copy kept: %v", listed)
	}
	f.Update(func(s *fakestate.State) { s.CopyOnResume, s.FailResume = false, true })
	if err := a.Send(t.Context(), ref, execution.Message{Text: "again"}); err == nil || errors.As(err, &b) {
		t.Fatalf("failed wake: %v", err)
	}
}

// While a Send delivers, the stopped entry of the session it woke reads
// running (no false interrupted); afterwards it reads truthfully.
func TestSendGuardsTheStoppedFlicker(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-g"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.SetState(id, "stopped", "", "")
	release := a.Wake(id)
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "running" {
		release()
		t.Fatalf("during send %+v %v", o, err)
	}
	release()
	f.SetState(id, "stopped", "", "")
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "interrupted" {
		t.Fatalf("after %+v %v", o, err)
	}
}
