import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, type ArchivedFilter } from "./client.ts";
import type { StatsQuery, TaskListQuery, ThreadListQuery } from "./types.ts";

export const keys = {
  t3Status: ["t3Status"],
  catalog: ["catalog"],
  projects: ["projects"],
  tasks: ["tasks"],
  task: ["task"],
  threads: ["threads"],
  templates: ["templates"],
  stats: ["stats"],
  preferences: ["preferences"],
  badge: ["badge"],
  notifications: ["notifications"],
} as const;

export const useT3Status = () => useQuery({ queryKey: keys.t3Status, queryFn: api.t3Status });

// Catalog is cached server-side for 10 min; no need to poll it.
export const useCatalog = () =>
  useQuery({ queryKey: keys.catalog, queryFn: api.catalog, refetchInterval: false, staleTime: 60_000, retry: false });

export const useProjects = (archived: ArchivedFilter = "false") =>
  useQuery({ queryKey: [...keys.projects, "list", archived], queryFn: () => api.projects(archived) });

export const useProject = (id: number) =>
  useQuery({ queryKey: [...keys.projects, "one", id], queryFn: () => api.project(id) });

export const useTasks = (q: TaskListQuery = {}) =>
  useQuery({ queryKey: [...keys.tasks, q], queryFn: () => api.tasks(q) });

export const useTask = (id: number) => useQuery({ queryKey: [...keys.task, id], queryFn: () => api.task(id) });

export const useThreads = (q: ThreadListQuery) =>
  useQuery({ queryKey: [...keys.threads, q], queryFn: () => api.threads(q), placeholderData: keepPreviousData });

/** External threads needing input (not task-owned; tasks come from useTasks). */
export const useNeedsInputThreads = (project?: number) =>
  useThreads(project === undefined ? { needsInput: true } : { needsInput: true, project });

/** Tasks + external threads needing input (app badge, nav count). */
export const useBadge = () => useQuery({ queryKey: keys.badge, queryFn: api.badge });

export const latestNotificationsKey = [...keys.notifications, "latest"];

/** Latest notifications + unread count (bell, toasts). Polled. */
export const useNotifications = () =>
  useQuery({ queryKey: latestNotificationsKey, queryFn: () => api.notifications({ limit: 50 }) });

/** Notifications drawer: newest first, older pages on demand. */
export const useNotificationInbox = () =>
  useInfiniteQuery({
    queryKey: [...keys.notifications, "inbox"],
    // pageParam 0 = newest page.
    queryFn: ({ pageParam }) => api.notifications({ limit: 50, before: pageParam === 0 ? undefined : pageParam }),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextBefore ?? undefined,
  });

export const useTemplates = () => useQuery({ queryKey: keys.templates, queryFn: api.templates });

// Changes only on task create or pin; no polling.
export const usePreferences = () =>
  useQuery({ queryKey: keys.preferences, queryFn: api.preferences, refetchInterval: false, staleTime: 30_000 });

export const useStats = (q: StatsQuery) =>
  useQuery({ queryKey: [...keys.stats, q], queryFn: () => api.stats(q), placeholderData: keepPreviousData });

/** Task mutations can change any task, thread, stat or prefill view: refresh them all. */
export function invalidateWork(qc: QueryClient): Promise<void> {
  return Promise.all(
    [keys.tasks, keys.task, keys.threads, keys.stats, keys.preferences, keys.badge].map((queryKey) =>
      qc.invalidateQueries({ queryKey }),
    ),
  ).then(() => undefined);
}

export function useInvalidateWork(): () => Promise<void> {
  const qc = useQueryClient();
  return () => invalidateWork(qc);
}
