package claudecode_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
)

func openHooks(t *testing.T, dir string) *claudecode.Hooks {
	t.Helper()
	h, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// settingsOf is the settings file's hooks: event -> url, Authorization.
func settingsOf(t *testing.T, path string) map[string][2]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Type    string            `json:"type"`
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
				Timeout int               `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	out := map[string][2]string{}
	for ev, ms := range f.Hooks {
		if len(ms) != 1 || ms[0].Matcher != nil || len(ms[0].Hooks) != 1 {
			t.Fatalf("%s: want one hook and no matcher: %s", ev, b)
		}
		hk := ms[0].Hooks[0]
		timeout := 5
		if ev == "PermissionRequest" {
			timeout = 3600 // held for an answer in Stage
		}
		if hk.Type != "http" || hk.Timeout != timeout {
			t.Fatalf("%s: %+v", ev, hk)
		}
		out[ev] = [2]string{hk.URL, hk.Headers["Authorization"]}
	}
	return out
}

func post(t *testing.T, url, auth, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// The settings file points every subscribed event at the listener with
// the bearer token, is private, and Claude's real hook inputs are
// accepted as nudges naming their session.
func TestHooksSettingsAndRealInputs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	h := openHooks(t, dir)
	if h.SettingsPath() != filepath.Join(dir, claudecode.HooksFile) || !strings.HasPrefix(h.URL(), "http://127.0.0.1:") {
		t.Fatalf("path %s url %s", h.SettingsPath(), h.URL())
	}
	for p, mode := range map[string]os.FileMode{dir: 0o700, h.SettingsPath(): 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", p, fi.Mode(), err)
		}
	}
	hooks := settingsOf(t, h.SettingsPath())
	if len(hooks) != len(claudecode.HookEvents) {
		t.Fatalf("events %v", hooks)
	}
	auth := ""
	for _, ev := range claudecode.HookEvents {
		got, ok := hooks[ev]
		if !ok || got[0] != h.URL() || !strings.HasPrefix(got[1], "Bearer ") || len(got[1]) < len("Bearer ")+64 {
			t.Fatalf("%s: %v", ev, got)
		}
		if auth != "" && got[1] != auth {
			t.Fatal("one token for every event")
		}
		auth = got[1]
	}
	if st := h.Status(); st.Mode != claudecode.ModePush || st.LastEventAt != nil {
		t.Fatalf("status %+v", st)
	}
	sub := h.Subscribe()
	fixtures := []struct{ file, event, session, notification string }{
		{"stop.json", "Stop", "aa583336-6a62-4182-889a-55d74d490cfb", ""},
		{"user-prompt-submit.json", "UserPromptSubmit", "aa583336-6a62-4182-889a-55d74d490cfb", ""},
		{"permission-request.json", "PermissionRequest", "e9455d86-e696-4452-8002-0a373629eec8", ""},
		{"notification-permission-prompt.json", "Notification", "e9455d86-e696-4452-8002-0a373629eec8", "permission_prompt"},
		{"session-end.json", "SessionEnd", "e9455d86-e696-4452-8002-0a373629eec8", ""},
	}
	for _, fx := range fixtures {
		b, err := os.ReadFile(filepath.Join("testdata", "hooks", fx.file))
		if err != nil {
			t.Fatal(err)
		}
		var held *claudetest.HookCall
		if fx.event == "PermissionRequest" {
			// It names a tool: it stays open for an answer (it nudges first).
			held = claudetest.Post(h.URL(), map[string]string{"Authorization": auth}, b)
		} else if code := post(t, h.URL(), auth, string(b)); code != http.StatusNoContent {
			t.Fatalf("%s: %d", fx.file, code)
		}
		select {
		case n := <-sub.Nudges():
			if n.Event != fx.event || n.Session != fx.session || n.NotificationType != fx.notification || n.At.IsZero() {
				t.Fatalf("%s: %+v", fx.file, n)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: no nudge", fx.file)
		}
		if held != nil && held.Answered() {
			t.Fatalf("%s: answered without Stage", fx.file)
		}
	}
	if st := h.Status(); st.LastEventAt == nil {
		t.Fatalf("status after hooks %+v", st)
	}
}

// Only POST /hooks with the token and a session is accepted; nothing else
// reaches the adapter.
func TestHooksRefuseBadRequests(t *testing.T) {
	h := openHooks(t, t.TempDir())
	auth := settingsOf(t, h.SettingsPath())["Stop"][1]
	sub := h.Subscribe()
	ok := `{"hook_event_name":"Stop","session_id":"aa583336-6a62-4182-889a-55d74d490cfb"}`
	cases := []struct {
		name, url, auth, body string
		want                  int
	}{
		{"no token", h.URL(), "", ok, http.StatusUnauthorized},
		{"wrong token", h.URL(), "Bearer " + strings.Repeat("0", 64), ok, http.StatusUnauthorized},
		{"other path", strings.TrimSuffix(h.URL(), "/hooks") + "/api/tasks", auth, ok, http.StatusNotFound},
		{"malformed", h.URL(), auth, `{"hook_event_name":`, http.StatusBadRequest},
		{"no session", h.URL(), auth, `{"hook_event_name":"Stop"}`, http.StatusBadRequest},
		{"not a uuid", h.URL(), auth, `{"hook_event_name":"Stop","session_id":"../../etc"}`, http.StatusBadRequest},
		{"too large", h.URL(), auth, `{"hook_event_name":"Stop","pad":"` + strings.Repeat("x", 1<<20) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if got := post(t, c.url, c.auth, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	res, err := http.Get(h.URL())
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", res.StatusCode)
	}
	select {
	case n := <-sub.Nudges():
		t.Fatalf("refused request nudged %+v", n)
	default:
	}
	if h.Status().LastEventAt != nil {
		t.Fatal("a refused request counts as a hook")
	}
}

// A restart keeps the token and the port the settings file names, so
// sessions launched before it keep reaching Stage; a taken port moves the
// listener and the file follows.
func TestHooksReopenKeepsPortAndToken(t *testing.T) {
	dir := t.TempDir()
	h, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(h.SettingsPath())
	url := h.URL()
	_ = h.Close()
	again := openHooks(t, dir)
	if again.URL() != url {
		t.Fatalf("port moved: %s -> %s", url, again.URL())
	}
	if b, _ := os.ReadFile(again.SettingsPath()); !bytes.Equal(b, first) {
		t.Fatal("settings rewritten without a change")
	}
	// The port is taken (the open receiver holds it): a new port, the
	// same token.
	moved := openHooks(t, dir)
	if moved.URL() == url {
		t.Fatal("bound a taken port")
	}
	hooks := settingsOf(t, moved.SettingsPath())
	if hooks["Stop"][0] != moved.URL() {
		t.Fatalf("settings %v", hooks)
	}
	if !bytes.Contains(first, []byte(strings.TrimPrefix(hooks["Stop"][1], "Bearer "))) {
		t.Fatal("token changed")
	}
}

// A settings file that is not Stage's (or an unreadable one) gets a fresh
// token and port; an unusable folder is an error the setup can show.
func TestHooksStartFreshAndFailClearly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, claudecode.HooksFile), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"http","url":"http://example.com:80/hooks","headers":{"Authorization":"Bearer x"}}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := openHooks(t, dir)
	if strings.Contains(settingsOf(t, h.SettingsPath())["Stop"][1], "Bearer x") || !strings.HasPrefix(h.URL(), "http://127.0.0.1:") {
		t.Fatal("kept a foreign hook")
	}
	if _, err := claudecode.OpenHooks(claudecode.HooksOptions{}); err == nil || !strings.HasPrefix(err.Error(), "Stage has no folder") {
		t.Fatalf("no dir: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: filepath.Join(file, "claude")}); err == nil || !strings.HasPrefix(err.Error(), "Stage couldn't write its Claude Code hook settings") {
		t.Fatalf("unusable dir: %v", err)
	}
}

// One adapter receives nudges at a time: a new subscription or a reset
// ends the previous one, and hooks without a subscriber are still
// answered (no error in the session) and dropped.
func TestHooksSubscriptions(t *testing.T) {
	h := openHooks(t, t.TempDir())
	auth := settingsOf(t, h.SettingsPath())["Stop"][1]
	body := `{"hook_event_name":"Stop","session_id":"aa583336-6a62-4182-889a-55d74d490cfb"}`
	if code := post(t, h.URL(), auth, body); code != http.StatusNoContent {
		t.Fatalf("no subscriber: %d", code)
	}
	first := h.Subscribe()
	second := h.Subscribe()
	select {
	case <-first.Done():
	default:
		t.Fatal("superseded subscription still open")
	}
	h.Unsubscribe(first) // a late unsubscribe leaves the current one
	select {
	case <-second.Done():
		t.Fatal("current subscription ended by a stale one")
	default:
	}
	if code := post(t, h.URL(), auth, body); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if n := <-second.Nudges(); n.Event != "Stop" {
		t.Fatal(n)
	}
	h.Reset()
	select {
	case <-second.Done():
	default:
		t.Fatal("reset left the subscription open")
	}
	// Closed: Claude gets a connection error (a non-blocking hook error).
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", strings.TrimSuffix(strings.TrimPrefix(h.URL(), "http://"), "/hooks"), time.Second); err == nil {
		t.Fatal("listener open after Close")
	}
}

// A PermissionRequest naming a tool stays open past the server's read and
// write bounds; one naming none is answered at once. Close releases the
// open one with no decision (Claude keeps its prompt).
func TestHooksHoldPermissionRequests(t *testing.T) {
	h, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: t.TempDir(), IOTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	auth := map[string]string{"Authorization": settingsOf(t, h.SettingsPath())["PermissionRequest"][1]}
	session := `"session_id":"e9455d86-e696-4452-8002-0a373629eec8","hook_event_name":"PermissionRequest"`
	held := claudetest.Post(h.URL(), auth, []byte(`{`+session+`,"tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if c := claudetest.Post(h.URL(), auth, []byte(`{`+session+`}`)).Wait(t, 2*time.Second); c.Err != nil || c.Status != http.StatusNoContent {
		t.Fatalf("no tool: %d %v", c.Status, c.Err)
	}
	time.Sleep(400 * time.Millisecond)
	if held.Answered() {
		t.Fatalf("held hook ended: %d %v", held.Status, held.Err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if c := held.Wait(t, 2*time.Second); c.Err != nil || c.Status != http.StatusNoContent || c.Body != "" {
		t.Fatalf("released hook %d %q %v", c.Status, c.Body, c.Err)
	}
}
