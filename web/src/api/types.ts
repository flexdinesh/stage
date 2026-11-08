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

export type ProjectSource = "app" | "t3";
export type ThreadSource = "app" | "t3";

// ---- Shared ----

export interface ApiErrorBody {
  error: string;
}

/** T3 ProviderOptionSelection, e.g. { id: "effort", value: "low" }. */
export interface ModelOption {
  id: string;
  value: string | boolean;
}

export type Workspace =
  | { type: "root" }
  | { type: "worktree"; baseRef: string; branch: string | null };

// ---- Health ----

export interface Health {
  status: "ok" | "error";
  db: "ok" | "error";
}

// ---- T3 connection / sync / catalog ----

export interface T3Status {
  connected: boolean;
  t3Url: string;
  publicUrl: string;
  environmentId: string | null;
  clientId: string | null;
  tokenExpiresAt: string | null;
  adminTokenConfigured: boolean;
  lastError: string | null;
  lastFullSyncAt: string | null;
}

export interface PairRequest {
  code: string;
}

export interface SyncResult {
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

// ---- Projects ----

export interface Project {
  id: number;
  t3ProjectId: string;
  title: string;
  workspaceRoot: string;
  source: ProjectSource;
  archivedAt: string | null;
  createdAt: string;
  updatedAt: string;
  /** Latest thread update/settle or task update; null when none. */
  lastActivityAt: string | null;
}

export interface ProjectList {
  projects: Project[];
}

export interface CreateProjectRequest {
  title: string;
  workspaceRoot: string;
}

export interface UpdateProjectRequest {
  title?: string;
  archived?: boolean;
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

export interface Task {
  id: number;
  projectId: number;
  projectTitle: string;
  title: string;
  prompt: string;
  providerInstanceId: string | null;
  model: string | null;
  modelOptions: ModelOption[];
  runtimeMode: RuntimeMode;
  interactionMode: InteractionMode;
  workspace: Workspace;
  tags: string[];
  notes: string;
  state: TaskState;
  t3ThreadId: string | null;
  /** Runs after this task (launches when it settles). */
  afterTaskId: number | null;
  /** The task that runs after this one. */
  nextTaskId: number | null;
  /** Images sent with the prompt on every launch, upload order. */
  attachments: Attachment[];
  launchAttempt: number;
  lastError: string | null;
  t3WebUrl: string | null;
  t3AppUrl: string | null;
  launchedAt: string | null;
  finishedAt: string | null;
  settledAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface TaskEvent {
  id: number;
  taskId: number;
  fromState: TaskState | null;
  toState: TaskState;
  detail: string;
  at: string;
}

export interface TaskDetail extends Task {
  events: TaskEvent[];
  thread: Thread | null;
  /** Linked run order containing this task, head first (just this task when unlinked). */
  sequence: Task[];
}

export interface TaskList {
  tasks: Task[];
}

export interface CreateTaskRequest {
  projectId: number;
  title: string;
  prompt: string;
  providerInstanceId?: string | null;
  model?: string | null;
  modelOptions?: ModelOption[];
  runtimeMode?: RuntimeMode;
  interactionMode?: InteractionMode;
  workspace?: Workspace;
  tags?: string[];
  notes?: string;
  /** Run after this task (start is then ignored). */
  afterTaskId?: number | null;
  /** Staged uploads to attach (max 8). */
  attachmentIds?: number[];
  start?: boolean;
}

export interface UpdateTaskRequest {
  projectId?: number;
  title?: string;
  prompt?: string;
  providerInstanceId?: string | null;
  model?: string | null;
  modelOptions?: ModelOption[];
  runtimeMode?: RuntimeMode;
  interactionMode?: InteractionMode;
  workspace?: Workspace;
  tags?: string[];
  notes?: string;
  /** null unlinks. */
  afterTaskId?: number | null;
  /** Backlog only: the exact image set (task's own ids + staged uploads). */
  attachmentIds?: number[];
}

export interface FollowupRequest {
  message: string;
  /** Not allowed with attachmentIds. */
  mode?: SendMode;
  /** Staged uploads to send with the message. */
  attachmentIds?: number[];
}

export interface TaskListQuery {
  project?: number;
  state?: TaskState[];
  tag?: string;
  q?: string;
  from?: string;
  to?: string;
  /** settledAt >= this (ISO date or datetime). */
  settledFrom?: string;
}

// ---- Threads (history) ----

export interface Thread {
  t3ThreadId: string;
  t3ProjectId: string;
  projectId: number | null;
  projectTitle: string | null;
  title: string;
  status: ThreadStatus;
  settled: boolean;
  settledAt: string | null;
  pendingRequestCount: number;
  providerInstanceId: string | null;
  model: string | null;
  runtimeMode: RuntimeMode | null;
  interactionMode: InteractionMode | null;
  branch: string | null;
  worktreePath: string | null;
  parentThreadId: string | null;
  relationship: string | null;
  creationSource: string | null;
  source: ThreadSource;
  taskId: number | null;
  t3WebUrl: string | null;
  t3AppUrl: string | null;
  createdAt: string;
  updatedAt: string;
  syncedAt: string;
}

export interface ThreadList {
  threads: Thread[];
  total: number;
}

export type SubagentsFilter = "include" | "exclude";

export interface ThreadListQuery {
  project?: number;
  status?: ThreadStatus[];
  settled?: boolean;
  provider?: string;
  source?: ThreadSource;
  q?: string;
  from?: string;
  to?: string;
  subagents?: SubagentsFilter;
  /** Only external threads needing input (the ones the badge counts). */
  needsInput?: boolean;
  limit?: number;
  offset?: number;
}

// ---- Templates ----

export interface Template {
  id: number;
  name: string;
  prompt: string;
  providerInstanceId: string | null;
  model: string | null;
  modelOptions: ModelOption[];
  runtimeMode: RuntimeMode;
  interactionMode: InteractionMode;
  createdAt: string;
  updatedAt: string;
}

export interface TemplateList {
  templates: Template[];
}

export interface CreateTemplateRequest {
  name: string;
  prompt?: string;
  providerInstanceId?: string | null;
  model?: string | null;
  modelOptions?: ModelOption[];
  runtimeMode?: RuntimeMode;
  interactionMode?: InteractionMode;
}

export type UpdateTemplateRequest = Partial<CreateTemplateRequest>;

// ---- Stats ----

export interface DayCount {
  day: string;
  count: number;
}

export interface ProjectCount {
  projectId: number | null;
  t3ProjectId: string;
  title: string;
  count: number;
}

export interface StatusCount {
  status: ThreadStatus;
  count: number;
}

export interface StateCount {
  state: TaskState;
  count: number;
}

export interface Stats {
  from: string;
  to: string;
  tz: string;
  threadsByDay: DayCount[];
  threadsByProject: ProjectCount[];
  threadsByStatus: StatusCount[];
  tasksByState: StateCount[];
  needsInput: number;
}

export interface StatsQuery {
  from?: string;
  to?: string;
  tz?: string;
  subagents?: SubagentsFilter;
}

// ---- Preferences ----

/** New-task model preset. providerInstanceId/model null = T3 default. */
export interface ModelDefaults {
  providerInstanceId: string | null;
  model: string | null;
  modelOptions: ModelOption[];
  runtimeMode: RuntimeMode;
  interactionMode: InteractionMode;
}

/** New task prefills from `pinned`, else `lastUsed` (the latest created task). */
export interface Preferences {
  pinned: ModelDefaults | null;
  lastUsed: ModelDefaults | null;
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
