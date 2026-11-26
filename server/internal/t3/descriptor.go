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

// Wrapped session origins: T3 runs other harnesses' sessions and leaves
// these in them (a Claude Code transcript's entrypoint, a Codex thread's
// originator). T3 tracks those sessions, so Stage refuses them elsewhere.
const (
	OriginEntrypoint = "t3-code"
	OriginOriginator = "T3 Code"
)

// Descriptor: T3 is a connected service whose environment id names one
// instance for good; a settled thread may complete its task. It wraps the
// sessions it runs in other harnesses (Wraps).
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "T3", Sessions: harness.SessionsMachine, StableIdentity: true,
	MovedRetire: func(old, next harness.Summary) bool { return !old.Configured && next.Configured },
	LinkSchemes: []string{"t3code"},
	Closures: []harness.Closure{{
		Signal: execution.ClosedSettled, Provenance: "harness_settled",
		Setting:   "Complete tasks when settled in T3",
		Completed: "Completed automatically: settled in T3. This result stays unchanged when execution continues.",
		Via:       "via T3 settle",
		Event:     "Completed from T3 settle; accepted evidence saved in Stage.",
	}},
	Setup: harness.Setup{
		Fields: []harness.Field{
			{ID: "url", Label: "T3 URL", Kind: harness.FieldURL, Help: "scheme://host[:port] of the T3 server; null resets to the default."},
			{ID: "publicUrl", Label: "Browser URL", Kind: harness.FieldURL, Help: "T3 address for Open in T3 links in the browser; null follows the T3 URL."},
			{ID: "adminToken", Label: "Admin token", Kind: harness.FieldSecret, Help: "Optional; lets Stage renew its T3 token itself. null clears it."},
		},
		Actions: []execution.Action{
			{
				ID: ActionPair, Label: "Pair with code",
				Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
				Input: []execution.Field{{
					ID: "code", Label: "Pairing code", Kind: execution.FieldSecret, Required: true, Help: "Printed by `t3 pair` on the T3 host.",
				}},
				Availability: execution.Availability{Available: true},
			},
			{
				ID: ActionAutoPair, Label: "Pair automatically",
				Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
				Availability: execution.Availability{Available: true},
			},
		},
		Disconnect: true,
	},
	Wraps: &harness.Wraps{Origins: []string{OriginEntrypoint, OriginOriginator}},
	Attribution: harness.Attribution{
		Env:  "T3CODE_THREAD_ID",
		Hint: "T3 sets your session id in T3CODE_THREAD_ID; the {cli} CLI and its stdio MCP detect it ({cli} context shows it). Otherwise MCP: pass callerSession {connectionId: {connectionId}, externalSessionId: $T3CODE_THREAD_ID}; HTTP MCP: send the header Stage-Caller-Session: {connectionId}:$T3CODE_THREAD_ID.",
	},
}
