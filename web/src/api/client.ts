import type {
  Attachment,
  Badge,
  Catalog,
  CreateProjectRequest,
  CreateTaskRequest,
  CreateSnippetRequest,
  FollowupRequest,
  Health,
  ModelDefaults,
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
  ThreadListQuery,
  UpdateProjectRequest,
  UpdateTaskRequest,
  UpdateSnippetRequest,
} from "./types.ts";

/** Non-2xx API response; `message` is the server's `{ error }` text. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type QueryValue = string | number | boolean | readonly (string | number)[] | undefined;

/** Builds `?a=1&b=x,y`, skipping undefined and empty values. Arrays join with commas. */
export function toQuery(params: Record<string, QueryValue>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined) continue;
    const text = Array.isArray(value) ? value.join(",") : String(value);
    if (text !== "") search.set(key, text);
  }
  const query = search.toString();
  return query === "" ? "" : `?${query}`;
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
    throw new ApiError(res.status, errorMessage(body, `${res.status} ${res.statusText}`));
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
  updateProject: (id: number, body: UpdateProjectRequest) => apiFetch<Project>(`/projects/${id}`, patch(body)),

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

  uploadImage: (file: File) => {
    const form = new FormData();
    form.append("file", file);
    return apiFetch<Attachment>("/uploads", post(form));
  },
  deleteAttachment: (id: number) => apiSend(`/attachments/${id}`, del),

  threads: (q: ThreadListQuery = {}) =>
    apiFetch<ThreadList>(
      `/threads${toQuery({
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
        limit: q.limit,
        offset: q.offset,
      })}`,
    ),


  snippets: () => apiFetch<SnippetList>("/snippets"),
  createSnippet: (body: CreateSnippetRequest) => apiFetch<Snippet>("/snippets", post(body)),
  updateSnippet: (id: number, body: UpdateSnippetRequest) => apiFetch<Snippet>(`/snippets/${id}`, patch(body)),
  deleteSnippet: (id: number) => apiSend(`/snippets/${id}`, del),

  preferences: () => apiFetch<Preferences>("/preferences"),
  setPinned: (body: ModelDefaults | null) => apiFetch<Preferences>("/preferences/pinned", { method: "PUT", body }),

  stats: (q: StatsQuery = {}) => apiFetch<Stats>(`/stats${toQuery({ from: q.from, to: q.to, tz: q.tz, subagents: q.subagents })}`),

  pushConfig: () => apiFetch<PushConfig>("/push/config"),
  subscribePush: (body: PushSubscriptionBody) => apiSend("/push/subscriptions", post(body)),
  unsubscribePush: (body: PushEndpointBody) => apiSend("/push/subscriptions", { method: "DELETE", body }),
  testPush: (body: PushEndpointBody) => apiSend("/push/test", post(body)),
  badge: () => apiFetch<Badge>("/badge"),
  notifications: (q: { limit?: number; before?: number | undefined } = {}) =>
    apiFetch<NotificationPage>(`/notifications${toQuery({ limit: q.limit, before: q.before })}`),
  markNotificationsRead: (upTo: number) => apiSend("/notifications/read", post({ upTo })),
};

export type ArchivedFilter = "true" | "false" | "all";

/** Image bytes URL (immutable; safe to cache). */
export const attachmentUrl = (id: number) => `/api/attachments/${id}`;
