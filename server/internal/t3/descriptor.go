package t3

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Kind is T3's harness kind.
const Kind = "t3"

// Setup actions (t3setup.Connector).
const (
	ActionPair     = "pair"      // {"code"}: redeem a `t3 pair` code
	ActionAutoPair = "auto-pair" // {}: pair through the t3 CLI on the server's machine
)

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
	Setup: harness.Setup{
		Fields: []harness.Field{{ID: "url", Label: "T3 URL", Kind: harness.FieldURL, Env: "T3_URL", Help: "scheme://host[:port] of the T3 server; null resets to the default."}},
		Actions: []harness.Action{
			{ID: ActionPair, Label: "Pair with code", Input: []harness.Field{{ID: "code", Label: "Pairing code", Kind: harness.FieldSecret, Help: "Printed by `t3 pair` on the T3 host."}}},
			{ID: ActionAutoPair, Label: "Pair automatically"},
		},
		Disconnect: true,
	},
	Attribution: harness.Attribution{
		Env:  "T3CODE_THREAD_ID",
		Hint: "T3 sets your session id in T3CODE_THREAD_ID. MCP: pass callerSession {connectionId: {connectionId}, externalSessionId: $T3CODE_THREAD_ID}, or over HTTP MCP send the header Stage-Caller-Session: {connectionId}:$T3CODE_THREAD_ID. CLI: export STAGE_CALLER_SESSION={connectionId}:$T3CODE_THREAD_ID, or pass --caller-connection {connectionId} --caller-session $T3CODE_THREAD_ID.",
	},
}
