// REST contract types. Mirrors docs/api.md exactly; change both together.
// Timestamps are ISO 8601 strings. App ids are numbers; T3 ids are strings.

// ---- Enums ----

export const TASK_STATES = [
  "backlog",
  "launching",
  "running",
  "needs_input",
  "done",
  "failed",
  "interrupted",
  "cancelled",
] as const;
export type TaskState = (typeof TASK_STATES)[number];

/** When a tree member launches once its needs are completed: auto (the server launches it) or manual (it reads ready). */
export const START_MODES = ["auto", "manual"] as const;
export type StartMode = (typeof START_MODES)[number];

/** A backlog tree member: blocked (a need is incomplete) or ready (every need completed and manual, or no needs). */
export type Readiness = "blocked" | "ready";

// ---- Shared ----

export interface SessionRef {
  connectionId: number;
  externalSessionId: string;
}

export interface ApiErrorBody {
  error: string;
  /** Machine-readable reason on some 409s (e.g. already_answered, request_stale). */
  code?: string;
  preview?: GroupEdit;
}

/** T3 ProviderOptionSelection, e.g. { id: "effort", value: "low" }. */
export interface ModelOption {
  id: string;
  value: string | boolean;
}

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
  /** Present on a server running in shared mode. */
  sharedAccess?: boolean;
  startedAt: string | null;
  harnesses: { kind: Harness; connectionId: number; configured: boolean; connected: boolean; lastError: string | null }[];
}

export interface SyncResult {
  unavailableConnections?: number[];
  projects: number;
  sessions: number;
  finishedAt: string;
}

// ---- Users ----

/** GET /api/me: the acting user, with an email in shared access. */
export interface User {
  id: number;
  displayName: string;
  createdAt: string;
  email?: string | null;
}

export interface AccessStatus {
  mode: "personal" | "setup_pending" | "claimed";
  installationId: string;
  canonicalUrl?: string;
}

export type AccountRole = "admin" | "member";
export interface Account extends User {
  email: string | null;
  role: AccountRole;
  status: "active" | "disabled";
}

export type GrantKind = "setup" | "invite" | "reset";
export interface GrantInfo {
  email: string | null;
  expiresAt: string;
}

export interface IssuedLink {
  id: number;
  url: string;
  expiresAt: string;
}

export interface AgentCredential {
  token: string;
  expiresAt: string;
  installationId: string;
}

export interface Invitation {
  id: number;
  email: string | null;
  issuerUserId: number | null;
  status: "pending" | "used" | "revoked" | "expired";
  createdAt: string;
  expiresAt: string;
  consumedAt: string | null;
  revokedAt: string | null;
}

// ---- Projects ----

export interface Project {
  id: number;
  title: string;
  bindings: ProjectBinding[];
  aliases: ProjectAlias[];
  archivedAt: string | null;
  createdAt: string;
  /** User id of its creator. */
  createdBy: number;
  /** Machine whose agent gateway forwarded the create; null when not through a gateway. */
  sourceMachineId: number | null;
  updatedAt: string;
  /** Latest session or task update; null when none. */
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

export interface GroupWorkspace {
  connectionId: number;
  workspaceId: string;
  machineId: number;
  title: string;
  path: string;
  catalogState: "listed" | "retired" | "missing" | "archived";
  /** Why the harness won't start a session in this listed workspace ("" when nothing known stops it). */
  blockedReason: string;
}

export interface ProjectGroup extends Pick<Project, "id" | "title" | "archivedAt" | "aliases" | "lastActivityAt"> {
  state: "current" | "empty" | "retired";
  members: GroupWorkspace[];
  relatedProjectIds: number[];
}

export interface GroupEditInput {
  connectionId: number;
  workspaceId: string;
  targetProjectId: number | null;
}

export interface GroupEdit {
  applied: boolean;
  workspace: GroupWorkspace;
  source: Pick<Project, "id" | "title">;
  target: Pick<Project, "id" | "title"> | null;
  newTitle?: string;
  blocked: { code: string; reason: string } | null;
  previewToken: string;
}

/** An exact workspace offered for selection on one connection. */
export interface Destination {
  connectionId: number;
  workspaceId: string;
  projectId?: number;
  groupProjectId?: number;
  title: string;
  connectionLabel: string;
  machineLabel: string;
  discriminator?: string;
  available: boolean;
  reason?: string;
}

export interface DestinationCatalog {
  projects: Pick<Project, "id" | "title" | "archivedAt" | "aliases">[];
  connections?: Pick<HarnessConnection, "id" | "machineId" | "harness" | "label" | "active" | "configured">[];
  destinations: Destination[];
}

export interface ProjectAlias {
  id: number;
  title: string;
  mergedAt: string;
}

export interface CreateProjectRequest {
  title: string;
}

export interface ProjectBinding {
  id: number;
  projectId: number;
  connectionId: number;
  machineId: number;
  harness: Harness;
  externalProjectId: string;
  title: string;
  workspace: string;
  revision: number;
  status: "available" | "missing" | "changed";
  observedWorkspace: string | null;
  /** False when the binding's connection was replaced: history only, never repaired. */
  connectionActive: boolean;
  /** The connection's T3 environment label or OpenCode address. */
  connectionLabel: string;
  primary: boolean;
  rule: "manual" | "new" | "remote" | "local" | "moved";
  remote: string;
  subpath: string;
  fileHost: number | null;
  evidenceAt: string | null;
  facts: HarnessFact[];
}

export interface WorkspaceRef {
  externalId: string;
  workspace: string;
}

export type WorkspaceReviewReason = "suggested" | "ambiguous" | "declined" | "moved";

export interface WorkspaceResolution {
  outcome: "bound" | "join" | "new" | "review";
  reason: WorkspaceReviewReason | "";
  rule: "existing" | "declined" | "moved" | "external" | "remote" | "local" | "none";
  candidates: number[];
}

export interface Workspace extends WorkspaceRef {
  connectionId: number;
  workspaceId?: string;
  title: string;
  available: boolean;
  importable: boolean;
  harnessArchived: boolean;
  /** Why the harness won't start a session here, in its own words ("" when nothing known stops it). */
  blockedReason: string;
  activeAt: string | null;
  projectId: number | null;
  groupProjectId?: number | null;
  bindingId: number | null;
  primary: boolean;
  fileHost: number | null;
  canonicalPath: string;
  remote: string;
  subpath: string;
  identityKey: string;
  evidenceAt: string | null;
  evidenceSource: string;
  declined: boolean;
  resolution: WorkspaceResolution;
}

export interface WorkspaceInventory {
  workspaces: Workspace[];
  inventory?: {
    state: "pending" | "refreshing" | "ready" | "stale" | "unavailable" | "retired";
    refreshedAt: string | null;
    lastError: string | null;
  };
}

export interface ProjectEvent extends WorkAttribution {
  id: number;
  type: "workspace_moved" | "project_merged" | "project_split" | "workspace_added";
  text: string;
  createdAt: string;
  createdBy: number;
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
export interface ExecutionLinks { web?: string; native?: string; command?: string }
/** A harness kind: the server's registry (GET /harnesses) says which exist. */
export type Harness = string;
/** A closure signal the harness reports and the copy that names it (an inferred completion's provenance). */
export interface HarnessClosure { signal: string; provenance: Provenance; setting: string; completed: string; via: string }
/** GET /api/harnesses: one harness kind, its policies and copy, and whether this machine can run it. */
/** A connection setup setting or action input; settings are one object keyed by id (null resets). */
export interface SetupField {
  id: string;
  label: string;
  /** `path`: a filesystem path on the harness's machine (rendered monospace; the harness checks it). */
  kind: "text" | "url" | "secret" | "bool" | "choice" | "path";
  help?: string;
  required?: boolean;
  choices?: { id: string; label: string }[];
}
export type ActionTarget = "connection" | "session" | "project";
export type ActionCaller = "user" | "agent";
export interface HarnessAction {
  id: string;
  label: string;
  target: ActionTarget;
  input?: SetupField[];
  confirm?: string;
  destructive: boolean;
  primary?: boolean;
  callers: ActionCaller[];
  availability: { available: boolean; reason?: string };
}
export type SetupAction = HarnessAction;
export interface HarnessFact {
  id: string;
  label: string;
  value: string;
  tone?: "neutral" | "ok" | "warn" | "bad";
  link?: string;
  copyable?: boolean;
}
export type ConnectionPhase = "disconnected" | "needs_setup" | "connecting" | "connected" | "degraded" | "unavailable";
export interface ActionResult {
  message: string;
  facts?: HarnessFact[];
  link?: string;
  replaced?: number;
  journalError?: string;
}
/** A harness's connection setup schema; hosts render it unless they register a panel for the kind. */
export interface HarnessSetup { fields: SetupField[]; actions: SetupAction[]; disconnect: boolean }
export interface HarnessDescriptor {
  kind: Harness;
  name: string;
  /** Requires a machine profile that permits local harness processes. */
  localProcesses: boolean;
  sessions: "machine" | "cloud";
  stateReasons: string[];
  closures: HarnessClosure[];
  linkSchemes: string[];
  /** Session web links can require a browser address in connection settings. */
  webAddress?: boolean;
  /** The harness can wait on requests Stage can't list: a needs-input session with none listed waits on one of those, not on a reply. The copy says where to answer them. */
  unlistedRequests?: string;
  setup: HarnessSetup;
  /** How the harness names an agent's session: MCP _meta keys it sends per call, and the variable its agents read their session id from. */
  attribution: {
    metaKeys: string[];
    env?: string;
    precedence?: {
      owns?: { env: string; value?: string }[];
      yields?: { env: string; value?: string }[];
    };
    /** It records the client that started each session, so a wrapping harness may run them; env holds that origin in its agents. */
    origin?: { env?: string };
  };
  /** How a launch learns its session identity: reserved before any effect, or correlated by a marker after. */
  launch: "reserved" | "correlated";
  /** It runs other harnesses' sessions and leaves these origins in them; Stage refuses those sessions elsewhere. */
  wraps?: { origins: string[] };
  available: boolean;
  /** Why it cannot run here (e.g. Stage runs in a container); "" when available. */
  reason?: string;
  /** This server has connection setup for it (/harnesses/{kind}/status and friends). */
  connectable: boolean;
  /** How its sessions are named when tracking one (the Track session field's hint); absent: the generic hint. */
  sessionHint?: string;
  discoveryHint?: string;
}
/** Full sync progress and the last completed sync, when the harness reports them. */
export interface HarnessSyncStatus {
  lastError?: string | null;
  lastFullSyncAt?: string | null;
  startedAt?: string | null;
  running: boolean;
  projects: number;
  sessions: number;
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
  phase: ConnectionPhase;
  facts?: HarnessFact[];
  fix?: { text: string; command?: string };
  fields: { id: string; value: string; set: boolean; source: "saved" | "default" }[];
  actions: { id: string; available: boolean; reason?: string; code?: string }[];
  details?: D;
  sync?: HarnessSyncStatus;
  /** The connection a setting change or action just retired. */
  replaced?: ReplacedConnection | null;
  message?: string;
  link?: string;
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
  /** Local: always. Remote: its worker is ready to serve requests. */
  online: boolean;
  status: "online" | "offline" | "negotiating" | "needs_update";
  /** Explains ongoing negotiation or which side needs a Stage update. */
  statusReason?: string;
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
  /** Complete tasks when their harness closes a successful session. */
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
/** The connection a pairing replaced. */
export interface ReplacedConnection { id: number; label: string; tasks: number; bindings: number }
export interface HarnessChoice {id: string; label: string; available: boolean; reason?: string}
/** How a launch option takes its value: one of `values`, true or false, or a string the harness validates. */
export type HarnessOptionKind = "choice" | "boolean" | "text";
/**
 * A launch option a harness declares (harness-wide, or a model's own). An older harness may omit `kind` (boolean when
 * every value is boolean, else choice) and `label` (reads as the id): lib/options.ts fills them in.
 */
export interface HarnessOption {
  id: string;
  kind?: HarnessOptionKind;
  /** The option's name in the harness's own words. */
  label?: string;
  /** A choice's values; a boolean's true and false; none for text. */
  values: (string | boolean)[];
  /** Display labels keyed by a choice's string values. */
  valueLabels?: Record<string, string>;
  default?: string | boolean | null;
  /** It applies only while another option of the same config has this value (baseRef only with worktree on). */
  dependsOn?: { option: string; value: string | boolean };
}
export interface HarnessModel extends HarnessChoice {
  group: string;
  options: HarnessOption[] | null;
  permissions: HarnessChoice[] | null;
}
/** An operation a harness declares in its capabilities (Go execution.Op). */
export type OperationId =
  | "launch"
  | "stop"
  | "adopt"
  | "reply"
  | "opening"
  | "attention"
  | "images"
  | "respond"
  | "send"
  | "sessionRename"
  | "workspaceAdd"
  | "directoryBrowse";
export interface Capabilities {
  permissions?: HarnessChoice[];
  /** Harness-wide options, beside the selected model's own (ids never collide). */
  options?: HarnessOption[];
  /** The mode choice in the harness's own words ("Agent"); absent reads "Interaction mode". */
  modeLabel?: string;
  operations: Partial<Record<OperationId, {available: boolean; reason?: string}>>;
  models: HarnessModel[] | null;
  modes: HarnessChoice[] | null;
  actions?: HarnessAction[];
  /** How `workspaceAdd` adds a workspace, in the harness's words (ADR 0012). */
  workspaceSources?: WorkspaceSource[];
}
/** One harness-declared way to add a workspace: its input fields and, when set, the confirmation it needs. */
export interface WorkspaceSource {
  id: string;
  label: string;
  help?: string;
  input?: SetupField[];
  /** The harness's confirmation text: send `confirmed: true` only after the person agreed. */
  confirm?: string;
  availability: { available: boolean; reason?: string };
}
/** GET /api/connections/:id/directories: one folder's child folders on the harness's host (at most 500; `parent` "" at the root). */
export interface DirectoryListing {
  path: string;
  parent: string;
  entries: { name: string; path: string }[];
  truncated: boolean;
}
/** POST /api/connections/:id/workspaces. Retry an uncertain outcome (504 `outcome_uncertain`) with the same requestId. */
export interface AddWorkspaceRequest {
  sourceId: string;
  input: Record<string, unknown>;
  requestId: string;
  /** Put the workspace in this project group; omitted lets Stage group it. */
  projectId?: number;
  confirmed?: boolean;
}
/** 201 body: the destination the harness now lists, its group, and whether the harness already had it. */
export interface AddedWorkspace {
  workspace: Destination & { path?: string };
  group: { id: number; title: string };
  existing: boolean;
  message?: string;
  facts?: HarnessFact[];
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
    facts?: HarnessFact[];
  };
}
export interface TaskWorkspace {
  bindingId: number;
  path: string;
  externalId?: string;
  source: "pinned" | "chosen" | "primary";
}

export interface Task {
  links?: ExecutionLinks;
  executionConfig?: ExecutionConfig | null;
 launch?: LaunchRecovery;
  session: SessionRef | null;
  originSession: SessionRef | null;
  connectionId: number;
  bindingId: number | null;
  workspaceId?: string;
  workspace: TaskWorkspace | null;
  harness: Harness;
  /** False once the task's connection was replaced: read-only history, Stage no longer follows it. */
  connectionActive: boolean;
  /** The connection's T3 environment label or OpenCode address. */
  connectionLabel: string;
  acceptanceId: number | null;
  acceptanceRevision: number;
  completedAt: string | null;
  /** Provenance of the current acceptance; null while open. */
  completionProvenance: Provenance | null;
  id: number;
  projectId: number;
  projectTitle: string;
  title: string;
  prompt: string;
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
  /** Tracks an existing harness session (prompt ""); stage never starts, restarts or duplicates it. */
  adopted: boolean;
  /** All of a manual task's needs completed (the ready notification episode); null otherwise. */
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
  /** Append one handoff block per finished need (final reply, branch and session link) to the launch prompt. */
  handoff: boolean;
  /** Images sent with the prompt on every launch, upload order. */
  attachments: Attachment[];
  launchAttempt: number;
  lastError: string | null;
  launchedAt: string | null;
  finishedAt: string | null;
  /** Elapsed run span of the task's session: first run start to newest run completion. */
  runStartedAt: string | null;
  runEndedAt: string | null;
  /** The session's latest assistant reply as one plain line. */
  lastWordPreview: string | null;
  lastWordAt: string | null;
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

export interface WorkAttribution {
  actor?: {
    kind: "human" | "personal" | "agent" | "machine" | "system" | "cron" | "unknown" | "historical";
    userId?: number;
    initiatorUserId?: number;
    credentialAccountId?: number;
    displayName?: string;
  };
  sourceMachineId?: number | null;
  cause?: {
    kind: string;
    taskId?: number;
    acceptanceId?: number;
    scheduleId?: number;
    attempt?: number;
    session?: SessionRef;
    requestId?: string;
  };
}

export interface TaskEvent extends WorkAttribution {
  id: number;
  taskId: number;
  fromState: TaskState | null;
  toState: TaskState;
  detail: string;
  at: string;
}

/** What a session consumed over its life, as its harness records it (cumulative, best effort). */
export interface Usage {
  /** Prompts the session answered or is answering. */
  turns: number;
  /** Uncached input; cache reads and writes are separate. */
  inputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  outputTokens: number;
  /** Time spent on finished turns; 0 when unknown. */
  workingMs: number;
  /** A cost the harness itself reports; null when it reports none (Claude Code never does). */
  costUsd: number | null;
}

export interface TaskDetail extends Task {
  finalReply?: { text: string; truncated: boolean; updatedAt: string; runId: string };
  /** The current session's usage, when its harness reports one. */
  usage?: Usage;
  facts?: HarnessFact[];
  /** Where the current session's harness says it works; absent when it reports neither. */
  reported?: { branch?: string; directory?: string };
  acceptances: Acceptance[];
  events: TaskEvent[];
  /** The task's tree; null outside one. */
  tree: TreeDetail | null;
  /** Tasks the task's origin session created; null without an origin session. */
  /** Handoff records of the current launch attempt, one per need; empty when none. */
  currentHandoffs: Handoff[];
}

export interface AcceptedEvidence {
  session: SessionRef | null;
  taskId: number;
  title: string;
  treeId: number | null;
  treeTitle: string | null;
  projectId: number;
  externalSessionId: string | null;
  sessionLink: string | null;
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
  journalThroughId: number;
}
/** `user`, `historical_settlement`, or a harness closure’s provenance (`HarnessDescriptor.closures`). */
export type Provenance = string;

export interface Acceptance extends WorkAttribution {
  id: number;
  taskId: number;
  acceptedAt: string;
  capturedAt: string;
  provenance: Provenance;
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

/** What a launch attempt appended (or why it appended nothing) from one need. */
export interface Handoff {
  attempt: number;
  status: "sent" | "skipped";
  sourceTaskId: number | null;
  session?: SessionRef;
  sourceTitle: string;
  /** The appended block; "" when skipped. */
  text: string;
  /** Skip reason; null when sent. */
  reason: string | null;
  at: string;
}

// ---- Trees ----

/** A DAG of tasks: each member's needs are members that must be completed before it starts. */
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
  /** Latest of the tree's creation, a member's updatedAt and its session's updatedAt. */
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
  completed: number;
  failed: number;
  interrupted: number;
  cancelled: number;
}

export interface TreeSummary extends Tree, TreeTimes {
  counts: TreeCounts;
  levels: number;
  /** Member project titles, sorted. */
  projects: string[];
  /** When the final member was completed; null while work remains. */
  completedAt: string | null;
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

/** Trees page and API view: active (open, with an incomplete member), done (marked done or all members completed), archived, all. */
export type TreeView = "active" | "done" | "archived" | "all";

export interface TreeListQuery {
  status?: TreeView;
  /** With active: also open trees completed at or after this. */
  completedFrom?: string;
  project?: number;
}

export interface StartTreeResponse {
  started: Task[];
  errors: { taskId: number; error: string }[];
}

/** A pending approval, question or input request a task's session waits on (docs/api.md "Session interaction"). */
export interface SessionRequest {
  id: string;
  kind: "approval" | "question" | "input";
  title: string;
  detail?: string;
  /** Preformatted: the command, path, URL or input the request acts on. */
  preview?: string;
  options?: HarnessChoice[];
  /** The option whose answer may carry `text` (a reason sent with a denial). */
  textOption?: string;
  /** A question request's questions, answered together. */
  questions?: RequestQuestion[];
  createdAt: string;
}

export interface RequestQuestion {
  id: string;
  header?: string;
  prompt: string;
  /** Answered by label. */
  options?: RequestQuestionOption[];
  /** Several options may be picked. */
  multi?: boolean;
  /** Free text is accepted in place of an option. */
  custom?: boolean;
}

export interface RequestQuestionOption {
  label: string;
  description?: string;
  /** Preformatted text the option shows (monospaced). */
  preview?: string;
}

/** Answers a SessionRequest: an option id, text, or both; a question request answers each question id (labels, or one free text). */
export interface RequestResponse {
  option?: string;
  text?: string;
  answers?: Record<string, string[]>;
}

/** A follow-up message into a task's session (POST /tasks/:id/messages); nonblank, at most 32 KiB. */
export interface SendMessageRequest {
  text: string;
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
  /** Null saves an explicitly unresolved backlog item. */
  workspaceId?: string | null;
  executionConfig?: ExecutionConfig;
  projectId?: number;
  title: string;
  prompt: string;
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

export type DuplicateTaskRequest = Pick<CreateTaskRequest, "projectId" | "title" | "prompt" | "executionConfig" | "attachmentIds"> & {
  connectionId: number;
  workspaceId: string | null;
};

export interface UpdateTaskRequest {
  connectionId?: number;
  workspaceId?: string | null;
  executionConfig?: ExecutionConfig;
  projectId?: number;
  title?: string;
  prompt?: string;
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

export interface JournalEntry extends WorkAttribution {
  sourceSession: SessionRef | null;
  id: number;
  kind: "note" | "event";
  taskId: number | null;
  treeId: number | null;
  sourceTaskId: number | null;
  attempt: number | null;
  category: string;
  text: string;
  eventType: string;
  fromTreeId: number | null;
  toTreeId: number | null;
  createdAt: string;
  /** Legacy creator id. Actor attribution is in actor. */
  createdBy: number;
  /** Source machine recorded for the write, when known. */
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
  bindingId: number | null;
  workspaceId?: string;
  originSession: SessionRef | null;
  id: number;
  projectId: number;
  projectTitle: string;
  title: string;
  prompt: string;
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

export interface ScheduleRun extends WorkAttribution {
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
  workspaceId?: string;
  executionConfig?: ExecutionConfig;
  projectId?: number;
  title: string;
  prompt: string;
  notes?: string;
  attachmentIds?: number[];
  cron: string;
  tz: string;
  paused?: boolean;
}

export type UpdateScheduleRequest = Partial<Omit<CreateScheduleRequest, "workspaceId">> & { workspaceId?: string | null };

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
  /** completedAt >= this (ISO date or datetime). */
  completedFrom?: string;
  /** Omitted: open tasks only; true: archived or in a done/archived tree; all: both. */
  closed?: "true" | "all";
}

export interface SessionPreview {
  session: SessionRef;
  task: Task | null;
  project: Project | null;
  title: string;
  workspace?: string;
  unavailableReason?: string;
}

export interface SessionSearchResult {
  session: SessionRef;
  title?: string;
  workspace?: string;
  startedAt?: string;
  eligibility: { available: boolean; reason?: string };
  trackedTaskId?: number;
}

export interface SessionSearchPage {
  sessions: SessionSearchResult[];
  nextCursor?: string;
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
 * defaultConnectionId: the preferred connection in the new task form (null = no preference). Each connection
 * pins its own new-task model (HarnessConnection.newTaskConfig).
 * notifyEvents: events that notify on every device. Shared mode keeps muted events in history as read.
 */
export interface Preferences {
  defaultConnectionId: number | null;
  notifyEvents: NotificationEvent[];
  sharedNotifications?: boolean;
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
  /** Tasks in needs_input + external sessions needing input. */
  needsInput: number;
  /** The signed-in user's unread notification count, in shared mode. */
  unread?: number;
}

// ---- Notifications inbox ----

/** States a notification is sent for. */
export type NotificationEvent = Extract<TaskState, "needs_input" | "done" | "failed"> | "ready";

export interface Notification {
  links?: ExecutionLinks;
  connectionId: number;
  id: number;
  event: NotificationEvent;
  /** null for external sessions (or once the task is deleted). */
  taskId: number | null;
  externalSessionId: string | null;
  /** Snapshot at send time. */
  title: string;
  projectTitle: string | null;
  /** Stored notification destination (historical values are preserved). */
  url: string;
  createdAt: string;
  readAt: string | null;
  /** Live: the task's state, or the external session's. null = unknown. */
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

/** Push tags (`task-<id>`, `session-<connectionId>:<externalSessionId>`) of groups with unread notifications. */
export interface UnreadNotificationTags {
  tags: string[];
}
