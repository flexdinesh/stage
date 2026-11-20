package t3

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Kind is T3's harness kind.
const Kind = "t3"

// Descriptor: T3 is a connected service whose environment id names one
// instance for good; a settled thread may complete its task.
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "T3", Hosting: harness.Connected, StableIdentity: true,
	Closures: []harness.Closure{{
		Signal: execution.ClosedSettled, Provenance: "harness_settled",
		Setting:   "Complete tasks when settled in T3",
		Completed: "Completed automatically: settled in T3. This result stays unchanged when execution continues.",
		Via:       "via T3 settle",
		Event:     "Completed from T3 settle; accepted evidence saved in Stage.",
	}},
}
