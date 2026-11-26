package cli

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/flexdinesh/stage/server/internal/client"
)

type claudeDetails struct {
	// Binary is the resolved claude executable ("" when none was found).
	Binary string `json:"binary,omitempty"`
	// Version is claude --version ("" until the checks ran).
	Version         string `json:"version,omitempty"`
	ConfigDirectory string `json:"configDirectory,omitempty"`
	LoggedIn        bool   `json:"loggedIn"`
	// Scope: background launches run in their own systemd user scope.
	Scope bool `json:"scope"`
}

// The Claude Code status panel of `stage harness claude`.
func init() {
	statusPrinters["claude"] = func(w io.Writer, st client.HarnessStatus, cli string) error {
		var d claudeDetails
		if err := json.Unmarshal(st.Details, &d); err != nil {
			return err
		}
		printClaudeStatus(w, st, d, cli)
		return nil
	}
}

func printClaudeStatus(w io.Writer, st client.HarnessStatus, d claudeDetails, cli string) {
	t := stateTone(st.State)
	if st.State == "disconnected" {
		// Not set up, or disconnected: ready to connect, not a fault.
		t = toneWarn
	}
	version := ""
	if d.Version != "" {
		version = " " + dim("(Claude Code "+strings.TrimSuffix(d.Version, " (Claude Code)")+")")
	}
	line(w, t, "claude %s%s", t.paint(st.State), version)
	saved := ""
	for _, f := range st.Fields {
		if f.ID == "binary" && f.Source == "saved" {
			saved = f.Value
		}
	}
	command, source := d.Binary, "default lookup"
	if saved != "" {
		source = "saved setting"
	}
	if command == "" {
		command = saved
	}
	if command == "" {
		command = "claude"
	}
	kv(w, "command", "%s %s", command, dim("("+source+")"))
	if d.ConfigDirectory != "" {
		kv(w, "config", "%s", d.ConfigDirectory)
	}
	if d.Version != "" {
		if d.LoggedIn {
			kv(w, "login", "logged in")
		} else {
			kv(w, "login", "%s", toneWarn.paint("logged out"))
		}
	}
	if d.Scope {
		kv(w, "scope", "own systemd scope %s", dim("(Claude's daemon outlives a Stage restart)"))
	} else {
		kv(w, "scope", "%s", dim("none (a Claude daemon Stage starts may stop when Stage restarts)"))
	}
	if st.Reason != "" && !st.Connected {
		kv(w, "error", "%s", toneBad.paint(inlineCmds(st.Reason)))
	}
	connect := step{cli + " harness claude run connect", ""}
	switch st.State {
	case "not_found":
		nextSteps(w, step{cli + " harness claude set binary=PATH", "or install Claude Code"})
	case "logged_out":
		nextSteps(w, step{"claude auth login", "in a terminal"}, connect)
	case "disconnected":
		nextSteps(w, connect)
	case "error":
		nextSteps(w, step{cli + " harness claude run connect", "after checking the command"})
	}
}
