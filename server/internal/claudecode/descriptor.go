// Package claudecode is the Claude Code harness: Stage starts background
// sessions with the claude CLI (`claude --bg`), reads Claude's session list
// (`claude agents --json --all`) and transcripts, and stops sessions. Claude
// owns the sessions, worktrees, models and login (docs/adr/0005).
package claudecode

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Kind is Claude Code's harness kind.
const Kind = "claude"

// ActionConnect is the setup action that checks the CLI and its login and
// records the connection (claudecodesetup).
const ActionConnect = "connect"
const ActionCheck = "check"

// SessionEnv is the variable Claude Code sets to the session id in its
// agents' processes.
const SessionEnv = "CLAUDE_CODE_SESSION_ID"

// Descriptor: Claude Code runs as local processes owned by Claude's daemon.
// Its config directory is a lasting identity. Claude assigns session ids, so
// a launch is correlated by its marker. A session removed in Claude (`claude
// rm`) after its turn ended may complete its task (the archived closure).
// Sessions Stage discovers waiting (not launched or adopted) are silent: a
// waiting row, never an inbox event or push (docs/adr/0005 decision 9).
var Descriptor = harness.Descriptor{
	Kind: Kind, Name: "Claude Code", Sessions: harness.SessionsMachine, LocalProcesses: true,
	StateReasons:   []string{execution.ReasonOwnerExited},
	StableIdentity: true,
	Launch:         harness.LaunchCorrelated,
	Attention:      harness.AttentionSilent,
	Closures: []harness.Closure{{
		Signal: execution.ClosedArchived, Provenance: "harness_archived",
		Setting:   "Complete tasks when removed in Claude Code",
		Completed: "Completed automatically: removed in Claude Code. This result stays unchanged when execution continues.",
		Via:       "via Claude Code removal",
		Event:     "Completed from Claude Code removal; accepted evidence saved in Stage.",
	}},
	SessionHint: "A session UUID, the 8-character ID claude agents shows, or a pasted claude attach or --resume command.",
	Setup: harness.Setup{
		Fields: []harness.Field{
			{ID: "binary", Label: "Claude command", Kind: harness.FieldText, Help: "Name or path of the claude executable. Blank: Stage looks on its PATH, then ~/.local/bin."},
		},
		Actions: []execution.Action{{
			ID: ActionConnect, Label: "Connect", Primary: true,
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
		Env:      SessionEnv,
		// A background session owns its variable even when Claude's daemon
		// leaked another harness's into it. Claude records the client that
		// started a session (its entrypoint, also in CLAUDE_CODE_ENTRYPOINT),
		// so a harness that wraps Claude sessions tracks them and names
		// their caller (harness.Wraps).
		Precedence: &harness.Precedence{
			Owns: []harness.EnvMatch{{Env: "CLAUDE_CODE_SESSION_KIND", Value: "bg"}},
		},
		Origin: &harness.Origin{Env: "CLAUDE_CODE_ENTRYPOINT"},
		Hint:   "Claude Code sets your session id in CLAUDE_CODE_SESSION_ID; the {cli} CLI and its stdio MCP detect it ({cli} context shows it). Otherwise MCP: pass callerSession {connectionId: {connectionId}, externalSessionId: $CLAUDE_CODE_SESSION_ID}; HTTP MCP: send the header Stage-Caller-Session: {connectionId}:$CLAUDE_CODE_SESSION_ID.",
	},
}
