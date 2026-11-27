# ADR 0012: Adding harness workspaces from Stage

Accepted on 2026-11-28. Planned in Stage task #481 (tree #208). The
contract ships in task #483. Later tree tasks add each harness's sources.

This ADR supersedes three earlier statements:

- the "no creating harness projects" clause of
  [ADR 0007 decision 2](0007-harness-boundary.md#decisions);
- the "Stage never asks a harness to create a project" sentence of
  [ADR 0007 decision 11](0007-harness-boundary.md#decisions);
- the "Stage never writes Codex's config" clause of
  [ADR 0006 decision 8](0006-codex-app-server-harness.md#decisions).

[ADR 0010](0010-harness-workspaces-and-logical-projects.md) still governs
the catalog, identity and grouping. Everything else in those ADRs stays.

## Context

[ADR 0010](0010-harness-workspaces-and-logical-projects.md) made each
harness connection the source of truth for its workspaces. Stage projects
became logical groups over them. A person who wanted new work in a folder
the harness did not list yet had to leave Stage, add the folder in the
harness, and then come back. Some harnesses let a client add a project
through their own API:

- T3 has project create and clone tools.
- Codex has `config/batchWrite` on its app server.
- OpenCode persists a location when it is asked to serve it.
- Claude Code documents a manual trust step,
  `projects[dir].hasTrustDialogAccepted`.

Future harnesses, Amp first, have several project types.

The earlier rule ("Stage never asks a harness to create a project") kept
harness state out of Stage core. That rule is still right for core. It
was too broad for an explicit request that the harness itself performs.

## Decision

1. **Relay, never create.**
   - Stage may relay an explicit add-workspace request from a person or an
     agent to a harness connection.
   - The adapter performs the request with the harness's own API, inside
     its adapter or setup package.
   - The harness validates the request, creates the workspace and owns the
     result.
   - Stage core never creates directories, runs git or edits harness
     files. Discovery stays read-only.
   - Nothing is automatic: no sync, import or launch adds a workspace.
2. **A declared operation with harness-declared sources.**
   - A harness declares `execution.OpWorkspaceAdd`.
   - It also declares `Capabilities.WorkspaceSources`: each source has a
     stable ID, a label, optional help, typed input fields, optional
     confirmation text and its availability.
   - Every adapter declares the operation; an unavailable one gives a
     reason.
   - Sources are the harness's vocabulary ([P3](0008-harness-foundation.md#p3)).
     A new project type, such as one of Amp's, is a new source. It needs no
     core change.
3. **One optional interface.**
   - `execution.WorkspaceAdder.AddWorkspace` receives the source, the
     validated input, a request ID and the confirmation.
   - It returns the workspace the harness will list (native ID and root),
     plus whether the target already existed, a message and facts.
   - It is idempotent per target: an existing target returns
     `Existing: true`.
   - Workers expose it as `adapter.workspaceAdd`, behind the
     `workspaceAdder` trait in the ready snapshot. The channel protocol
     version stays the same.
4. **Stage checks, then lists, then groups.**
   - `POST /api/connections/{id}/workspaces` checks admission, the
     operation, the source, the input against the source fields and the
     confirmation before anything is sent.
   - After the harness answers, Stage refreshes that connection's inventory
     through discovery and resolves the row by native ID and root.
   - The resolver groups the row as it groups any discovered workspace. An
     explicit `projectId` then applies the same membership edit as
     `set_membership`.
   - A row the harness does not list is an error that names the harness.
     Stage never fabricates a row.
5. **People and agents.** Both may add workspaces, under
   [ADR 0011](0011-agent-operation-parity.md) parity and the existing
   admission rules. Agent responses use the ADR 0010 projection without
   raw paths.
6. **Retries, not a Stage idempotency table.**
   - The caller supplies a required `requestId`, and the adapter receives it
     for reconciliation.
   - An outcome the harness did not confirm (a lost worker reply, a timeout,
     or the adapter's `OutcomeUncertain`) answers 504 `outcome_uncertain`.
     Retrying with the same `requestId` is safe, because adapters are
     idempotent per target.
7. **Audit.** Each successful add records a `workspace_added` project event
   on the workspace's group. The event carries the admitted actor and a
   `workspace_add` cause with the request ID and caller session. Its text
   names the connection and source.
8. **Claude Code's trust step is the one file-write exception.**
   - Claude Code documents trusting a folder as setting
     `projects[dir].hasTrustDialogAccepted` in its state file. It offers no
     API for it.
   - Its adapter may write that one documented key, atomically, for one
     folder at a time.
   - The write happens only behind the source's explicit confirmation,
     which the caller must send per request.
   - No other harness file is edited.

   Codex trust goes through its app server's `config/batchWrite`, never a
   file edit.

## Consequences

- A person or agent can add a workspace without leaving Stage on harnesses
  whose API supports it. Other harnesses keep their reason.
- Each harness's support lands in its own adapter task. At acceptance every
  harness declares the operation unavailable with "Adding workspaces from
  Stage is not supported for <name> yet."
- An agent can now change harness state (a trusted folder, a cloned
  repository). The confirmation text, the project event and the harness's
  own validation keep the change visible and bounded.
- Uncertain outcomes push reconciliation to the caller's retry. A harness
  that cannot detect an existing target must not declare a source.
- Tests never touch real harness state. Adapter tests use fakes and
  throwaway homes.
