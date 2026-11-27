# ADR 0006: Codex harness over the app-server daemon

- **Status:** accepted (2026-11-25), Stage task #384 (tree 200). Builds on
  the hosted-kind plumbing of #371 (PR #205).
- **Amended** 2026-11-26 by #420 ([ADR 0007](0007-harness-boundary.md)):
  decisions 8 (T3's threads are refused through T3's declared `wraps`, not
  by the adapter naming T3) and 11 (attribution yields to T3 through
  `wraps`, not by name);
  decision 10 (Codex is offered on workers whose machine profile allows
  local harnesses, like Claude Code; ADR 0007 decision 14). Decision 2
  stands, on workers too.
- **Sources:** live gates against `codex` 0.171.0 (app-server 0.171.0) on
  Linux x86_64 with a ChatGPT login, 2026-11-25, in scratch directories.
  Probe threads were deleted afterwards. Sanitized protocol fixtures:
  `server/internal/codex/testdata/`.

## Context

Stage adds Codex as harness kind `codex`. Codex ships `codex app-server`,
a JSON-RPC protocol (JSONL frames without a `jsonrpc` member) over stdio or
an experimental WebSocket. `codex app-server daemon start|version` runs one
long-lived daemon per Codex home that serves the protocol over WebSocket on
a unix control socket. Codex Desktop and `codex resume` attach to the same
daemon, so a thread Stage starts is the user's thread too.

## Decisions

1. **Kind and hosting.** Kind `codex`, hosting `hosted` (the daemon is a
   local process), stable identity. The identity is the Codex home the
   daemon reports (`initialize` `codexHome`); the label is
   "Codex · <account email>". Stage stores no Codex credentials: login is
   `codex login`.
2. **Process: the daemon, not a child.** Stage dials the daemon's socket.
   When `daemon version` reports it down, Stage runs `daemon start` on
   demand (at most once per 30 s); it never stops, restarts or updates it.
   A stdio child would die with Stage and hide threads from `codex resume`.
   The CLI runs with a scrubbed environment (no caller session variables)
   and a 20 s timeout. A missing CLI is `not_installed`.
3. **Launch is `reserved`.** `thread/start` (cwd, approval policy, sandbox,
   model), then Stage checkpoints the thread id before `turn/start`
   (prompt, data-URL images, effort, `clientUserMessageId` = the launch's
   request id). Replay after a lost reply reads the thread: a first turn
   whose user message carries that client id (or the prompt text) is the
   admission; no turn yet starts the turn; any other turn fails the
   attempt. Gate: no rollout exists before the first turn, and an unloaded
   thread answers -32600 `thread not loaded`, so replay resumes it first.
   `thread/name/set` is best effort. After admission Stage unsubscribes
   (`thread/unsubscribe` keeps the running turn alive; gate).
4. **Rejection.** Only validation before any effect is `Rejected` (a
   non-root workspace, a missing directory, a non-image attachment, an
   invalid model option or permission). On replay a rejection becomes a
   plain error: the thread may exist.
5. **Observation.** `thread/read` with turns. Status `active` is running,
   or needs_input with the `waitingOnApproval`/`waitingOnUserInput` flags;
   `systemError`, or an in-progress turn on a thread the daemon no longer
   has loaded, is an error (no terminal evidence). The last turn's
   completed/failed/interrupted maps to done/failed/interrupted. The reply
   prefers the `final_answer` agent message, capped at 4,000 characters.
6. **Push.** The daemon broadcasts `thread/status/changed`,
   `thread/archived`, `thread/unarchived` and `turn/completed` to every
   client, including threads it never resumed (gate). Stage observes and
   sinks them; polling remains the fallback. Initialize opts out of the
   noisy streaming notifications.
7. **Needs input is answered in Codex.** Approvals reach a resuming client
   with the same request id (gate), but Stage does not answer them: respond
   and send report unavailable with "Answer in Codex: copy the resume
   command and run it in a terminal." Every Codex task's links carry
   `cd <dir> && codex resume <id>` (shell-quoted); the task list offers
   "Copy resume command".
8. **Inventory.** Workspaces are the projects `config/read` reports
   trusted (`projects{path:{trust_level}}`); a Git checkout is importable.
   Stage never writes Codex's config: trust a project in Codex.
   *Superseded by [ADR 0012](0012-harness-workspace-add.md): an explicit
   add-workspace request may trust a folder through the app server's
   `config/batchWrite`; Stage still never edits Codex's files.* Adoption
   takes a thread id or a `codex resume <id>` command. It refuses T3 Code's
   threads ("Track it through T3": T3 runs its own app-server and tracks
   them), ephemeral threads and subagent threads. Amended by #420: the
   adapter reports the originator as the session's origin and the
   coordinator refuses `T3 Code` because T3's descriptor wraps it. Threads created through
   the daemon carry originator "Codex Desktop", not Stage's client name
   (gate), so Stage knows its threads by the ids it records.
9. **Closure.** Archive (`thread/archived`, or `thread/list` with
   `archived:true` for a thread no longer loaded) is the closure signal,
   with the auto-complete toggle "Complete tasks when archived in Codex".
10. **Scope.** Root workspace only (no Codex worktrees; amended by #420:
    Codex declares no launch options, so a `worktree` option is rejected), no Plan mode, images
    included. Permission presets: read-only (on-request, read-only), auto
    (on-request, workspace-write; default), full-access (never,
    danger-full-access). On a worker, Codex runs on that machine where its
    profile allows local harnesses, like Claude Code (ADR 0007 decision 14);
    the main server keeps the machine's `codex_settings` row.
11. **Attribution.** Codex sets `CODEX_THREAD_ID` in commands (gate). Its
    precedence ([ADR 0005](0005-claude-code-harness.md)'s generic
    owns/yields) yields to T3 (`T3CODE_THREAD_ID`): a Codex process T3 runs
    is T3's session. Amended by #420: Codex declares an origin; yielding
    follows from T3's `wraps`, not a precedence naming T3.
12. **Settings.** `codex_settings` (machine, CLI path, connected) stores the
    CLI path and whether the machine connected; boot reinstalls the adapter
    when connected (migration 0031).

## Consequences

- An unknown method answers -32600 `unknown variant`: Stage reports the
  method as unsupported by this Codex version, naming it.
- The daemon's protocol is experimental. Fixtures pin what Stage parses;
  rerun the gates when Codex changes the shapes.
- Stage sees threads the user starts in Codex Desktop through push and
  adoption, not through a full inventory scan.
