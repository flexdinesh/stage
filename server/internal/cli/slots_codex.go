package cli

import (
	"encoding/json"
	"io"

	"github.com/flexdinesh/stage/server/internal/client"
)

// The Codex status panel of `stage harness codex`.
func init() {
	statusPrinters["codex"] = func(w io.Writer, st client.HarnessStatus, cli string) error {
		var s client.CodexStatus
		if err := json.Unmarshal(st.Details, &s); err != nil {
			return err
		}
		printCodexStatus(w, st.State, st.Reason, s, cli)
		return nil
	}
}

func printCodexStatus(w io.Writer, state, reason string, s client.CodexStatus, cli string) {
	t := stateTone(state)
	line(w, t, "codex %s", t.paint(codexState(state)))
	binary := s.Binary
	if binary == "" {
		binary = "codex " + dim("(on PATH)")
	}
	kv(w, "binary", "%s", binary)
	if s.CLIVersion != "" {
		kv(w, "version", "%s %s", s.CLIVersion, dim("(app-server "+s.AppServerVersion+")"))
	}
	kv(w, "daemon", "%s", s.Daemon)
	if s.Account != nil && s.Account.Email != "" {
		kv(w, "account", "%s %s", s.Account.Email, dim("("+s.Account.Type+")"))
	}
	if s.CodexHome != "" {
		kv(w, "home", "%s", s.CodexHome)
	}
	if reason != "" && state != "connected" {
		kv(w, "error", "%s", toneBad.paint(reason))
	}
	switch {
	case s.Fix != "":
		nextSteps(w, step{s.Fix, ""}, step{cli + " harness codex run connect", "then connect"})
	case state == "not_installed":
		nextSteps(w, step{cli + " harness codex set binary=PATH", "point Stage at the Codex CLI"})
	case state != "connected":
		nextSteps(w, step{cli + " harness codex run connect", ""})
	}
}

func codexState(state string) string {
	switch state {
	case "not_connected":
		return "not connected"
	case "signed_out":
		return "signed out"
	case "not_installed":
		return "not installed"
	}
	return state
}
