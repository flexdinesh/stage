import { keepPreviousData, queryOptions, useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { useApi } from "../platform/context.tsx";
import { ApiError, type ArchivedFilter, type HarnessApi } from "./client.ts";
import { useHarnessApi, useHarnessScope, useScopedKey } from "./harnessScope.tsx";
import { harnessLookup, type Harnesses } from "../lib/harnesses.ts";
import { POLL, pollFirstPage } from "../lib/polling.ts";
import type { ExecutionConfig, Harness, RequestResponse, SendMessageRequest, SessionRequest, HarnessConnection, HarnessDescriptor, HarnessStatus, Machine, OperationId, JournalOwner, JournalQuery, ScheduleListQuery, SchedulePreviewRequest, TaskListQuery, TreeListQuery } from "./types.ts";

export const keys = {
  /** A harness's generic status (`[...harnessStatus, kind]`); a harness with its own status reader keeps its own key (src/harnesses/). */
  harnessStatus: ["harness-status"],
  connections: ["connections"],
  machines: ["machines"],
  pairing: ["pairing"],
  harnesses: ["harnesses"],
  projects: ["projects"],
  workspaces: ["workspaces"],
  projectActions: ["project-actions"],
  tasks: ["tasks"],
  task: ["task"],
  snippets: ["snippets"],
  preferences: ["preferences"],
  me: ["me"],
  health: ["health"],
  badge: ["badge"],
  notifications: ["notifications"],
  schedules: ["schedules"],
  schedule: ["schedule"],
  schedulePreview: ["schedulePreview"],
  trees: ["trees"],
  tree: ["tree"],
  journal: ["journal"],
  journalEntry: ["journalEntry"],
  /** A task's pending session requests (approvals, questions). */
  requests: ["requests"],
  /** This device's state (not the server's): its push subscription. */
  device: { push: ["device", "push"] },
  /** The native host's state (desktop): login startup and diagnostics. */
  host: { loginStartup: ["host", "loginStartup"], diagnostics: ["host", "diagnostics"] },
} as const;

/** Harness connections (live: sync counts and activity move on their own). */
export function useConnections() {
  const api = useApi();
  return useQuery({ queryKey: keys.connections, queryFn: api.connections, refetchInterval: POLL.live });
}

type Connections = { connections: HarnessConnection[] };

const NO_MACHINES: Machine[] = [];

/** Machines not removed, local first (poll faster while a worker is negotiating). */
export function useMachines() {
  const api = useApi();
  const q = useQuery({
    queryKey: keys.machines,
    queryFn: api.machines,
    refetchInterval: (query) => (query.state.data?.machines.some((m) => m.status === "negotiating") ? POLL.live : POLL.slow),
  });
  return { ...q, machines: q.data?.machines ?? NO_MACHINES };
}

export function useRenameMachine() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, label }: { id: number; label: string }) => api.renameMachine(id, label),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.machines }),
  });
}

/** Removing a machine retires its connections: everything that names them refreshes. */
export function useRemoveMachine() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.removeMachine(id),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.machines }),
        qc.invalidateQueries({ queryKey: keys.connections }),
        qc.invalidateQueries({ queryKey: keys.projects }),
        invalidateWork(qc),
      ]);
    },
  });
}

const NO_CONNECTIONS: HarnessConnection[] = [];
const NO_DESCRIPTORS: HarnessDescriptor[] = [];

/** The server's harness kinds (names, policies, copy, availability here). They change only with the server build. */
export function useHarnessDescriptors() {
  const api = useApi();
  return useQuery({ queryKey: keys.harnesses, queryFn: api.harnesses, staleTime: Infinity });
}

/** The harness scope's machine and its harness kinds with what it can run and set up (GET /api/machines/{id}/harnesses; the local machine's are useHarnessDescriptors). */
export function useScopedHarnessDescriptors(enabled: boolean) {
  const api = useHarnessApi();
  const key = useScopedKey();
  const local = useHarnessScope().machineId === null;
  // A remote machine's kinds follow what its worker hosts: re-read after a while, refreshed by connection changes.
  return useQuery({ queryKey: key(keys.harnesses), queryFn: api.harnesses, enabled, staleTime: local ? Infinity : 30_000 });
}

/** Harness lookups by connection id (name, harness, active, label), by kind (descriptor) and the active connection per harness. */
export function useHarnesses(): Harnesses & { loaded: boolean; error: unknown } {
  const connections = useConnections();
  const descriptors = useHarnessDescriptors();
  const list = connections.data?.connections ?? NO_CONNECTIONS;
  const kinds = descriptors.data?.harnesses ?? NO_DESCRIPTORS;
  const lookup = useMemo(() => harnessLookup(list, kinds), [list, kinds]);
  return { ...lookup, loaded: connections.data !== undefined, error: connections.error };
}

/** Forgets a retired or disconnected connection: it leaves Settings (and its machine) and its bindings stop asking for repair. */
export function useForgetConnection() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.forgetConnection(id),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.connections }),
        qc.invalidateQueries({ queryKey: keys.machines }),
        qc.invalidateQueries({ queryKey: keys.projects }),
        invalidateWork(qc),
      ]);
    },
  });
}

/** Toggles a connection's auto-complete; optimistic, reverted on failure. */
export function useSetAutoComplete() {
  const api = useApi();
  const qc = useQueryClient();
  const patch = (id: number, change: (c: HarnessConnection) => HarnessConnection) =>
    qc.setQueryData<Connections>(keys.connections, (prev) =>
      prev === undefined ? prev : { connections: prev.connections.map((c) => (c.id === id ? change(c) : c)) },
    );
  return useMutation({
    mutationFn: ({ id, on }: { id: number; on: boolean }) => api.setAutoComplete(id, on),
    onMutate: async ({ id, on }) => {
      await qc.cancelQueries({ queryKey: keys.connections });
      const previous = qc.getQueryData<Connections>(keys.connections);
      patch(id, (c) => ({ ...c, autoComplete: on }));
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous !== undefined) qc.setQueryData(keys.connections, ctx.previous);
    },
    onSuccess: ({ connection }) => {
      patch(connection.id, () => connection);
    },
  });
}

/** Pins (or clears, null) the model and options new tasks on a connection start with. */
export function useSetNewTaskConfig() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, config }: { id: number; config: ExecutionConfig | null }) => api.setNewTaskConfig(id, config),
    onSuccess: ({ connection }) =>
      qc.setQueryData<Connections>(keys.connections, (prev) =>
        prev === undefined ? prev : { connections: prev.connections.map((c) => (c.id === connection.id ? connection : c)) },
      ),
  });
}

/** Makes a connection the one new tasks start on (null clears it). */
export function useSetDefaultConnection() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number | null) => api.setDefaultConnection(id),
    onSuccess: (p) => qc.setQueryData(keys.preferences, p),
  });
}

/** A generic harness's status on a machine (`key`: that machine's scoped harnessStatus key for the kind). */
export const harnessStatusQuery = (api: HarnessApi, key: readonly unknown[], kind: Harness) =>
  queryOptions({ queryKey: key, queryFn: () => api.harnessStatus(kind), refetchInterval: 15000 });

/** A generic harness's status on the harness scope's machine (shared by its card and its Settings row). */
export function useHarnessStatus(kind: Harness) {
  const api = useHarnessApi();
  return useQuery(harnessStatusQuery(api, useScopedKey()([...keys.harnessStatus, kind]), kind));
}

/** Disconnects a generic harness on the harness scope's machine. */
export function useDisconnectHarness(kind: Harness, onDone?: () => void) {
  const api = useHarnessApi();
  const apply = useApplyHarnessStatus(kind);
  return useMutation({
    mutationFn: () => api.disconnectHarness(kind),
    onSuccess: (s) => {
      onDone?.();
      apply(s);
    },
  });
}

/** Puts a setup reply's status in the cache and refreshes what a connection change affects. */
export function useApplyHarnessStatus(kind: string) {
  const qc = useQueryClient();
  const scoped = useScopedKey();
  return (s: HarnessStatus) => {
    qc.setQueryData(scoped([...keys.harnessStatus, kind]), s);
    void invalidateConnectionChange(qc, scoped);
  };
}

/** The first full sync filled projects and sessions: refetch everything that read them empty. */
export function refreshAfterFirstSync(qc: QueryClient): Promise<void> {
  return Promise.all([
    invalidateWork(qc),
    qc.invalidateQueries({ queryKey: keys.projects }),
    qc.invalidateQueries({ queryKey: keys.workspaces }),
  ]).then(() => undefined);
}

/** `enabled: false` reads only what is cached (a picker that needs the list once it opens). */
export const useProjects = (archived: ArchivedFilter = "false", { enabled = true }: { enabled?: boolean } = {}) => {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.projects, "list", archived],
    queryFn: () => api.projects(archived),
    placeholderData: keepPreviousData,
    refetchInterval: POLL.slow,
    enabled,
  });
};

export function useDestinations(enabled = true) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.projects, "destinations"],
    queryFn: ({ signal }) => api.destinations(signal),
    refetchInterval: POLL.slow,
    enabled,
  });
}

/** The server answered 404: the record was deleted or never existed. */
export const isNotFound = (error: unknown): boolean => error instanceof ApiError && error.status === 404;

/** Retries transient failures; a 404 shows its not-found view at once. */
const retryUnlessNotFound = (count: number, error: unknown): boolean => !isNotFound(error) && count < 3;

export const useProject = (id: number) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.projects, "one", id], queryFn: () => api.project(id), retry: retryUnlessNotFound, refetchInterval: POLL.slow });
};

export function useProjectGroups() {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.projects, "groups"],
    queryFn: ({ signal }) => api.projectGroups(signal),
    refetchInterval: POLL.slow,
  });
}

export function useProjectGroup(id: number) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.projects, "group", id],
    queryFn: ({ signal }) => api.projectGroup(id, signal),
    retry: retryUnlessNotFound,
    refetchInterval: POLL.slow,
  });
}

/** `live: false` (a picker's one-off list): read once, re-read after 30s, no polling. */
export const useTasks = (q: TaskListQuery = {}, { live = true }: { live?: boolean } = {}) => {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.tasks, q],
    queryFn: () => api.tasks(q),
    placeholderData: keepPreviousData,
    refetchInterval: live ? POLL.live : false,
    staleTime: live ? 0 : 30_000,
  });
};

export const useTask = (id: number, enabled = true) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.task, id], queryFn: () => api.task(id), refetchInterval: POLL.live, enabled });
};

/** Declarations for the current session. A new attempt gets fresh controls and action state. */
export function useTaskActions(task: { id: number; launchAttempt: number; externalSessionId: string | null }, enabled: boolean) {
  const api = useApi();
  const qc = useQueryClient();
  const queryKey = [...keys.task, task.id, "actions", task.launchAttempt, task.externalSessionId];
  const q = useQuery({
    queryKey,
    queryFn: () => api.taskActions(task.id),
    enabled,
    refetchInterval: POLL.slow,
    retry: false,
  });
  const actions = enabled && !q.isError ? q.data ?? [] : [];
  const run = async (id: string, input: Record<string, unknown>, confirmed: boolean) => {
    const action = actions.find((a) => a.id === id && a.callers.includes("user"));
    if (action?.availability.available !== true) throw new Error(action?.availability.reason || "This action is unavailable.");
    try {
      return await api.runTaskAction(task.id, id, input, confirmed);
    } finally {
      void Promise.all([invalidateWork(qc), qc.invalidateQueries({ queryKey: keys.notifications })]);
    }
  };
  return { actions, run };
}

/** `live: false` (a picker's one-off list): read once, re-read after 30s, no polling. */
export const useTrees = (q: TreeListQuery = {}, { live = true }: { live?: boolean } = {}) => {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.trees, q],
    queryFn: () => api.trees(q),
    placeholderData: keepPreviousData,
    refetchInterval: live ? POLL.live : false,
    staleTime: live ? 0 : 30_000,
  });
};

export const useTree = (id: number, enabled = true) => {
  const api = useApi();
  // A deleted tree is a 404; dissolved and empty trees retain their archived identity.
  return useQuery({
    enabled,
    queryKey: [...keys.tree, id],
    queryFn: () => api.tree(id),
    retry: retryUnlessNotFound,
    refetchInterval: POLL.live,
  });
};

export const useSchedules = (q: ScheduleListQuery = {}) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.schedules, q], queryFn: () => api.schedules(q), placeholderData: keepPreviousData, refetchInterval: POLL.slow });
};

/** `active: false` (a hidden tab): keeps its cache but stops polling and refetching. */
export const useJournal = (owner: JournalOwner, q: Omit<JournalQuery, "cursor"> = {}, { active = true }: { active?: boolean } = {}) => {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...keys.journal, owner, q],
    queryFn: ({ pageParam }) => api.journal(owner, { ...q, ...(pageParam === "" ? {} : { cursor: pageParam }) }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    refetchInterval: (query) => pollFirstPage(query.state.data?.pages.length, POLL.live),
    subscribed: active,
    retry: false,
  });
};

/** A tree's steering feed, oldest first, a page at a time (`after` cursor); the first page polls while shown. */
export const useSteeringEvents = (treeId: number) => {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...keys.tree, treeId, "steering-events"],
    queryFn: ({ pageParam }) => api.steeringEvents(treeId, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.hasMore ? last.events.at(-1)?.id : undefined),
    refetchInterval: (query) => pollFirstPage(query.state.data?.pages.length, POLL.live),
    retry: false,
  });
};

export const useJournalEntry = (id: number | null, enabled = true) => {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.journalEntry, id],
    queryFn: () => {
      if (id === null) throw new Error("Missing journal entry id");
      return api.journalEntry(id);
    },
    enabled: id !== null && enabled,
    staleTime: Infinity,
    refetchInterval: false,
    retry: false,
  });
};

export const useSchedule = (id: number) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.schedule, id], queryFn: () => api.schedule(id), retry: retryUnlessNotFound, refetchInterval: POLL.slow });
};

/** Next fires for a cron + zone; null input disables the query. Errors carry the parse message. */
export const useSchedulePreview = (body: SchedulePreviewRequest | null) => {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.schedulePreview, body?.cron ?? "", body?.tz ?? ""],
    queryFn: () => (body === null ? Promise.resolve({ next: [] }) : api.previewSchedule(body)),
    enabled: body !== null,
    refetchInterval: false,
    staleTime: 30_000,
    retry: false,
    placeholderData: keepPreviousData,
  });
};

/** Tasks + external sessions needing input (app badge, nav count). */
export const useBadge = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.badge, queryFn: api.badge, refetchInterval: POLL.live });
};

export const latestNotificationsKey = [...keys.notifications, "latest"];

/** Latest notifications + unread count (bell, toasts). Polled. */
export const useNotifications = () => {
  const api = useApi();
  return useQuery({ queryKey: latestNotificationsKey, queryFn: () => api.notifications({ limit: 50 }), refetchInterval: POLL.live });
};

/** Notifications drawer: current needs input first, then recent task/session groups. */
export const useNotificationInbox = () => {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...keys.notifications, "inbox"],
    queryFn: ({ pageParam }) => api.notificationGroups({ limit: 50, cursor: pageParam === "" ? undefined : pageParam }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    refetchInterval: (query) => pollFirstPage(query.state.data?.pages.length, POLL.live),
  });
};

/** Mounted on expansion; history is notification events, not a run lifecycle. Not polled: it only grows, and read/prune invalidate it. */
export const useNotificationHistory = (group: string) => {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...keys.notifications, "history", group],
    queryFn: ({ pageParam }) => api.notifications({ group, limit: 20, before: pageParam === 0 ? undefined : pageParam }),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextBefore ?? undefined,
  });
};

export const useSnippets = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.snippets, queryFn: api.snippets });
};

// Changes only from Settings; no polling.
export const usePreferences = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.preferences, queryFn: api.preferences, refetchInterval: false, staleTime: 30_000 });
};

// The acting user (GET /api/me); fixed until sign-in exists, so no polling.
export const useMe = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.me, queryFn: api.me, refetchInterval: false, staleTime: Infinity });
};

/** Task mutations can change any task, session, stat or prefill view: refresh them all. */
export function invalidateWork(qc: QueryClient): Promise<void> {
  return Promise.all(
    [
      keys.tasks,
      keys.task,
      keys.preferences,
      keys.badge,
      keys.schedules,
      keys.schedule,
      keys.trees,
      keys.tree,
      keys.journal,
    ].map((queryKey) =>
      qc.invalidateQueries({ queryKey }),
    ),
  ).then(() => undefined);
}

/**
 * A harness connection changed (connect, pair, disconnect, settings). Everything that names connections or depends on
 * what tasks can start refreshes in the background; the returned promise waits only for the `awaited` setup queries
 * in the harness scope (`scopedKey`), so a setup action finishes when its own status is fresh.
 */
export function invalidateConnectionChange(
  qc: QueryClient,
  scopedKey: (key: readonly unknown[]) => readonly unknown[],
  awaited: readonly (readonly unknown[])[] = [],
): Promise<void> {
  void Promise.all([
    qc.invalidateQueries({ queryKey: keys.connections }),
    qc.invalidateQueries({ queryKey: keys.machines }),
    qc.invalidateQueries({ queryKey: keys.projects }),
    qc.invalidateQueries({ queryKey: keys.workspaces }),
    qc.invalidateQueries({ queryKey: ["capabilities"] }),
    qc.invalidateQueries({ queryKey: scopedKey(keys.harnesses) }),
    qc.invalidateQueries({ queryKey: ["capabilities"] }),
    invalidateWork(qc),
  ]);
  return Promise.all(awaited.map((key) => qc.invalidateQueries({ queryKey: scopedKey(key) }))).then(() => undefined);
}

export function useInvalidateWork(): () => Promise<void> {
  const qc = useQueryClient();
  return () => invalidateWork(qc);
}

/** Project membership and identity changes affect discovery and the harness catalogs. */
export function useInvalidateProjects(): () => Promise<void> {
  const qc = useQueryClient();
  return async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: keys.projects }),
      qc.invalidateQueries({ queryKey: keys.workspaces }),
      qc.invalidateQueries({ queryKey: keys.projectActions }),
      qc.invalidateQueries({ queryKey: keys.snippets }),
      qc.invalidateQueries({ queryKey: ["capabilities"] }),
      invalidateWork(qc),
    ]);
  };
}

const NO_REQUESTS: SessionRequest[] = [];

/** How often pending requests are re-read while a task needs input: a request can appear or be answered elsewhere at any time. */
const REQUESTS_POLL = 3000;

type CapabilityScope = { workspaceId: string };

/** One cached read per connection and workspace scope. Omit the scope for harness-wide declarations. */
export function connectionCapabilityOptions(api: Pick<ReturnType<typeof useApi>, "capabilities">, connectionId: number, scope?: CapabilityScope) {
  const key = scope === undefined ? ["", false] : ["workspace", scope.workspaceId];
  return queryOptions({
    queryKey: ["capabilities", connectionId, ...key],
    queryFn: ({ signal }) => api.capabilities(connectionId, scope, signal),
    staleTime: 60_000,
    retry: false,
  });
}

/** Read capabilities while enabled and the connection is not retired. */
export function useConnectionCapabilities(connectionId: number, enabled: boolean, scope?: CapabilityScope) {
  const api = useApi();
  const harnesses = useHarnesses();
  const live = enabled && connectionId > 0 && !harnesses.retired(connectionId);
  const caps = useQuery({ ...connectionCapabilityOptions(api, connectionId, scope), enabled: live });
  return { ...caps, live };
}

/**
 * Whether connection `connectionId` offers operation `op` (its capabilities, cached as NewTaskDefaults reads them), and
 * the harness's reason when it does not. Read only while `enabled`; `known` is false until the capabilities answer (or
 * always, when disabled or retired).
 */
export function useConnectionOperation(connectionId: number, op: OperationId, enabled: boolean): { offered: boolean; known: boolean; reason: string | null } {
  const caps = useConnectionCapabilities(connectionId, enabled);
  const o = caps.data?.operations[op];
  return { offered: caps.live && o?.available === true, known: caps.live && (caps.isSuccess || caps.isError), reason: o?.available === false && o.reason !== undefined && o.reason !== "" ? o.reason : null };
}

/**
 * Why each of `connectionIds` does not offer operation `op` (the harness's reason, else a generic one), by id; a
 * connection still being read, or one that offers it, is absent. Reads the same cached capabilities as
 * useConnectionOperation.
 */
export function useConnectionsOperationBlock(connectionIds: readonly number[], op: OperationId, enabled: boolean, fallback: string): ReadonlyMap<number, string> {
  const api = useApi();
  const results = useQueries({ queries: connectionIds.map((id) => ({ ...connectionCapabilityOptions(api, id), enabled: enabled && id > 0 })) });
  const blocked = new Map<number, string>();
  results.forEach((r, i) => {
    const id = connectionIds[i];
    const o = r.data?.operations[op];
    if (id === undefined || r.data === undefined || o?.available === true) return;
    blocked.set(id, o?.reason?.trim() || fallback);
  });
  return blocked;
}

/**
 * What a task's session waits on, when its connection offers operation `respond`. Read only while the task needs
 * input on an active connection; polled meanwhile. `ready`: whether the list is final for now (nothing to read, or
 * the first read answered), so a caller can tell "no requests" from "not read yet".
 */
export function useSessionRequests({ taskId, connectionId, needsInput }: { taskId: number | null; connectionId: number; needsInput: boolean }) {
  const api = useApi();
  const live = needsInput && taskId !== null;
  const respond = useConnectionOperation(connectionId, "respond", live);
  const respondable = respond.offered;
  const q = useQuery({
    queryKey: [...keys.requests, taskId],
    queryFn: () => api.taskRequests(taskId ?? 0),
    enabled: live && respondable,
    refetchInterval: REQUESTS_POLL,
    retry: false,
  });
  const ready = !live || (respond.known && (!respondable || q.isSuccess || q.isError));
  return { requests: live && respondable ? (q.data?.requests ?? NO_REQUESTS) : NO_REQUESTS, error: q.error, ready };
}

/** Answers one of a task's requests; the task, its requests and the inbox refresh either way (a 404 means it was answered elsewhere). */
export function useRespondRequest(taskId: number) {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ requestId, response, expectedStateRevision }: { requestId: string; response: RequestResponse; expectedStateRevision?: number }) =>
      api.respondRequest(taskId, requestId, response, expectedStateRevision),
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: [...keys.requests, taskId] }),
        qc.invalidateQueries({ queryKey: keys.notifications }),
        invalidateWork(qc),
      ]),
  });
}

/**
 * Sends a task's session a follow-up message; the task and the inbox refresh either way (409 `session_busy` names why it can't take one
 * now; 409 `already_answered` when a reply guarded by `expectedStateRevision` lost).
 */
export function useSendMessage(taskId: number) {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SendMessageRequest) => api.sendMessage(taskId, body),
    onSettled: () => Promise.all([qc.invalidateQueries({ queryKey: keys.notifications }), invalidateWork(qc)]),
  });
}
