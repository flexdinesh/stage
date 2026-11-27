# ADR 0010: Harness workspaces and logical projects

Accepted on 2026-11-27 in Stage task #461 (acceptance #444). Stage tree #207
delivers this decision. This ADR supersedes the project ownership and
routing rules of [ADR 0007](0007-harness-boundary.md) and
[ADR 0008 D9](0008-harness-foundation.md#d9) and [D10](0008-harness-foundation.md#d10).
It extends [ADR 0009](0009-shared-access.md) using the shared-access router
merged through `09609a6c`. The delivery table distinguishes the target
contract from the binding behavior still shipped when this ADR lands.

## Ownership and exact destinations

Each harness connection owns its workspace catalog, session roster and
native IDs. The adapter reports what exists and what its policies allow.
Stage owns installation-wide logical project groups and durable records of
tasks, trees, schedules, journals, acceptance and handoff. It can retain
catalog observations and read-only evidence. A group organizes navigation,
history, filters and snippets. Launch authority comes from the selected
destination and the admitted actor.

A destination is the pair `{connectionId, workspaceId}`. Here
`workspaceId` means a stable Stage selector for one catalog row within that
connection. The row retains the native workspace ID and the exact location
or other adapter-provided discriminator needed to launch. One native ID
may have several roots. When no path is needed, the adapter must be able to
identify the one workspace by its native key. Connection identity
distinguishes two instances of the same harness, including two on different
machines. Group IDs, labels, remote keys, bare paths and session IDs cannot
stand in for a destination. A replacement connection gets its own
selectors. A location change preserves the pin only if the owning adapter
can still verify the same exact workspace identity. Otherwise the old
selection is unresolved.

Creation, edit, duplicate, tree-member creation and schedule setup use a
specific connection followed by an exact workspace on it. A shared
default connection, project link or single eligible row can prefill a
visible choice. Submission pins the visible pair. Switching connections
clears the incompatible workspace and scoped options. A caller can choose
an eligible row that has never been bound or grouped. Capabilities and launch
options use that same scope. Schedules pin the template destination and
each fire uses that pair. Where a caller already supports planning-only work,
it can save an unresolved draft or backlog item. Start, automatic release
and schedule fire require a valid pin. Return a repair reason and ask for an
explicit selection when the pin is missing, ambiguous, retired or no longer
identifies the workspace. There is no fallback to another member of the group
or to a default.

At launch claim, revalidate the exact workspace and launch eligibility on
its owning connection before dispatch. Freeze the destination and launch
intent with the attempt. Admission, retries, idempotency and uncertain
outcome reconciliation keep the existing fences. Changing a future
destination never edits a frozen attempt or rehomes a linked session.

## Inventory, availability and visibility

Inventory includes meaningful reported folders, nested workspaces and
checkout roots regardless of Git evidence, grouping or legacy import
eligibility. Adapter trust, archive, deletion and launch restrictions still
apply. On connect and refresh, read inventory through the owning adapter.
Use `machine.Ops` on the verified file host for optional read-only evidence.
On a worker this uses the negotiated channel. A complete empty result is
authoritative. Failed, absent or known partial sources retain the last good
data and report freshness and the failure to humans. Distinguish discovery
in progress, empty inventory, unavailable connection and stale selection.
Historical visibility and cached rows provide no current launch eligibility.

An offline workspace remains visible with last-known status to people who
can view it. An existing pin can wait for the same connection and exact
workspace to return. A successful refresh and eligibility check can make it
usable again. Retirement, removal or replacement keeps history readable and
removes the old row from eligible choices. Candidate replacements may be
shown for repair. Even a unique replacement or identical path on another
machine requires explicit reselection. If the person has no eligible
destination, supported planning work can remain unresolved.

## Group membership and history

Use the existing [read-only evidence](../discovery.md#workspace-evidence-07)
for grouping. Attach a newly discovered row when a strong key points to
exactly one compatible group. Otherwise give it a separate group. Strong
evidence is a normalized
origin with a known checkout subpath, including `.` for a proven root, or
an attested same-file-host Git common directory and known subpath. A plain
directory can match by canonical path on its attested file host. A fork with
a different origin stays distinct. Workspace records without strong evidence
can stand alone. Matching labels, bare paths across hosts, unknown subpaths
and ambiguous moves cannot justify collapsing rows. Multiple candidate
groups stay separate until a person chooses. Existing groups with duplicate
evidence can be surfaced as suggestions.

Members can group and separate exact workspace rows. Preserve explicit
choices, including separation, against later automatic grouping. Apply edits
atomically and recheck the membership on confirmation. If a competing edit
changes it, require a fresh preview. Both users then see the resulting
installation-wide membership. Group changes affect where workspaces appear
in navigation and future selections. Tasks, schedules, session links,
destination pins, attempt records and recorded attribution retain their
original references.

Keep old project IDs resolvable, including existing aliases. A consolidated
group can expose an old URL as an alias, with the original project history
and features still addressable. Separation can restore a prior group or
create another for current workspace membership. It leaves recorded project
IDs and past work in place. Keep project-scoped snippets, their IDs and scopes,
and archived work reachable from their original group or through its old
link. Filters can use current membership for navigation and recorded project
IDs for historical results. Empty and retired-only groups remain addressable
for history and snippets, with no current launch choices. Stage group archive
controls Stage visibility. Harness workspace or session archive comes from
the adapter and controls that workspace or session's eligibility independently.

## Session selection and tracking

Browse only metadata from the selected connection's current harness roster
through an optional, declared capability. Scope IDs and cursors to the
connection. Provide bounded pagination, predictable ordering and title,
workspace and ID search where the adapter can support them. Include enough
metadata to distinguish matches, plus tracked state and eligibility.
Include eligible completed sessions still listed. Historical Claude
transcript-only sessions are outside search. A failed call, unavailable
capability or older worker has an explicit reason and keeps direct link or
ID entry available. Keep roster browsing independent of Attention, Observe,
the watch set and task creation. It transfers no transcript bodies and
creates no notifications or read receipts.

Select, preview and explicitly confirm through the existing adoption path.
Keep direct reference lookup, named session context and named session
adoption. Session identity stays `{connectionId, externalSessionId}`.
Revalidate existing top-level, unarchived, wrapper and adapter adoption
rules on confirmation. A session's workspace can be identified for
historical tracking independently of launch eligibility. Resolving it and
recording its provenance requires neither a manual Stage bind nor a launch
pin. Where the existing contract requires workspace verification, keep
that verification and report unavailable or ambiguous evidence. An already
tracked session opens its task. Concurrent adoption converges on one task
and attributes its creation once. Tree membership remains an explicit action.

## Access and attribution

A logical group belongs to the installation. In `personal`, the implicit
operator (user 1) uses the human product surface under the existing
listener policy. `setup_pending` closes every product surface below to
people and agents and pauses work-producing runners. An owner claim retains
user 1, project membership and historical IDs. In `claimed`, admission uses
the authenticated principal on each call, including each page and the final
mutation. Active members and admins share the same groups and can both edit
membership. The table uses the names in the
[shared-access router](../../server/internal/api/router.go).

| Route or operation | Claimed human | Claimed scoped agent |
| --- | --- | --- |
| `GET /api/projects`, `GET /api/projects/{id}`, and the minimal destination selection summary | Active member or admin | Allowed under `agentRoute` and existing transport scope |
| `GET /api/connections/{connectionId}/workspaces` and inventory refresh/resolve | Active member or admin through `humanRoute` (including existing `POST /api/sync` and `POST /api/connections/{connectionId}/resolve`) | Denied |
| `POST /api/projects`, `PATCH /api/projects/{id}` (including archive), and group/separate (legacy `POST /api/projects/{id}/merge` and `/split`) | Active member or admin through `humanRoute` | Denied |
| Session collection browse/search on a selected connection (new) | Active member or admin through `humanRoute` | Denied, including from MCP and worker agent gateways |
| `GET /api/refs/resolve`, `GET /api/connections/{connectionId}/sessions/{id}/context`, `POST /api/connections/{connectionId}/sessions/{id}/adopt` | Active member or admin | Existing `agentRoute` scope and eligibility retained for a named reference |

The destination summary returns exact selectors for selectable rows,
including unbound rows, within the caller's existing connection scope.
Provide workspace display labels, connection and machine display labels,
and a concise discriminator where labels repeat. Associate a group ID when
one exists and indicate whether the choice is usable. For a saved selection
that needs repair, return its scoped reference and actionable availability
reason. Agents submit the exact pair via the existing task or schedule
operation. The projection omits raw paths and native catalog payloads,
filesystem evidence, discovery failures and session collections. The server
resolves private routing fields internally and rechecks eligibility.
Shared group reads on agent surfaces use that projection.

In claimed mode, signed-out, expired or revoked credentials fail admission.
An admitted agent can use its existing scoped bearer, private agent socket
or verified worker gateway within that transport's scope. A signed-in
client calling MCP gets agent operation limits. Human search through adapter
RPC on an owning worker remains one authorized human request. A worker
gateway carries an agent request with machine provenance. Apply the router's
admission and gateway checks before inventory or roster work.

Creator, caller session and source machine are provenance. Keep actor,
initiator, credential account where applicable, request ID and cause as
recorded, including on delayed work and retries. Automatic discovery records
internal automation. An automatic schedule fire records cron, and Run now
records the requester. The spawned task retains the schedule creator.
Retain disabled-account IDs and immutable journal and acceptance evidence.
Account or desktop server switches discard pending selections, previews and
late responses. Identical numeric IDs across installations carry no authority.

## Worked example

One Stage server serves the workstation **Desk** and the worker **Lab**.
Both have folders named `atlas`. The first four rows report
`git.example/acme/atlas` with proven subpath `.`; the last reports
`git.example/lab/atlas` with proven subpath `.`. Each pair below is an
independent destination. Illustrative selector names omit adapter internals.

| Machine | Harness | Destination | Initial project membership |
| --- | --- | --- | --- |
| Desk | T3 | `(10, t3-atlas)` | #40 Atlas, using T3's reported origin and subpath |
| Desk | Codex | `(11, codex-atlas)` | #40 Atlas, using the trusted directory's local evidence |
| Lab | OpenCode | `(12, oc-atlas)` | #40 Atlas, using attested local-auth checkout evidence |
| Lab | Claude Code | `(13, acme-root)` | #40 Atlas, using local checkout evidence |
| Lab | Claude Code | `(13, lab-root)` | #41 Atlas, a different origin |

1. **Create and launch.** The personal operator chooses connection 13 and
   `acme-root` for task #700, which launches on Lab in the Acme checkout.
   A schedule pinned to `(13, lab-root)` always uses Lab's other checkout.
   A backlog item without a destination saves for planning where supported
   and requires a selection before start. Without a recorded legacy
   destination, an older caller passing only #40 has four possible rows.
   It gets repair guidance and starts nothing. A legacy binding that
   uniquely identifies `(13, lab-root)` can be translated to that exact pin.
2. **Claim and regroup.** During `setup_pending`, humans and agents cannot
   list, regroup, browse or launch. Claim preserves #700 and user 1. After
   claim, ordinary members Ada and Bea sign into the same server on separate
   accounts. Ada moves `(12, oc-atlas)` into #41. Bea opens the same group
   and sees Ada's edit. Bea's stale confirmation must refresh, after which
   she can move the row back to #40. The two launch pins, #700's author,
   past project IDs and project snippets remain as recorded. If Bea
   archives #41 in Stage, its old URL and snippets still open. The Lab
   workspace's eligibility continues to follow Claude's policy.
3. **Lose the connection.** Lab disconnects. Stage shows last-known rows
   and can still open #700 and project #40. The unlaunched Lab schedule
   cannot fire into another checkout. A return of 13 with the exact rows
   lets the existing pins work once verified. If 13 is retired and replaced
   by 19, even identical Claude roots and evidence leave the schedule
   unresolved. Bea chooses `(19, lab-root)` explicitly for future fires.
   #700 still links to its session on 13. The Stage task and acceptance
   remain readable when the harness link is unreachable.
4. **Browse and track.** Ada can search 19's live roster for a listed
   completed session and preview adoption. If Bea confirms the same
   eligible session first, Ada opens the resulting task attributed to Bea.
   A transcript found only in historical Claude files never appears in
   the list. An admitted agent can still submit a direct named reference
   and receive the existing lookup or adoption outcome. Switching from
   Ada's account to Bea's while the picker is open requires a fresh browse.

## Delivery and compatibility

Use additive reads and dual-engine, roll-forward migrations before removing
legacy inputs. Preserve existing explicit pins where they identify exactly
one row. Backfill a legacy default or primary only when the recorded
connection and workspace resolve uniquely to an eligible exact row under
the legacy selection rules. Leave ambiguous or missing candidates unresolved
with a repair reason. During transition, accept legacy HTTP, CLI and MCP
selectors only when they can be translated unambiguously under those rules,
and persist the resulting exact pin before launch. A project ID can still
select historical context or support an existing planning-only caller.
Never infer a destination from new group membership. Keep frozen attempt
payloads and original historical IDs readable on upgrade and supported
database copy. Allocate paired SQLite and Postgres migrations after the
latest merged version. [Storage contract](../storage.md)

Each row is one independently mergeable PR to `main`. Later consumers use
the additive contracts from their prerequisites.

| Stage task | Change | What keeps main usable |
| --- | --- | --- |
| #462 | Record the contract and update ownership and authoring guidance. | Shipped binding and tracking behavior stays in place. |
| #463 | Add independent inventory and the scoped destination projection. | Existing binding clients continue to work. New reads include unbound choices. |
| #464 | Pin exact destinations through requests, schedules, claims and retries. | Migrate provable legacy selections, retain compatible request forms and frozen attempts, and report repair for unresolved work. |
| #465 | Add the logical group read model and reversible membership changes. | Preserve legacy URLs and writes during client migration. Regrouping cannot reroute work. |
| #466 | Add optional human roster search with a Claude adapter and worker negotiation. | Direct reference lookup and adoption remain available for unsupported adapters and older workers. |
| #467 | Add supported T3, Codex and OpenCode search and document coverage. | Each adapter declares its own availability. Direct references and Attention continue to work. |
| #468 | Switch task, tree-member and schedule forms to connection-first selection. | Existing planning-only forms still save. Earlier HTTP, CLI and MCP callers have the scoped compatibility path. |
| #469 | Switch Projects navigation to groups and shared membership edits. | Old links, snippets, filters and archived and retired history remain reachable. |
| #470 | Add search, preview and confirmation to Track session. | Explicit link/ID remains available. Existing tracked items and adoption guards are preserved. |
| #471 | Retire obsolete binding and primary-routing routes and inputs after consumers migrate. | Preserve historical links and saved work. Return actionable guidance for retired selectors. Verify fresh and upgraded SQLite and Postgres, local and worker paths, all four adapters and personal-to-claimed access. |
