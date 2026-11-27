package codexsetup

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// probeTimeout bounds each display probe call to the daemon.
const probeTimeout = 3 * time.Second

// extras probes the connected daemon for display facts: whether Codex has
// the Stage MCP server, and the account's usage windows (ChatGPT login
// only). Cached harness.FactsTTL; the check action probes again. A failed
// or slow probe becomes a fact, never a status error.
func (s *Service) extras(ctx context.Context, account *codex.Account) harness.Probed {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return harness.Probed{}
	}
	return s.probed.Get(ctx, func(ctx context.Context) harness.Probed {
		var out harness.Probed
		mcp, fix := s.stageMCP(ctx, client)
		out.Facts, out.Fix = append(out.Facts, mcp), fix
		if account != nil && account.Type == "chatgpt" {
			out.Facts = append(out.Facts, s.usage(ctx, client)...)
		}
		return out
	})
}

func (s *Service) timeout() time.Duration {
	if s.opts.ProbeTimeout > 0 {
		return s.opts.ProbeTimeout
	}
	return probeTimeout
}

func (s *Service) stageMCP(ctx context.Context, client *codex.Client) (execution.Fact, *harness.Fix) {
	timeout := s.timeout()
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	st, found, err := client.MCPServer(call, harness.StageMCPName)
	switch {
	case err != nil:
		return harness.StageMCPProbeError(call, timeout, err), nil
	case !found:
		return harness.StageMCPNotConfigured(), harness.StageMCPFix("Codex", s.opts.CLIName()+" setup mcp --codex")
	case st.ToolsError != nil:
		return harness.StageMCPFailed(*st.ToolsError), nil
	}
	return harness.StageMCPConnected(), nil
}

func (s *Service) usage(ctx context.Context, client *codex.Client) []execution.Fact {
	call, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	rl, err := client.RateLimits(call)
	if err != nil {
		s.opts.Logger.Debug("codex rate limits unavailable", "err", err)
		return nil
	}
	now, loc := time.Now(), time.Local
	if s.opts.Now != nil {
		now = s.opts.Now()
	}
	if s.opts.Location != nil {
		loc = s.opts.Location
	}
	return usageFacts(rl, now, loc)
}

// usageFacts are the primary and secondary windows in Codex's /status
// words: "5h limit", "42% used · resets 14:30".
func usageFacts(rl codex.RateLimits, now time.Time, loc *time.Location) []execution.Fact {
	var facts []execution.Fact
	for _, w := range []struct {
		id, fallback string
		win          *codex.RateLimitWindow
	}{
		{"usagePrimary", "Primary limit", rl.Primary},
		{"usageSecondary", "Secondary limit", rl.Secondary},
	} {
		if w.win == nil {
			continue
		}
		pct := int(math.Round(w.win.UsedPercent))
		value := fmt.Sprintf("%d%% used", pct)
		if w.win.ResetsAt != nil {
			value += " · resets " + resetText(time.Unix(*w.win.ResetsAt, 0).In(loc), now.In(loc))
		}
		var tone execution.Tone
		switch {
		case pct >= 100:
			tone = execution.ToneBad
		case pct >= 80:
			tone = execution.ToneWarn
		}
		facts = append(facts, execution.Fact{ID: w.id, Label: windowLabel(w.win.WindowDurationMins, w.fallback), Value: value, Tone: tone})
	}
	return facts
}

// windowLabel names a usage window by its length, as Codex does.
func windowLabel(mins *int64, fallback string) string {
	switch {
	case mins == nil || *mins <= 0:
		return fallback
	case *mins == 7*24*60:
		return "Weekly limit"
	case *mins%(24*60) == 0:
		return fmt.Sprintf("%dd limit", *mins/(24*60))
	case *mins%60 == 0:
		return fmt.Sprintf("%dh limit", *mins/60)
	}
	return fmt.Sprintf("%dm limit", *mins)
}

// resetText is the time on the same day, else the time and date.
func resetText(at, now time.Time) string {
	if y, m, d := at.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return at.Format("15:04")
	}
	return at.Format("15:04 on 2 Jan")
}
