package harness_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func TestCachedReusesUntilStaleOrForgotten(t *testing.T) {
	now := time.Unix(0, 0)
	c := harness.Cached[int]{Now: func() time.Time { return now }}
	runs := 0
	probe := func(context.Context) int { runs++; return runs }
	ctx := t.Context()
	if v := c.Get(ctx, probe); v != 1 {
		t.Fatalf("first = %d", v)
	}
	now = now.Add(harness.FactsTTL - time.Second)
	if v := c.Get(ctx, probe); v != 1 || runs != 1 {
		t.Fatalf("fresh read = %d after %d runs", v, runs)
	}
	now = now.Add(time.Second)
	if v := c.Get(ctx, probe); v != 2 {
		t.Fatalf("stale read = %d", v)
	}
	c.Forget()
	if v := c.Get(ctx, probe); v != 3 {
		t.Fatalf("after forget = %d", v)
	}
}

func TestStageMCPProbeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if f := harness.StageMCPProbeError(ctx, 3*time.Second, context.DeadlineExceeded); f.Value != "No answer within 3s" || f.Tone != execution.ToneBad {
		t.Fatalf("timeout %+v", f)
	}
	if f := harness.StageMCPProbeError(t.Context(), 3*time.Second, errors.New("boom")); f.Value != "Couldn't check: boom" || f.Tone != execution.ToneBad || f.ID != "stageMcp" || f.Label != "Stage MCP" {
		t.Fatalf("error %+v", f)
	}
	for _, f := range []execution.Fact{harness.StageMCPConnected(), harness.StageMCPNotConfigured(), harness.StageMCPFailed("")} {
		if err := execution.ValidateFacts([]execution.Fact{f}); err != nil {
			t.Fatal(err)
		}
	}
	if (harness.Ports{}).CLIName() != "stage" || (harness.Ports{CLI: "stage-dev"}).CLIName() != "stage-dev" {
		t.Fatal("CLI name")
	}
}
