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
	Kind: Kind, Name: "Claude Code", Hosting: harness.Hosted,
	StableIdentity: true,
	Launch:         harness.LaunchCorrelated,
	Attention:      harness.AttentionSilent,
	// Claude names its worktree branches and starts them from origin's
	// default branch, or from a predecessor's branch (docs/adr/0005 E10).
	Worktree: harness.Worktree{Predecessor: true},
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
		Actions:    []harness.Action{{ID: ActionConnect, Label: "Connect"}},
		Disconnect: true,
	},
	Attribution: harness.Attribution{
		MetaKeys: []string{},
		Env:      SessionEnv,
		// A background session owns its variable even when Claude's daemon
		// leaked T3's into it; a Claude process T3 runs is T3's session.
		Precedence: &harness.Precedence{
			Owns:   []harness.EnvMatch{{Env: "CLAUDE_CODE_SESSION_KIND", Value: "bg"}},
			Yields: []harness.EnvMatch{{Env: "T3CODE_THREAD_ID"}, {Env: "CLAUDE_CODE_ENTRYPOINT", Value: "t3-code"}},
		},
		Hint: "Claude Code sets your session id in CLAUDE_CODE_SESSION_ID; the {cli} CLI and its stdio MCP detect it ({cli} context shows it). Otherwise MCP: pass callerSession {connectionId: {connectionId}, externalSessionId: $CLAUDE_CODE_SESSION_ID}; HTTP MCP: send the header Stage-Caller-Session: {connectionId}:$CLAUDE_CODE_SESSION_ID.",
	},
}
