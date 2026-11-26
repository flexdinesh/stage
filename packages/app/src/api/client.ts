import type {
  Machine, MachinePairing, PairingStatus,
  OpenCodeStatus, OpenCodeStatusWire, Harness, HarnessConnection, HarnessesReply, HarnessStatus, Capabilities, ExecutionConfig, ExecutionLinks, TaskState,
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
  T3SettingsRequest,
  SyncResult,
  T3State,
  T3Status,
  T3StatusWire,
  RequestResponse,
  SendMessageRequest,
  SessionRequest,
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
  User,
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

/** Fills the pairing fields an older server does not send: Basic credentials when it has them, and no local service. */
export function normalizeOpenCodeStatus(w: OpenCodeStatusWire): OpenCodeStatus {
  return {
    ...w,
    auth: w.auth ?? (w.hasCredentials ? "basic" : "none"),
    local: w.local ?? { available: false, url: "", version: "", reason: "" },
  };
}

const harnessPath = (kind: Harness, rest: string) => `/harnesses/${encodeURIComponent(kind)}${rest}`;

/** T3's panel state from its harness status (details plus the connection a pairing replaced). */
export function t3Details(s: HarnessStatus<T3StatusWire>, now = Date.now()): T3Status {
  if (s.details === undefined) throw new ApiError(502, "T3 status without details");
  return normalizeT3Status({ ...s.details, replaced: s.replaced ?? null }, now);
}

/** OpenCode's panel state from its harness status. */
export function openCodeDetails(s: HarnessStatus<OpenCodeStatusWire>): OpenCodeStatus {
  if (s.details === undefined) throw new ApiError(502, "OpenCode status without details");
  return normalizeOpenCodeStatus(s.details);
}

type Fetch = <T>(path: string, init?: RequestOptions) => Promise<T>;
type Send = (path: string, init?: RequestOptions) => Promise<void>;

/** A machine's harness connection setup calls: base "" is the local machine (/api/harnesses), else /machines/{id}. A remote machine's call answers 503 with code machine_offline, machine_needs_update or machine_setup_starting when its worker cannot answer. */
function harnessCalls(apiFetch: Fetch, apiSend: Send, base: string) {
  const path = (kind: Harness, rest: string) => `${base}${harnessPath(kind, rest)}`;
  return {
    /** The harness kinds the server knows, their policies and copy, and what the machine can run and set up. */
    harnesses: () => apiFetch<HarnessesReply>(`${base}/harnesses`),
    /** Connection setup of a connectable harness (404 unknown kind, 409 none on the machine); `refresh` probes again instead of answering from cache. */
    harnessStatus: <D = unknown>(kind: Harness, { refresh = false }: { refresh?: boolean } = {}) =>
      apiFetch<HarnessStatus<D>>(path(kind, refresh ? "/status?refresh=1" : "/status")),
    /** Saves settings keyed by setup field id (null resets). */
    configureHarness: <D = unknown>(kind: Harness, settings: Record<string, unknown>) => apiFetch<HarnessStatus<D>>(path(kind, "/settings"), put(settings)),
    runHarnessAction: <D = unknown>(kind: Harness, action: string, input?: Record<string, unknown>) =>
      apiFetch<HarnessStatus<D>>(path(kind, `/actions/${encodeURIComponent(action)}`), post(input)),
    /** Forgets the connection's credentials; tasks and history stay. */
    disconnectHarness: <D = unknown>(kind: Harness) => apiFetch<HarnessStatus<D>>(path(kind, "/connection"), del),
    openCodeStatus: () => apiFetch<HarnessStatus<OpenCodeStatusWire>>(path("opencode", "/status")).then(openCodeDetails),
    /** 409 `opencode_auth_required` when the service demands a password; 409 code instance_changed when the address is another OpenCode service (instance_returned: a replaced one); resend with newInstance to connect it. */
    saveOpenCode: (body: OpenCodeSettings) => apiSend(path("opencode", "/settings"), put(body)),
    /** Connects the OpenCode service on the machine with its own password (409 `opencode_local_unavailable`, or instance_changed / instance_returned as for saveOpenCode). */
    connectLocalOpenCode: (newInstance = false) => apiSend(path("opencode", "/actions/connect-local"), post(newInstance ? { newInstance: true } : undefined)),
    /** Redeems a link from `opencode pair` (400 when invalid, 409 `opencode_pairing_expired`, or instance_changed / instance_returned: resend the same link with newInstance). */
    pairOpenCode: (link: string, newInstance = false) => apiSend(path("opencode", "/actions/pair"), post(newInstance ? { link, newInstance: true } : { link })),
    /** Forgets the OpenCode connection; tasks and history stay. */
    disconnectOpenCode: () => apiSend(path("opencode", "/connection"), del),
    t3Status: () => apiFetch<HarnessStatus<T3StatusWire>>(path("t3", "/status")).then((s) => t3Details(s)),
    pair: (body: PairRequest) => apiFetch<HarnessStatus<T3StatusWire>>(path("t3", "/actions/pair"), post(body)).then((s) => t3Details(s)),
    /** Pairs through the t3 CLI on the machine (409 when unavailable, 502 when it fails). */
    autoPair: () => apiFetch<HarnessStatus<T3StatusWire>>(path("t3", "/actions/auto-pair"), post()).then((s) => t3Details(s)),
    /** Forgets the T3 token, URL, browser URL and admin token; the connection record stays, so pairing the same T3 resumes it. */
    disconnectT3: () => apiFetch<HarnessStatus<T3StatusWire>>(path("t3", "/connection"), del).then((s) => t3Details(s)),
    /** 400 when a URL is invalid; 409 when paired and the URL serves another T3 environment. */
    saveT3Settings: (body: T3SettingsRequest) => apiFetch<HarnessStatus<T3StatusWire>>(path("t3", "/settings"), put(body)).then((s) => t3Details(s)),
    };
}

/** One machine's harness setup calls (the local machine's are on Api itself). */
export type HarnessApi = ReturnType<typeof harnessCalls>;

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
    ...harnessCalls(apiFetch, apiSend, ""),
    /** A remote machine's harness setup, driven through its worker. */
    forMachine: (id: number): HarnessApi => harnessCalls(apiFetch, apiSend, `/machines/${id}`),
    connections: () => apiFetch<{connections: HarnessConnection[]}>("/connections"),
    /** Machines not removed, local (id 1) first. */
    machines: () => apiFetch<{machines: Machine[]}>("/machines"),
    /** 400 blank or over 100 characters; 404 unknown or removed. */
    renameMachine: (id: number, label: string) => apiFetch<Machine>(`/machines/${id}`, patch({label})),
    /** Revokes its token and retires its connections as history (409 `local_machine` for machine 1). */
    removeMachine: (id: number) => apiSend(`/machines/${id}`, del),
    /** A one-time 10 minute code (409 `public_url_required` without STAGE_PUBLIC_URL). The code appears only here. */
    createPairing: (label: string) => apiFetch<MachinePairing>("/machines/pairings", post(label === "" ? {} : {label})),
    /** 404 when unknown, revoked or pruned. */
    pairingStatus: (id: number) => apiFetch<PairingStatus>(`/machines/pairings/${id}`),
    /** Revokes a code not yet redeemed (409 `pairing_redeemed` once it added a machine). */
    revokePairing: (id: number) => apiSend(`/machines/pairings/${id}`, del),
    setAutoComplete: (connectionId: number, autoComplete: boolean) => apiFetch<{connection: HarnessConnection}>(`/connections/${connectionId}/auto-complete`, put({autoComplete})),
    setNewTaskConfig: (connectionId: number, config: ExecutionConfig | null) => apiFetch<{connection: HarnessConnection}>(`/connections/${connectionId}/new-task-config`, put({config})),
    /** Forgets a retired connection, or a disconnected active one (409 while connected or its machine is offline, 404 when unknown or already forgotten). */
    forgetConnection: (id: number) => apiSend(`/connections/${id}`, del),
    capabilities: (id: number, projectId?: number) => apiFetch<Capabilities>(`/connections/${id}/capabilities${toQuery({projectId})}`),
    sync: () => apiFetch<SyncResult>("/sync", post()),
    catalog: () => apiFetch<Catalog>("/catalog"),

    projects: (archived: ArchivedFilter = "false") => apiFetch<ProjectList>(`/projects${toQuery({ archived })}`),
    project: (id: number) => apiFetch<Project>(`/projects/${id}`),
    createProject: (body: CreateProjectRequest) => apiFetch<Project>("/projects", post(body)),
    updateProject: (id: number, body: {title: string; archived: boolean}) => apiFetch<Project>(`/projects/${id}`, patch(body)),
    matchWorkspace: (project: number, connection: number) => apiFetch<{bound: Project | null; projects: ExternalProject[]; suggestions: Record<string,string>}>(`/projects/${project}/bindings/${connection}/match`, post()),
    sharedPaths: () => apiFetch<{paths:{connectionId:number;harnessRoot:string;stageRoot:string}[]}>("/shared-paths"),
    setSharedPath: (connectionId:number,harnessRoot:string,stageRoot:string) => apiFetch("/shared-paths",put({connectionId,harnessRoot,stageRoot})),
    externalProjects: (connectionId: number) => apiFetch<{projects: ExternalProject[]}>(`/connections/${connectionId}/projects`),
    bindProject: (id: number, project: ExternalProject) => apiFetch<Project>(`/projects/${id}/binding`, put({connectionId: project.connectionId, externalProjectId: project.externalId, workspace: project.workspace})),
    unbindProject: (id: number, connectionId: number) => apiFetch<Project>(`/projects/${id}/bindings/${connectionId}`, {method: "DELETE"}),

    tasks: (q: TaskListQuery = {}) =>
      apiFetch<TaskList>(
        `/tasks${toQuery({ project: q.project, state: q.state, q: q.q, from: q.from, to: q.to, settledFrom: q.settledFrom, closed: q.closed })}`,
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
    /** What the task's session waits on (409 `operation_unavailable` when its harness can't answer from Stage). */
    taskRequests: (id: number) => apiFetch<{requests: SessionRequest[]}>(`/tasks/${id}/requests`),
    /** Answers one request (400 the answer doesn't fit, 404 no longer pending, 403 `agent_forbidden` for agents). */
    respondRequest: (id: number, requestId: string, body: RequestResponse) => apiFetch<Task>(`/tasks/${id}/requests/${encodeURIComponent(requestId)}`, post(body)),
    /** Sends the session a follow-up (409 `session_busy` when it can't take one now, 403 `agent_forbidden` for agents). */
    sendMessage: (id: number, body: SendMessageRequest) => apiFetch<Task>(`/tasks/${id}/messages`, post(body)),
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

    thread: (connectionId: number, id: string) => apiFetch<ThreadDetail>(`/connections/${connectionId}/sessions/${encodeURIComponent(id)}`),
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

    me: () => apiFetch<User>("/me"),

    preferences: () => apiFetch<Preferences>("/preferences"),
    setDefaultConnection: (connectionId: number | null) =>
      apiFetch<Preferences>("/preferences/default-connection", { method: "PUT", body: { connectionId } }),
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

export type OpenCodeSettings = Partial<Record<"endpoint" | "browserEndpoint" | "webClient" | "username" | "password", string>> & {
  /** Connect a different OpenCode service as a new connection, retiring the current one. */
  newInstance?: boolean;
};

export type ArchivedFilter = "true" | "false" | "all";

function journalPath(owner: JournalOwner): string {
  return owner.taskId === undefined ? `/trees/${owner.treeId}/journal` : `/tasks/${owner.taskId}/journal`;
}
