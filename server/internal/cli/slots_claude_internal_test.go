package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
)

// claudeStatus is a GET /api/harnesses/claude/status reply.
func claudeStatus(t *testing.T, raw string) client.HarnessStatus {
	t.Helper()
	var st client.HarnessStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func printClaude(t *testing.T, st client.HarnessStatus) string {
	t.Helper()
	return styledOut(nil, func(w *bytes.Buffer) {
		if err := statusPrinters["claude"](w, st, "stage"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestClaudeStatusPanel(t *testing.T) {
	for name, tc := range map[string]struct {
		reply      string
		want, deny []string
	}{
		"disconnected, ready to connect": {
			reply: `{"kind":"claude","state":"disconnected","fields":[{"id":"binary","value":"","source":"default"}],
				"details":{"binary":"/home/u/.local/bin/claude","version":"2.1.332","configDirectory":"/home/u/.claude","loggedIn":true,"scope":true}}`,
			want: []string{
				"! claude disconnected (Claude Code 2.1.332)\n",
				"  command     /home/u/.local/bin/claude (default lookup)\n",
				"  config      /home/u/.claude\n",
				"  login       logged in\n",
				"  scope       own systemd scope (Claude's daemon outlives a Stage restart)\n",
				"Next:\n  stage harness claude run connect\n",
			},
			deny: []string{"error"},
		},
		"connected, saved command, no scope": {
			reply: `{"kind":"claude","state":"connected","connected":true,"configured":true,"fields":[{"id":"binary","value":"/opt/claude","set":true,"source":"saved"}],
				"details":{"binary":"/opt/claude","version":"2.1.332 (Claude Code)","configDirectory":"/home/u/.claude","loggedIn":true,"scope":false}}`,
			want: []string{
				"✓ claude connected (Claude Code 2.1.332)\n",
				"  command     /opt/claude (saved setting)\n",
				"  scope       none (a Claude daemon Stage starts may stop when Stage restarts)\n",
			},
			deny: []string{"Next:", "error"},
		},
		"logged out": {
			reply: `{"kind":"claude","state":"logged_out","configured":true,"reason":"Claude Code isn't logged in. Run ` + "`claude auth login`" + ` in a terminal, then connect again.",
				"fields":[{"id":"binary","value":"","source":"default"}],
				"details":{"binary":"/usr/bin/claude","version":"2.1.332","configDirectory":"/home/u/.claude","loggedIn":false,"scope":true}}`,
			want: []string{
				"! claude logged_out (Claude Code 2.1.332)\n",
				"  login       logged out\n",
				"  error       Claude Code isn't logged in. Run claude auth login in a terminal, then connect again.\n",
				"Next:\n  claude auth login                  in a terminal\n  stage harness claude run connect\n",
			},
		},
		"not found: no checks ran": {
			reply: `{"kind":"claude","state":"not_found","reason":"Stage can't find the claude command.","fields":[{"id":"binary","value":"","source":"default"}],
				"details":{"loggedIn":false,"scope":false}}`,
			want: []string{
				"! claude not_found\n",
				"  command     claude (default lookup)\n",
				"  error       Stage can't find the claude command.\n",
				"Next:\n  stage harness claude set binary=PATH   or install Claude Code\n",
			},
			deny: []string{"login", "config"},
		},
		"error": {
			reply: `{"kind":"claude","state":"error","configured":true,"reason":"claude --version didn't answer within 20s.","fields":[],"details":{"binary":"/usr/bin/claude","loggedIn":false,"scope":false}}`,
			want: []string{
				"✗ claude error\n",
				"Next:\n  stage harness claude run connect   after checking the command\n",
			},
		},
	} {
		got := printClaude(t, claudeStatus(t, tc.reply))
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", name, w, got)
			}
		}
		for _, d := range tc.deny {
			if strings.Contains(got, d) {
				t.Errorf("%s: unexpected %q in\n%s", name, d, got)
			}
		}
	}
}

// Unparsable details fall back to the generic print.
func TestClaudeStatusPanelFallsBack(t *testing.T) {
	st := claudeStatus(t, `{"kind":"claude","state":"connected","fields":[],"details":[1]}`)
	if err := statusPrinters["claude"](&bytes.Buffer{}, st, "stage"); err == nil {
		t.Fatal("want an error for unparsable details")
	}
}
