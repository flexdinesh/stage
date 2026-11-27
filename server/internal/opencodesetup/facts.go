package opencodesetup

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// probeTimeout bounds each display probe request.
const probeTimeout = 3 * time.Second

// extras probes the active service for display facts: whether OpenCode has
// the Stage MCP server. Cached harness.FactsTTL; the check action probes
// again. A failed or slow probe becomes a fact, never a status error.
func (s *Service) extras(ctx context.Context) harness.Probed {
	s.mu.Lock()
	a := s.active
	s.mu.Unlock()
	if a == nil {
		return harness.Probed{}
	}
	return s.probed.Get(ctx, func(ctx context.Context) harness.Probed {
		call, cancel := context.WithTimeout(ctx, s.probeTimeout)
		defer cancel()
		m, found, err := a.MCPServer(call, harness.StageMCPName)
		var fact execution.Fact
		var fix *harness.Fix
		switch {
		case err != nil:
			fact = harness.StageMCPProbeError(call, s.probeTimeout, err)
		case !found:
			fact = harness.StageMCPNotConfigured()
			fix = &harness.Fix{
				Text:    "Set up Stage MCP so OpenCode agents can report back to Stage: run this, then add the server it prints to OpenCode's config.",
				Command: s.cli + " setup --opencode --mcp",
			}
		default:
			fact = stageMCPFact(m.Status, m.Error)
		}
		return harness.Probed{Facts: []execution.Fact{fact}, Fix: fix}
	})
}

// stageMCPFact maps OpenCode's MCP status to the Stage MCP fact.
func stageMCPFact(status, detail string) execution.Fact {
	switch status {
	case "connected":
		return harness.StageMCPConnected()
	case "pending":
		return harness.StageMCPFact("Connecting", execution.ToneNeutral)
	case "disabled":
		return harness.StageMCPFact("Disabled", execution.ToneWarn)
	case "needs_auth":
		return harness.StageMCPFact("Needs sign-in", execution.ToneBad)
	case "failed":
		return harness.StageMCPFailed(detail)
	case "":
		return harness.StageMCPFact("Unknown", execution.ToneNeutral)
	}
	return harness.StageMCPFact(status, execution.ToneNeutral)
}
