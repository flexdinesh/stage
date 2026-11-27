package claudecodesetup

import (
	"context"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// probeExtras starts the display probes beside the CLI checks for a set-up
// connection whose command is found (nil otherwise), reusing a result
// younger than harness.FactsTTL. The channel gets one result; a failed or
// slow probe is a fact, never a status error (s.mu held; the probe itself
// takes no Service lock).
func (s *Service) probeExtras(ctx context.Context) <-chan harness.Probed {
	if !s.summary().Configured {
		return nil
	}
	bin := s.find()
	if bin == "" {
		return nil
	}
	cli, home, timeout, setup := s.cli(bin), s.opts.Home, s.opts.MCPTimeout, s.ports.CLIName()+" setup mcp --claude"
	out := make(chan harness.Probed, 1)
	go func() {
		out <- s.probed.Get(ctx, func(ctx context.Context) harness.Probed {
			fact, fix := stageMCP(ctx, cli, home, timeout, setup)
			return harness.Probed{Facts: []execution.Fact{fact}, Fix: fix}
		})
	}()
	return out
}

// stageMCP asks Claude Code for the Stage MCP server, run from home so a
// project's .mcp.json does not apply.
func stageMCP(ctx context.Context, cli *claudecode.CLI, home string, timeout time.Duration, setup string) (execution.Fact, *harness.Fix) {
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	m, err := claudecode.ReadMCPServer(call, cli, home, harness.StageMCPName)
	if err != nil {
		return harness.StageMCPProbeError(call, timeout, err), nil
	}
	if !m.Found {
		return harness.StageMCPNotConfigured(), harness.StageMCPFix("Claude Code", setup)
	}
	status := strings.ToLower(m.Status)
	switch {
	case strings.Contains(status, "fail"):
		return harness.StageMCPFailed(m.Issue), nil
	case strings.Contains(status, "auth"):
		return harness.StageMCPFact("Needs sign-in", execution.ToneBad), nil
	case strings.Contains(status, "connected"):
		return harness.StageMCPConnected(), nil
	case strings.Contains(status, "pending"), strings.Contains(status, "disabled"):
		return harness.StageMCPFact(m.Status, execution.ToneWarn), nil
	}
	return harness.StageMCPFact(m.Status, execution.ToneNeutral), nil
}
