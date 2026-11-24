import { keepPreviousData, queryOptions, useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { useApi } from "../platform/context.tsx";
import { ApiError, type ArchivedFilter, type HarnessApi } from "./client.ts";
import { useHarnessApi, useHarnessScope, useScopedKey } from "./harnessScope.tsx";
import { firstSyncLanded } from "../lib/onboarding.ts";
import { harnessLookup, type Harnesses } from "../lib/harnesses.ts";
import { machineKey, machineScopeId } from "../lib/machines.ts";
import { POLL, pollFirstPage } from "../lib/polling.ts";
import type { ExecutionConfig, Harness, HarnessConnection, HarnessDescriptor, Machine, JournalOwner, JournalQuery, ScheduleListQuery, SchedulePreviewRequest, T3Status, TaskListQuery, TreeListQuery } from "./types.ts";

export const keys = {
  t3Status: ["t3Status"],
  openCodeStatus: ["opencode-status"],
  harnessStatus: ["harness-status"],
  connections: ["connections"],
  machines: ["machines"],
  pairing: ["pairing"],
  harnesses: ["harnesses"],
  catalog: ["catalog"],
  projects: ["projects"],
  tasks: ["tasks"],
  task: ["task"],
  thread: ["thread"],
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

/** Machines not removed, local first (polled: presence and last seen move on their own). */
export function useMachines() {
  const api = useApi();
  const q = useQuery({ queryKey: keys.machines, queryFn: api.machines, refetchInterval: POLL.slow });
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
export function useScopedHarnessDescriptors() {
  const api = useHarnessApi();
  const key = useScopedKey();
  const local = useHarnessScope().machineId === null;
  // A remote machine's kinds follow what its worker hosts: re-read after a while, refreshed by connection changes.
  return useQuery({ queryKey: key(keys.harnesses), queryFn: api.harnesses, staleTime: local ? Infinity : 30_000 });
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

/** OpenCode's setup status on the harness scope's machine. */
export function useOpenCodeStatus() {
  const api = useHarnessApi();
  const key = useScopedKey();
  return useQuery(openCodeStatusQuery(api, key(keys.openCodeStatus)));
}

/** OpenCode's status on a machine (`api`, `key`: that machine's calls and scoped key). */
const openCodeStatusQuery = (api: HarnessApi, key: readonly unknown[]) => queryOptions({ queryKey: key, queryFn: api.openCodeStatus, refetchInterval: 15000 });

/** A generic harness's status on a machine (`key`: that machine's scoped harnessStatus key for the kind). */
export const harnessStatusQuery = (api: HarnessApi, key: readonly unknown[], kind: Harness) =>
  queryOptions({ queryKey: key, queryFn: () => api.harnessStatus(kind), refetchInterval: 15000 });

const T3_STATUS_POLL_MS = 5000;
const T3_SYNC_POLL_MS = 1000;

/** T3's setup status on the harness scope's machine; `enabled: false` (T3 not set up there) reads only what is cached. */
export const useT3Status = ({ enabled = true }: { enabled?: boolean } = {}) => {
  const api = useHarnessApi();
  const key = useScopedKey()(keys.t3Status);
  const qc = useQueryClient();
  return useQuery({ ...t3StatusQuery(api, key, qc), enabled });
};

/** T3's status on a machine (`api`, `key`: that machine's calls and scoped key). */
function t3StatusQuery(api: HarnessApi, key: readonly unknown[], qc: QueryClient) {
  return queryOptions({
    queryKey: key,
    // Compared here (once per fetch, however many observers) so the first sync refreshes the views it fills.
    queryFn: async () => {
      const prev = qc.getQueryData<T3Status>(key);
      const next = await api.t3Status();
      if (firstSyncLanded(prev, next)) void refreshAfterFirstSync(qc);
      return next;
    },
    refetchInterval: (q) => (q.state.data?.sync.running === true ? T3_SYNC_POLL_MS : T3_STATUS_POLL_MS),
  });
}

/** A connection whose status is read on its machine. */
export interface MachineConnection {
  harness: Harness;
  machine: Pick<Machine, "id" | "kind">;
}

/**
 * Each connection's status on its own machine (the queries Settings' rows run there, so the cache is shared): T3,
 * OpenCode and generic harnesses, each paired with its connection.
 */
export function useConnectionStatuses<C extends MachineConnection>(connections: readonly C[], { poll = true }: { poll?: boolean } = {}) {
  // poll false: read what another observer (the status row) keeps fresh, without a second poll of the same status.
  const every: { refetchInterval?: false } = poll ? {} : { refetchInterval: false };
  const api = useApi();
  const qc = useQueryClient();
  const on = (m: MachineConnection["machine"]) => {
    const id = machineScopeId(m);
    return { api: id === null ? api : api.forMachine(id), key: (k: readonly unknown[]) => machineKey(id, k) };
  };
  const t3 = connections.filter((c) => c.harness === "t3");
  const openCode = connections.filter((c) => c.harness === "opencode");
  const other = connections.filter((c) => c.harness !== "t3" && c.harness !== "opencode");
  const t3Queries = useQueries({
    queries: t3.map((c) => {
      const m = on(c.machine);
      return { ...t3StatusQuery(m.api, m.key(keys.t3Status), qc), ...every };
    }),
  });
  const openCodeQueries = useQueries({
    queries: openCode.map((c) => {
      const m = on(c.machine);
      return { ...openCodeStatusQuery(m.api, m.key(keys.openCodeStatus)), ...every };
    }),
  });
  const otherQueries = useQueries({
    queries: other.map((c) => {
      const m = on(c.machine);
      return { ...harnessStatusQuery(m.api, m.key([...keys.harnessStatus, c.harness]), c.harness), ...every };
    }),
  });
  return { t3: paired(t3, t3Queries), openCode: paired(openCode, openCodeQueries), other: paired(other, otherQueries) };
}

/** Each connection with its query (useQueries returns one per option, in order). */
const paired = <C, Q>(connections: readonly C[], queries: readonly Q[]): { connection: C; query: Q }[] =>
  connections.flatMap((connection, i) => {
    const query = queries[i];
    return query === undefined ? [] : [{ connection, query }];
  });

/** The first full sync filled projects, threads and the catalog: refetch everything that read them empty. */
function refreshAfterFirstSync(qc: QueryClient): Promise<void> {
  return Promise.all([
    invalidateWork(qc),
    qc.invalidateQueries({ queryKey: keys.projects }),
    qc.invalidateQueries({ queryKey: keys.catalog }),
  ]).then(() => undefined);
}

// Catalog is cached server-side for 10 min; no need to poll it.
export const useCatalog = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.catalog, queryFn: api.catalog, refetchInterval: false, staleTime: 60_000, retry: false });
};

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

/** The server answered 404: the record was deleted or never existed. */
export const isNotFound = (error: unknown): boolean => error instanceof ApiError && error.status === 404;

/** Retries transient failures; a 404 shows its not-found view at once. */
const retryUnlessNotFound = (count: number, error: unknown): boolean => !isNotFound(error) && count < 3;

export const useProject = (id: number) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.projects, "one", id], queryFn: () => api.project(id), retry: retryUnlessNotFound, refetchInterval: POLL.slow });
};

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

export const useTask = (id: number) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.task, id], queryFn: () => api.task(id), refetchInterval: POLL.live });
};

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

/** A session on the active T3 connection. */
export const useThread = (id: string) => {
  const api = useApi();
  const t3 = useHarnesses().activeId("t3");
  return useQuery({
    queryKey: [...keys.thread, t3, id],
    queryFn: () => (t3 === null ? Promise.reject(new Error("T3 is not connected")) : api.thread(t3, id)),
    enabled: id !== "" && t3 !== null,
    refetchInterval: POLL.live,
  });
};

/** Tasks + external threads needing input (app badge, nav count). */
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

/** Notifications drawer: current needs input first, then recent task/thread groups. */
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

/** Task mutations can change any task, thread, stat or prefill view: refresh them all. */
export function invalidateWork(qc: QueryClient): Promise<void> {
  return Promise.all(
    [
      keys.tasks,
      keys.task,
      keys.thread,
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
    qc.invalidateQueries({ queryKey: keys.catalog }),
    qc.invalidateQueries({ queryKey: scopedKey(keys.harnesses) }),
    invalidateWork(qc),
  ]);
  return Promise.all(awaited.map((key) => qc.invalidateQueries({ queryKey: scopedKey(key) }))).then(() => undefined);
}

export function useInvalidateWork(): () => Promise<void> {
  const qc = useQueryClient();
  return () => invalidateWork(qc);
}
