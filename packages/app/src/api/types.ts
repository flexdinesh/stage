// REST contract types. Mirrors docs/api.md exactly; change both together.
// Timestamps are ISO 8601 strings. App ids are numbers; T3 ids are strings.

// ---- Enums ----

export const TASK_STATES = [
  "backlog",
  "launching",
  "running",
  "needs_input",
  "done",
  "settled",
  "failed",
  "interrupted",
  "cancelled",
] as const;
export type TaskState = (typeof TASK_STATES)[number];

export const THREAD_STATUSES = [
  "idle",
  "preparing",
  "queued",
  "starting",
  "running",
  "waiting",
  "completed",
  "interrupted",
  "failed",
  "cancelled",
  "rolled_back",
] as const;
export type ThreadStatus = (typeof THREAD_STATUSES)[number];

export const RUNTIME_MODES = ["approval-required", "auto-accept-edits", "auto", "full-access"] as const;
export type RuntimeMode = (typeof RUNTIME_MODES)[number];

export const INTERACTION_MODES = ["default", "plan"] as const;
export type InteractionMode = (typeof INTERACTION_MODES)[number];

export const SEND_MODES = ["auto", "queue", "steer", "restart"] as const;
export type SendMode = (typeof SEND_MODES)[number];

/** When a tree member launches once its needs are completed: auto (the server launches it) or manual (it reads ready). */
export const START_MODES = ["auto", "manual"] as const;
export type StartMode = (typeof START_MODES)[number];

/** A backlog tree member: blocked (a need is not settled) or ready (every need settled and manual, or no needs). */
export type Readiness = "blocked" | "ready";

export type ThreadSource = "app" | "t3";

// ---- Shared ----

export interface SessionRef {
  connectionId: number;
  externalSessionId: string;
}

export interface ApiErrorBody {
  error: string;
  /** Machine-readable reason on some 409s (e.g. already_answered, request_stale). */
  code?: string;
}

/** T3 ProviderOptionSelection, e.g. { id: "effort", value: "low" }. */
export interface ModelOption {
  id: string;
  value: string | boolean;
}

/** Worktree base: ref (from baseRef) or predecessor (from a need's branch; baseRef is the fallback). */
export type WorkspaceBase = "ref" | "predecessor";

export type Workspace =
  | { type: "root" }
  | {
      type: "worktree";
      baseRef: string;
      branch: string | null;
      base?: WorkspaceBase;
      /** With base predecessor: the need whose branch the worktree starts from (null = the sole need). */
      baseTaskId?: number | null;
    };

// ---- Health ----

/** GET /api/health: a live Stage server (status/db) and, separately, each connected harness's last known state. */
export interface Health {
  status: "ok" | "error";
  db: "ok" | "error";
  service: "stage";
  identity: string;
  version: string;
  commit: string;
  /** /api contract version; clients refuse a server on another one. */
  apiVersion: number;
  instanceId: string;
  startedAt: string | null;
  harnesses: { kind: Harness; connectionId: number; configured: boolean; connected: boolean; lastError: string | null }[];
}

// ---- T3 connection / sync / catalog ----

/** Precedence: unreachable > unpaired > expired > rejected > connected. */
export type T3State = "unreachable" | "unpaired" | "expired" | "rejected" | "connected";

/** Where the T3 URL comes from: saved in Settings, or the default. */
export type T3UrlSource = "settings" | "default";

/** Why the server cannot pair through the local t3 CLI. */
export type AutoPairReason = "not_local" | "cli_not_found" | "fake";

/** The T3 server that answered the last probe. */
export interface T3Identity {
  environmentId: string;
  label: string;
  serverVersion: string;
}

/** Background full sync; counts tick while it runs. */
export interface T3SyncProgress {
  running: boolean;
  startedAt: string | null;
  projects: number;
  threads: number;
}

/** The details of GET /api/harnesses/t3/status (also of its settings, actions and disconnect replies). */
export interface T3Status {
  /** Holds a valid token (unchanged meaning). */
  connected: boolean;
  state: T3State;
  t3Url: string;
  /** The saved Browser URL, else the T3 URL. */
  publicUrl: string;
  t3UrlSource: T3UrlSource;
  environmentId: string | null;
  clientId: string | null;
  tokenExpiresAt: string | null;
  /** An admin token is saved in Settings (Stage renews its token itself). */
  adminTokenConfigured: boolean;
  lastError: string | null;
  lastFullSyncAt: string | null;
  /** Last probe of the T3 URL answered. */
  reachable: boolean;
  checkedAt: string | null;
  probeError: string | null;
  t3: T3Identity | null;
  autoPair: { available: boolean; reason: AutoPairReason | null };
  sync: T3SyncProgress;
  /** Pair and auto-pair only: the previous T3 connection this pairing replaced. */
  replaced?: ReplacedConnection | null;
}

/** T3Status as an older server may send it: fields added with onboarding can be missing. */
export type T3StatusWire = Omit<
  T3Status,
  "state" | "t3UrlSource" | "reachable" | "checkedAt" | "probeError" | "t3" | "autoPair" | "sync"
> &
  Partial<Pick<T3Status, "state" | "t3UrlSource" | "reachable" | "checkedAt" | "probeError" | "t3" | "autoPair" | "sync">>;

/** PUT T3 settings: at least one field; a string sets it, null clears it (url: the default, publicUrl: the T3 URL, adminToken: none). */
export interface T3SettingsRequest {
  url?: string | null;
  /** Browser URL for Open in T3 links. */
  publicUrl?: string | null;
  /** Lets Stage renew its token itself. */
  adminToken?: string | null;
}

export interface PairRequest {
  code: string;
}

export interface SyncResult {
  unavailableConnections?: number[];
  projects: number;
  threads: number;
  finishedAt: string;
}

export interface CatalogModelOption {
  id: string;
  values: (string | boolean)[];
  default: string | boolean | null;
}

export interface CatalogModel {
  id: string;
  label: string;
  options: CatalogModelOption[];
}

export interface CatalogProvider {
  providerInstanceId: string;
  driverKind: string;
  displayName: string;
  models: CatalogModel[];
  runtimeModes: RuntimeMode[] | null;
}

export interface Catalog {
  providers: CatalogProvider[];
  fetchedAt: string | null;
}

// ---- Users ----

/** GET /api/me: the acting user. No sign-in yet: always the default user (id 1). */
export interface User {
  id: number;
  displayName: string;
  createdAt: string;
}

// ---- Projects ----

export interface Project {
  id: number;
  title: string;
  bindings: ProjectBinding[];
  archivedAt: string | null;
  createdAt: string;
  /** User id of its creator. */
  createdBy: number;
  /** Machine whose agent gateway forwarded the create; null when not through a gateway. */
  sourceMachineId: number | null;
  updatedAt: string;
  /** Latest thread update/settle or task update; null when none. */
  lastActivityAt: string | null;
  /** "discovered" when harness discovery imported it. */
  origin: "discovered" | null;
  importedAt: string | null;
  /** Normalized origin remotes (host/owner/name) of its bound workspaces. Search metadata only. */
  repos: string[];
}

/** Projects one connection's discovery created in the last 7 days. */
export interface ImportSummary {
  connectionId: number;
  harness: Harness;
  count: number;
  at: string;
}

export interface ProjectList {
  projects: Project[];
  imports: ImportSummary[];
}

export interface CreateProjectRequest { title: string }
export interface ProjectBinding {
 id: number;
 projectId: number;
 connectionId: number;
 harness: Harness;
 externalProjectId: string;
 workspace: string;
 revision: number;
 status: "available" | "missing" | "changed";
 observedWorkspace: string | null;
 /** False when the binding's connection was replaced: history only, never repaired. */
 connectionActive: boolean;
 /** The connection's T3 environment label or OpenCode address. */
 connectionLabel: string;
 /** manual: a user bound it; imported: discovery created the project for it; path: discovery bound it to the project already at that path on this machine; remote: discovery bound it to the one project with the same Git origin remote and subproject on another machine. */
 origin: "manual" | "imported" | "path" | "remote";
}
export interface ExternalProject {
 connectionId: number;
 externalId: string;
 title: string;
 workspace: string;
 projectId: number | null;
 /** Normalized origin remote, "" when unknown. */
 repo: string;
}


// ---- Tasks ----

/** An uploaded image (bytes at GET /api/attachments/:id). */
export interface Attachment {
  id: number;
  name: string;
  mimeType: string;
  sizeBytes: number;
}

/** Accepted image types and limits (server enforces the same). */
export const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"] as const;
export const MAX_IMAGES = 8;
export const MAX_IMAGE_BYTES = 10 * 1024 * 1024;

export interface LaunchRecovery {attempt:number; revision:number; phase:string; reason:string; retryAt:string|null; retryCount:number; session:LaunchSession|null}
/** A session the harness returned for an unconfirmed launch before Stage recorded it. */
export interface LaunchSession extends SessionRef { title: string; state: string; links?: ExecutionLinks }
/** Session destinations; `command` resumes it in the harness's own client (copy, never run). */
export interface ExecutionLinks { web?: string; native?: string; app?: string; service?: string; command?: string }
/** A harness kind ("t3", "opencode", ...): the server's registry (GET /harnesses) says which exist. */
export type Harness = string;
/** A closure signal the harness reports and the copy that names it (an inferred completion's provenance). */
export interface HarnessClosure { signal: string; provenance: Acceptance["provenance"]; setting: string; completed: string; via: string }
/** GET /api/harnesses: one harness kind, its policies and copy, and whether this machine can run it. */
/** A connection setup setting or action input; settings are one object keyed by id (null resets). */
export interface SetupField {
  id: string;
  label: string;
  kind: "text" | "url" | "secret" | "bool";
  help?: string;
}
export interface SetupAction { id: string; label: string; input?: SetupField[] }
/** A harness's connection setup schema; hosts render it unless they register a panel for the kind. */
export interface HarnessSetup { fields: SetupField[]; actions: SetupAction[]; disconnect: boolean }
export interface HarnessDescriptor {
  kind: Harness;
  name: string;
  /** connected: Stage connects to its service; hosted: it runs as processes on Stage's machine. */
  hosting: "connected" | "hosted";
  closures: HarnessClosure[];
  setup: HarnessSetup;
  /** How the harness names an agent's session: MCP _meta keys it sends per call, and the variable its agents read their session id from. */
  attribution: {
    metaKeys: string[];
    env?: string;
    precedence?: {
      owns?: { env: string; value?: string }[];
      yields?: { env: string; value?: string }[];
    };
  };
  available: boolean;
  /** Why it cannot run here (e.g. Stage runs in a container); "" when available. */
  reason?: string;
  /** This server has connection setup for it (/harnesses/{kind}/status and friends). */
  connectable: boolean;
}
/** GET /api/harnesses/{kind}/status; D is the harness's own details (its panel's state). */
export interface HarnessStatus<D = unknown> {
  kind: Harness;
  name: string;
  connectionId: number;
  configured: boolean;
  connected: boolean;
  lastError?: string;
  /** A harness-defined word: connected, unpaired, unavailable... */
  state: string;
  reason?: string;
  fields: { id: string; value: string; set: boolean; source: "saved" | "default" }[];
  actions: { id: string; available: boolean; reason?: string }[];
  details?: D;
  /** The connection a setting change or action just retired. */
  replaced?: ReplacedConnection | null;
}
/** How this machine runs Stage: desktop (managed service), server (foreground) or container. */
export interface MachineProfile { kind: "desktop" | "server" | "container"; localProcesses: boolean }
/** GET /api/harnesses (the local machine) and GET /api/machines/{id}/harnesses. */
export interface HarnessesReply { machine: Machine & { profile: MachineProfile }; harnesses: HarnessDescriptor[] }
/** GET /api/machines: a host that runs harnesses. Machine 1 is local (this server); others run `stage worker`. */
export interface Machine {
  id: number;
  label: string;
  kind: "local" | "remote";
  /** Local: always. Remote: its worker channel is live. */
  online: boolean;
  /** online; offline; needs_update: its worker last connected with an incompatible API version (cleared when it connects again). */
  status: "online" | "offline" | "needs_update";
  /** Last worker contact; null for the local machine. */
  lastSeenAt: string | null;
  /** The build it runs; null when unknown. */
  version: string | null;
  createdBy: number;
  createdAt: string;
  /** Its active harness connections. */
  connections: { id: number; harness: Harness; label: string }[];
}
/** POST /api/machines/pairings: a one-time code (10 minutes) that adds a machine. Shown once. */
export interface MachinePairing {
  /** Names the code in GET/DELETE /api/machines/pairings/{id}. */
  id: number;
  code: string;
  label: string;
  expiresAt: string;
  /** STAGE_PUBLIC_URL/join#<code>. */
  link: string;
  /** `stage worker join <link>`, run on the machine to add. */
  command: string;
}
/** GET /api/machines/pairings/{id}: where a pairing code stands (never the code). */
export interface PairingStatus {
  id: number;
  /** waiting: redeemable; joined: machineId is the machine it added; expired: unused past expiresAt. */
  state: "waiting" | "joined" | "expired";
  expiresAt: string;
  machineId: number | null;
}
/** One harness instance; at most one active per harness kind on a machine. */
export interface HarnessConnection {
  id: number;
  /** The machine that runs it (one local machine today). */
  machineId: number;
  harness: Harness;
  /** Meaningful for the active connection only. */
  configured: boolean;
  /** Complete tasks when a successful run is settled (T3) or archived (OpenCode) there. */
  autoComplete: boolean;
  /** False once another instance of the harness replaced it: its tasks and bindings stay read-only. */
  active: boolean;
  /** T3 environment label or OpenCode address. */
  label: string;
  retiredAt: string | null;
  /** Forgotten retired connections leave Settings; their tasks keep the label. */
  forgottenAt: string | null;
  tasks: number;
  bindings: number;
  /** The model and options new tasks here start with (permission and mode empty); null = the harness default. */
  newTaskConfig: ExecutionConfig | null;
}
/** The T3 connection a pairing replaced. */
export interface ReplacedConnection { id: number; label: string; tasks: number; bindings: number }
export interface OpenCodeStatus {
  connectionId: number;
  endpoint: string;
  browserEndpoint: string;
  webClient: string;
  hasCredentials: boolean;
  /** `rejected`: OpenCode answered but refused Stage's sign-in (pairing expired or password changed). */
  state: "unconfigured" | "connected" | "unavailable" | "rejected";
  reason?: string;
  checkedAt?: string;
  /** How Stage signs in: the local service's own password, a pairing session, Basic credentials, or none. */
  auth: OpenCodeAuth;
  /** Pairing session expiry (RFC3339); paired only. */
  expiresAt?: string;
  /** The OpenCode service discovered on the Stage server's machine (one-click Connect). */
  local: OpenCodeLocal;
}
/** Status as sent; servers before OpenCode pairing omit `auth` and `local` (see normalizeOpenCodeStatus). */
export type OpenCodeStatusWire = Omit<OpenCodeStatus, "auth" | "local"> & Partial<Pick<OpenCodeStatus, "auth" | "local">>;
export type OpenCodeAuth = "none" | "local" | "paired" | "basic";
export interface OpenCodeLocal {
  available: boolean;
  url: string;
  version: string;
  reason: "" | "missing" | "invalid" | "unreachable";
}
/** Codex's harness state words (HarnessStatus.state). */
export type CodexState = "connected" | "not_connected" | "signed_out" | "unreachable" | "not_installed";
/** How the Codex CLI is signed in. */
export interface CodexAccount { type: "chatgpt" | "apiKey" | "amazonBedrock" | "gcp"; email: string | null; plan: string | null }
/** Codex's own status details (only its panel and summaries read them). */
export interface CodexDetails {
  /** The configured CLI path; "" uses codex on PATH. */
  binary: string;
  /** "" when unknown. */
  cliVersion: string;
  appServerVersion: string;
  codexHome: string;
  daemon: "running" | "stopped" | "unknown";
  account: CodexAccount | null;
  /** A shell command that fixes the current problem ("codex login"); "" none. */
  fix: string;
  checkedAt: string | null;
}
/** Codex's harness status with its details parsed (lib/codex.ts); null details: missing or malformed. */
export type CodexStatus = Omit<HarnessStatus, "details"> & { details: CodexDetails | null };
export interface HarnessChoice {id: string; label: string; available: boolean; reason?: string}
export interface HarnessOption {id: string; values: (string | boolean)[]; default?: string | boolean}
export interface HarnessModel extends HarnessChoice {
  group: string;
  options: HarnessOption[] | null;
  permissions: HarnessChoice[] | null;
}
export interface Capabilities {
  permissions?: HarnessChoice[];
  /** Harness-wide options, beside the selected model's own (ids never collide). */
  options?: HarnessOption[];
  /** The mode choice in the harness's own words ("Agent"); absent reads "Interaction mode". */
  modeLabel?: string;
  operations: Record<string, {available: boolean; reason?: string}>;
  models: HarnessModel[] | null;
  modes: HarnessChoice[] | null;
  workspaces: HarnessChoice[] | null;
}
export interface ExecutionConfig {
  model: string;
  options: ModelOption[];
  permission: string;
  mode: string;
}

export interface LaunchLinkPreview {
  attempt: number;
  revision: number;
  environment: string;
  observation: {
    session: { connectionId: number; externalSessionId: string };
    title: string;
    state: string;
    project: string;
    branch: string;
    directory: string;
  };
}
export interface Task {
  links?: ExecutionLinks;
  executionConfig?: ExecutionConfig | null;
 launch?: LaunchRecovery;
  session: SessionRef | null;
  originSession: SessionRef | null;
  connectionId: number;
  harness: Harness;
  /** False once the task's connection was replaced: read-only history, Stage no longer follows it. */
  connectionActive: boolean;
  /** The connection's T3 environment label or OpenCode address. */
  connectionLabel: string;
  acceptanceId: number | null;
  acceptanceRevision: number;
  completedAt: string | null;
  /** Provenance of the current acceptance; null while open. */
  completionProvenance: Acceptance["provenance"] | null;
  artifactLinks: { label: string; url: string }[];
  id: number;
  projectId: number;
  projectTitle: string;
  title: string;
  prompt: string;
  workspace: Workspace;
  state: TaskState;
  externalSessionId: string | null;
  /** The task's tree; null outside one. */
  treeId: number | null;
  treeTitle: string | null;
  /** The task's own start mode; null inherits the tree's. */
  startMode: StartMode | null;
  /** startMode, else the tree's; null outside a tree. */
  effectiveStartMode: StartMode | null;
  /** Backlog tree members only; null otherwise. */
  readiness: Readiness | null;
  /** Tasks (same tree) this one needs, by id. */
  needs: number[];
  /** Tasks that need this one, by id. */
  dependents: number[];
  /** Tracks an existing T3 session (prompt ""); stage never starts, restarts or duplicates it. */
  adopted: boolean;
  /** A manual task's needs all settled (the ready notification episode); null otherwise. */
  readyAt: string | null;
  /** One-shot start (UTC instant) of a backlog task; null once launched or when unset. */
  startAt: string | null;
  /** IANA zone the start time was entered in (set with startAt). */
  startTz: string | null;
  /** Recurring schedule that spawned this task. */
  scheduleId: number | null;
  scheduleTitle: string | null;
  /** The origin session's title; null when unknown. */
  originSessionTitle: string | null;
  /** Append one handoff block per finished need (final reply, PRs, branch, thread link) to the launch prompt. */
  handoff: boolean;
  /** Images sent with the prompt on every launch, upload order. */
  attachments: Attachment[];
  launchAttempt: number;
  lastError: string | null;
  launchedAt: string | null;
  finishedAt: string | null;
  settledAt: string | null;
  /** Elapsed run span of the task's thread: first run start to newest run completion. */
  runStartedAt: string | null;
  runEndedAt: string | null;
  /** The thread's latest assistant reply as one plain line (detail: `thread.lastWord`). */
  lastWordPreview: string | null;
  lastWordAt: string | null;
  /** Pull requests of the task's thread (T3 mirror); empty without a thread. */
  /** Answerable questions the task's thread is waiting on (approvals excluded). */
  createdAt: string;
  /** User id of its creator (a schedule's creator for a task the schedule spawned). */
  createdBy: number;
  /** Machine whose agent gateway forwarded the create or adoption; null when not through a gateway or spawned by a schedule. */
  sourceMachineId: number | null;
  updatedAt: string;
  /** When the task entered its current state (latest event into it); null without one. */
  stateSince: string | null;
  /** When the task was archived; null when not archived. Activity on it unarchives it. */
  archivedAt: string | null;
  /** Its tree's status; null outside a tree. */
  treeStatus: TreeStatus | null;
  /** Server eligibility: deletable only when no recorded work, launch or session exists. */
  deletable: boolean;
}

/** A pull request T3 associates with a thread. `state` is T3's string (open, merged, closed, draft, ...). */
export interface TaskEvent {
  id: number;
  taskId: number;
  fromState: TaskState | null;
  toState: TaskState;
  detail: string;
  at: string;
}

export interface TaskDetail extends Task {
  finalReply?: { text: string; truncated: boolean; updatedAt: string; runId: string };
  acceptances: Acceptance[];
  events: TaskEvent[];
  thread: Thread | null;
  /** The task's tree; null outside one. */
  tree: TreeDetail | null;
  /** Tasks the task's origin session created; null without an origin session. */
  batch: CreatedTasks | null;
  /** Handoff records of the current launch attempt, one per need; empty when none. */
  currentHandoffs: Handoff[];
  /** Worktree base the current launch attempt used (base predecessor); null when none was recorded. */
  currentLaunchBase: LaunchBase | null;
}

export interface AcceptedEvidence {
  session: SessionRef | null;
  taskId: number;
  title: string;
  treeId: number | null;
  treeTitle: string | null;
  projectId: number;
  threadId: string | null;
  threadLink: string | null;
  branch: string | null;
  worktreePath: string | null;
  executionState: TaskState;
  runId: string | null;
  replyRunId: string | null;
  replyItemId: string | null;
  reply: string | null;
  replyTruncated: boolean;
  replyAt: string | null;
  observedAt: string | null;
  historicalSettledAt: string | null;
  artifacts: { url: string; repository: string; number: number }[];
  journalThroughId: number;
}
export interface Acceptance {
  id: number;
  taskId: number;
  acceptedAt: string;
  capturedAt: string;
  provenance: "user" | "historical_settlement" | "harness_settled" | "harness_archived";
  evidence: AcceptedEvidence;
  note: string;
}
export interface CompletionPreview {
  revision: number;
  evidenceVersion: string;
  evidence: AcceptedEvidence;
  blockedReason: string;
  dependents: { taskId: number; title: string; claimed: boolean; startMode: StartMode | null }[];
}
export interface AcceptanceRequest {
  requestId: string;
  revision: number;
  evidenceVersion?: string;
  note?: string;
}
export interface AcceptanceResult {
  revision: number;
  acceptanceId: number | null;
}

/** The worktree base a launch attempt with base "predecessor" used: a need's branch, or the fallback and why. */
export interface LaunchBase {
  attempt: number;
  baseRef: string;
  startFromOrigin: boolean;
  source: "predecessor" | "fallback";
  sourceTaskId: number | null;
  sourceThreadId: string | null;
  sourceTitle: string;
  /** Why it fell back; null when it used the need's branch. */
  reason: string | null;
  at: string;
}

/** What a launch attempt appended (or why it appended nothing) from one need. */
export interface Handoff {
  attempt: number;
  status: "sent" | "skipped";
  sourceTaskId: number | null;
  sourceThreadId: string;
  sourceTitle: string;
  /** The appended block; "" when skipped. */
  text: string;
  /** Skip reason; null when sent. */
  reason: string | null;
  at: string;
}

/** A tree's name and start mode. */
export interface TreeRef {
  id: number;
  title: string;
  startMode: StartMode;
}

/** A session batch: tasks one session created, grouped by tree. */
export interface CreatedTasks {
  /** Batch tasks per tree, ordered by each group's lowest task id; tasks by id. */
  trees: { tree: TreeRef; tasks: Task[] }[];
  /** Batch tasks outside any tree. */
  independent: Task[];
}

// ---- Trees ----

/** A DAG of tasks: each member's needs are members that must settle before it starts. */
/** open; done (marked done by hand: nothing more starts); archived. Activity on a member reopens it. */
export type TreeStatus = "open" | "done" | "archived";

export interface Tree {
  id: number;
  title: string;
  startMode: StartMode;
  status: TreeStatus;
  /** When it was marked done or archived; null while open. */
  closedAt: string | null;
  createdAt: string;
  /** User id of its creator. */
  createdBy: number;
  /** Machine whose agent gateway forwarded the create; null when not through a gateway. */
  sourceMachineId: number | null;
  updatedAt: string;
  /** Server eligibility: deletion must not discard recorded work, including departed members. */
  deletable: boolean;
}

/** taskId needs needId. */
export interface TreeEdge {
  taskId: number;
  needId: number;
}

/** Tree roll-up times. */
export interface TreeTimes {
  /** First member launch; null before any. */
  startedAt: string | null;
  /** Latest of the tree's creation, a member's updatedAt and its thread's updatedAt. */
  lastActivityAt: string;
}

export interface TreeDetail extends Tree, TreeTimes {
  tasks: Task[];
  edges: TreeEdge[];
  /** Task ids per level (longest need path from a root), roots first; each level by id. */
  levels: number[][];
  /** The members' 50 newest task events, newest first. */
  events: TaskEvent[];
}

/** Member counts by state; blocked and ready split backlog. */
export interface TreeCounts {
  total: number;
  backlog: number;
  blocked: number;
  ready: number;
  launching: number;
  running: number;
  needsInput: number;
  done: number;
  settled: number;
  failed: number;
  interrupted: number;
  cancelled: number;
}

export interface TreeSummary extends Tree, TreeTimes {
  counts: TreeCounts;
  levels: number;
  /** Member project titles, sorted. */
  projects: string[];
  /** Every member settled: the latest settle; null otherwise. */
  settledAt: string | null;
}

export interface TreeList {
  trees: TreeSummary[];
}

export interface TreeMemberRequest {
  taskId: number;
  /** Members of the tree this task needs; omitted keeps the task's needs. */
  needs?: number[];
}

export interface CreateTreeRequest {
  title: string;
  startMode?: StartMode;
  tasks: TreeMemberRequest[];
}

export interface UpdateTreeRequest {
  title?: string;
  startMode?: StartMode;
  status?: TreeStatus;
}

/** Trees page and API view: active (open, a member not settled), done (marked done or all settled), archived, all. */
export type TreeView = "active" | "done" | "archived" | "all";

export interface TreeListQuery {
  status?: TreeView;
  /** With active: also open trees completed at or after this. */
  settledFrom?: string;
  project?: number;
}

export interface StartTreeResponse {
  started: Task[];
  errors: { taskId: number; error: string }[];
}

export interface AdoptSessionRequest {
  projectId?: number;
  newProjectTitle?: string;
  title?: string;
  initialNote?: string;
  treeId?: number;
}

export interface TaskList {
  tasks: Task[];
}

export interface CreateTaskRequest {
  connectionId?: number;
  executionConfig?: ExecutionConfig;
  projectId: number;
  title: string;
  prompt: string;
  providerInstanceId?: string | null;
  model?: string | null;
  modelOptions?: ModelOption[];
  runtimeMode?: RuntimeMode;
  interactionMode?: InteractionMode;
  workspace?: Workspace;
  initialNote?: string;
  /** Join this tree (default: the needs' tree, else a new tree when there are needs). */
  treeId?: number;
  /** Tasks this one needs (not with start or startLocal). */
  needs?: number[];
  /** Also need this session (adopted as a task first). */
  needsSession?: SessionRef;
  /** Omitted inherits the tree's. */
  startMode?: StartMode;
  /** Append each need's handoff at launch (server default true). */
  handoff?: boolean;
  /** Staged uploads to attach (max 8). */
  attachmentIds?: number[];
  /** One-shot start, wall time "YYYY-MM-DDTHH:mm" in startTz (not with needs or start). */
  startLocal?: string | null;
  startTz?: string | null;
  start?: boolean;
}

export interface UpdateTaskRequest {
  connectionId?: number;
  executionConfig?: ExecutionConfig;
  projectId?: number;
  title?: string;
  prompt?: string;
  providerInstanceId?: string | null;
  model?: string | null;
  modelOptions?: ModelOption[];
  runtimeMode?: RuntimeMode;
  interactionMode?: InteractionMode;
  workspace?: Workspace;
  /** Backlog only: null inherits the tree's. */
  startMode?: StartMode | null;
  /** Backlog only: append each need's handoff at launch. */
  handoff?: boolean;
  /** Backlog only: the exact image set (task's own ids + staged uploads). */
  attachmentIds?: number[];
  /** Backlog only: one-shot start; both null clears. */
  startLocal?: string | null;
  startTz?: string | null;
}

// ---- Append-only journals ----

export type JournalOwner = { taskId: number; treeId?: never } | { treeId: number; taskId?: never };
export type JournalKind = "notes" | "events" | "all";
export type NoteCategory = "" | "finding" | "decision" | "blocker" | "outcome";

export interface JournalEntry {
  sourceSession: SessionRef | null;
  id: number;
  kind: "note" | "event";
  taskId: number | null;
  treeId: number | null;
  sourceTaskId: number | null;
  sourceThreadId: string | null;
  attempt: number | null;
  category: string;
  text: string;
  eventType: string;
  fromTreeId: number | null;
  toTreeId: number | null;
  createdAt: string;
  /** User id of the note's author or of the user behind the event (trigger-written events: the default user). */
  createdBy: number;
  /** Machine whose agent gateway forwarded the write; null when not through a gateway (and for trigger-written events). */
  sourceMachineId: number | null;
  truncated: boolean;
}

export interface JournalQuery {
  kind?: JournalKind;
  category?: NoteCategory;
  q?: string;
  /** Tree reads only. */
  taskId?: number;
  cursor?: string;
  limit?: number;
}

export interface JournalPage {
  entries: JournalEntry[];
  nextCursor: string | null;
}

export interface AppendNoteInput {
  text: string;
  category: NoteCategory;
  requestId: string;
  sourceTaskId?: number | null;
  sourceSession?: SessionRef | null;
}

// ---- Schedules ----

export const SCHEDULE_RUN_OUTCOMES = ["started", "skipped", "missed", "manual"] as const;
export type ScheduleRunOutcome = (typeof SCHEDULE_RUN_OUTCOMES)[number];

/** Recurring task template: each cron fire spawns a task with this config. */
export interface Schedule {
  executionConfig?: ExecutionConfig | null;
  connectionId: number;
  originSession: SessionRef | null;
  id: number;
  projectId: number;
  projectTitle: string;
  title: string;
  prompt: string;
  workspace: Workspace;
  notes: string;
  attachments: Attachment[];
  /** Standard 5-field cron, evaluated in tz. */
  cron: string;
  tz: string;
  paused: boolean;
  /** Next fire (UTC); null while paused. */
  nextRunAt: string | null;
  lastRunAt: string | null;
  lastTaskId: number | null;
  lastTaskState: TaskState | null;
  createdAt: string;
  /** User id of its creator; the tasks it spawns inherit it. */
  createdBy: number;
  /** Machine whose agent gateway forwarded the create; null when not through a gateway. Spawned tasks do not inherit it. */
  sourceMachineId: number | null;
  updatedAt: string;
}

export interface ScheduleRun {
  id: number;
  scheduleId: number;
  fireAt: string;
  outcome: ScheduleRunOutcome;
  taskId: number | null;
  detail: string;
  at: string;
  /** The spawned task on detail reads. */
  task: Task | null;
}

export interface ScheduleDetail extends Schedule {
  /** Newest first, at most 50. */
  runs: ScheduleRun[];
}

export interface ScheduleList {
  schedules: Schedule[];
}

export interface ScheduleListQuery {
  project?: number;
  /** Go duration, e.g. "24h". */
  dueWithin?: string;
}

export interface CreateScheduleRequest {
  connectionId?: number;
  executionConfig?: ExecutionConfig;
  projectId: number;
  title: string;
  prompt: string;
  providerInstanceId?: string | null;
  model?: string | null;
  modelOptions?: ModelOption[];
  runtimeMode?: RuntimeMode;
  interactionMode?: InteractionMode;
  workspace?: Workspace;
  notes?: string;
  attachmentIds?: number[];
  cron: string;
  tz: string;
  paused?: boolean;
}

export type UpdateScheduleRequest = Partial<CreateScheduleRequest>;

export interface SchedulePreviewRequest {
  cron: string;
  tz: string;
}

export interface SchedulePreview {
  /** Next three fires, UTC. */
  next: string[];
}

export interface TaskListQuery {
  project?: number;
  state?: TaskState[];
  q?: string;
  from?: string;
  to?: string;
  /** settledAt >= this (ISO date or datetime). */
  settledFrom?: string;
  /** Omitted: open tasks only; true: archived or in a done/archived tree; all: both. */
  closed?: "true" | "all";
}

// ---- Threads (history) ----

export interface Thread {
  session: SessionRef;
  connectionId: number;
  externalSessionId: string;
  externalProjectId: string;
  projectId: number | null;
  projectTitle: string | null;
  title: string;
  status: ThreadStatus;
  state: TaskState;
  archived: boolean;
  activeRunId: string | null;
  latestRunId: string | null;
  settled: boolean;
  settledAt: string | null;
  /** Elapsed run span: first run start to newest run completion. */
  runStartedAt: string | null;
  runEndedAt: string | null;
  pendingRequestCount: number;
  model: string | null;
  runtimeMode: RuntimeMode | null;
  interactionMode: InteractionMode | null;
  branch: string | null;
  worktreePath: string | null;
  parentThreadId: string | null;
  relationship: string | null;
  source: ThreadSource;
  taskId: number | null;
  createdAt: string;
  updatedAt: string;
  syncedAt: string;
  /** Latest assistant reply as one plain line. */
  lastWordPreview: string | null;
  lastWordAt: string | null;
  /** Reply excerpt (markdown); set on detail reads only, null in lists. */
  lastWord: LastWord | null;
}

/** A thread's latest assistant reply as last synced: markdown excerpt capped at 4000 chars. */
export interface LastWord {
  text: string;
  /** The reply is longer than `text`; the rest is in T3. */
  truncated: boolean;
  runId: string | null;
  /** When the reply was last updated in T3. */
  at: string | null;
}

export interface SessionPreview {
  session: SessionRef;
  task: Task | null;
  project: Project | null;
  title: string;
  workspace?: string;
  unavailableReason?: string;
}

export interface ThreadDetail {
  thread: Thread;
  task: Task | null;
}

/** The adopted task's first own user message. */
export interface OpeningItem {
  itemId: string;
  text: string;
  truncated: boolean;
  updatedAt: string;
}
export interface OpeningPromptResponse { item: OpeningItem | null; }

// ---- Snippets ----

/** Reusable prompt text; `projectId` null = global (shown in every project). */
export interface Snippet {
  id: number;
  name: string;
  body: string;
  projectId: number | null;
  createdAt: string;
  updatedAt: string;
}

export interface SnippetList {
  snippets: Snippet[];
}

export interface CreateSnippetRequest {
  name: string;
  body: string;
  projectId?: number | null;
}

export type UpdateSnippetRequest = Partial<CreateSnippetRequest>;

// ---- Preferences ----

/**
 * defaultConnectionId: the connection new tasks start on (null = none: the project's only binding). Each connection
 * pins its own new-task model (HarnessConnection.newTaskConfig).
 * notifyEvents: events that notify (inbox + push) on every device; others are dropped.
 */
export interface Preferences {
  defaultConnectionId: number | null;
  notifyEvents: NotificationEvent[];
}

// ---- Notifications (Web Push) ----

export interface PushConfig {
  /** VAPID public key, base64url (the browser's applicationServerKey). */
  publicKey: string;
}

/** `PushSubscription.toJSON()` subset. */
export interface PushSubscriptionBody {
  endpoint: string;
  keys: { p256dh: string; auth: string };
}

export interface PushEndpointBody {
  endpoint: string;
}

export interface Badge {
  /** Tasks in needs_input + external threads needing input. */
  needsInput: number;
}

// ---- Notifications inbox ----

/** States a notification is sent for. */
export type NotificationEvent = Extract<TaskState, "needs_input" | "done" | "failed" | "settled"> | "ready";

export interface Notification {
  links?: ExecutionLinks;
  connectionId: number;
  id: number;
  event: NotificationEvent;
  /** null for external threads (or once the task is deleted). */
  taskId: number | null;
  externalSessionId: string | null;
  /** Snapshot at send time. */
  title: string;
  projectTitle: string | null;
  /** Stored notification destination (historical values are preserved). */
  url: string;
  createdAt: string;
  readAt: string | null;
  /** Live: the task's state, or the external thread's (`settled` when settled). null = unknown. */
  currentState: TaskState | null;
}

export interface NotificationPage {
  notifications: Notification[];
  unread: number;
  /** `before` for the next (older) page; null when none. */
  nextBefore: number | null;
}

export interface NotificationGroup extends Notification {
  key: string;
  eventCount: number;
  unreadCount: number;
}

export interface WaitingSession {
  links?: ExecutionLinks;
  connectionId: number;
  externalSessionId: string;
  title: string;
  projectTitle: string | null;
  /** The session's working folder when its harness reports one (null for T3). */
  directory: string | null;
}

export interface NotificationGroupPage {
  waitingSessions: WaitingSession[];
  groups: NotificationGroup[];
  unread: number;
  /** Newest event across all groups, independent of priority order. */
  latestId: number;
  nextCursor: string | null;
}

/** Push tags (`task-<id>`, `thread-<id>`) of groups with unread notifications. */
export interface UnreadNotificationTags {
  tags: string[];
}
