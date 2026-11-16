package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Diagnostics is a plain-text report for support: app, CLI and server
// versions, identity, the active server, the connection state, and for
// this machine's server `service status --json` without secrets and the
// last 50 server log lines.
func (l *Launcher) Diagnostics(ctx context.Context) string {
	st := l.State()
	var b strings.Builder
	fmt.Fprintf(&b, "Stage desktop diagnostics\n\n")
	fmt.Fprintf(&b, "app        %s %s\n", st.App.Name, st.App.Version)
	fmt.Fprintf(&b, "cli        %s\n", l.cfg.CLI)
	if l.devMode() {
		fmt.Fprintf(&b, "dev        socket %s (STAGE_DESKTOP_SOCKET)\n", l.cfg.DevSocket)
	}
	if t := st.Target; t.Remote() {
		fmt.Fprintf(&b, "target     %s (%s), remote %s\n", t.Name, t.ID, t.URL)
	} else {
		fmt.Fprintf(&b, "target     this machine\n")
	}
	l.mu.Lock()
	info := l.info
	l.mu.Unlock()
	if info != nil {
		fmt.Fprintf(&b, "bundled    stage %s (identity %s, commit %s, api v%d)\n", info.Version, info.Identity, orNone(info.Commit), info.APIVersion)
	}
	if s := st.Server; s != nil {
		pub := "none"
		if s.PublicURL != nil {
			pub = *s.PublicURL
		}
		pid := "unknown"
		if s.PID != nil {
			pid = fmt.Sprint(*s.PID)
		}
		fmt.Fprintf(&b, "server     stage %s (identity %s, api v%d), managed %v, pid %s, public URL %s\n", s.Version, s.Identity, s.APIVersion, s.Managed, pid, pub)
		if s.Socket != "" {
			fmt.Fprintf(&b, "socket     %s\n", s.Socket)
		}
	}
	fmt.Fprintf(&b, "state      %s (epoch %d, server epoch %d, foreground %v, version skew %v)\n", st.Phase, st.Epoch, st.ServerEpoch, st.Foreground, st.VersionSkew)
	for _, line := range []struct{ k, v string }{{"title", st.Title}, {"message", st.Message}, {"fix", st.Fix}, {"command", st.Command}} {
		if line.v != "" {
			fmt.Fprintf(&b, "%-10s %s\n", line.k, line.v)
		}
	}
	for _, p := range st.Problems {
		fmt.Fprintf(&b, "problem    %s: %s (%s)\n", p.Code, p.Message, p.Fix)
	}
	if l.devMode() || st.Target.Remote() {
		return b.String()
	}
	b.WriteString("\n--- stage service status --json ---\n")
	res := l.run(ctx, l.t.Status, "service", "status", "--json")
	if res.Err != nil {
		fmt.Fprintf(&b, "(failed: %v)\n", res.Err)
	} else {
		b.WriteString(redact(res.Stdout))
		b.WriteString("\n")
	}
	b.WriteString("\n--- stage service logs -n 50 ---\n")
	res = l.run(ctx, l.t.Command, "service", "logs", "-n", "50")
	switch {
	case res.Err != nil:
		fmt.Fprintf(&b, "(failed: %v)\n", res.Err)
	case res.Code != 0:
		fmt.Fprintf(&b, "(exit %d) %s\n", res.Code, strings.TrimSpace(string(res.Stderr)))
	default:
		for _, line := range lines(res.Stdout, 50) {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// secretKey matches JSON keys (and {key, value} entries) that may hold a
// credential.
var secretKey = regexp.MustCompile(`(?i)token|secret|password|passwd|credential|auth`)

// redact pretty-prints a JSON document with every value under a
// secret-looking key replaced; non-JSON output is dropped (it may echo
// anything).
func redact(doc []byte) string {
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(doc), &v); err != nil {
		return "(not JSON)"
	}
	out, err := json.MarshalIndent(redactValue(v), "", "  ")
	if err != nil {
		return "(unprintable)"
	}
	return string(out)
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if k, ok := t["key"].(string); ok && secretKey.MatchString(k) {
			if _, has := t["value"]; has {
				t["value"] = "[redacted]"
			}
		}
		for k, x := range t {
			if secretKey.MatchString(k) {
				t[k] = "[redacted]"
				continue
			}
			t[k] = redactValue(x)
		}
	case []any:
		for i, x := range t {
			t[i] = redactValue(x)
		}
	}
	return v
}

// LoginStartup reports whether the app opens at login.
func (l *Launcher) LoginStartup() bool {
	if l.cfg.OpenAtLogin == nil {
		return false
	}
	return l.cfg.OpenAtLogin()
}

// SetLoginStartup turns "Open Stage at login" on or off: the app's login
// item and the service's login startup (`service enable|disable`). A
// development run changes the login item only. ErrRemote while a remote
// is active (it concerns this machine's service).
func (l *Launcher) SetLoginStartup(ctx context.Context, on bool) error {
	if l.remote() {
		return ErrRemote
	}
	var errs []error
	if l.cfg.SetOpenAtLogin != nil {
		if err := l.cfg.SetOpenAtLogin(on); err != nil {
			errs = append(errs, fmt.Errorf("login item: %w", err))
		}
	}
	if !l.devMode() {
		args := []string{"service", "disable", "--json"}
		if on {
			args = []string{"service", "enable", "--exe", l.cfg.CLI, "--json"}
		}
		res := l.run(ctx, l.t.Command, args...)
		var rep report
		switch {
		case res.Err != nil:
			errs = append(errs, fmt.Errorf("stage service %s: %w", args[1], res.Err))
		case res.Code != 0:
			msg := fmt.Sprintf("exit %d", res.Code)
			if parseJSON(res.Stdout, &rep) == nil && rep.Message != "" {
				msg = rep.Message
			}
			errs = append(errs, fmt.Errorf("stage service %s: %s", args[1], msg))
		}
	}
	return errors.Join(errs...)
}
