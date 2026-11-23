package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Human output style (gh-style): a colored state glyph leads an outcome or a
// status heading, labels are dim, commands bold. Hue marks state only.
// Printers always emit ANSI; styled strips it for pipes, NO_COLOR and
// TERM=dumb, so every reader gets the same layout.

var (
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Green)
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	badStyle  = lipgloss.NewStyle().Foreground(lipgloss.Red)
	dimStyle  = lipgloss.NewStyle().Faint(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
)

// colorEnv is what colorprofile reads to pick a profile.
var colorEnv = []string{"TERM", "COLORTERM", "CLICOLOR", "CLICOLOR_FORCE", "TTY_FORCE", "TMUX", "NO_COLOR"}

// styled wraps w for human output: ANSI passes on a color terminal (or with
// CLICOLOR_FORCE) and is stripped when w is not a terminal or TERM is dumb.
// NO_COLOR (any value, per no-color.org) drops color even over
// CLICOLOR_FORCE; a terminal keeps bold and faint.
func styled(w io.Writer, getenv func(string) string) io.Writer {
	if _, ok := w.(*colorprofile.Writer); ok {
		return w
	}
	noColor := getenv("NO_COLOR") != ""
	var env []string
	for _, k := range colorEnv {
		v := getenv(k)
		switch {
		case v == "", noColor && k == "CLICOLOR_FORCE":
			continue
		case k == "NO_COLOR":
			v = "1"
		}
		env = append(env, k+"="+v)
	}
	return &colorprofile.Writer{Forward: w, Profile: colorprofile.Detect(w, env)}
}

// stdout and stderr are the env's streams wrapped for styled human output.
func (e Env) stdout() io.Writer { return styled(e.Stdout, e.Getenv) }
func (e Env) stderr() io.Writer { return styled(e.Stderr, e.Getenv) }

// tone is a state's color: ok, warn or bad.
type tone int

const (
	toneOK tone = iota
	toneWarn
	toneBad
)

func (t tone) style() lipgloss.Style {
	switch t {
	case toneWarn:
		return warnStyle
	case toneBad:
		return badStyle
	}
	return okStyle
}

// glyph is the tone's colored mark: ✓, ! or ✗.
func (t tone) glyph() string {
	switch t {
	case toneWarn:
		return t.style().Render("!")
	case toneBad:
		return t.style().Render("✗")
	}
	return t.style().Render("✓")
}

// paint colors s in the tone.
func (t tone) paint(s string) string { return t.style().Render(s) }

// line prints "<glyph> msg".
func line(w io.Writer, t tone, format string, a ...any) {
	fmt.Fprintf(w, "%s %s\n", t.glyph(), fmt.Sprintf(format, a...))
}

// success prints "✓ msg".
func success(w io.Writer, format string, a ...any) { line(w, toneOK, format, a...) }

// warn prints "! msg" (a warning or a note that needs attention).
func warn(w io.Writer, format string, a ...any) { line(w, toneWarn, format, a...) }

// fail prints "✗ error: msg".
func fail(w io.Writer, format string, a ...any) {
	line(w, toneBad, "error: %s", fmt.Sprintf(format, a...))
}

// fixLine prints the recovery under a failure or problem: "  fix: cmd".
func fixLine(w io.Writer, s string) {
	fmt.Fprintf(w, "  %s %s\n", dim("fix:"), s)
}

// kvWidth is the label column of a status block.
const kvWidth = 11

// kv prints an indented status row: a dim label padded to kvWidth, then value.
func kv(w io.Writer, label, format string, a ...any) { kvPad(w, kvWidth, label, format, a...) }

// kvPad is kv with a label column of width.
func kvPad(w io.Writer, width int, label, format string, a ...any) {
	pad := max(width-len(label), 0)
	fmt.Fprintf(w, "  %s%s %s\n", dim(label), strings.Repeat(" ", pad), fmt.Sprintf(format, a...))
}

// step is one next-step command and why to run it.
type step struct{ cmd, why string }

// nextSteps prints a blank line, "Next:" and the steps with aligned commands.
func nextSteps(w io.Writer, steps ...step) { nextStepsOn(w, "", steps...) }

// nextStepsOn is nextSteps with where to run them: "Next: on the machine to add".
func nextStepsOn(w io.Writer, where string, steps ...step) {
	width := 0
	for _, s := range steps {
		width = max(width, len(s.cmd))
	}
	head := boldStyle.Render("Next:")
	if where != "" {
		head += " " + where
	}
	fmt.Fprintf(w, "\n%s\n", head)
	for _, s := range steps {
		if s.why == "" {
			fmt.Fprintf(w, "  %s\n", cmdText(s.cmd))
			continue
		}
		fmt.Fprintf(w, "  %s%s   %s\n", cmdText(s.cmd), strings.Repeat(" ", width-len(s.cmd)), dim(s.why))
	}
}

// cmdText is a command to run, in bold.
func cmdText(s string) string { return boldStyle.Render(s) }

// dim is secondary text.
func dim(s string) string { return dimStyle.Render(s) }

// stateTone is a state word's tone: live states are ok, transitional or
// unset ones warn, broken ones bad.
func stateTone(state string) tone {
	switch state {
	case "online", "connected", "ready", "running", "active", "paired", "ok":
		return toneOK
	case "offline", "revoked", "failed", "error", "unreachable", "unavailable", "rejected", "disconnected":
		return toneBad
	}
	return toneWarn
}

// inlineCmds bolds `backticked` commands and drops the backticks; an
// unbalanced backtick leaves s as is.
func inlineCmds(s string) string {
	parts := strings.Split(s, "`")
	if len(parts)%2 == 0 {
		return s
	}
	for i := 1; i < len(parts); i += 2 {
		parts[i] = cmdText(parts[i])
	}
	return strings.Join(parts, "")
}

// prose is a message from another package in sentence case, with inline
// commands bold. A message that opens with the product name or a command
// keeps its case (stage is lowercase).
func prose(s string) string {
	if !strings.HasPrefix(s, "stage") {
		if r, n := utf8.DecodeRuneInString(s); unicode.IsLower(r) {
			s = string(unicode.ToUpper(r)) + s[n:]
		}
	}
	return inlineCmds(s)
}

// unchanged prints "✓ Unchanged path", all dim: nothing to do.
func unchanged(w io.Writer, path string) {
	fmt.Fprintf(w, "%s\n", dim("✓ Unchanged "+path))
}
