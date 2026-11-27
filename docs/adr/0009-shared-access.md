# ADR 0009: Shared access

Stage task #452 establishes the identity and storage foundation. This ADR also specifies the target contracts for tasks #453–460. Task #460 enables public opt-in after integration.

[ADR 0011](0011-agent-operation-parity.md) supersedes the agent limits here for acceptance, session interaction, project lifecycle, group membership, snippets and attachment deletion.

## Installation and trust

Personal is the default for desktop, foreground servers and trusted home LANs with workers. Existing bind defaults and desktop's private-socket startup apply. A reachable personal product listener grants implicit operator access as user 1. Keep personal listeners within the intended trust boundary.

Shared access is per installation. There is one trusted workspace across projects, tasks, trees, schedules, machines and harnesses. Active members can operate shared work, pair and manage workers, and configure harness connections. Admins also manage invitations, roles and account access. Members can run code wherever configured harnesses allow. The installation owner's OS account protects Stage secrets and local recovery. Use separate OS accounts or hosts when stronger execution isolation is needed. Stage and harness ownership follows [ADR 0007](0007-harness-boundary.md) and [ADR 0008](0008-harness-foundation.md).

| Persisted state | Startup and transition |
| --- | --- |
| `personal` | Use implicit admin user 1 without a password. Explicit shared opt-in persists `setup_pending` before product traffic. |
| `setup_pending` | Allow the owner to redeem a valid setup grant. Claim atomically consumes the grant, updates user 1 in place, creates its local identity and verifier, and persists `claimed`. Concurrent claims have one winner. |
| `claimed` | Require sign-in for human work. User 1 remains the same account and the installation has a single first-owner claim. |

An omitted setting preserves the persisted shared state. Explicit false against shared data fails startup and directs the operator to use shared configuration. Mode can only advance through these transitions. The shared runtime serves health, account setup and worker liveness during setup. Product and gateway work remain closed. Work-producing runners resume after claim with an internal system principal. Existing launch fences prevent duplicates, and recurring fires due before the claim are recorded as missed. Operators opt in with `STAGE_MULTI_USER=1` or `stage serve --multi-user`. See [runtime configuration](../runtime.md#one-process-many-listeners).

The first opt-in prints one 30-minute setup URL to the operator's foreground terminal, or writes it to the owner-only `owner/bootstrap.txt` under the data directory for managed, container and redirected runs. Pending restart directs the owner to run `stage owner reissue-setup`. Reissue revokes the prior grant. `stage owner recover-admin --email ADDRESS` issues a reset link for an active admin. Both commands use `owner/control.sock` under the protected data directory. Run them as the server's OS user with the same `STAGE_DATA_DIR`, including inside a TCP-only container. Ordinary product socket access, remote requests and machine tokens confer no owner capability. Keep grants, passwords and session secrets out of normal logs, service status, diagnostics and error reporting. See [runtime](../runtime.md) for listener and output configuration.

Use `STAGE_PUBLIC_URL` as the canonical origin for links and exact browser origin checks. Shared browser access requires HTTPS except for loopback HTTP testing with a matching Stage TCP listener. A browser-reachable HTTPS proxy forwards the configured Host to loopback TCP or a protected Unix socket. Shared startup rejects wildcard and non-loopback TCP binds. Forwarded headers carry no authority.

## Durable identity and disposable credentials

- `users.id` is the stable person ID. Preserve row 1, existing foreign keys, `createdBy` and prior evidence on claim and upgrade. Store display name, email presentation, `admin` or `member`, and `active` or `disabled`. The login address is unique after trimming and case-insensitive canonicalization. Dots and plus suffixes are significant. Invite possession does not verify the address.
- The local password verifier belongs to a local `identities(provider, subject)` entry with subject `user:<id>`, independent of email. Future external identities use a provider namespace that includes the issuer, plus the immutable subject, and require explicit linking. V1 ships email/password only, with manual invite delivery. OAuth, SMTP, public registration, organizations and project ACLs are outside v1.
- Generate and persist `installation_id` at first runtime startup. It identifies a logical installation across restarts, backup and supported database copies. The process `instanceId` continues to identify one running process. CLI, desktop and configured agent clients scope credentials to both the installation ID and the canonical target (scheme, host and port, or exact socket). Send credentials only to that target and refuse cross-origin credential forwarding on redirects.
- Store opaque random 256-bit sessions and grants by digest. Sessions have `browser`, `cli` or `agent` audiences. Grants have `setup`, `invite` or `password_reset` purpose, expiry, issuer and consumption/revocation state. Validate purpose, target and expiry when redeeming in a transaction. Put grant secrets in URL fragments, remove them promptly from the address bar, and redeem only on explicit POST. GET may display a form without consuming a grant.
- Use salted, versioned Argon2id verifiers with bounded concurrency. Accept 15–128 Unicode characters, paste and password managers. Check a local common-password blocklist and bound account/IP login attempts. Use generic sign-in errors. Browser sessions expire after seven days idle or 30 days absolute. Agent credentials expire after 30 days and support explicit rotation. Check credential validity, current account status and role on every request. End streams on expiry or revocation. Logout revokes the active session. Password change, reset and disable revoke the affected account's login and agent credentials.
- Backups are secret material. Preserve user IDs, identity mappings, verifiers, roles, account status and sticky mode, plus the installation ID for the same logical installation. Explicit restore/import invalidates copied browser, CLI and agent sessions and outstanding setup, invite and reset links before product access. A restored pending installation uses local owner reissue. Ordinary restarts retain valid credentials. Preserve worker identity and pairing under the existing [storage](../storage.md) and [runtime](../runtime.md) contracts.

The foundation exposes `db.Store.ConfigureAccess`, `ClaimInstallation`,
`ReadAccess` and `WriteAccess`. Use the installation row lock in
read-check-write account operations; SQLite serializes writers and
Postgres locks the row. Account lookup uses a Unicode case-folded login key.
Reissue revokes the outstanding row before inserting a replacement, including
when its deadline has passed. Browser idle extension is monotonic and capped
by absolute expiry.

The `internal/auth` service is the application boundary for #454. Authenticated
callers supply the current human account ID to account and admin operations.
The service rechecks account status and role when it acts. Setup and invite
redemption return a browser session after commit. Reset and password change
revoke every session for the account and return the user to sign-in. Role
changes revoke outstanding reset links. Reset replacement and redemption check
the target's current role, with member links issued by an admin and admin links
issued through local owner control. Audited account mutations commit in the
same transaction as their access, role or credential change. Audit records
carry action, time and relevant account and grant IDs. They carry no email,
password, verifier, digest or link. A missing issuer on a setup or recovery
issuance identifies the local OS owner.

Links use `/auth/setup`, `/auth/invite` and `/auth/reset` with `#token=`. The
client reads the fragment into memory and replaces the current history entry
with the clean URL before requesting account data. Form inspection may check a
link in a request body without consuming it. Only the explicit redemption POST
consumes it. The transport explicitly unwraps the one-time URL to return it to
the authorized caller and unwraps session credentials only to deliver them to
the intended client. Use the service's public error messages for auth responses
and owner output. Exclude bodies, fragments, cookies, authorization headers and
underlying storage error details from logs and diagnostics.

V1 password derivation uses Argon2id v19 with 64 MiB, three passes, one lane,
and a fresh 16-byte salt. The service runs at most two simultaneous derivations.
It normalizes passwords with NFC after enforcing the Unicode length bound and
checks a bundled list of common passphrases. Sign-in uses a five-minute window
with 10 attempts per canonical account and 40 per peer address and bounded
process-local state. The transport supplies the verified peer address. Passive
stream checks must leave the browser idle deadline unchanged.

## Principals and endpoint policy

Apply one server policy across REST, HTTP MCP (including in-process dispatch), SSE, images, attachments, push and direct Unix/TCP access. Apply it to aliases and inner gateway dispatch too. Keep `guard.Policy`, Host/origin checks and scoped hook authentication. Return structured 401 for missing or invalid credentials on protected endpoints and 403 when a valid principal lacks permission. Personal implicit human access exists at the human boundary only.

| Surface | Shared principal and credential |
| --- | --- |
| Static shell and assets, minimal health/version/readiness and setup state | Public. Harness errors and operational metadata require authentication. Preserve compatibility probes. |
| Sign-in and setup/invite/reset redemption | Public entry points with exact browser origin and CSRF controls for writes. Sign-in checks password; redemption checks its purpose-bound grant. |
| `/api/me`, own account, sign-out, inbox, preferences, push and device bindings | Active human with the appropriate browser or CLI session. Access and delivery are scoped to that account. |
| Shared projects, tasks, trees, schedules, journals, work streams and attachments | Active human. Agents get the scoped agent operations where explicitly allowed ([ADR 0011](0011-agent-operation-parity.md)), never on their own task. Human-only actions keep their restriction. |
| Worker registry, pairing, default execution connection and harness management | Active human, including members. Agent and worker gateway reads remain limited by existing policy. |
| Invitations, role changes, disable and member reset | Active admin. Admins retain member permissions. Enforce the last-active-admin rule transactionally. |
| HTTP/stdio MCP and agent API paths | Trusted agent via its scoped bearer, the main machine's private agent socket, or a verified worker gateway. MCP retains agent operation limits, including when a signed-in browser invokes it. The general-purpose Unix socket requires the corresponding credential. Interactive CLI sign-in never silently supplies credentials to agent integrations. |
| `/api/worker/*`, including channel and gateway | Pairing grant for enrollment, machine token for worker routes. Preserve worker protocol negotiation. A worker gateway forwards allowed agent operations and records its machine. Require a private local gateway listener or agent authentication on remotely reachable gateways. |
| Observation and hook ingress | Authenticate the sender under the ingress policy. Workers are restricted to their own connections. Existing hooks retain their scoped credentials. |
| Setup reissue and admin recovery | Local installation owner through the dedicated OS-protected control path. |
| Cron, dependent release, observation and other internal work | Internal system, with authorized machine or hook provenance where applicable. The system principal exists only inside Stage. |

Browser sessions use a host-only `HttpOnly`, `SameSite=Lax`, `Path=/` cookie with `Secure` on HTTPS. Protect login, logout and all state changes with the browser origin policy. Native desktop Go captures and injects only the selected upstream's Stage credential for that exact target, including streams and assets. Store it in OS-protected storage or an atomic owner-only file in a protected directory. The webview receives no raw session cookie. CLI login uses a hidden password prompt and owner-only storage. Agent setup uses a private agent endpoint or explicit secret configuration. Caller-session headers, body fields and launch hints are provenance only. Keep secrets out of prompts.

The [API contract](../api.md#shared-access-transport) publishes credential
issuance for #455 and #458 and form endpoints for #457. A signed-in person
can rotate or revoke one account-scoped 30-day agent credential. Rotation
preserves human sessions. Each long-lived MCP request checks its admitted
credential at one-second intervals without renewing browser idle expiry.
The worker gateway verifies a machine-token MAC emitted only by the
owner-private worker socket. Older workers retain status and channel
negotiation. Their gateway requests are closed in shared mode, as are
explicit TCP or group socket gateways. Existing harness hooks retain their
own authentication. Inbox, badge and push use the active human credential.
Web Push subscription management requires a browser session.

The principal is authoritative for access. The existing non-null
`createdBy` columns still carry legacy attribution for system and machine
writes with no person. Work records carry a durable actor kind and cause. Scheduled
tasks preserve their template creator, and worker writes keep
`sourceMachineId`. Neither column is an authorization input.

Record direct actor kind and optional human initiator separately from `sourceMachineId`, connection/session, schedule and request ID. Get human IDs from authenticated context. Keep actor and cause on delayed work and retries when known. Scheduled tasks and their initial notes keep the schedule creator. Cron is the actor on automatic fires. Run now records the requester. Releases and harness observations record automation with known cause. Keep old creator IDs and immutable journal/acceptance bytes. Label ambiguous pre-identity activity historical. Disabled accounts retain IDs and history. Existing workspace work and schedules continue under the normal execution policy. Audit access-management actions without secrets and omit login emails from operational display where unnecessary.

## Account and inbox flows

Use Stage's existing design and operating patterns. Setup, sign-in, invite and reset use compact `Field`, `Input`, `Button` and `Alert` primitives outside the protected layout. Supply accessible labels, password-manager autocomplete, focus and error feedback, keyboard operation, narrow-phone layout and both themes. Use existing `TabNav`, ruled panels, `CopyButton` and `ConfirmInline` for account administration. Task #457 owns visual implementation and desktop/phone evidence. [Stage design](../../DESIGN.md#primitives) · [Product principles](../../PRODUCT.md#product-principles)

| Flow | Result |
| --- | --- |
| Personal | Open the dashboard or a deep-linked task directly, including before any harness is configured. Hide Account and Admin surfaces. |
| First setup | A bare pending URL shows a waiting-for-owner message. The private setup link asks for email, name and password. Successful claim signs in and opens the dashboard. |
| Invitation | Admin copies the single-use URL once for manual delivery. It expires in seven days. One live link per canonical address, with explicit revoke/replace. Show pending, used, revoked and expired invitations with recipient, date and inviter. The form shows the fixed invited address and asks for name and password. Redemption creates a member and signs in to the dashboard. Existing accounts cannot be overwritten. |
| Sign-in | Show email and password before mounting protected queries or layout. Restore a safe same-server relative deep link. Distinguish sign-in needed from offline and forbidden. Desktop signs in independently. Open setup and invite links at the canonical browser URL. |
| Account and recovery | Account settings offer name, current-password-confirmed password change, agent credential issue/revoke and sign-out. Admin issues a 30-minute single-use member reset link. Local owner can recover an admin. Reset preserves the account and its role. Expired or revoked links explain how to get a replacement. |
| Admin | People and Invitations subnav in a peer to Settings, reachable from desktop sidebar and mobile More. People shows roles and status, with confirmed access changes. Members see forbidden on direct Admin navigation. |
| Session ends or changes | Close streams and clear query caches, private assets, sensitive form values, badges, delivery state and cursors. Handle expiry, revocation, logout, cross-tab change and back navigation. Desktop also isolates background servers and clears the affected credentials on sign-out, target edit or removal. |

Store each workspace event once with per-user read receipts, preferences and
device push bindings. New members start with retained history read and can
still see open work. A member's filter determines which new events arrive
unread and can alert their devices. Muted events remain in shared history.
Preserve user 1's existing filter and read state on upgrade. Personal
installations keep their existing filter and needs-input badge.

Opt-in removes legacy unclaimed push endpoints. Each browser binds its
subscription to its active session after sign-in. Shared pushes contain an
opaque binding and event reference. The service worker must confirm both
with the server before showing work details, including after offline
delivery. Logout revokes that session's device, and disabling an account or
changing its password revokes all of its devices. An explicit restore
invalidates session-bound subscriptions along with the sessions. Close
shown alerts and clear device badges and cursors on session loss or
replacement where the platform permits.

## Delivery and migration order

Each task merges independently and uses merged predecessor contracts.
Use paired SQLite/Postgres forward-only migrations. Shared access uses
`0040` (#452), access audit uses `0041` (#453), notifications use `0043`
(#459), and work attribution uses `0044` (#456). Renumber unmerged
migrations above the freshly fetched integration head as needed.
Keep merged migration numbers and contents fixed.

| Task | Owner and evidence |
| --- | --- |
| #452 | Identity, mode and storage. Fresh and populated databases, single claim under concurrency, canonical email uniqueness, restart, both engines and supported data copy. |
| #453 | Account service and operator commands. Bootstrap/recovery across profiles, grant races and replay, password bounds, last admin, revocation and secret-free diagnostics. |
| #454 | Transport policy. Two-user authorization, exact origin, direct Unix, proxy, REST/MCP, worker gateways, nested paths, hooks, paused runners, incompatible clients and stream revocation. Keep unimplemented shared entry points closed. |
| #455 | CLI, MCP and harness integration. Exact-target credentials, rotation, OS user isolation, local and remote agent paths, real launch/report-back across supported harnesses. |
| #456 | Actor persistence and product projections. Human, agent, machine and automation writes, atomic events, delayed retries and unchanged historical evidence. |
| #457 | Product forms, Account and Admin. Desktop/phone layouts, accessibility, deep links and two-account cache isolation. |
| #458 | Native bridge and saved-server credentials. Zero-TCP personal startup, shared remote, mixed endpoints, restart, attachments, SSE and background isolation. |
| #459 | Inbox and delivery. Two users on one event, one user on two devices, recipient isolation, legacy push invalidation, retention and reconnect. |
| #460 | Public opt-in, operator and product docs, and integrated launch evidence. Depends on #455–459, which depend on the #454 transport contract. |

## Launch checklist

- Verify default desktop on its private socket and trusted personal LAN with multiple workers. Upgrade populated personal databases, claim user 1, confirm work and paired workers survive, and prove sticky shared startup and explicit-false failure. Exercise foreground, `service.env` and container configuration behind HTTPS, and local owner recovery.
- Have two people share work end to end. One creates a tree, the other runs a task on a worker and accepts it. Verify actor labels and independent inbox state. Complete manual invitation, phone/browser/desktop sign-in, role changes, disable and recovery.
- Verify REST, CLI, both MCP transports, both agent gateways, SSE, attachments and push. Exercise 401/403, gateway restrictions, legacy client/worker compatibility, scanner GET, concurrent redemption, last-admin races, logout/revocation, exact origin and target binding, and diagnostic redaction.
- Run populated SQLite and Postgres migrations and restore, repository checks and build, and applicable dist, desktop and service checks using isolated identities and throwaway workers. Record any unavailable native runs and cross-built targets. Public opt-in ships only with the integrated feature.

## References

- Take the private operator setup link pattern from Jupyter and the manually copyable invitation pattern from Plex. [Jupyter Server security](https://jupyter-server.readthedocs.io/en/latest/operators/security.html) · [Plex library access](https://support.plex.tv/articles/201105738-creating-and-managing-server-shares/)
- Use expiring, single-use grants and salted Argon2id with bounded work. Follow passphrase, password-manager, blocklist and throttling guidance. [OWASP recovery](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html) · [OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) · [NIST SP 800-63B-4](https://pages.nist.gov/800-63-4/sp800-63b/authenticators/)
- Reserve issuer plus immutable subject for external identity. [OpenID Connect claim stability](https://openid.net/specs/openid-connect-core-1_0.html#ClaimStability)
