package claudecode_test

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
)

// Outputs recorded from `claude mcp get stage` (2.1.332, throwaway home).
func TestParseMCPServer(t *testing.T) {
	for _, c := range []struct {
		out           string
		status, issue string
	}{
		{"stage:\n  Scope: User config (available in all your projects)\n  Status: ✔ Connected\n  Type: stdio\n  Command: /home/u/.local/bin/stage\n  Args: mcp\n  Environment:\n\nTo remove this server, run: claude mcp remove stage -s user\n", "Connected", ""},
		{"stage:\n  Scope: User config (available in all your projects)\n  Status: ✘ Failed to connect\n  Issue: ECONNREFUSED: Unable to connect. Is the computer able to access the url?\n  Type: http\n  URL: http://127.0.0.1:1/mcp\n", "Failed to connect", "ECONNREFUSED: Unable to connect. Is the computer able to access the url?"},
		{"stage:\n  Status: Disabled\n", "Disabled", ""},
	} {
		m, err := claudecode.ParseMCPServer(c.out)
		if err != nil || !m.Found || m.Status != c.status || m.Issue != c.issue {
			t.Errorf("ParseMCPServer(%q) = %+v, %v", c.out, m, err)
		}
	}
	if _, err := claudecode.ParseMCPServer("stage:\n  Type: stdio\n"); err == nil {
		t.Error("output without a status parsed")
	}
}
