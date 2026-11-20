package opencode

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Kind is OpenCode's harness kind.
const Kind = "opencode"

// Descriptor: OpenCode is a connected service. Its address is not a lasting
// identity (the connector proves continuity); an archived session may
// complete its task.
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "OpenCode", Hosting: harness.Connected,
	Closures: []harness.Closure{{
		Signal: execution.ClosedArchived, Provenance: "harness_archived",
		Setting:   "Complete tasks when archived in OpenCode",
		Completed: "Completed automatically: archived in OpenCode. This result stays unchanged when execution continues.",
		Via:       "via OpenCode archive",
		Event:     "Completed from OpenCode archive; accepted evidence saved in Stage.",
	}},
}
