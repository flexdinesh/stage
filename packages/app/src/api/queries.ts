import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useApi } from "../platform/context.tsx";
import { ApiError, type ArchivedFilter } from "./client.ts";
import { firstSyncLanded } from "../lib/onboarding.ts";
import type { HarnessConnection, JournalOwner, JournalQuery, ScheduleListQuery, SchedulePreviewRequest, T3Status, TaskListQuery, TreeListQuery } from "./types.ts";

export const keys = {
  t3Status: ["t3Status"],
  connections: ["connections"],
  catalog: ["catalog"],
  projects: ["projects"],
  tasks: ["tasks"],
  task: ["task"],
  thread: ["thread"],
  snippets: ["snippets"],
  preferences: ["preferences"],
  badge: ["badge"],
  notifications: ["notifications"],
  schedules: ["schedules"],
  schedule: ["schedule"],
  schedulePreview: ["schedulePreview"],
  trees: ["trees"],
  tree: ["tree"],
  journal: ["journal"],
  journalEntry: ["journalEntry"],
} as const;

/** Default poll; 1s while a full sync runs so its counts tick. */
export function useConnections() {
  const api = useApi();
  return useQuery({queryKey:keys.connections, queryFn: api.connections});
}

type Connections = { connections: HarnessConnection[] };

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

export function useOpenCodeStatus() {
  const api = useApi();
  return useQuery({queryKey:["opencode-status"], queryFn: api.openCodeStatus, refetchInterval: 15000});
}

const T3_STATUS_POLL_MS = 5000;
const T3_SYNC_POLL_MS = 1000;

export const useT3Status = () => {
  const api = useApi();
  const qc = useQueryClient();
  return useQuery({
    queryKey: keys.t3Status,
    // Compared here (once per fetch, however many observers) so the first sync refreshes the views it fills.
    queryFn: async () => {
      const prev = qc.getQueryData<T3Status>(keys.t3Status);
      const next = await api.t3Status();
      if (firstSyncLanded(prev, next)) void refreshAfterFirstSync(qc);
      return next;
    },
    refetchInterval: (q) => (q.state.data?.sync.running === true ? T3_SYNC_POLL_MS : T3_STATUS_POLL_MS),
  });
};

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

export const useProjects = (archived: ArchivedFilter = "false") => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.projects, "list", archived], queryFn: () => api.projects(archived) });
};

export const useProject = (id: number) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.projects, "one", id], queryFn: () => api.project(id) });
};

export const useTasks = (q: TaskListQuery = {}) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.tasks, q], queryFn: () => api.tasks(q) });
};

export const useTask = (id: number) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.task, id], queryFn: () => api.task(id) });
};

export const useTrees = (q: TreeListQuery = {}) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.trees, q], queryFn: () => api.trees(q), placeholderData: keepPreviousData });
};

export const useTree = (id: number) => {
  const api = useApi();
  // A deleted tree is a 404; dissolved and empty trees retain their archived identity.
  return useQuery({
    queryKey: [...keys.tree, id],
    queryFn: () => api.tree(id),
    retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 3,
  });
};

export const useSchedules = (q: ScheduleListQuery = {}) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.schedules, q], queryFn: () => api.schedules(q) });
};

export const useJournal = (owner: JournalOwner, q: Omit<JournalQuery, "cursor"> = {}) => {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...keys.journal, owner, q],
    queryFn: ({ pageParam }) => api.journal(owner, { ...q, ...(pageParam === "" ? {} : { cursor: pageParam }) }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
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
  return useQuery({ queryKey: [...keys.schedule, id], queryFn: () => api.schedule(id) });
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

export const useThread = (id: string) => {
  const api = useApi();
  return useQuery({ queryKey: [...keys.thread, id], queryFn: () => api.thread(id), enabled: id !== "" });
};

/** Tasks + external threads needing input (app badge, nav count). */
export const useBadge = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.badge, queryFn: api.badge });
};

export const latestNotificationsKey = [...keys.notifications, "latest"];

/** Latest notifications + unread count (bell, toasts). Polled. */
export const useNotifications = () => {
  const api = useApi();
  return useQuery({ queryKey: latestNotificationsKey, queryFn: () => api.notifications({ limit: 50 }) });
};

/** Notifications drawer: current needs input first, then recent task/thread groups. */
export const useNotificationInbox = () => {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...keys.notifications, "inbox"],
    queryFn: ({ pageParam }) => api.notificationGroups({ limit: 50, cursor: pageParam === "" ? undefined : pageParam }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
};

/** Mounted on expansion; history is notification events, not a run lifecycle. */
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

// Changes only on task create or pin; no polling.
export const usePreferences = () => {
  const api = useApi();
  return useQuery({ queryKey: keys.preferences, queryFn: api.preferences, refetchInterval: false, staleTime: 30_000 });
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

export function useInvalidateWork(): () => Promise<void> {
  const qc = useQueryClient();
  return () => invalidateWork(qc);
}
