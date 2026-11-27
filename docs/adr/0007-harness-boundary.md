# ADR 0007: The Stage and harness boundary

- **Status:** accepted (2026-11-26), Stage task #420. Supersedes the
  parts of [ADR 0005](0005-claude-code-harness.md) and
  [ADR 0006](0006-codex-app-server-harness.md) named below. Earlier
  harness decisions are provisional: where one conflicts with this ADR,
  this ADR wins and the change revises it.
- **Delivery:** one stack of PRs (`harness/01` to `harness/09`); each
  layer updates the contract docs it changes.

## Context

Stage grew four harnesses (T3 Code, OpenCode, Claude Code, Codex). The
rules in [execution.md](../execution.md#ownership-stage-and-the-harness) already said the
harness keeps what it owns and product code reads descriptors and
capabilities, never a harness name. The code drifted:

- Stage owned worktrees: a `workspace` on every task and schedule, a
  `task_launch_bases` table, predecessor branches resolved by observing
  another task's session, git calls in the Claude adapter, worktree
  pickers in the UI and worktree parameters in MCP and the CLI.
- T3 was the implicit default: a seeded connection 1, `IsT3` and
  `LegacyT3ID` in storage, API, sync and notify, legacy T3 model inputs, a
  T3-only `threads` mirror, `/api/catalog` and
  `/api/connections/t3/projects`.
- Image support was decided by "is this T3" in the HTTP API while MCP
  read the capability, and Claude images were written into the user's
  repository.
- Adapters knew about each other (Claude and Codex refused T3's sessions
  by name; attribution yielded to `T3CODE_THREAD_ID` by name).
- The UI kept three parallel per-kind registries and kind checks.

## Decisions

[ADR 0010](0010-harness-workspaces-and-logical-projects.md) supersedes
the project ownership, identity, binding and launch-routing portions of
these decisions. Stage projects become logical groups. Each harness
connection owns its catalog and supplies exact workspace destinations.

1. **What Stage is.** Stage is an MCP server, a CLI and an HTTP API with
   a UI and a database. It owns projects, tasks, trees, schedules,
   journals, acceptance and handoff. It launches a task into a harness
   connection, observes the task's session until it ends, and lets agents
   report back (journal notes, task and tree tools). Nothing else.
2. **What a harness owns.** Sessions and everything in them: workspaces,
   worktrees, branches, archive, harness projects, models and what their
   options mean, permission policy, and its own client. Core code (outside
   an adapter and its setup) never does harness work: no git for sessions,
   no worktree naming, no creating, renaming or archiving harness projects.

   [ADR 0012](0012-harness-workspace-add.md) supersedes "no creating" for
   an explicit add-workspace request that the adapter performs with the
   harness's own API. Core still creates nothing itself.
3. **The Stage-level contract every harness meets.** Launch (with
   durable checkpoints), observe (poll, push, or a worker beside the
   harness), stop, the final reply, workspace discovery for project
   binding, and caller attribution for journal provenance. A harness that
   cannot meet one of these is not a harness Stage supports.

   [ADR 0008 P2](0008-harness-foundation.md#p2) and [D7](0008-harness-foundation.md#d7)
   refine the operation requirement for the target contract. Layer 06
   gates Stop, Track and Start on declared availability.
4. **Extras are declared, never implied.** A harness's extra ability is
   one of: a descriptor policy, a typed operation in `Capabilities`, a
   typed launch option, or an optional interface reached through
   `execution.As`. Product code, MCP, the CLI and the UI read only those.
   `harness/boundary_test.go` (allowlist empty) and the UI boundary test
   (no harness-kind literals outside `packages/app/src/harnesses/`) fail
   otherwise. A harness without an extra never limits the others.
5. **Tasks are harness-specific through options.** A task names its
   connection; its `executionConfig` is opaque to Stage and validated by
   the adapter against its declared capabilities. Launch options are typed
   (`choice`, `boolean`, `text`, with `dependsOn`). Worktree, base ref and
   branch are launch options a harness may declare (T3: worktree, baseRef,
   branch; Claude Code and OpenCode: worktree; Codex: none). An adapter
   may translate options into harness calls (`claude -w`, OpenCode's
   worktree endpoint); core code computes nothing from them.
6. **Predecessor bases are removed.** Stage no longer starts a dependent
   task from its predecessor's branch. Handoff carries the branch and
   directory the harness reported; the agent and the harness decide.
   Migration translates saved workspaces into options and notes, on each
   task that used a predecessor base, that it now starts from the
   harness default.
7. **States.** The seven Stage states stay fixed (running, needs_input,
   done, failed, interrupted, cancelled, idle). Harness detail goes in
   `stateReason`; no harness-specific states.
8. **Observing a harness that cannot push.** The adapter polls it where
   the harness runs: in the server for the local machine, in `stage
   worker` for another machine. Stage builds no separate polling daemon.
   External bridges and hooks may also push observations for an existing
   connection to `POST /api/connections/{id}/observations`, through the
   same validation and ingest path as `execution.EventSource`; polling
   still reconciles.

   [ADR 0008 D11](0008-harness-foundation.md#d11) supersedes the polling
   placement wording. Layer 10 runs an observer beside the adapter,
   including inside the worker. The no-daemon rule and bridge ingest
   still apply.
9. **Interaction is an optional capability.** Answering approvals and
   questions in Stage (`execution.Interactive`, operations `respond` and
   `send`) stays where a harness offers it. It is the pattern for
   harness extras: declared, gated, absent elsewhere.
10. **Images.** `images` is an operation; the API, MCP and CLI gate on it
    alike. Stage keeps image bytes in its store; an adapter that needs
    files writes them under Stage's data dir, never into the user's
    repository.
11. **Projects stay a Stage primitive.** Harnesses discover workspaces;
    bindings attach them to Stage projects with stable Stage ids.
    Deduplication across harnesses is best effort, one heuristic ladder,
    in one place:
    1. an existing binding (connection plus the harness's project id or
       path) is stable, and the user's bind, unbind or decline sticks;
    2. on one machine, the same canonical path, or the same Git common
       directory plus subproject, is the same project;
    3. across machines, the same normalized origin remote plus subproject
       joins only when exactly one project matches, else it suggests;
    4. otherwise per-harness fallback: a path on its own machine only; no
       remote, no merge across machines; a harness with no paths gets
       per-connection bindings only.
    Adapters report harness facts only (external id, name, path, and a
    remote when the harness itself knows one). Git evidence is computed by
    `machine.Ops` on the machine where the files live, never in adapters.
    Stage never asks a harness to create a project (`registerWorkspace` is
    removed). *Superseded by [ADR 0012](0012-harness-workspace-add.md):
    Stage may relay an explicit request through the declared
    `workspaceAdd` operation; nothing is automatic.*

    [ADR 0008 D9](0008-harness-foundation.md#d9) and
    [D10](0008-harness-foundation.md#d10) supersede this identity ladder
    with persisted workspace evidence, identity keys and one resolver
    in layer 07. The Stage and harness ownership rules above still apply.
12. **No default harness.** No connection is seeded; `connectionId` is
    required unless a project binding or the default connection picks one.
    An omitted `executionConfig` means the harness defaults; an update
    that changes the connection must send one. Legacy T3
    model inputs, `/api/catalog`, `/api/connections/t3/projects` and the
    `threads` mirror are removed (breaking).
13. **Wrapping harnesses.** A harness that runs other harnesses' sessions
    (T3 runs Claude Code and Codex) declares it in its descriptor
    (`Wraps`: the session markers and caller variable it leaves). The
    coordinator refuses to track a session carrying another registered
    harness's marker, and the wrapper's caller variable wins attribution.
    No adapter names another harness.
14. **Harness processes stay the harness's.** Stage may start a harness
    service it needs (the Codex app-server daemon) and never stops it: it
    serves the harness's own clients (Codex Desktop, `codex resume`) and
    outlives Stage. Codex is offered on workers where the machine profile
    allows local harnesses, like Claude Code.

## Superseded

- ADR 0005 decision 10 (predecessor bases) and the worktree naming in its
  launch decision: Claude Code worktrees are the `worktree` launch option;
  the adapter still names `-w` worktrees per attempt, because `-w` reuses
  an existing name silently.
- ADR 0005's image placement (`<repo>/.claude/stage-images`): images live
  under Stage's data dir; the adapter removes the directories it created
  in repositories.
- ADR 0005 and 0006 refusing T3's sessions by name, and attribution
  yielding to `T3CODE_THREAD_ID` by name: decision 13.
- ADR 0006 decision 10's "Codex is unavailable on a worker": decision 14.
  Its decision 2 (never stop the daemon) stands and is generalized.

## Consequences

- Removing worktree and T3 legacy inputs breaks API, MCP and CLI callers
  that send them; each gets a clear error naming the replacement.
- Stacked work loses the automatic predecessor branch; the handoff
  carries what the harness reported.
- New harness work starts from the contract in decision 3 and declares
  each extra; a reviewer rejects a core branch on a harness name.
