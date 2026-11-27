import type {
  AccessStatus, Account, AccountRole, AgentCredential, GrantInfo, GrantKind, Invitation, IssuedLink,
  HarnessAction,
  Machine, MachinePairing, PairingStatus,
  ActionResult, Harness, HarnessFact, HarnessConnection, HarnessesReply, HarnessStatus, Capabilities, ExecutionConfig, ExecutionLinks, TaskState,
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
  SessionRef, SessionPreview,
  Badge,
  BindWorkspaceRequest,
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
  Health,
  NotificationEvent,
  NotificationGroupPage,
  NotificationPage,
  Preferences,
  Project,
  ProjectEvent,
  ProjectList,
  ProjectMergeResult,
  PushConfig,
  PushEndpointBody,
  PushSubscriptionBody,
  SyncResult,
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
  WorkspaceInventory,
  WorkspaceRef,
  WorkspaceResolveResult,
  WorkspaceReview,
} from "./types.ts";
import type { Transport } from "../platform/index.ts";

/** Non-2xx API response; `message` is the server's `{ error }` text, `code` its optional reason. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string | null;
  readonly preview: unknown;

  constructor(status: number, message: string, code: string | null = null, preview?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.preview = preview;
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

type RequestOptions = { method?: string; body?: unknown; actionConfirmed?: boolean };

const post = (body?: unknown): RequestOptions => (body === undefined ? { method: "POST" } : { method: "POST", body });
const actionPost = (body: Record<string, unknown>, confirmed: boolean): RequestOptions => ({ ...post(body), actionConfirmed: confirmed });
const patch = (body: unknown): RequestOptions => ({ method: "PATCH", body });
const put = (body: unknown): RequestOptions => ({ method: "PUT", body });
const del: RequestOptions = { method: "DELETE" };

const harnessPath = (kind: Harness, rest: string) => `/harnesses/${encodeURIComponent(kind)}${rest}`;

type Fetch = <T>(path: string, init?: RequestOptions) => Promise<T>;

/** A machine's harness connection setup calls: base "" is the local machine (/api/harnesses), else /machines/{id}. A remote machine's call answers 503 with code machine_offline, machine_needs_update or machine_setup_starting when its worker cannot answer. */
function harnessCalls(apiFetch: Fetch, base: string) {
  const path = (kind: Harness, rest: string) => `${base}${harnessPath(kind, rest)}`;
  return {
    /** The harness kinds the server knows, their policies and copy, and what the machine can run and set up. */
    harnesses: () => apiFetch<HarnessesReply>(`${base}/harnesses`),
    /** Connection setup of a connectable harness (404 unknown kind, 409 none on the machine); `refresh` probes again instead of answering from cache. */
    harnessStatus: <D = unknown>(kind: Harness, { refresh = false }: { refresh?: boolean } = {}) =>
      apiFetch<HarnessStatus<D>>(path(kind, refresh ? "/status?refresh=1" : "/status")),
    /** Saves settings keyed by setup field id (null resets). */
    configureHarness: <D = unknown>(kind: Harness, settings: Record<string, unknown>) => apiFetch<HarnessStatus<D>>(path(kind, "/settings"), put(settings)),
    runHarnessAction: <D = unknown>(kind: Harness, action: string, input?: Record<string, unknown>, confirmed = false) =>
      apiFetch<HarnessStatus<D>>(path(kind, `/actions/${encodeURIComponent(action)}`), confirmed ? actionPost(input ?? {}, true) : post(input)),
    /** Forgets the connection's credentials; tasks and history stay. */
    disconnectHarness: <D = unknown>(kind: Harness) => apiFetch<HarnessStatus<D>>(path(kind, "/connection"), del),
    };
}

/** One machine's harness setup calls (the local machine's are on Api itself). */
export type HarnessApi = ReturnType<typeof harnessCalls>;

/** Typed Stage API client over a host transport (trusted contract: docs/api.md). */
export type Api = ReturnType<typeof createApi>;

export function createApi(transport: Transport) {
  async function request(path: string, init: RequestOptions): Promise<Response> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (init.actionConfirmed) headers["Stage-Action-Confirmed"] = "1";
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
      const code = errorCode(body);
      const preview = code === "preview_changed" && typeof body === "object" && body !== null && "preview" in body ? body.preview : undefined;
      throw new ApiError(res.status, errorMessage(body, `${res.status} ${res.statusText}`), code, preview);
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
    accessStatus: () => apiFetch<AccessStatus>("/auth/status"),
    account: () => apiFetch<Account>("/me"),
    login: (email: string, password: string) => apiFetch<Account>("/auth/login", post({ email, password })),
    desktopSignIn: (email: string, password: string) => apiSend("/auth/login", post({ email, password })),
    logout: () => apiSend("/auth/logout", post()),
    inspectGrant: (kind: GrantKind, token: string) => apiFetch<GrantInfo>(`/auth/${kind}/inspect`, post({ token })),
    setupAccount: (token: string, email: string, name: string, password: string) =>
      apiFetch<Account>("/auth/setup", post({ token, email, name, password })),
    acceptInvite: (token: string, name: string, password: string) =>
      apiFetch<Account>("/auth/invite", post({ token, name, password })),
    resetPassword: (token: string, password: string) => apiSend("/auth/reset", post({ token, password })),
    updateName: (name: string) => apiFetch<Account>("/account", patch({ name })),
    changePassword: (currentPassword: string, password: string) =>
      apiSend("/account/password", post({ currentPassword, password })),
    issueAgentCredential: () => apiFetch<AgentCredential>("/account/agent-credential", post()),
    revokeAgentCredential: () => apiSend("/account/agent-credential", del),
    accounts: (limit: number, beforeId?: number) => apiFetch<Account[]>(`/admin/accounts${toQuery({ limit, beforeId })}`),
    invitations: (limit: number, beforeId?: number) => apiFetch<Invitation[]>(`/admin/invitations${toQuery({ limit, beforeId })}`),
    setAccountRole: (id: number, role: AccountRole) => apiSend(`/admin/accounts/${id}/role`, patch({ role })),
    disableAccount: (id: number) => apiSend(`/admin/accounts/${id}/disable`, post()),
    issueResetLink: (id: number) => apiFetch<IssuedLink>(`/admin/accounts/${id}/reset`, post()),
    invite: (email: string) => apiFetch<IssuedLink>("/admin/invitations", post({ email })),
    replaceInvite: (id: number) => apiFetch<IssuedLink>(`/admin/invitations/${id}/replace`, post()),
    revokeInvite: (id: number) => apiSend(`/admin/invitations/${id}`, del),
    ...harnessCalls(apiFetch, ""),
    /** A remote machine's harness setup, driven through its worker. */
    forMachine: (id: number): HarnessApi => harnessCalls(apiFetch, `/machines/${id}`),
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
    connectionFacts: (id: number) => apiFetch<{ facts: HarnessFact[] }>(`/connections/${id}/facts`),
    runConnectionAction: (id: number, action: string, input: Record<string, unknown>, confirmed: boolean) =>
      apiFetch<ActionResult>(`/connections/${id}/actions/${encodeURIComponent(action)}`, actionPost(input, confirmed)),
    capabilities: (id: number, scope?: { projectId: number; bindingId?: number }) => apiFetch<Capabilities>(`/connections/${id}/capabilities${toQuery({ ...scope })}`),
    sync: () => apiFetch<SyncResult>("/sync", post()),

    projects: (archived: ArchivedFilter = "false") => apiFetch<ProjectList>(`/projects${toQuery({ archived })}`),
    project: (id: number) => apiFetch<Project>(`/projects/${id}`),
    createProject: (body: CreateProjectRequest) => apiFetch<Project>("/projects", post(body)),
    updateProject: (id: number, body: {title: string; archived: boolean}) => apiFetch<Project>(`/projects/${id}`, patch(body)),
    workspaces: (connectionId: number, refresh = true) =>
      apiFetch<WorkspaceInventory>(`/connections/${connectionId}/workspaces${refresh ? "" : "?refresh=false"}`),
    resolveWorkspaces: (connectionId: number, body: { apply: boolean; previewToken?: string }) =>
      apiFetch<WorkspaceResolveResult>(`/connections/${connectionId}/resolve`, post(body)),
    workspaceReview: () => apiFetch<WorkspaceReview>("/workspaces/review"),
    bindProject: (id: number, body: BindWorkspaceRequest) =>
      apiFetch<Project>(`/projects/${id}/binding`, put(body)),
    unbindProject: (id: number, bindingId: number) =>
      apiFetch<Project>(`/projects/${id}/bindings/${bindingId}`, del),
    setPrimaryBinding: (id: number, bindingId: number) =>
      apiFetch<Project>(`/projects/${id}/bindings/${bindingId}/primary`, { method: "PUT" }),
    declineWorkspace: (connectionId: number, body: WorkspaceRef) =>
      apiSend(`/connections/${connectionId}/workspaces/decline`, post(body)),
    undeclineWorkspace: (connectionId: number, body: WorkspaceRef) =>
      apiSend(`/connections/${connectionId}/workspaces/undecline`, post(body)),
    mergeProject: (id: number, body: { from: number; confirm?: boolean; previewToken?: string }) =>
      apiFetch<ProjectMergeResult>(`/projects/${id}/merge`, post(body)),
    dismissProjectMerge: (id: number, otherId: number) => apiSend(`/projects/${id}/merge-dismissals`, post({ otherId })),
    splitProject: (id: number, body: { bindingId: number; title?: string }) =>
      apiFetch<{ project: Project; source: Project }>(`/projects/${id}/split`, post(body)),
    projectEvents: (id: number) => apiFetch<{ events: ProjectEvent[] }>(`/projects/${id}/events`),
    projectActions: (id: number, scope: { connectionId: number; bindingId?: number }) =>
      apiFetch<HarnessAction[]>(`/projects/${id}/actions${toQuery(scope)}`),
    runProjectAction: (id: number, scope: { connectionId: number; bindingId?: number }, action: string, input: Record<string, unknown>, confirmed: boolean) =>
      apiFetch<ActionResult>(`/projects/${id}/actions/${encodeURIComponent(action)}${toQuery(scope)}`, actionPost(input, confirmed)),

    tasks: (q: TaskListQuery = {}) =>
      apiFetch<TaskList>(
        `/tasks${toQuery({ project: q.project, state: q.state, q: q.q, from: q.from, to: q.to, completedFrom: q.completedFrom, closed: q.closed })}`,
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
    taskActions: (id: number) => apiFetch<HarnessAction[]>(`/tasks/${id}/actions`),
    runTaskAction: (id: number, actionId: string, input: Record<string, unknown>, confirmed: boolean) =>
      apiFetch<ActionResult>(`/tasks/${id}/actions/${encodeURIComponent(actionId)}`, actionPost(input, confirmed)),
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
      apiFetch<TreeList>(`/trees${toQuery({ status: q.status, completedFrom: q.completedFrom, project: q.project })}`),
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
    checkPushSubscription: (body: PushEndpointBody) => apiFetch<{ bound: boolean }>("/push/subscriptions/check", post(body)),
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
