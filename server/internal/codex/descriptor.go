// Package codex is the Codex harness: an adapter over the Codex app-server
// daemon's JSON-RPC protocol (WebSocket over its unix control socket).
// Codex owns the sessions: the daemon runs turns, keeps them across Stage
// restarts, and lets the user attach with `codex resume`
// (docs/adr/0006-codex-app-server-harness.md, docs/execution.md).
package codex

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Kind is Codex's harness kind.
const Kind = "codex"

// ActionConnect is the setup action that connects Codex on the machine
// (codexsetup.Connector).
const ActionConnect = "connect"
const ActionCheck = "check"

// ClientName is the clientInfo name Stage connects with. Threads started
// through the daemon carry the daemon's originator, not this name: Stage
// knows its threads by the ids it records.
const ClientName = "stage"

// Descriptor: Codex runs on Stage's machine as a local daemon.
// Its identity is the Codex home the daemon serves, which names
// one instance for good. Stage reserves a launch's identity by
// checkpointing the thread id between creating the thread and starting its
// turn. An archived thread may complete its task.
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "Codex", Sessions: harness.SessionsMachine, LocalProcesses: true, StableIdentity: true,
	StateReasons: []string{execution.ReasonOwnerExited},
	SessionHint:  "A thread ID, or a pasted codex resume command.",
	Closures: []harness.Closure{{
		Signal: execution.ClosedArchived, Provenance: "harness_archived",
		Setting:   "Complete tasks when archived in Codex",
		Completed: "Completed automatically: archived in Codex. This result stays unchanged when execution continues.",
		Via:       "via Codex archive",
		Event:     "Completed from Codex archive; accepted evidence saved in Stage.",
	}},
	Setup: harness.Setup{
		Fields: []harness.Field{
			{ID: "binary", Label: "Codex CLI", Kind: harness.FieldText, Help: "Path to the codex binary. Empty uses codex on PATH."},
		},
		Actions: []execution.Action{{
			ID: ActionConnect, Label: "Connect",
			Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
			Availability: execution.Availability{Available: true},
		}, {
			ID: ActionCheck, Label: "Check again",
			Target: execution.TargetConnection, Callers: []execution.Caller{execution.CallerUser},
			Availability: execution.Availability{Available: true},
		}},
		Disconnect: true,
	},
	Attribution: harness.Attribution{
		MetaKeys: []string{},
		Env:      "CODEX_THREAD_ID",
		// Codex records the client that started a thread (its originator),
		// so a harness that wraps Codex threads tracks them and names their
		// caller (harness.Wraps).
		Origin: &harness.Origin{},
		Hint:   "Codex sets your session id in CODEX_THREAD_ID; the {cli} CLI and its stdio MCP detect it ({cli} context shows it). Otherwise MCP: pass callerSession {connectionId: {connectionId}, externalSessionId: $CODEX_THREAD_ID}; HTTP MCP: send the header Stage-Caller-Session: {connectionId}:$CODEX_THREAD_ID.",
	},
}
