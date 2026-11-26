package claudecode_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// pushes runs a's Events until the test ends; the returned channel carries
// each pushed observation and errc Events' result.
func pushes(t *testing.T, a *claudecode.Adapter) (<-chan execution.Observation, <-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	out := make(chan execution.Observation, 16)
	errc := make(chan error, 1)
	go func() {
		errc <- a.Events(ctx, func(_ context.Context, o execution.Observation) error {
			out <- o
			return nil
		})
	}()
	t.Cleanup(cancel)
	return out, errc, cancel
}

func next(t *testing.T, ch <-chan execution.Observation) execution.Observation {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("no observation pushed")
	}
	return execution.Observation{}
}

func none(t *testing.T, ch <-chan execution.Observation, wait time.Duration) {
	t.Helper()
	select {
	case o := <-ch:
		t.Fatalf("unexpected push %+v", o)
	case <-time.After(wait):
	}
}

// launched starts a hooked session and waits until Events subscribed.
func launched(t *testing.T, f *claudetest.Fake, a *claudecode.Adapter) string {
	t.Helper()
	o, err := a.Launch(t.Context(), intent(f, "req-hooks"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return o.Session.ExternalSessionID
}

// A launch with hooks passes the settings file; without hooks it passes
// none.
func TestLaunchPassesHookSettings(t *testing.T) {
	f := claudetest.New(t)
	h := f.Hooks()
	launched(t, f, f.HookedAdapter(conn, h))
	args := f.Launches()[0].Args
	i := slices.Index(args, "--settings")
	if i < 0 || args[i+1] != h.SettingsPath() || slices.Index(args, "--") < i {
		t.Fatalf("args %q", args)
	}
	launched(t, f, f.Adapter(conn))
	if slices.Contains(f.Launches()[1].Args, "--settings") {
		t.Fatal("settings without hooks")
	}
}

// A hook makes Events push the session as its list and transcript show it:
// the finished turn with its reply, then the permission prompt.
func TestEventsPushWhatHooksAnnounce(t *testing.T) {
	f := claudetest.New(t)
	a := f.HookedAdapter(conn, f.Hooks())
	id := launched(t, f, a)
	ch, _, _ := pushes(t, a)
	f.Reply(id, "All done.", true)
	f.SetState(id, "done", "idle", "")
	// Events subscribes asynchronously: retry the first hook until it lands.
	var o execution.Observation
	deadline := time.Now().Add(5 * time.Second)
	for {
		if code := f.Hook(id, "Stop", map[string]any{"last_assistant_message": "ignored"}); code != http.StatusNoContent {
			t.Fatal(code)
		}
		select {
		case o = <-ch:
		case <-time.After(100 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("no push after Stop")
			}
			continue
		}
		break
	}
	if o.State != "done" || o.Reply == nil || o.Reply.Text != "All done." || o.Session != (execution.SessionRef{ConnectionID: conn, ExternalSessionID: id}) {
		t.Fatalf("after Stop %+v", o)
	}
	for len(ch) > 0 { // repeated Stops while subscribing
		<-ch
	}
	// The prompt's hook stays open for an answer in Stage; it nudges first.
	call := f.Ask(id, "Bash", map[string]any{"command": "ls"}, nil)
	if o := next(t, ch); o.State != "needs_input" {
		t.Fatalf("after PermissionRequest %+v", o)
	}
	if call.Answered() {
		t.Fatal("PermissionRequest answered without Stage")
	}
	f.SetState(id, "working", "busy", "")
	f.Hook(id, "UserPromptSubmit", nil)
	if o := next(t, ch); o.State != "running" {
		t.Fatalf("after UserPromptSubmit %+v", o)
	}
}

// subscribed launches a hooked session, runs Events and returns once a
// first hook was pushed (Events subscribes asynchronously).
func subscribed(t *testing.T, f *claudetest.Fake, a *claudecode.Adapter) (string, <-chan execution.Observation, <-chan error, context.CancelFunc) {
	t.Helper()
	id := launched(t, f, a)
	ch, errc, cancel := pushes(t, a)
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.Hook(id, "Notification", map[string]any{"notification_type": "auth_success"})
		select {
		case <-ch:
			return id, ch, errc, cancel
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("Events never subscribed")
			}
		}
	}
}

// Claude may post Stop a moment before its list shows the turn over:
// Events reads again until it does. A list that never changes still
// pushes what was read last (as a poll would).
func TestEventsSettleOnTheList(t *testing.T) {
	f := claudetest.New(t)
	a := f.HookedAdapter(conn, f.Hooks())
	id, ch, _, _ := subscribed(t, f, a)
	f.Hook(id, "Stop", nil)
	time.Sleep(10 * time.Millisecond)
	f.SetState(id, "done", "idle", "")
	if o := next(t, ch); o.State != "done" {
		t.Fatalf("settled %+v", o)
	}
	f.SetState(id, "working", "busy", "")
	f.Hook(id, "Stop", nil)
	if o := next(t, ch); o.State != "running" {
		t.Fatalf("never settled %+v", o)
	}
	none(t, ch, 200*time.Millisecond)
}

// A hook naming a session Claude does not have pushes nothing; one naming
// a session another client started pushes it with its origin (the
// coordinator leaves an unowned session to discovery).
func TestEventsIgnoreUnknownSessions(t *testing.T) {
	f := claudetest.New(t)
	h := f.Hooks()
	a := f.HookedAdapter(conn, h)
	id, ch, _, _ := subscribed(t, f, a)
	// Post as Claude would, with the launched session's hook settings.
	if code := f.HookAs(id, "99999999-2222-4333-8444-555555555555", "Stop", nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	none(t, ch, 300*time.Millisecond)
	wrapped := "11111111-2222-4333-8444-555555555555"
	f.AddInteractive(wrapped, f.Workspace, "wrapper-entrypoint", "from a wrapper")
	if code := f.HookAs(id, wrapped, "Stop", nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if o := next(t, ch); o.Session.ExternalSessionID != wrapped || o.Origin != "wrapper-entrypoint" {
		t.Fatalf("%+v", o)
	}
	f.Hook(id, "SessionEnd", map[string]any{"reason": "other"})
	if o := next(t, ch); o.Session.ExternalSessionID != id {
		t.Fatalf("%+v", o)
	}
}

// Events ends when the setup replaces or removes the adapter (its
// subscription ends) and when its context ends; without hooks it returns
// at once.
func TestEventsLifetime(t *testing.T) {
	f := claudetest.New(t)
	if err := f.Adapter(conn).Events(t.Context(), func(context.Context, execution.Observation) error { return nil }); err != nil {
		t.Fatalf("no hooks: %v", err)
	}
	h := f.Hooks()
	a := f.HookedAdapter(conn, h)
	_, _, errc, _ := subscribed(t, f, a)
	h.Reset()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Events outlived its subscription")
	}
	b := f.HookedAdapter(conn, h)
	_, _, errc, cancel := subscribed(t, f, b)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Events outlived its context")
	}
}
