import type {
  OpenCodeStatus, HarnessConnection, Capabilities, ExecutionLinks, TaskState,
  CompletionPreview,
  AcceptanceRequest,
  AcceptanceResult,
  AppendNoteInput,
  JournalEntry,
  JournalOwner,
  JournalPage,
  JournalQuery,
  AdoptSessionRequest,
  CreateTreeRequest,
  StartTreeResponse,
  TreeDetail,
  TreeList,
  TreeListQuery,
  UpdateTreeRequest,
  Attachment,
  ThreadDetail, SessionRef, SessionPreview,
  Badge,
  Catalog,
  CreateProjectRequest,
  ExternalProject,
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
  SetT3UrlRequest,
  SyncResult,
  T3State,
  T3Status,
  T3StatusWire,
  Task,
  TaskDetail,
  LaunchLinkPreview,
  TaskList,
  TaskListQuery,
  Snippet,
  SnippetList,
  OpeningPromptResponse,
  UnreadNotificationTags,
  UpdateTaskRequest,
  UpdateSnippetRequest,
} from "./types.ts";
import type { Transport } from "../platform/index.ts";

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
const put = (body: unknown): RequestOptions => ({ method: "PUT", body });
const del: RequestOptions = { method: "DELETE" };

/** State an older server (no `state`) implies: `connected` is a valid token, else unpaired or expired. */
function legacyState(w: T3StatusWire, now: number): T3State {
  if (w.connected) return "connected";
  if (w.tokenExpiresAt !== null && Date.parse(w.tokenExpiresAt) <= now) return "expired";
  return "unpaired";
}

/** Fills the onboarding fields an older server does not send, so the UI never reads a missing object. */
export function normalizeT3Status(w: T3StatusWire, now = Date.now()): T3Status {
  return {
    ...w,
    state: w.state ?? legacyState(w, now),
    t3UrlSource: w.t3UrlSource ?? "default",
    reachable: w.reachable ?? true,
    checkedAt: w.checkedAt ?? null,
    probeError: w.probeError ?? null,
    t3: w.t3 ?? null,
    autoPair: w.autoPair ?? { available: false, reason: null },
    sync: w.sync ?? { running: false, startedAt: null, projects: 0, threads: 0 },
  };
}

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
    connections: () => apiFetch<{connections: HarnessConnection[]}>("/connections"),
    openCodeStatus: () => apiFetch<OpenCodeStatus>("/opencode/status"),
    saveOpenCode: (body: Partial<Record<"endpoint" | "browserEndpoint" | "webClient" | "username" | "password", string>>) => apiSend("/opencode/settings", put(body)),
    capabilities: (id: number, projectId?: number) => apiFetch<Capabilities>(`/connections/${id}/capabilities${toQuery({projectId})}`),
    t3Status: () => apiFetch<T3StatusWire>("/t3/status").then((w) => normalizeT3Status(w)),
    pair: (body: PairRequest) => apiFetch<T3StatusWire>("/t3/pair", post(body)).then((w) => normalizeT3Status(w)),
    /** Pairs through the t3 CLI on the server's machine (409 when unavailable, 502 when it fails). */
    autoPair: () => apiFetch<T3StatusWire>("/t3/pair/auto", post()).then((w) => normalizeT3Status(w)),
    /** Forgets the T3 token and environment binding (409 while T3_ADMIN_TOKEN renews it). */
    disconnectT3: () => apiFetch<T3StatusWire>("/t3/pair", del).then((w) => normalizeT3Status(w)),
    /** 409 when T3_URL locks it or the URL is another T3; 400 when invalid. */
    setT3Url: (body: SetT3UrlRequest) => apiFetch<T3StatusWire>("/t3/url", put(body)).then((w) => normalizeT3Status(w)),
    sync: () => apiFetch<SyncResult>("/sync", post()),
    catalog: () => apiFetch<Catalog>("/catalog"),

    projects: (archived: ArchivedFilter = "false") => apiFetch<ProjectList>(`/projects${toQuery({ archived })}`),
    project: (id: number) => apiFetch<Project>(`/projects/${id}`),
    createProject: (body: CreateProjectRequest) => apiFetch<Project>("/projects", post(body)),
    updateProject: (id: number, body: {title: string; archived: boolean}) => apiFetch<Project>(`/projects/${id}`, patch(body)),
    matchWorkspace: (project: number, connection: number) => apiFetch<{bound: Project | null; projects: ExternalProject[]; suggestions: Record<string,string>}>(`/projects/${project}/bindings/${connection}/match`, post()),
    sharedPaths: () => apiFetch<{paths:{connectionId:number;harnessRoot:string;stageRoot:string}[]}>("/shared-paths"),
    setSharedPath: (connectionId:number,harnessRoot:string,stageRoot:string) => apiFetch("/shared-paths",put({connectionId,harnessRoot,stageRoot})),
    externalProjects: (connectionId = 1) => apiFetch<{projects: ExternalProject[]}>(`/connections/${connectionId}/projects`),
    bindProject: (id: number, project: ExternalProject) => apiFetch<Project>(`/projects/${id}/binding`, put({connectionId: project.connectionId, externalProjectId: project.externalId, workspace: project.workspace})),
    unbindProject: (id: number, connectionId = 1) => apiFetch<Project>(`/projects/${id}/bindings/${connectionId}`, {method: "DELETE"}),
    registerWorkspace: (body: {title: string; workspace: string}) => apiFetch<ExternalProject>("/connections/t3/projects", post(body)),

    tasks: (q: TaskListQuery = {}) =>
      apiFetch<TaskList>(
        `/tasks${toQuery({ originThreadId: q.originThreadId, project: q.project, state: q.state, tag: q.tag, q: q.q, from: q.from, to: q.to, settledFrom: q.settledFrom, closed: q.closed })}`,
      ),
    previewLaunchLink: (id: number, link: string) => apiFetch<LaunchLinkPreview>(`/tasks/${id}/launch-link/preview`, { method: "POST", body: { link } }),
    attachLaunchLink: (id: number, link: string, preview: LaunchLinkPreview) => apiFetch<Task>(`/tasks/${id}/launch-link`, { method: "POST", body: { link, attempt: preview.attempt, revision: preview.revision, environment: preview.environment } }),
    task: (id: number) => apiFetch<TaskDetail>(`/tasks/${id}`),
    completionPreview: (id: number) => apiFetch<CompletionPreview>(`/tasks/${id}/completion-preview`),
    completeTask: (id: number, body: AcceptanceRequest) => apiFetch<AcceptanceResult>(`/tasks/${id}/complete`, post(body)),
    reopenTask: (id: number, body: AcceptanceRequest) => apiFetch<AcceptanceResult>(`/tasks/${id}/reopen`, post(body)),
    journal: (owner: JournalOwner, q: JournalQuery = {}) =>
      apiFetch<JournalPage>(`${journalPath(owner)}${toQuery({ ...q })}`),
    journalEntry: (id: number) => apiFetch<JournalEntry>(`/journal/${id}`),
    appendNote: (owner: JournalOwner, body: AppendNoteInput) =>
      apiFetch<JournalEntry>(journalPath(owner), post(body)),
    createTask: (body: CreateTaskRequest) => apiFetch<Task>("/tasks", post(body)),
    updateTask: (id: number, body: UpdateTaskRequest) => apiFetch<Task>(`/tasks/${id}`, patch(body)),
    /** Deletes only when the server permits it (409 when work is recorded: archive it). */
    deleteTask: (id: number) => apiSend(`/tasks/${id}`, del),
    archiveTask: (id: number) => apiFetch<Task>(`/tasks/${id}/archive`, post()),
    unarchiveTask: (id: number) => apiFetch<Task>(`/tasks/${id}/unarchive`, post()),
    startTask: (id: number) => apiFetch<Task>(`/tasks/${id}/start`, post()),
    stopTask: (id: number) => apiFetch<Task>(`/tasks/${id}/stop`, post()),
    duplicateTask: (id: number) => apiFetch<Task>(`/tasks/${id}/duplicate`, post()),
    /** Replaces a backlog task's needs ([] clears them); a task outside a tree joins the needs' tree. */
    setNeeds: (id: number, needs: number[]) => apiFetch<Task>(`/tasks/${id}/needs`, { method: "PUT", body: { needs } }),

    trees: (q: TreeListQuery = {}) =>
      apiFetch<TreeList>(`/trees${toQuery({ status: q.status, settledFrom: q.settledFrom, project: q.project })}`),
    tree: (id: number) => apiFetch<TreeDetail>(`/trees/${id}`),
    createTree: (body: CreateTreeRequest) => apiFetch<TreeDetail>("/trees", post(body)),
    updateTree: (id: number, body: UpdateTreeRequest) => apiFetch<TreeDetail>(`/trees/${id}`, patch(body)),
    /** Deletes a tree and its tasks only when no recorded work prevents it. */
    deleteTree: (id: number) => apiSend(`/trees/${id}`, del),
    /** Detaches tasks and archives the tree, retaining its notes and event history. */
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

    thread: (id: string) => apiFetch<ThreadDetail>(`/connections/1/sessions/${encodeURIComponent(id)}`),
    /** Tracks a top-level session as a task (201 new, 200 when a task already owns it). */
    sessionContext: (ref: SessionRef) => apiFetch<SessionPreview>(`/connections/${ref.connectionId}/sessions/${encodeURIComponent(ref.externalSessionId)}/context`),
    adoptSession: (ref: SessionRef, body: AdoptSessionRequest = {}) =>
      apiFetch<Task>(`/connections/${ref.connectionId}/sessions/${encodeURIComponent(ref.externalSessionId)}/adopt`, post(body)),
    openingPrompt: (taskId: number, full = false) =>
      apiFetch<OpeningPromptResponse>(`/tasks/${taskId}/opening-prompt${full ? "?full=true" : ""}`),
    resolveRef: (ref: string, connectionId?: number, cached?: boolean) => apiFetch<{ kind: string; taskId: number | null; session: SessionRef | null; links: ExecutionLinks; state?: TaskState; sessionTitle?: string }>(`/refs/resolve${toQuery({ ref, connectionId,cached })}`),

    snippets: () => apiFetch<SnippetList>("/snippets"),
    createSnippet: (body: CreateSnippetRequest) => apiFetch<Snippet>("/snippets", post(body)),
    updateSnippet: (id: number, body: UpdateSnippetRequest) => apiFetch<Snippet>(`/snippets/${id}`, patch(body)),
    deleteSnippet: (id: number) => apiSend(`/snippets/${id}`, del),

    preferences: () => apiFetch<Preferences>("/preferences"),
    setPinned: (body: ModelDefaults | null) => apiFetch<Preferences>("/preferences/pinned", { method: "PUT", body }),
    setNotifyEvents: (events: NotificationEvent[]) =>
      apiFetch<Preferences>("/preferences/notifications", { method: "PUT", body: { events } }),


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

function journalPath(owner: JournalOwner): string {
  return owner.taskId === undefined ? `/trees/${owner.treeId}/journal` : `/tasks/${owner.taskId}/journal`;
}
