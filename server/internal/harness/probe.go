package harness

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// FactsTTL is how long a status's display probes (Stage MCP, usage) are
// reused. Status reads (polls included) reuse them; only an explicit check
// action probes again (Cached.Forget).
const FactsTTL = time.Minute

// StageMCPName is the MCP server name `stage setup` registers with agents
// (internal/cli mcpName uses it).
const StageMCPName = "stage"

// DefaultCLI is the Stage CLI name when Ports.CLI is empty.
const DefaultCLI = "stage"

// CLIName is the Stage CLI on this machine's PATH for fix commands.
func (p Ports) CLIName() string {
	if p.CLI == "" {
		return DefaultCLI
	}
	return p.CLI
}

// Probed is a status's display probe result: facts and, at most, one fix.
type Probed struct {
	Facts []execution.Fact
	Fix   *Fix
}

// Cached reuses a probe's result for TTL (FactsTTL when zero). Failures are
// part of the result, so they are cached too. The zero value is ready.
type Cached[T any] struct {
	TTL time.Duration
	Now func() time.Time

	mu  sync.Mutex
	at  time.Time
	val T
}

// Get returns the cached value while fresh, otherwise runs probe, which
// bounds its own calls.
func (c *Cached[T]) Get(ctx context.Context, probe func(context.Context) T) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	ttl := c.TTL
	if ttl == 0 {
		ttl = FactsTTL
	}
	if !c.at.IsZero() && now().Sub(c.at) < ttl {
		return c.val
	}
	c.val, c.at = probe(ctx), now()
	return c.val
}

// Forget drops the cached value.
func (c *Cached[T]) Forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero T
	c.at, c.val = time.Time{}, zero
}

// Stage MCP fact values shared by the harness probes.
const (
	StageMCPFactID    = "stageMcp"
	StageMCPFactLabel = "Stage MCP"
)

// StageMCPFact is a Stage MCP fact with value and tone.
func StageMCPFact(value string, tone execution.Tone) execution.Fact {
	return execution.Fact{ID: StageMCPFactID, Label: StageMCPFactLabel, Value: value, Tone: tone}
}

// StageMCPConnected, StageMCPNotConfigured and StageMCPFailed are the
// common Stage MCP outcomes.
func StageMCPConnected() execution.Fact { return StageMCPFact("Connected", execution.ToneOK) }

func StageMCPNotConfigured() execution.Fact {
	return StageMCPFact("Not configured", execution.ToneWarn)
}

func StageMCPFailed(issue string) execution.Fact {
	if issue == "" {
		return StageMCPFact("Failed", execution.ToneBad)
	}
	return StageMCPFact("Failed: "+issue, execution.ToneBad)
}

// StageMCPProbeError is the fact for a probe that could not answer: a
// timeout (ctx's deadline passed) or another error.
func StageMCPProbeError(ctx context.Context, timeout time.Duration, err error) execution.Fact {
	if ctx.Err() != nil {
		return StageMCPFact(fmt.Sprintf("No answer within %s", timeout), execution.ToneBad)
	}
	return StageMCPFact("Couldn't check: "+err.Error(), execution.ToneBad)
}

// StageMCPFix is the fix for an unconfigured Stage MCP: harness names the
// harness, command the matching `stage setup` command.
func StageMCPFix(harness, command string) *Fix {
	return &Fix{Text: "Set up Stage MCP so " + harness + " agents can report back to Stage.", Command: command}
}
