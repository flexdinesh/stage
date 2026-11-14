import type {
  AnswerResult,
  PendingQuestions,
  QuestionAnswer,
  Attachment,
  Continuation,
  ContinuationList,
  CreateContinuationRequest,
  ThreadDetail,
  Badge,
  Catalog,
  CreateProjectRequest,
  CreateTaskRequest,
  CreateSnippetRequest,
  CreateScheduleRequest,
  Schedule,
  ScheduleDetail,
  ScheduleList,
  ScheduleListQuery,
  SchedulePreview,
  SchedulePreviewRequest,
  UpdateScheduleRequest,
  FollowupRequest,
  Health,
  ModelDefaults,
  NotificationEvent,
  NotificationGroupPage,
  NotificationPage,
  OrderTasksResponse,
  PairRequest,
  Preferences,
  Project,
  ProjectList,
  PushConfig,
  PushEndpointBody,
  PushSubscriptionBody,
  Stats,
  StatsQuery,
  SyncResult,
  T3Status,
  Task,
  TaskDetail,
  TaskList,
  TaskListQuery,
  Snippet,
  SnippetList,
  ThreadList,
  ThreadSearch,
  ThreadListQuery,
  TranscriptItemText,
  TranscriptPage,
  TranscriptQuery,
  UnreadNotificationTags,
  UpdateTaskRequest,
  UpdateSnippetRequest,
} from "./types.ts";

/** Non-2xx API response; `message` is the server's `{ error }` text, `code` its optional reason. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string | null;

  constructor(status: number, message: string, code: string | null = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

type QueryValue = string | number | boolean | readonly (string | number)[] | undefined;

/** Builds `?a=1&b=x,y`, skipping undefined and empty values. Arrays join with commas. */
export function toQuery(params: Record<string, QueryValue>, repeatable: readonly string[] = []): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined) continue;
    if (Array.isArray(value) && repeatable.includes(key)) {
      for (const item of value) search.append(key, String(item));
      continue;
    }
    const text = Array.isArray(value) ? value.join(",") : String(value);
    if (text !== "") search.set(key, text);
  }
  const query = search.toString();
  return query === "" ? "" : `?${query}`;
}

function errorCode(body: unknown): string | null {
  if (typeof body === "object" && body !== null && "code" in body && typeof body.code === "string") return body.code;
  return null;
}

function errorMessage(body: unknown, fallback: string): string {
  if (typeof body === "object" && body !== null && "error" in body && typeof body.error === "string") {
    return body.error;
  }
  return fallback;
}

type RequestOptions = { method?: string; body?: unknown };

async function request(path: string, init: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const req: RequestInit = { method: init.method ?? "GET", headers };
  if (init.body instanceof FormData) {
    req.body = init.body; // fetch sets the multipart boundary
  } else if (init.body !== undefined) {
    headers["Content-Type"] = "application/json";
    req.body = JSON.stringify(init.body);
  }
  const res = await fetch(`/api${path}`, req);
  if (!res.ok) {
    const body: unknown = await res.json().catch(() => null);
    throw new ApiError(res.status, errorMessage(body, `${res.status} ${res.statusText}`), errorCode(body));
  }
  return res;
}

/** Fetches `/api{path}` and returns the JSON body typed as `T` (trusted contract: docs/api.md). */
export async function apiFetch<T>(path: string, init: RequestOptions = {}): Promise<T> {
  const res = await request(path, init);
  return res.json();
}

/** For endpoints that return 204 No Content. */
export async function apiSend(path: string, init: RequestOptions = {}): Promise<void> {
  await request(path, init);
}

export const getHealth = () => apiFetch<Health>("/health");

// ---- Endpoints (docs/api.md) ----

const post = (body?: unknown): RequestOptions => (body === undefined ? { method: "POST" } : { method: "POST", body });
const patch = (body: unknown): RequestOptions => ({ method: "PATCH", body });
const del: RequestOptions = { method: "DELETE" };

export const api = {
  t3Status: () => apiFetch<T3Status>("/t3/status"),
  pair: (body: PairRequest) => apiFetch<T3Status>("/t3/pair", post(body)),
  sync: () => apiFetch<SyncResult>("/sync", post()),
  catalog: () => apiFetch<Catalog>("/catalog"),

  projects: (archived: ArchivedFilter = "false") => apiFetch<ProjectList>(`/projects${toQuery({ archived })}`),
  project: (id: number) => apiFetch<Project>(`/projects/${id}`),
  createProject: (body: CreateProjectRequest) => apiFetch<Project>("/projects", post(body)),

  tasks: (q: TaskListQuery = {}) =>
    apiFetch<TaskList>(
      `/tasks${toQuery({ project: q.project, state: q.state, tag: q.tag, q: q.q, from: q.from, to: q.to, settledFrom: q.settledFrom })}`,
    ),
  task: (id: number) => apiFetch<TaskDetail>(`/tasks/${id}`),
  createTask: (body: CreateTaskRequest) => apiFetch<Task>("/tasks", post(body)),
  updateTask: (id: number, body: UpdateTaskRequest) => apiFetch<Task>(`/tasks/${id}`, patch(body)),
  deleteTask: (id: number) => apiSend(`/tasks/${id}`, del),
  startTask: (id: number) => apiFetch<Task>(`/tasks/${id}/start`, post()),
  followupTask: (id: number, body: FollowupRequest) => apiFetch<Task>(`/tasks/${id}/followup`, post(body)),
  stopTask: (id: number) => apiFetch<Task>(`/tasks/${id}/stop`, post()),
  duplicateTask: (id: number) => apiFetch<Task>(`/tasks/${id}/duplicate`, post()),
  /** Links backlog tasks to run in this order (each after the previous one). */
  orderTasks: (taskIds: number[]) => apiFetch<OrderTasksResponse>("/tasks/order", post({ taskIds })),

  schedules: (q: ScheduleListQuery = {}) =>
    apiFetch<ScheduleList>(`/schedules${toQuery({ project: q.project, dueWithin: q.dueWithin })}`),
  schedule: (id: number) => apiFetch<ScheduleDetail>(`/schedules/${id}`),
  createSchedule: (body: CreateScheduleRequest) => apiFetch<Schedule>("/schedules", post(body)),
  updateSchedule: (id: number, body: UpdateScheduleRequest) => apiFetch<Schedule>(`/schedules/${id}`, patch(body)),
  deleteSchedule: (id: number) => apiSend(`/schedules/${id}`, del),
  runSchedule: (id: number) => apiFetch<Task>(`/schedules/${id}/run`, post()),
  previewSchedule: (body: SchedulePreviewRequest) => apiFetch<SchedulePreview>("/schedules/preview", post(body)),

  uploadImage: (file: File) => {
    const form = new FormData();
    form.append("file", file);
    return apiFetch<Attachment>("/uploads", post(form));
  },
  deleteAttachment: (id: number) => apiSend(`/attachments/${id}`, del),

  threads: (q: ThreadListQuery = {}) =>
    apiFetch<ThreadList>(
      `/threads${toQuery({
        active: q.active === true ? 1 : undefined,
        sort: q.sort,
        parent: q.parent,
        state: q.state,
        project: q.project,
        status: q.status,
        settled: q.settled,
        provider: q.provider,
        source: q.source,
        q: q.q,
        from: q.from,
        to: q.to,
        subagents: q.subagents,
        needsInput: q.needsInput,
        running: q.running,
        settledFrom: q.settledFrom,
        limit: q.limit,
        offset: q.offset,
      }, ["state"])}`,
    ),

  searchThreads: (q: string, opts: { project?: number; limit?: number } = {}) =>
    apiFetch<ThreadSearch>(`/threads/search${toQuery({ q, project: opts.project, limit: opts.limit })}`),
  thread: (id: string) => apiFetch<ThreadDetail>(`/threads/${encodeURIComponent(id)}`),
  transcript: (id: string, q: TranscriptQuery = {}) =>
    apiFetch<TranscriptPage>(`/threads/${encodeURIComponent(id)}/transcript${toQuery({ before: q.before, limit: q.limit })}`),
  transcriptItem: (id: string, itemId: string) =>
    apiFetch<TranscriptItemText>(`/threads/${encodeURIComponent(id)}/transcript/items/${encodeURIComponent(itemId)}`),
  /** SSE URL (not fetched: opened with EventSource). */
  transcriptStreamUrl: (id: string, after: number) =>
    `/api/threads/${encodeURIComponent(id)}/transcript/stream${toQuery({ after })}`,
  questions: (id: string) => apiFetch<PendingQuestions>(`/threads/${encodeURIComponent(id)}/questions`),
  answerQuestion: (id: string, requestId: string, answers: Record<string, QuestionAnswer>) =>
    apiFetch<AnswerResult>(
      `/threads/${encodeURIComponent(id)}/questions/${encodeURIComponent(requestId)}/answer`,
      post({ answers }),
    ),
  continuations: (thread?: string) => apiFetch<ContinuationList>(`/continuations${toQuery({ thread })}`),
  createContinuation: (body: CreateContinuationRequest) => apiFetch<Continuation>("/continuations", post(body)),
  updateContinuation: (id: number, prompt: string) => apiFetch<Continuation>(`/continuations/${id}`, patch({ prompt })),
  continuationAction: (id: number, action: "pause" | "resume" | "cancel") =>
    apiFetch<Continuation>(`/continuations/${id}/${action}`, post()),

  snippets: () => apiFetch<SnippetList>("/snippets"),
  createSnippet: (body: CreateSnippetRequest) => apiFetch<Snippet>("/snippets", post(body)),
  updateSnippet: (id: number, body: UpdateSnippetRequest) => apiFetch<Snippet>(`/snippets/${id}`, patch(body)),
  deleteSnippet: (id: number) => apiSend(`/snippets/${id}`, del),

  preferences: () => apiFetch<Preferences>("/preferences"),
  setPinned: (body: ModelDefaults | null) => apiFetch<Preferences>("/preferences/pinned", { method: "PUT", body }),
  setNotifyEvents: (events: NotificationEvent[]) =>
    apiFetch<Preferences>("/preferences/notifications", { method: "PUT", body: { events } }),

  stats: (q: StatsQuery = {}) => apiFetch<Stats>(`/stats${toQuery({ from: q.from, to: q.to, tz: q.tz, subagents: q.subagents })}`),

  pushConfig: () => apiFetch<PushConfig>("/push/config"),
  subscribePush: (body: PushSubscriptionBody) => apiSend("/push/subscriptions", post(body)),
  unsubscribePush: (body: PushEndpointBody) => apiSend("/push/subscriptions", { method: "DELETE", body }),
  testPush: (body: PushEndpointBody) => apiSend("/push/test", post(body)),
  badge: () => apiFetch<Badge>("/badge"),
  notifications: (q: { limit?: number; before?: number | undefined; group?: string } = {}) =>
    apiFetch<NotificationPage>(`/notifications${toQuery({ limit: q.limit, before: q.before, group: q.group })}`),
  notificationGroups: (q: { limit?: number; cursor?: string | undefined } = {}) =>
    apiFetch<NotificationGroupPage>(`/notifications/groups${toQuery({ limit: q.limit, cursor: q.cursor })}`),
  markNotificationsRead: (upTo: number) => apiSend("/notifications/read", post({ upTo })),
  markNotificationGroupsRead: (groups: string[]) => apiSend("/notifications/read", post({ groups })),
  unreadNotificationTags: () => apiFetch<UnreadNotificationTags>("/notifications/unread-tags"),
};

export type ArchivedFilter = "true" | "false" | "all";

/** Image bytes URL (immutable; safe to cache). */
export const attachmentUrl = (id: number) => `/api/attachments/${id}`;
