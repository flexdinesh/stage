import type {
  AdoptSessionRequest,
  AnswerResult,
  CreateTreeRequest,
  StartTreeResponse,
  TreeDetail,
  TreeList,
  TreeListQuery,
  UpdateTreeRequest,
  PendingQuestions,
  QuestionAnswer,
  Attachment,
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
  TranscriptOpening,
  TranscriptPage,
  TranscriptQuery,
  UnreadNotificationTags,
  UpdateTaskRequest,
  UpdateSnippetRequest,
} from "./types.ts";
import type { StreamHandlers, Transport } from "../platform/index.ts";

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

const post = (body?: unknown): RequestOptions => (body === undefined ? { method: "POST" } : { method: "POST", body });
const patch = (body: unknown): RequestOptions => ({ method: "PATCH", body });
const del: RequestOptions = { method: "DELETE" };

/** Typed Stage API client over a host transport (trusted contract: docs/api.md). */
export type Api = ReturnType<typeof createApi>;

export function createApi(transport: Transport) {
  async function request(path: string, init: RequestOptions): Promise<Response> {
    const headers: Record<string, string> = { Accept: "application/json" };
    const req: RequestInit = { method: init.method ?? "GET", headers };
    if (init.body instanceof FormData) {
      req.body = init.body; // the transport sets the multipart boundary
    } else if (init.body !== undefined) {
      headers["Content-Type"] = "application/json";
      req.body = JSON.stringify(init.body);
    }
    const res = await transport.request(path, req);
    if (!res.ok) {
      const body: unknown = await res.json().catch(() => null);
      throw new ApiError(res.status, errorMessage(body, `${res.status} ${res.statusText}`), errorCode(body));
    }
    return res;
  }

  /** Requests `path` and returns the JSON body typed as `T`. */
  async function apiFetch<T>(path: string, init: RequestOptions = {}): Promise<T> {
    const res = await request(path, init);
    return res.json();
  }

  /** For endpoints that return 204 No Content. */
  async function apiSend(path: string, init: RequestOptions = {}): Promise<void> {
    await request(path, init);
  }

  return {
    health: () => apiFetch<Health>("/health"),
    t3Status: () => apiFetch<T3Status>("/t3/status"),
    pair: (body: PairRequest) => apiFetch<T3Status>("/t3/pair", post(body)),
    sync: () => apiFetch<SyncResult>("/sync", post()),
    catalog: () => apiFetch<Catalog>("/catalog"),

    projects: (archived: ArchivedFilter = "false") => apiFetch<ProjectList>(`/projects${toQuery({ archived })}`),
    project: (id: number) => apiFetch<Project>(`/projects/${id}`),
    createProject: (body: CreateProjectRequest) => apiFetch<Project>("/projects", post(body)),

    tasks: (q: TaskListQuery = {}) =>
      apiFetch<TaskList>(
        `/tasks${toQuery({ project: q.project, state: q.state, tag: q.tag, q: q.q, from: q.from, to: q.to, settledFrom: q.settledFrom, closed: q.closed })}`,
      ),
    task: (id: number) => apiFetch<TaskDetail>(`/tasks/${id}`),
    createTask: (body: CreateTaskRequest) => apiFetch<Task>("/tasks", post(body)),
    updateTask: (id: number, body: UpdateTaskRequest) => apiFetch<Task>(`/tasks/${id}`, patch(body)),
    /** Deletes a task that never started (409 once started: archive it). */
    deleteTask: (id: number) => apiSend(`/tasks/${id}`, del),
    archiveTask: (id: number) => apiFetch<Task>(`/tasks/${id}/archive`, post()),
    unarchiveTask: (id: number) => apiFetch<Task>(`/tasks/${id}/unarchive`, post()),
    startTask: (id: number) => apiFetch<Task>(`/tasks/${id}/start`, post()),
    followupTask: (id: number, body: FollowupRequest) => apiFetch<Task>(`/tasks/${id}/followup`, post(body)),
    stopTask: (id: number) => apiFetch<Task>(`/tasks/${id}/stop`, post()),
    duplicateTask: (id: number) => apiFetch<Task>(`/tasks/${id}/duplicate`, post()),
    /** Replaces a backlog task's needs ([] clears them); a task outside a tree joins the needs' tree. */
    setNeeds: (id: number, needs: number[]) => apiFetch<Task>(`/tasks/${id}/needs`, { method: "PUT", body: { needs } }),

    trees: (q: TreeListQuery = {}) =>
      apiFetch<TreeList>(`/trees${toQuery({ status: q.status, settledFrom: q.settledFrom, project: q.project })}`),
    tree: (id: number) => apiFetch<TreeDetail>(`/trees/${id}`),
    createTree: (body: CreateTreeRequest) => apiFetch<TreeDetail>("/trees", post(body)),
    updateTree: (id: number, body: UpdateTreeRequest) => apiFetch<TreeDetail>(`/trees/${id}`, patch(body)),
    /** Deletes a tree whose members never started, with its tasks (409 otherwise: archive it). */
    deleteTree: (id: number) => apiSend(`/trees/${id}`, del),
    /** Dissolves the tree: its tasks stay, edges go. */
    dissolveTree: (id: number) => apiSend(`/trees/${id}/dissolve`, post()),
    /** Adds a task (a member of another tree moves); needs omitted keeps its needs. */
    addToTree: (id: number, taskId: number, needs?: number[]) =>
      apiFetch<TreeDetail>(`/trees/${id}/tasks`, post(needs === undefined ? { taskId } : { taskId, needs })),
    /** Splices the task out: its dependents take over its needs. */
    removeFromTree: (id: number, taskId: number) => apiSend(`/trees/${id}/tasks/${taskId}`, del),
    /** Starts every ready member. */
    startTree: (id: number) => apiFetch<StartTreeResponse>(`/trees/${id}/start`, post()),

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
    /** Tracks a top-level session as a task (201 new, 200 when a task already owns it). */
    adoptThread: (id: string, body: AdoptSessionRequest = {}) =>
      apiFetch<Task>(`/threads/${encodeURIComponent(id)}/adopt`, post(body)),
    transcript: (id: string, q: TranscriptQuery = {}) =>
      apiFetch<TranscriptPage>(`/threads/${encodeURIComponent(id)}/transcript${toQuery({ before: q.before, limit: q.limit })}`),
    transcriptItem: (id: string, itemId: string) =>
      apiFetch<TranscriptItemText>(`/threads/${encodeURIComponent(id)}/transcript/items/${encodeURIComponent(itemId)}`),
    transcriptOpening: (id: string) => apiFetch<TranscriptOpening>(`/threads/${encodeURIComponent(id)}/transcript/opening`),
    /** Live transcript upserts (server-sent events) after cursor `after`. */
    transcriptStream: (id: string, after: number, handlers: StreamHandlers) =>
      transport.subscribe(`/threads/${encodeURIComponent(id)}/transcript/stream${toQuery({ after })}`, handlers),
    questions: (id: string) => apiFetch<PendingQuestions>(`/threads/${encodeURIComponent(id)}/questions`),
    answerQuestion: (id: string, requestId: string, answers: Record<string, QuestionAnswer>) =>
      apiFetch<AnswerResult>(
        `/threads/${encodeURIComponent(id)}/questions/${encodeURIComponent(requestId)}/answer`,
        post({ answers }),
      ),

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

    /** Image bytes URL (immutable; safe to cache). */
    attachmentUrl: (id: number) => transport.assetUrl(`/attachments/${id}`),
  };
}

export type ArchivedFilter = "true" | "false" | "all";
