package cli

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// Usage is one line: all input with its cache reads, output, working time,
// and a cost only when the harness reported one.
func TestPrintUsage(t *testing.T) {
	var b strings.Builder
	printTaskDetail(&b, client.TaskDetail{Usage: &execution.Usage{Turns: 1, InputTokens: 2, CacheReadTokens: 27618, CacheWriteTokens: 16652, OutputTokens: 4875, WorkingMs: 23015}})
	cost := 1.234
	printTaskDetail(&b, client.TaskDetail{Usage: &execution.Usage{Turns: 3, InputTokens: 999, OutputTokens: 1_260_000, CostUSD: &cost}})
	printTaskDetail(&b, client.TaskDetail{})
	for _, want := range []string{
		"usage    1 turn · 44.3k in (27.6k cached) · 4.9k out · 23s working\n",
		"usage    3 turns · 999 in · 1.3M out · $1.23\n",
	} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q:\n%s", want, b.String())
		}
	}
	if n := strings.Count(b.String(), "usage "); n != 2 {
		t.Fatalf("%d usage lines, want 2:\n%s", n, b.String())
	}
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1k", 4875: "4.9k", 999_949: "999.9k", 999_950: "1M", 1_260_000: "1.3M"} {
		if got := compactCount(n); got != want {
			t.Fatalf("compactCount(%d) %q, want %q", n, got, want)
		}
	}
}

func TestPrintLaunchBase(t *testing.T) {
	id := int64(12)
	reason := "#12 ran at project root"
	var b strings.Builder
	printTaskDetail(&b, client.TaskDetail{CurrentLaunchBase: &db.LaunchBase{BaseRef: "feat/a", Source: db.LaunchBasePredecessor, SourceTaskID: &id}})
	printTaskDetail(&b, client.TaskDetail{CurrentLaunchBase: &db.LaunchBase{BaseRef: "origin/main", Source: db.LaunchBaseFallback, Reason: &reason}})
	for _, want := range []string{"base     feat/a (from #12)", "base     origin/main (fallback: #12 ran at project root)"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q:\n%s", want, b.String())
		}
	}
}
