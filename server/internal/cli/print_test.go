package cli

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

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
