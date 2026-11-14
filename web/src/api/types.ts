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
  /** Machine-readable reason on some 409s (e.g. already_answered, request_stale). */
  code?: string;
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
  /** One-shot start (UTC instant) of a backlog task; null once launched or when unset. */
  startAt: string | null;
  /** IANA zone the start time was entered in (set with startAt). */
  startTz: string | null;
  /** Recurring schedule that spawned this task. */
  scheduleId: number | null;
  scheduleTitle: string | null;
  /** T3 session that created the task (agent CLI/MCP); null otherwise. */
  originThreadId: string | null;
  originThreadTitle: string | null;
  /** Runs after this T3 session (launches when it settles); not set together with afterTaskId. */
  afterThreadId: string | null;
  afterThreadTitle: string | null;
  /** Append the predecessor's handoff block (final reply, PRs, branch, thread link) to the launch prompt. */
  handoff: boolean;
  /** Images sent with the prompt on every launch, upload order. */
  attachments: Attachment[];
  launchAttempt: number;
  lastError: string | null;
  t3WebUrl: string | null;
  t3AppUrl: string | null;
  /** t3code://threads/<env>/<thread>: opens the thread in the T3 desktop/mobile app. */
  t3DeepLink: string | null;
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
  pullRequests: PullRequest[];
  /** Answerable questions the task's thread is waiting on (approvals excluded). */
  pendingQuestionCount: number;
  createdAt: string;
  updatedAt: string;
}

/** A pull request T3 associates with a thread. `state` is T3's string (open, merged, closed, draft, ...). */
export interface PullRequest {
  host: string;
  /** owner/name */
  repository: string;
  number: number;
  url: string;
  /** created | agent | linked */
  source: string;
  state: string;
}

/** One thread run (each follow-up is a new run), newest first in `recentRuns`. */
export interface ThreadRun {
  runId: string;
  ordinal: number;
  status: ThreadStatus;
  providerInstanceId: string | null;
  /** Requested model. */
  model: string | null;
  /** Model the provider ran; null when T3 did not report it. */
  executedModel: string | null;
  requestedAt: string;
  startedAt: string | null;
  completedAt: string | null;
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
  /** Tasks the task's origin session created; null without an origin session. */
  batch: CreatedTasks | null;
  /** Handoff record of the current launch attempt; null when none was built. */
  currentHandoff: Handoff | null;
}

/** What a launch attempt appended (or why it appended nothing) from its predecessor. */
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

/** A session batch: tasks one session created, grouped by run order. */
export interface CreatedTasks {
  /** Runs of batch tasks linked by run order, head first; ordered by head id. */
  chains: Task[][];
  /** Batch tasks with no batch predecessor or follower. */
  independent: Task[];
}

export interface OrderTasksResponse {
  /** Resulting run order, head first. */
  tasks: Task[];
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
  /** Append the predecessor's handoff at launch (server default true). */
  handoff?: boolean;
  /** Staged uploads to attach (max 8). */
  attachmentIds?: number[];
  /** One-shot start, wall time "YYYY-MM-DDTHH:mm" in startTz (not with afterTaskId or start). */
  startLocal?: string | null;
  startTz?: string | null;
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
  /** null unlinks the session wait. */
  afterThreadId?: string | null;
  /** Backlog only: append the predecessor's handoff at launch. */
  handoff?: boolean;
  /** Backlog only: the exact image set (task's own ids + staged uploads). */
  attachmentIds?: number[];
  /** Backlog only: one-shot start; both null clears. */
  startLocal?: string | null;
  startTz?: string | null;
}

// ---- Schedules ----

export const SCHEDULE_RUN_OUTCOMES = ["started", "skipped", "missed", "manual"] as const;
export type ScheduleRunOutcome = (typeof SCHEDULE_RUN_OUTCOMES)[number];

/** Recurring task template: each cron fire spawns a task with this config. */
export interface Schedule {
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
  /** T3 session that created the schedule (agent CLI/MCP); spawned tasks inherit it. */
  originThreadId: string | null;
  createdAt: string;
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
  state: TaskState;
  archived: boolean;
  activeRunId: string | null;
  latestRunId: string | null;
  settled: boolean;
  settledAt: string | null;
  /** Elapsed run span: first run start to newest run completion. */
  runStartedAt: string | null;
  runEndedAt: string | null;
  /** Runs T3 reported for the thread; 0 until a detail read. */
  runCount: number;
  pullRequests: PullRequest[];
  pendingRequestCount: number;
  /** Pending requests stage can answer (user-input questions); the rest are approvals handled in T3. */
  pendingQuestionCount: number;
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
  /** t3code://threads/<env>/<thread>: opens the thread in the T3 desktop/mobile app. */
  t3DeepLink: string | null;
  createdAt: string;
  updatedAt: string;
  syncedAt: string;
  /** Latest assistant reply as one plain line. */
  lastWordPreview: string | null;
  lastWordAt: string | null;
  /** Reply excerpt (markdown); set on detail reads only, null in lists. */
  lastWord: LastWord | null;
  /** Runs, newest first (at most 50; `runCount` is the T3 total); set on detail reads only, null in lists. */
  recentRuns: ThreadRun[] | null;
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

export interface ThreadList {
  threads: Thread[];
  total: number;
}

/** Why a session matched: title/branch/project/pr (title-ish) or user/assistant (message text). */
export type MatchSource = "title" | "branch" | "project" | "pr" | "user" | "assistant";
/** T3 half of a session search: anything but ok means stage title matches only. */
export type T3SearchStatus = "ok" | "offline" | "timeout" | "error";

export interface ThreadSearchResult {
  t3ThreadId: string;
  title: string;
  projectTitle: string | null;
  matchSource: MatchSource;
  /** Message text around the match (message matches only). */
  snippet: string | null;
  messageCreatedAt: string | null;
  t3DeepLink: string | null;
  /** null until stage syncs the session. */
  thread: Thread | null;
}

export interface ThreadSearch {
  results: ThreadSearchResult[];
  t3: T3SearchStatus;
}

export type SubagentsFilter = "include" | "exclude";

export interface ThreadListQuery {
  active?: boolean;
  sort?: "activity";
  parent?: string;
  state?: TaskState[];
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
  /** Only external threads observed running. */
  running?: boolean;
  /** Only external threads settled at or after this (ISO), newest settle first. */
  settledFrom?: string;
  limit?: number;
  offset?: number;
}

// ---- Session run order ----

export type ContinuationState = "waiting" | "paused" | "blocked" | "sending" | "running" | "awaiting_settle" | "settled" | "cancelled" | "uncertain";
export type WaitReason = "predecessor_unsettled" | "target_running" | "target_needs_input" | "predecessor_failed" | "target_failed" | "upstream_pending" | "upstream_cancelled" | "thread_missing" | "t3_offline" | "delivery_uncertain" | "legacy_conflict";

export interface Continuation {
  id: number;
  afterThreadId: string;
  targetThreadId: string;
  previousContinuationId: number | null;
  prompt: string;
  state: ContinuationState;
  waitReasons: WaitReason[];
  lastError: string | null;
  runId: string | null;
  messageId: string | null;
  createdAt: string;
  updatedAt: string;
  sentAt: string | null;
  settledAt: string | null;
  afterThread: Thread;
  targetThread: Thread;
}

export interface CreateContinuationRequest {
  afterThreadId: string;
  targetThreadId: string;
  prompt: string;
}

export interface ContinuationList {
  continuations: Continuation[];
}

export interface ThreadDetail {
  thread: Thread;
  children: Thread[];
  continuations: Continuation[];
  task: Task | null;
  /** Tasks this session created (agent CLI/MCP). */
  createdTasks: CreatedTasks;
}

// ---- Transcript (docs/api.md "Transcript") ----

export const TRANSCRIPT_KINDS = ["user", "assistant", "plan"] as const;
export type TranscriptKind = (typeof TRANSCRIPT_KINDS)[number];

/** One message; an upsert keyed by `position` (newer `updatedAt` wins). */
export interface TranscriptItem {
  position: number;
  itemId: string;
  sourceThreadId: string;
  inherited: boolean;
  kind: TranscriptKind;
  status: string;
  streaming: boolean;
  createdBy: string | null;
  runId: string | null;
  /** At most 2000 chars; `truncated` = full text is longer. */
  text: string;
  truncated: boolean;
  updatedAt: string;
}

export interface TranscriptPage {
  items: TranscriptItem[];
  hasEarlier: boolean;
  /** Stream resume point; null on a `before` page, -1 when empty. */
  cursor: number | null;
  active: boolean;
  status: ThreadStatus;
}

export interface TranscriptQuery {
  before?: number;
  limit?: number;
}

/** SSE `state` event. */
export interface TranscriptState {
  active: boolean;
  status: ThreadStatus;
  error: string | null;
}

export interface TranscriptItemText {
  itemId: string;
  text: string;
  truncated: boolean;
}

// ---- Pending questions ----

/** T3 user-input question option; `value` absent = answered by label. */
export interface QuestionOption {
  label: string;
  description: string;
  value?: string | null;
}

export interface Question {
  id: string;
  header: string;
  question: string;
  options: QuestionOption[];
  multiSelect?: boolean | null;
  /** Free text allowed unless false. */
  allowCustomAnswer?: boolean | null;
  initialAnswer?: string | null;
  required?: boolean | null;
}

export interface PendingRequest {
  requestId: string;
  questions: Question[];
}

export interface PendingQuestions {
  pendingRequestCount: number;
  /** Pending permission requests stage cannot resolve (handled in T3). */
  approvalCount: number;
  requests: PendingRequest[];
}

/** An option value/label or custom text; an array of options for multi-select. */
export type QuestionAnswer = string | string[];

export interface AnswerResult {
  requestId: string;
  respondedAt: string;
  thread: Thread;
}

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
/** notifyEvents: events that notify (inbox + push) on every device; others are dropped. */
export interface Preferences {
  pinned: ModelDefaults | null;
  lastUsed: ModelDefaults | null;
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
export type NotificationEvent = Extract<TaskState, "needs_input" | "done" | "failed" | "settled">;

export interface Notification {
  id: number;
  event: NotificationEvent;
  /** null for external threads (or once the task is deleted). */
  taskId: number | null;
  t3ThreadId: string | null;
  /** Snapshot at send time. */
  title: string;
  projectTitle: string | null;
  /** In-app path (task detail or filtered History). */
  url: string;
  createdAt: string;
  readAt: string | null;
  /** Live: the task's state, or the external thread's (`settled` when settled). null = unknown. */
  currentState: TaskState | null;
  /** Answerable questions the session is waiting on now. */
  pendingQuestionCount: number;
  t3DeepLink: string | null;
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

export interface NotificationGroupPage {
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
