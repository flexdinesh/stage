package execution

import (
	"errors"
	"fmt"
	"strings"
)

// Op names an operation a harness declares in Capabilities.Operations, with
// its availability (and, when unavailable, why). Product code gates on
// these, never on a harness name (docs/execution.md).
type Op string

// Operations.
const (
	OpLaunch        Op = "launch"        // start a session for a task
	OpStop          Op = "stop"          // stop a session
	OpAdopt         Op = "adopt"         // track a session Stage did not launch
	OpReply         Op = "reply"         // read the session's final reply
	OpOpening       Op = "opening"       // read the session's opening prompt
	OpAttention     Op = "attention"     // list sessions waiting for their user
	OpSessionSearch Op = "sessionSearch" // browse a harness's listed session metadata
	OpImages        Op = "images"        // launch with image attachments
	OpRespond       Op = "respond"       // list and answer pending requests (Interactive)
	OpSend          Op = "send"          // send a message to the session (Interactive)
	OpSessionRename Op = "sessionRename" // rename a Stage-launched session after its task's title (SessionNamer)
)

// Ops are every operation, in a fixed order.
var Ops = []Op{OpLaunch, OpStop, OpAdopt, OpReply, OpOpening, OpAttention, OpSessionSearch, OpImages, OpRespond, OpSend, OpSessionRename}

// DeclaresOperations reports an operation c leaves out, or one unavailable
// without a reason. Every adapter declares them all (its contract tests).
func (f Capabilities) DeclaresOperations() error {
	var errs []error
	for _, op := range Ops {
		a, ok := f.Operations[op]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("operation %s is not declared", op))
		case !a.Available && strings.TrimSpace(a.Reason) == "":
			errs = append(errs, fmt.Errorf("operation %s is unavailable without a reason", op))
		}
	}
	return errors.Join(errs...)
}
