package cli

import (
	"bytes"
	"strings"
	"testing"
)

func styledOut(env map[string]string, print func(w *bytes.Buffer)) string {
	var raw bytes.Buffer
	print(&raw)
	var out bytes.Buffer
	_, _ = styled(&out, func(k string) string { return env[k] }).Write(raw.Bytes())
	return out.String()
}

func TestStyledColor(t *testing.T) {
	print := func(w *bytes.Buffer) { success(w, "Joined %s", "x") }
	for name, tc := range map[string]struct {
		env  map[string]string
		ansi bool
	}{
		"pipe":                 {map[string]string{"TERM": "xterm-256color"}, false},
		"forced":               {map[string]string{"TERM": "xterm-256color", "CLICOLOR_FORCE": "1"}, true},
		"forced, NO_COLOR any": {map[string]string{"TERM": "xterm-256color", "CLICOLOR_FORCE": "1", "NO_COLOR": "yes"}, false},
		"dumb":                 {map[string]string{"TERM": "dumb"}, false},
	} {
		got := styledOut(tc.env, print)
		if strings.Contains(got, "\x1b[") != tc.ansi {
			t.Errorf("%s: ansi=%v, got %q", name, tc.ansi, got)
		}
		if !strings.Contains(got, "Joined x\n") || !strings.HasPrefix(stripped(got), "✓ Joined x\n") {
			t.Errorf("%s: text %q", name, got)
		}
	}
}

// stripped is styled output with color off: the layout every pipe reads.
func stripped(s string) string { return styledOut(nil, func(w *bytes.Buffer) { w.WriteString(s) }) }

func TestOutcomeLines(t *testing.T) {
	got := styledOut(nil, func(w *bytes.Buffer) {
		success(w, "Done")
		warn(w, "Careful")
		fail(w, "broke")
		fixLine(w, inlineCmds("run `stage-dev fix` now"))
		unchanged(w, "/p")
	})
	want := "✓ Done\n! Careful\n✗ error: broke\n  fix: run stage-dev fix now\n✓ Unchanged /p\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestKVAndNextSteps(t *testing.T) {
	got := styledOut(nil, func(w *bytes.Buffer) {
		kv(w, "config", "%s", "/c")
		kv(w, "login start", "on")
		kv(w, "a-very-long-label", "v")
		nextSteps(w, step{"stage-dev worker service start", "background"}, step{"stage-dev worker run", "foreground"}, step{"bare", ""})
		nextStepsOn(w, "on the machine to add", step{"stage worker join L", ""})
	})
	want := "  config      /c\n  login start on\n  a-very-long-label v\n" +
		"\nNext:\n  stage-dev worker service start   background\n  stage-dev worker run             foreground\n  bare\n" +
		"\nNext: on the machine to add\n  stage worker join L\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestProse(t *testing.T) {
	for in, want := range map[string]string{
		"the definition changed; `stage service restart` applies it": "The definition changed; stage service restart applies it",
		"stage-dev service started":                                  "stage-dev service started",
		"/path is held":                                              "/path is held",
		"unbalanced ` tick":                                          "Unbalanced ` tick",
		"":                                                           "",
	} {
		if got := stripped(prose(in)); got != want {
			t.Errorf("prose(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStateTone(t *testing.T) {
	for s, want := range map[string]tone{
		"online": toneOK, "connected": toneOK, "connecting": toneWarn, "unpaired": toneWarn,
		"needs_update": toneWarn, "offline": toneBad, "revoked": toneBad, "unreachable": toneBad, "rejected": toneBad,
	} {
		if got := stateTone(s); got != want {
			t.Errorf("stateTone(%q) = %d, want %d", s, got, want)
		}
	}
}
