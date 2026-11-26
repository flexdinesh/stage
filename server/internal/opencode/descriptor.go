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
)

// Descriptor: OpenCode is a connected service. Its address is not a lasting
// identity (the connector proves continuity); an archived session may
// complete its task.
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "OpenCode", Hosting: harness.Connected, ContinuityProof: true,
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
		Actions: []harness.Action{
			{ID: ActionConnectLocal, Label: "Connect local service", Input: []harness.Field{newInstance}},
			{ID: ActionPair, Label: "Pair with link", Input: []harness.Field{{ID: "link", Label: "Pairing link", Kind: harness.FieldSecret, Help: "Printed by `opencode pair`."}, newInstance}},
		},
		Disconnect: true,
	},
	Attribution: harness.Attribution{
		MetaKeys: []string{"ai.opencode/sessionID"},
		Env:      "OPENCODE_SESSION_ID",
		Hint:     "MCP: direct OpenCode MCP supplies it per call; an intermediary passes callerSession {connectionId: {connectionId}, externalSessionId}. The {cli} CLI and its stdio MCP detect it from OPENCODE_SESSION_ID ({cli} context shows it); otherwise pass --caller-connection {connectionId} --caller-session ID.",
	},
}

var newInstance = harness.Field{ID: "newInstance", Label: "Connect as a new instance", Kind: harness.FieldBool}
