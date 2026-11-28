package opencode

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Kind is OpenCode's harness kind.
const Kind = "opencode"

// Setup actions (opencodesetup.Connector).
const (
	ActionConnectLocal = "connect-local" // {"newInstance"}: the service registered on the server's machine
	ActionPair         = "pair"          // {"link","newInstance"}: an `opencode pair` link
	ActionCheck        = "check"
)

// Descriptor: OpenCode is a connected service. Its address is not a lasting
// identity (the connector proves continuity); an archived session may
// complete its task.
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "OpenCode", Sessions: harness.SessionsMachine, WebAddress: true, ContinuityProof: true,
	DiscoveryHint: "Open the workspace in OpenCode on this connection, then refresh discovery.",
	// Forms with conditions, hidden or external fields (Interactive.Requests).
	UnlistedRequests: "Answer this form in OpenCode.",
	Closures: []harness.Closure{{
		Signal: execution.ClosedArchived, Provenance: "harness_archived",
		Setting:   "Complete tasks when archived in OpenCode",
		Completed: "Completed automatically: archived in OpenCode. This result stays unchanged when execution continues.",
		Via:       "via OpenCode archive",
		Event:     "Completed from OpenCode archive; accepted evidence saved in Stage.",
	}},
	Setup: harness.Setup{
		Fields: []harness.Field{
			{ID: "endpoint", Label: "Server URL", Kind: harness.FieldURL},
			{ID: "browserEndpoint", Label: "Browser URL", Kind: harness.FieldURL},
			{ID: "webClient", Label: "Web client URL", Kind: harness.FieldURL},
			{ID: "username", Label: "Username", Kind: harness.FieldText},
			{ID: "password", Label: "Password", Kind: harness.FieldSecret},
			{ID: "newInstance", Label: "Connect as a new instance", Kind: harness.FieldBool, Help: "Confirms a different OpenCode service; the current connection retires."},
		},
		Actions: []execution.Action{
			{
				ID: ActionConnectLocal, Label: "Connect local service", Primary: true,
				Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
				Input:        []execution.Field{newInstance},
				Availability: execution.Availability{Available: true},
			},
			{
				ID: ActionPair, Label: "Pair with link",
				Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
				Input: []execution.Field{
					{ID: "link", Label: "Pairing link", Kind: execution.FieldSecret, Required: true, Help: "Printed by `opencode pair`."},
					newInstance,
				},
				Availability: execution.Availability{Available: true},
			},
			{
				ID: ActionCheck, Label: "Check again",
				Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
				Availability: execution.Availability{Available: true},
			},
		},
		Disconnect: true,
	},
	Attribution: harness.Attribution{
		MetaKeys: []string{"ai.opencode/sessionID"},
		Env:      "OPENCODE_SESSION_ID",
		Hint:     "MCP: direct OpenCode MCP supplies it per call; an intermediary passes callerSession {connectionId: {connectionId}, externalSessionId}. The {cli} CLI and its stdio MCP detect it from OPENCODE_SESSION_ID ({cli} context shows it); otherwise pass --caller-connection {connectionId} --caller-session ID.",
	},
}

var newInstance = execution.Field{ID: "newInstance", Label: "Connect as a new instance", Kind: execution.FieldBool}
