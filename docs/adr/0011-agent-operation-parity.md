# ADR 0011: Agent operation parity

Accepted on 2026-11-27. This ADR supersedes the agent limits of
[ADR 0009](0009-shared-access.md#principals-and-endpoint-policy) and the
scoped-agent column of [ADR 0010](0010-harness-workspaces-and-logical-projects.md#access-and-attribution)
for the operations below. Everything else in both stays.
[Current agent interface](../api.md#agent-interface)

## Context

Agents drive Stage through MCP, the CLI and agent HTTP credentials. They
could plan, launch and report work, but not finish it: acceptance, session
answers and follow-ups, project lifecycle, group membership and snippets
were human routes. An orchestrating agent had to stop and ask the user for
each step, even when the user had asked it to run the whole tree.

MCP should cover most product operations. The primitives already exist and
keep their contracts: [acceptance](../api.md#user-acceptance-api-v4),
[session interaction](../api.md#session-interaction-api-v6),
[opening prompt](../api.md#get-apitasksidopening-prompt),
[projects](../api.md#projects), [group membership](../api.md#logical-project-groups),
[snippets](../api.md#snippets) and [images](../api.md#images). This
decision changes who may call them, not what they do.

## Decision

Agents (MCP, CLI, an agent HTTP credential, a worker agent gateway) may:

| Area | Routes | MCP tools | CLI |
| --- | --- | --- | --- |
| Acceptance | `GET /api/tasks/{id}/completion-preview`, `POST /api/tasks/{id}/complete`, `POST /api/tasks/{id}/reopen` | `get_completion_preview`, `complete_task`, `reopen_task` | `stage task preview\|complete\|reopen` |
| Opening prompt | `GET /api/tasks/{id}/opening-prompt` | `get_opening_prompt` | `stage task opening-prompt` |
| Session interaction | `GET /api/tasks/{id}/requests`, `POST /api/tasks/{id}/requests/{rid}`, `POST /api/tasks/{id}/messages` | `list_requests`, `respond_request`, `send_message` | `stage task requests\|respond\|send` |
| Projects | `POST /api/projects`, `PATCH /api/projects/{id}`, `GET /api/projects/{id}/events` | `create_project`, `update_project`, `get_project_events` | `stage projects add\|edit` |
| Group membership | `POST /api/project-groups/membership` | `preview_membership`, `set_membership` | `stage call` |
| Workspace add ([ADR 0012](0012-harness-workspace-add.md)) | `GET /api/connections/{id}/capabilities` (`workspaceSources`), `POST /api/connections/{id}/workspaces` | `list_workspace_sources`, `add_workspace` | `stage workspaces sources\|add` |
| Snippets and attachments | `/api/snippets` CRUD, `DELETE /api/attachments/{id}` | `list_snippets`, `get_snippet`, `create_snippet`, `update_snippet`, `delete_snippet`, `delete_attachment` | `stage call` |

Agent completion runs the same transaction as a person's: it saves the
evidence, updates the tree and releases dependents, which launch in auto
trees.

### Never on its own work

A request whose caller session (`Stage-Caller-Session`, which the CLI and
MCP tools send) owns the target task gets 403 `self_action_forbidden` on
complete, reopen, respond, send and declared session actions (closing a
session in its harness can infer completion). An agent reports its own outcome with a
journal note; the user or another agent completes it. The guard applies to
any principal that names a caller. It is best effort: a request naming no
caller session passes, and a caller session no task owns passes.

### Two-step acceptance

`complete_task` takes the `revision` and `evidenceVersion` from
`get_completion_preview`, as the app does. A changed task or evidence is
409; the tool tells the agent to preview again. `reopen_task` takes the
revision. `requestId` is required and replays exactly. The CLI derives a
request ID from the task and revision when omitted.

### Attribution

The acceptance and its journal event record actor kind `agent`, the caller
session in `cause.session`, the request ID and the forwarding machine. The
event reads "Completed by agent" or "Reopened by agent". Acceptance
provenance stays `user` (the complete action); the actor tells person from
agent. Agent project and membership responses use the summary projection
without raw workspace paths, including the fresh preview in a 409
`preview_changed`.

### Still human

| Operation | Why |
| --- | --- |
| `POST /api/sync`, raw inventory | Reads harness catalogs and paths beyond the destination projection. |
| Connection writes: auto-complete, new-task config, forget | Policy that governs every agent's launches and inferred completion. |
| Harness management (settings, setup actions, disconnect), machine and pairing operations | Credentials and execution placement. |
| Preferences writes | Per-person defaults and delivery. |
| Session roster search | Broad harness metadata ([ADR 0010](0010-harness-workspaces-and-logical-projects.md#session-selection-and-tracking)). |
| Launch-link | Attaches an unconfirmed launch to a session a person identified. |
| Account, admin, inbox, push and device bindings | Belong to a person ([ADR 0009](0009-shared-access.md#principals-and-endpoint-policy)). |

User-only declared actions keep their caller policy. An action an agent
may run can still require the confirmation a person gives: MCP
`run_action` relays it as `confirmed` (Claude Code's `trust`, [ADR
0012](0012-harness-workspace-add.md) decision 8).

## Consequences and risks

- An agent can release dependents with no person in the loop. Auto trees
  then launch work on the agent's word. The guide tells agents to leave
  acceptance to the user unless asked. Reopen and the journal event make it
  visible and reversible before a claim.
- One agent can approve another session's permission prompt or answer its
  question. Cross-agent approval is possible by design; the harness's own
  approval policy still applies to what the session may run.
- The self-action guard needs a caller session. A bare HTTP request, or a
  caller the server cannot resolve to a task, is not refused. Treat it as a
  guard against mistakes, not an isolation boundary.
- [ADR 0009](0009-shared-access.md#principals-and-endpoint-policy) requires
  one policy across transports, so the shared agent credential gains the
  same powers over REST as over MCP.
- MCP now lists 67 tools. Each registered MCP server loads every schema
  into context; the CLI stays the default for agents with a shell.
