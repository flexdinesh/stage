# ADR 0005: Claude Code harness (background sessions)

- **Status:** accepted (2026-11-25), Stage task #370 (spike), planned in
  #369. Docs and parser fixtures only: no code, no schema. Tree 200's task
  prompts call this "ADR 0003"; that number went to
  [storage adapters](0003-storage-adapters.md) first, so this is 0005.
- **Amended** 2026-11-25 by #378: decision 11 (`rm` is the archived
  closure) and the "entry gone" row of decision 4.
- **Extended** 2026-11-25 by #382: [cloud sessions](#cloud-sessions-claude---cloud-not-built)
  researched and not built.
- **Amended** 2026-11-25 by #377: decision 16 (push via HTTP hooks),
  evidence E9, the hooks approval route.
- **Amended** 2026-11-25 by #380: decision 17 (usage).
- **Amended** 2026-11-25 by #379: decision 10 (predecessor bases),
  evidence [E10](#e10-predecessor-base).
- **Sources:** probes of the local `claude` CLI on Linux x86_64 with a
  claude.ai (Max) login, 2026-11-25, in scratch directories with haiku
  prompts. The CLI auto-updated from 2.1.331 to 2.1.332 two minutes in;
  every result below is from 2.1.332 unless it names 2.1.331. T3 source
  `074a0d35be` (2026-11-22). Fixtures:
  [`server/internal/claudecode/testdata/`](../../server/internal/claudecode/testdata/README.md).

## Context

Stage adds Claude Code as harness kind `claude`. Claude owns the session:
Stage starts a background session, reads Claude's roster and transcript,
and stops it. Claude Code has no HTTP server, so Stage drives the CLI
([why not HTTP](#why-not-http)). The plan (#369) assumed
`claude --bg --session-id <uuid>`, a reserved launch. The spike shows `--bg`
ignores `--session-id`, so the launch is correlated (decision 2).

## Decisions

Decisions 1 and 4 to 12 were made in #369; the spike confirms or refines
them. 2, 3, 13 and 14 come from the evidence.

1. **Kind and hosting.** Kind `claude`, hosting `hosted`: sessions run as
   processes on Stage's machine. Claude owns sessions, worktrees, models and
   login. Stage stores no Claude credentials. The connection's stable identity is
   `configDirectory` from `claude auth status --json`, which also reports
   `projectsDirectory` (the transcript root) and `loggedIn`.
2. **Launch is `correlated`.** Stage runs, in the workspace directory:

   ```sh
   claude --bg -n "<task title>" [--model M] [--effort E] [--permission-mode P] [-w <name>] "<prompt>\n\nstage-launch:<requestId>"
   ```

   It never passes `--session-id`: `--bg` ignores it with a warning
   ([E1](#e1-launch)). On success the CLI exits 0 within about 0.6 s and
   prints `backgrounded · <short>[ · <name>]`, where `<short>` is the first 8
   hex characters of the new session's UUID. Stage maps it to the UUID
   through the roster (`id` to `sessionId`, same `cwd`) and confirms the
   attempt with that UUID. After an uncertain outcome (no stdout read, Stage
   restart), the correlator lists roster entries and transcript files under
   the workspace's project directory created after the dispatch and matches
   the marker in the first user prompt. Stage never launches again
   ([execution.md](../execution.md#execution-contract-v2)). A seeded reserve
   (`-p --session-id U` then `--bg --resume U`) keeps the UUID but costs a
   model turn, writes a fake first exchange and runs it in the foreground:
   rejected.
3. **Rejection.** Only a refusal the CLI prints before it dispatches
   (`Workspace not trusted`; usage errors likely too, not probed) is
   `Rejected`; the task returns to
   backlog with the CLI's message. Any other failure stays unconfirmed and
   goes to the correlator. Exit 0 does not prove the session started (a bad
   `--resume` exits 0 and then the session fails): the roster decides.
4. **Observation** reads `claude agents --json --all` once per tick, shared
   by every session (about 0.13 s; cache about 2 s). Mapping by `state`:

   | Roster `state` | Stage state | Note |
   | --- | --- | --- |
   | `working` | running | `status: busy` |
   | `blocked` | needs_input | permission prompt (`status: waiting`, `waitingFor: "permission prompt"`), or a turn that ended waiting for the user (`status: idle`, no `waitingFor`) |
   | `done` | done | the process may stay alive and take more turns: a later `working` is new activity |
   | `failed` | failed | e.g. a session that never started |
   | `stopped` | interrupted | after `claude stop` |
   | entry gone (`rm`) | idle, with the reply when the transcript exists, else not found | never failure; closed (`archived`) after a finished background turn (decision 11) |

   The hourly reap ([E7](#e7-reap)) ends the process but keeps the entry and
   its `state`, so a reaped session maps as above (no `pid`).

   Entries with `kind: interactive` have no `state`. They are terminal or SDK
   sessions (T3's among them). Stage ignores them unless adopted
   (decision 9); an adopted one maps by `status`: `busy` running,
   `waiting` needs_input, `idle` idle.
5. **Reply** is the transcript tail, best effort, at most 4,000 characters,
   failing soft ([E4](#e4-transcripts)).
6. **Stop** is `claude stop <short>`, for background sessions only.
7. **Resume command** (`Links.command`): `claude attach <short>` while the
   roster lists the session, else `cd <cwd> && claude --resume <uuid>`.
   `--resume` on a live background session is refused without a write,
   so the command is always safe to run ([E3](#e3-stop-attach-resume-rm)).
8. **Needs-input is answered in Claude** in v1 (attach or resume). Approval
   routes are recorded [below](#approval-routes-record-only), not built.
9. **Scope.** Stage sees the sessions it launched plus ones the user adopts
   by UUID (or short id, resolved through the roster). Adoption is refused
   with "Track it through T3" when the transcript's first `user` record has
   `entrypoint: "t3-code"`. Opening an adopted session returns its first
   user prompt. Amended by #383: discovery surfaces, adoption tracks. The
   adapter's attention feed lists every session waiting for its user
   (background `blocked`, retired ones included, and interactive `waiting`,
   T3's left out), read from the same roster. Under the descriptor's
   `silent` attention policy they appear only as inbox waiting rows and in
   the needs-input count, never as events or pushes, until the user adopts
   one (the row's Track). Global hooks (`stage setup --claude`) were
   rejected: they would write the user's global Claude settings, and the
   roster already reports waiting sessions.
10. **Workspaces**: the project root, or Claude's `-w <name>`. Claude picks
    the base and the branch (`worktree-<name>`), so `worktreeBase` is
    unavailable. `predecessorBase` is available (#379): Stage launches
    from the checkout that has the predecessor's branch checked out (its
    Claude worktree) with `worktree.baseRef` `head` in its one
    `--settings` (the hooks file's head variant, decision 16), so Claude makes, names and owns the new worktree, starting at the
    predecessor's commit ([E10](#e10-predecessor-base)). Stage never creates
    or removes worktrees itself. Stage names
    worktrees uniquely per attempt (e.g. `stage-<taskId>-<attempt>`):
    `-w` silently reuses an existing worktree of that name, dirty or not
    ([E5](#e5-worktrees)). Stage never removes worktrees; `claude rm`
    does.
11. **`rm` is the archived closure** (#378; v1 had none). The idle reap
    keeps the entry and its `state` (E7); `rm` drops the entry and keeps the
    transcript (E3), so the two are distinguishable. Stage reports
    `closed: archived` (provenance `harness_archived`, no migration) only
    when the session list was read, the session is not on it, its
    transcript exists, and the transcript's last turn ran in a background
    session (`sessionKind: "bg"`) and is over (`turn_duration`). Not
    closures: an adopted terminal or SDK session leaving the list when its
    process exits, `rm` mid-turn (it kills the turn), a refused `rm` (dirty
    worktree: the entry stays), `purge` (no transcript). The state stays
    idle (the entry took it), so inferred completion reads the task's
    stored `done`: a session removed while blocked completes nothing.
    A removed session is not `archived` for adoption or linking: its
    transcript stays resumable.
12. **Inventory** is `~/.claude.json` `projects` (under `CLAUDE_CONFIG_DIR`
    when set). Git checkouts are importable. `-w` worktrees are not listed
    there, only their repository.
13. **Environment.** Stage spawns the CLI with `CLAUDECODE`,
    `CLAUDE_CODE_*`, `CLAUDE_PID`, `CLAUDE_AGENT_SDK_VERSION` and
    `T3CODE_*`/`T3_*` cleared. The first `--bg` launch starts a per-user
    Claude daemon, and every later background session inherits the daemon's
    environment, not the launcher's ([E6](#e6-environment-and-daemon)).
    Stage does not set `CLAUDE_CODE_ENTRYPOINT`: it is safe but overwritten
    to `cli`. Stage tells its sessions apart by their recorded UUIDs.
    Attribution reads `CLAUDE_CODE_SESSION_ID`, which background sessions
    set. A per-launch variable such as `STAGE_CALLER_SESSION` never reaches
    the session.
14. **Identity keys.** Stage keys sessions on `sessionId` (and `id` for CLI
    calls), never on `pid` or `startedAt`: both change when the daemon
    restarts or wakes a session.
15. **Images** (#381) are files. Stage writes a launch's images to
    `<workspace>/.claude/stage-images/stage-<requestId>/` (a `*`
    `.gitignore` in `stage-images`), passes that folder with `--add-dir`
    and lists each file after the prompt for the Read tool. The folder is
    below the workspace because Claude refuses an untrusted added folder
    ([E8](#e8-images)). A worktree session's cwd is its worktree, so the
    folder is added either way.
16. **Push via HTTP hooks** (#377, [E9](#e9-hooks)). Every launch passes
    `--settings <state dir>/hooks-settings.json`: Claude HTTP hooks for
    `Stop`, `StopFailure`, `Notification`, `PermissionRequest`,
    `SessionEnd` and `UserPromptSubmit`, posting to a Stage listener on
    `127.0.0.1` with a bearer token from the file. A hook only wakes Stage:
    it names the session, and the adapter reads the roster and transcript
    again (retrying briefly until the roster shows the event's change),
    then pushes that observation through `execution.EventSource`. The body
    is never trusted for state (Stop cannot tell done from blocked; the
    roster can), so a forged hook costs a read. Polling stays the
    reconciliation: adopted sessions, copies (`--resume` of a live session
    drops `--settings`), a session woken after the port moved, and hooks a
    policy blocks (`allowedHttpHookUrls`, `disableAllHooks`,
    `allowManagedHooksOnly`, which fail silently) are polled only. Stage's
    main API is not the target: the managed service listens on a Unix socket
    only, and HTTP hooks need TCP. A command hook running the `stage` binary
    was rejected: its path changes with updates and identities.
17. **Usage** (#380) comes from the transcript, best effort: turns are the
    typed user prompts on the main chain (the current one included). A user
    record's `origin.kind` is `human` for a typed prompt (absent in `-p` and
    SDK sessions); a background subagent finishing appends a
    `task-notification` user record that starts another model turn with its
    own `turn_duration` (live probe, 2.1.332), and `peer` marks another
    session's message: neither is a turn. Tokens
    count every assistant `message.usage` once (deduped by `message.id`
    plus `requestId`, the largest value of each channel: a message's records
    repeat it), sidechains and subagent transcripts included, `<synthetic>`
    skipped; working time sums the main chain's `turn_duration.durationMs`
    (finished turns). No cost. Evidence: transcripts carry per-message
    `usage` (`input_tokens`, `cache_read_input_tokens`,
    `cache_creation_input_tokens`, `output_tokens`) and `turn_duration`
    records, and no cost field. `~/.claude.json` `projects[<path>].lastCost`
    is per folder, for its last interactive session only (folders whose last
    session ran in the background showed 0 or none), so it is not per
    session. The jobs `state.json` `tokens` is undocumented (output tokens
    only); Stage does not use it. Subagents write
    `<projectsDirectory>/<encoded cwd>/<sessionId>/subagents/agent-*.jsonl`
    (records `isSidechain: true`, plus a `.meta.json`), with no message
    overlap with the main file. Nested subagents (depth 2 and more) were not
    probed and may be undercounted. Stage never prices tokens: the
    observation's `costUsd` stays null.

## Evidence

Commands ran with the inherited Claude and T3 variables cleared. Paths
are shortened; `<dir>` is a trusted scratch directory.

### E1 Launch

| Probe | Result |
| --- | --- |
| `claude --bg --session-id U "<p>"` | exit 0 in 0.6 s. stderr: `warning: --bg manages the session id; ignoring --session-id (use --resume <id> to continue an existing session)` and, on the first launch, `Starting background service…`. stdout: `backgrounded · 385d212a` plus four hint lines (`claude agents`, `attach`, `logs`, `stop`). Session UUID `385d212a-048c-…`, not U. |
| `claude --bg -n "Stage task 999" "<p>"` | stdout `backgrounded · b4889af7 · Stage task 999`; roster `name` is the given name. Without `-n` Claude names the session from the prompt, marker words included (`wt stage marker test`). |
| `claude --bg --resume <unused uuid> "<p>"` | exit 0, prints `backgrounded · 2277a5ab` (the given id). The session goes `state: failed` with no transcript. Daemon log: `bg settled 2277a5ab (crashed): source session … not found`. |
| `claude -p --session-id U "<p>"` then `claude --bg --resume U "<p2>"` | Both exit 0; the background session keeps U. The transcript starts with an `entrypoint: sdk-cli` exchange. |
| Same UUID while it runs: `claude --bg --resume U "<p>"` | exit 0, new session: `note: session 62d9077f is already running in the background, so this started a copy as ea01826d.` |
| Same UUID after `stop`, with flags | copy: `note: background session 19e6075e keeps its own saved options, so the flags you passed started a copy as c8ed5945. Without flags, the same command continues 19e6075e itself.` |
| Same UUID after `stop`, no flags | same session: `backgrounded · 19e6075e · touch blocked file`, `note: woke session 19e6075e with its saved options (--permission-mode, --model).` |
| Untrusted directory | exit 1, nothing started: `Workspace not trusted. Run claude in <dir> once and accept the trust prompt, then retry.` A plain subdirectory of a trusted directory inherits trust; a Git repository below it does not (its root is its own project). Trust is `projects[<path>].hasTrustDialogAccepted` in `~/.claude.json`. |

### E2 Roster: `claude agents --json [--all]`

A JSON array. Background entry fields: `pid` (absent once the process
exited), `id` (short), `cwd`, `kind: "background"`, `startedAt` (ms),
`sessionId`, `name`, `status`, `waitingFor`, `state`. Interactive entries
have `pid`, `cwd`, `kind: "interactive"`, `startedAt`, `sessionId`, `name`,
`status`, `waitingFor`; no `id` or `state`.

| Observed | Values |
| --- | --- |
| `state` | `working`, `blocked`, `done`, `failed`, `stopped` |
| `status` | `busy`, `idle`, `waiting`; absent when no process |
| `waitingFor` | `"permission prompt"` (background, blocked on approval); `"input needed"` (a T3 interactive session waiting for its user) |
| Without `--all` | entries with a live process, including idle `done` ones. `--all` adds entries with no process (`stopped`, `failed`, `done` after exit). |
| After `stop` | `state: stopped`, no `pid`, `status` or `waitingFor` |
| After `rm` | entry gone from both lists |
| T3 sessions | `kind: interactive`, `status: busy` while T3's SDK process runs. The entrypoint is not in the roster: `~/.claude/sessions/<pid>.json` has `entrypoint: "t3-code"`, `kind: "interactive"`; a background one has `entrypoint: "cli"`, `kind: "bg"`, `jobId`. |
| Daemon upgrade | the 2.1.332 daemon re-adopted the idle 2.1.331 session: new `pid` and `startedAt`, same `id`, `sessionId`, `state` |
| ~1 h reap | process ended, entry kept with the same `state`, no `pid` or `status` ([E7](#e7-reap)) |

Claude also keeps `~/.claude/jobs/<short>/state.json` (`state`, `tempo`,
`needs`, e.g. `approve Bash: touch blocked.txt && echo made`, `intent` =
the prompt, `output.result` = a one-line summary, `linkScanPath` = the
transcript) and `timeline.jsonl`. They are undocumented and removed by
`rm`; Stage does not depend on them. Fixtures keep samples.

### E3 Stop, attach, resume, rm

| Probe | Result |
| --- | --- |
| `claude stop <short>` (blocked session) | exit 0 in 0.37 s, stdout `stopped 19e6075e`; transcript kept. On a `-w` session it adds `worktree kept at <path>` and `run 'claude rm <short>' to remove the worktree and session`. |
| `claude stop` / `rm` unknown id | exit 1: `No session matching 'deadbeef'. Run 'claude agents' to list running sessions.` (`rm`: first sentence only) |
| `claude attach <short>` | full TUI of the session (prompt, tool calls, reply, input box). Killing the attaching client leaves the session alive with the same pid. |
| `claude -p --resume <uuid>` while the background session lives | exit 1, transcript unchanged: `Error: That session is running in the background (c83c2203). Run claude attach c83c2203 to open it, or claude stop c83c2203 first to resume it here. Add --fork-session to branch off a copy instead.` |
| `claude rm <short>` on a live `done` or `working` session | exit 0, `removed <short>`: kills the process, drops the roster entry and job directory, keeps the transcript |
| `claude logs <short>` | raw terminal output (ANSI) of a live session; after it ends: `Couldn't read logs for <short>`, then `session not found`, with exit 0 |

### E4 Transcripts

Path: `<projectsDirectory>/<encoded cwd>/<sessionId>.jsonl`, where the
encoding turns `/` and `.` into `-` and keeps `-` and alphanumerics
(`/tmp/x/.claude/worktrees/w` becomes `-tmp-x--claude-worktrees-w`; other
characters were not probed). For
a `-w` session the cwd is the worktree. A session with no process may
report its launch directory as `cwd` (seen after a refused `rm`), so the
lookup falls back to a glob `<projectsDirectory>/*/<sessionId>.jsonl`.

One JSON object per line. Record types seen: `user`, `assistant`,
`system` (`stop_hook_summary`, `turn_duration`), `attachment`, `mode`,
`permission-mode`, `session-owner`, `ai-title`, `agent-name`,
`last-prompt`, `tools-used`, `custom-title`, `queue-operation`,
`api-request`, `api-request-shape`, `api-request-blob`,
`data-flow-records`, `atis-latch`. The parser skips unknown types.
`api-request-blob` holds the full system prompt (user CLAUDE.md, email),
so fixtures drop it and Stage never stores or shows it.

`user` and `assistant` records carry `entrypoint` (`cli` with
`sessionKind: "bg"` for background sessions, `sdk-cli` for `-p`, `t3-code`
for T3), `isSidechain`, `cwd`, `gitBranch`, `sessionId`, `uuid`,
`parentUuid`, `timestamp` and `message` (Messages API shape; `content` a
string for a typed prompt, else blocks). An assistant message can span
several records with the same `message.id`.

Last assistant text: walk the main chain (`isSidechain` false), take the
`text` blocks of the last assistant `message.id` that has any, and join
them. The turn is over once a `system` record with subtype `turn_duration`
follows. The reference implementation was run on every fixture:

| Fixture | entrypoint | turn over | reply |
| --- | --- | --- | --- |
| `done-text` | cli | yes | the story |
| `blocked-question` | cli | yes | `Blocked: the harness rejected …` |
| `blocked-permission` | cli | no | none (tail is a pending `tool_use`) |
| `working` | cli | no | none yet |
| `worktree` | cli | yes | `wt ok …` |
| `print-then-bg` | sdk-cli first, then cli | yes | `r2 bg` |
| `t3-code` | t3-code | no | `Reply text.` |

### E5 Worktrees

`claude --bg -w spikewt` in a repository at `main`: worktree
`<repo>/.claude/worktrees/spikewt`, new branch `worktree-spikewt` at HEAD,
`git worktree list` shows it `locked`. `-w` without a name picks one
(`jazzy-bubbling-bonbon`). `-w <existing>` reuses that worktree without
a message, even when it is dirty. The roster `cwd` is the worktree.

| Cleanup | Result |
| --- | --- |
| `stop` | worktree and branch kept |
| `rm` right after `stop` | exit 1, `kept <short>`, then `its worktree is still at …`, `A Claude Code lock on the worktree names a process that is still running (pid N)`; a retry once the pid exits succeeds |
| `rm`, clean worktree | `removed <short>` plus `worktree: <path>`; worktree directory and `worktree-<name>` branch deleted |
| `rm`, uncommitted changes | exit 1, worktree kept: `The worktree has uncommitted changes. Deleting it would lose them.` The process was still ended (no `pid` afterwards). |

### E6 Environment and daemon

- `claude --bg` hands the session to `claude daemon run … --origin transient`,
  a per-user process reparented to `systemd --user`. It keeps spare
  processes warm and claims one per launch (`bg claimed-spare <short>`
  in `~/.claude/daemon.log`). It restarts itself when the binary changes
  and re-adopts live sessions (`bg adopt: adopted=1`).
- Inside a background session (`env`, via its Bash tool):
  `CLAUDE_CODE_SESSION_ID=<its uuid>`, `CLAUDE_CODE_ENTRYPOINT=cli`,
  `CLAUDE_CODE_SESSION_KIND=bg`, `CLAUDE_BG_BACKEND=daemon`,
  `CLAUDE_JOB_DIR=~/.claude/jobs/<short>`, `CLAUDECODE=1`,
  `CLAUDE_CODE_CHILD_SESSION=1`, `CLAUDE_PID`, `CLAUDE_CODE_MESSAGING_SOCKET`.
- Launched with `CLAUDE_CODE_ENTRYPOINT=stage`: the session started
  normally and reported `cli` in env, transcript and session file.
- Launched with `STAGE_PROBE_VAR=xyz`: absent from the session process.
- The daemon's environment held `T3_BOOT_SERVICE_UNIT` and
  `T3_SERVICE_LAUNCHER_CONTEXT` from the process that first started it, and
  sessions inherit it. A daemon first started from inside a T3 thread
  would pass `T3CODE_THREAD_ID` on to every background session (inferred,
  not run). So attribution inside a session must check
  `CLAUDE_CODE_SESSION_KIND=bg` (a Claude background session) before
  `T3CODE_THREAD_ID` (#372's precedence rule).

### E7 Reap

Ten sessions idled past an hour, checked at 04:05Z and 04:20Z:

- The daemon ends a session's process after 60 idle minutes, on a
  one-minute sweep: `bg retire 385d212a: settled, idle 60m` then
  `bg settled 385d212a (done)` (launched 03:02Z, retired 04:02Z). A session
  still waiting for its user retires the same way (`bg retire abab23c5:
  idle-prompt, idle 60m`).
- The roster keeps every retired entry under `--all` with its `state`
  unchanged (`done`, and `blocked` for the waiting one) and no `pid` or
  `status`; `--json` alone drops them. `~/.claude/jobs/<short>/` stays.
  Nothing was removed within 78 minutes; only `rm` removes an entry.
- `claude attach <short>` on a retired session wakes it (the daemon claims
  a spare, new `pid`), and it stays alive after the attaching client exits.
- Fixture: `roster/all-reaped.json`.

### E8 Images

claude 2.1.332, 2026-11-25, `mise run claude:live`:

- `claude --bg --add-dir <dir> -- <prompt>` with `<dir>` outside every
  trusted folder exits 1 before dispatch: `Added folder not trusted: <dir>.
  Leave out its --add-dir or, if you trust it and everything in its
  repository or below it, start claude there once and accept the trust
  prompt.`
- With `<dir>` below the trusted workspace, a `manual` (default
  permissions) haiku session read a PNG listed in the prompt with the Read
  tool, never blocked on a permission prompt, and named its color.
- The added folder does not join `~/.claude.json` `projects`.
### E9 Hooks

Probed 2026-11-25 on 2.1.332 (scratch folder, haiku, cleaned up), with a
logging HTTP server on `127.0.0.1` and `--settings <file>` naming one
`type: "http"` hook per event with an `Authorization` header. Fixtures:
`testdata/hooks/`.

| Probe | Result |
| --- | --- |
| `--bg --settings <file>` | the hooks fire from the background session (axios POST, `Content-Type: application/json`, the header as written); the user's own hooks still run beside them |
| Turn ends | `Stop` about 10 ms after the roster shows `done`; input has `session_id`, `transcript_path`, `cwd`, `prompt_id`, `permission_mode`, `last_assistant_message`, `stop_hook_active` |
| Permission prompt | `PermissionRequest` 0.3 s after the roster shows `blocked` (`tool_name`, `tool_input`, `permission_suggestions`); `Notification` `permission_prompt` only about 6 s later. An empty 200 is no decision: Claude's own prompt stays |
| `claude stop` | `SessionEnd`, `reason: "other"` |
| New turn | `UserPromptSubmit` (`prompt`, `session_title`) |
| `SessionStart` | not observed |
| Saved options | `~/.claude/jobs/<short>/state.json` `respawnFlags` keeps `--settings <path>`; a woken session (`--bg --resume <uuid>` after stop: "woke session … with its saved options (… --settings …)") reads the file again: a rewritten port is used |
| Copy | `--bg --resume` of a live session starts a copy without `--settings` |
| Listener down / 401 | `hook_non_blocking_error` attachment (`connect ECONNREFUSED …`, `HTTP 401 from …`) and `stop_hook_summary.hookErrors`; the turn completes, the roster is unaffected |

The binary also documents `allowedHttpHookUrls` and
`httpHookAllowedEnvVars` (managed allowlists) and env interpolation in
headers (`allowedEnvVars`); background sessions inherit the daemon's
environment, not the launcher's (decision 13), so Stage writes the token
into the file.

### E10 Predecessor base

Claude 2.1.332's `worktree.baseRef` setting picks where `-w` branches
from: `fresh` (the default) is origin's default branch, `head` is the HEAD
of the checkout Claude is launched from. Probed (#379, and on every
`mise run claude:live`) with `claude --bg -w stage-live-dep --settings
'{"worktree":{"baseRef":"head"}}'` launched from a Claude worktree
(`<repo>/.claude/worktrees/stage-live-wt`, branch
`worktree-stage-live-wt`) holding a commit not on `main`:

- `--bg` keeps `--settings`. The new worktree is
  `<repo>/.claude/worktrees/stage-live-dep` (under the main repository,
  not the launch worktree), on a new branch at the launch worktree's
  HEAD.
- Trust comes from the repository: the launch worktree needs no prompt.
- `claude rm` of the predecessor then refuses while its branch holds
  commits on no remote: `kept 08ad94b4`, then `its worktree is
  still at "…/stage-live-wt"`, `2 unpushed commits on "worktree-stage-live-wt":
  dc8167e "predecessor" and 1 more. They exist on no remote, so deleting
  the worktree would lose them.`, with a `claude rm <short>
  --discard-unpushed <token>` hint.

## Approval routes (record only)

v1 answers in Claude (decision 8). Candidates for #375:

| Route | How | Fit |
| --- | --- | --- |
| Hooks (probed, [E9](#e9-hooks); push built in decision 16) | the `PermissionRequest` HTTP hook Stage already passes (`--settings`) | An answer channel only if the hook holds the request open until Stage answers (its `timeout`); today it answers 204 at once |
| `--permission-prompt-tool` / `--permission-prompts host` | an MCP tool Stage serves decides each prompt | `-p` (print/SDK) mode only, not `--bg` |
| Agent SDK sidecar (T3's route) | a Node process hosting `query()` with `canUseTool` | Full control, but Stage then owns the process (#385); not a `--bg` session |
| Messaging socket | `CLAUDE_CODE_MESSAGING_SOCKET`, `peerProtocol` in `~/.claude/sessions/<pid>.json`, daemon `control.sock` | Undocumented internal protocol; do not build on it |
| Attach | the user answers in `claude attach` or the `claude agents` view | v1 |

## Why not HTTP

Claude Code serves nothing: no local HTTP API, no OpenAPI, no event
stream. Its programmatic surface is the CLI (`--bg`, `agents --json`,
`stop`, `rm`, `attach`), the transcript files, hooks, and the Agent SDK.
T3 uses the SDK (`ClaudeAdapterV2.ts`): it runs `query()` from
`@anthropic-ai/claude-agent-sdk` over a stream-json child process it owns,
passes `sessionId` (new) or `resume` (existing), answers prompts through
`canUseTool`, and calls `interrupt()` and `setPermissionMode()`. It sets
`CLAUDE_CODE_ENTRYPOINT=t3-code` (`provider/Drivers/ClaudeHome.ts:76`).
That model makes the host the session owner: the session lives only as long
as T3's process. Stage instead lets Claude's daemon own the session, so
it survives Stage restarts, shows in `claude agents`, and the user can
attach to it. Stage stays a Go server with no Node runtime, at the cost of
answering prompts in Claude for now. The SDK route stays open as #385.

## Cloud sessions (`claude --cloud`): not built

Stage task #382 asked for Claude Code cloud sessions as a harness that
works from a container (connected hosting). Research only; no code.

**Sources** (2026-11-25): the bundled JavaScript of the `claude` 2.1.332
binary, read only (no request sent, no cloud session created), and the
public docs at `code.claude.com/docs/en/` (`claude-code-on-the-web`,
`authentication`, `headless`, `routines`, `self-hosted-environments`,
`self-hosted-environments-testing`, `legal-and-compliance`) and
`platform.claude.com/docs/en/` (Managed Agents,
`api/claude-code/routines-fire`). "Seen" is in the binary, "documented"
in the docs, "inferred" neither.

### API surface (seen, undocumented)

`claude --cloud` talks to `https://api.anthropic.com` with
`Authorization: Bearer <claude.ai OAuth access token>` and
`anthropic-version: 2023-06-01`; create, the event stream and interrupt
add `x-organization-uuid`. No `x-api-key`, no beta header. Sessions open at
`https://claude.ai/code/session_<id>` (`cse_<id>` is the same session).

| Method, path | Purpose | Shape |
| --- | --- | --- |
| `GET /v1/environment_providers` | environments | first `kind: anthropic_cloud` unless configured; `--environment` takes only self-hosted `ccpool_` ids |
| `POST /v1/code/sessions` | create | `title`, `environment_id`, `events` (optional `control_request`s such as `set_permission_mode`, then the user prompt), `config` (`sources: [{type: git_repository, url, revision}]` from the local `origin`, `model`, `outcomes`), `tags`, `metadata`; returns `{session: {id}}` |
| `GET /v1/code/sessions/{id}` | status | `status` (`paused`, `archived`, `failed`), `worker_status` (`running`, `idle`, `requires_action`), `connection_status`, `status_bucket` (`working`, `blocked`, `completed`, `failed`), `external_metadata.pending_action` (the prompt waiting for an answer), pushed branch in `config.outcomes[].git_info` |
| `GET /v1/code/sessions?limit&cursor&statuses` | list | `{data, next_cursor}`; no server-side tag filter |
| `GET /v1/code/sessions/watch` | change feed | SSE |
| `GET /v1/code/sessions/{id}/events`, `.../events/stream` | events | `{data: [{sequence_num, payload}], next_cursor}`; SSE frames `client_event` |
| `POST /v1/code/sessions/{id}/events` | follow-up, interrupt, permission answer | `{events: [{payload}]}`: a `user` message, `control_request` `interrupt`, or `control_response` `allow`/`deny` |
| `POST /v1/code/sessions/{id}/archive` | archive | also `unarchive`, `DELETE`, `PUT` (title, tags, `client_metadata`) |

It has moved once: the CLI still tries `POST /v1/sessions` (beta
`ccr-byoc-2025-07-29`) and repeats on `/v1/code/sessions` when that answers
400 `sessions_v1_retired`. `/v1/sessions?beta=true` with
`managed-agents-2026-04-01` is a different product, Managed Agents.

### Why not

- **Credentials Stage may not hold.** Cloud sessions take only a claude.ai
  login: API keys, Console sign-ins and `claude setup-token` tokens are
  refused (documented; seen: "A cloud session would run on your claude.ai
  login, but this session signs in another way…"). The session scope
  (`user:sessions:claude_code`) is capped at 30 days server-side
  (documented). The Claude Code legal page: "Developers may not collect,
  store, or intermediate Claude.ai credentials or session tokens"; the
  only carve-out is a user signed in to the unmodified `claude` binary.
  A connected harness would have to store, refresh and send the user's
  claude.ai token, which is exactly that. Stage stores no Claude
  credentials (decision 1).
- **No public API.** The docs describe no API to create, list, observe or
  stop Claude Code cloud sessions; `/v1/code/sessions` is internal and
  has already changed version.
- **No scripted create through the CLI.** `claude -p --cloud "<task>"` is
  rejected (documented): creating a cloud session is interactive only.
  `claude -p "<msg>" --cloud <session id>` only queues a follow-up.

### Routes left open (not chosen)

| Route | What works | Why not now |
| --- | --- | --- |
| Managed Agents (`/v1/agents`, `/v1/sessions`, `/v1/environments`, beta `managed-agents-2026-04-01`) | Public API with an API key, so it works from a container: sessions with GitHub repository resources, statuses `idle`/`running`/`rescheduling`/`terminated`, SSE events, `requires_action` (needs input), interrupt, archive, follow-ups | A different product: API billing, not the subscription; sessions do not show in claude.ai/code. A new connected kind, not `claude --cloud` |
| Routine fire (`POST /v1/claude_code/routines/{id}/fire`, per-routine token) | Starts a claude.ai session from a container; returns its id and URL | Experimental and launch-only: no status or reply, so Stage cannot observe the session |
| Self-hosted pool (`claude -p --environment ccpool_… --output-format json`) | Scripted create through the unmodified CLI and the user's own login; `--metadata`, `--correlation-id` | Team and Enterprise only; needs the binary (hosted, not a container); no documented way to observe the session |

Revisit when Anthropic publishes an API for Claude Code cloud sessions, or
allows third-party apps to act on claude.ai sessions, or when a Managed
Agents harness is wanted on its own merits.

## Consequences

- #371 (hosted plumbing, merged as ca5cfeb): unaffected.
- #372 (adapter): descriptor `launch: correlated` with an
  `execution.Correlator`, not reserved. Map states per decision 4, not the
  working/blocked/done/failed/stopped strings with `status` the brief
  assumed. Map the trust refusal to `Rejected`. Name `-w` worktrees per
  attempt. Attribution: `CLAUDE_CODE_SESSION_KIND=bg` and
  `CLAUDE_CODE_SESSION_ID` first, then T3. Parser tests use the fixtures.
- #373 (surfacing): the setup and launch copy explains trust ("Open
  Claude in this folder once and trust it").
- #375: choose among the approval routes above. The hooks route reuses
  `claudecode.Hooks` and its settings file: the `PermissionRequest` hook
  would wait for Stage's decision instead of answering 204.
- #377 (push): decision 16.
- #383 (discovery): `Attention` reads the roster (decision 9, amended); a
  live interactive session has no resume command, since it is answered
  where it runs.
- #382 (cloud sessions): no harness; findings and the routes left open
  are [above](#cloud-sessions-claude---cloud-not-built).
- #380 (usage): the observation carries `usage` from the transcript
  (decision 17); task detail, `stage task show` and `get_task` show it.

## Reproduce

Clear the inherited Claude and T3 variables (`env -u CLAUDECODE -u
CLAUDE_CODE_SESSION_ID …`), trust a scratch directory once with an
interactive `claude`, and run the probes above with `--model haiku`.
Clean up with `claude rm <short>`, then `claude purge -y <dir>` for each
directory: purging a parent deletes its children's transcripts but leaves
their `~/.claude.json` entries.
