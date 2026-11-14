import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, type ArchivedFilter } from "./client.ts";
import type { ScheduleListQuery, SchedulePreviewRequest, StatsQuery, TaskListQuery, ThreadListQuery } from "./types.ts";

export const keys = {
  t3Status: ["t3Status"],
  catalog: ["catalog"],
  projects: ["projects"],
  tasks: ["tasks"],
  task: ["task"],
  threads: ["threads"],
  thread: ["thread"],
  questions: ["questions"],
  continuations: ["continuations"],
  snippets: ["snippets"],
  stats: ["stats"],
  preferences: ["preferences"],
  badge: ["badge"],
  notifications: ["notifications"],
  schedules: ["schedules"],
  schedule: ["schedule"],
  schedulePreview: ["schedulePreview"],
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

export const useSchedules = (q: ScheduleListQuery = {}) =>
  useQuery({ queryKey: [...keys.schedules, q], queryFn: () => api.schedules(q) });

export const useSchedule = (id: number) => useQuery({ queryKey: [...keys.schedule, id], queryFn: () => api.schedule(id) });

/** Next fires for a cron + zone; null input disables the query. Errors carry the parse message. */
export const useSchedulePreview = (body: SchedulePreviewRequest | null) =>
  useQuery({
    queryKey: [...keys.schedulePreview, body?.cron ?? "", body?.tz ?? ""],
    queryFn: () => (body === null ? Promise.resolve({ next: [] }) : api.previewSchedule(body)),
    enabled: body !== null,
    refetchInterval: false,
    staleTime: 30_000,
    retry: false,
    placeholderData: keepPreviousData,
  });

export const useThreads = (q: ThreadListQuery) =>
  useQuery({ queryKey: [...keys.threads, q], queryFn: () => api.threads(q), placeholderData: keepPreviousData });

export const useThread = (id: string) =>
  useQuery({ queryKey: [...keys.thread, id], queryFn: () => api.thread(id), enabled: id !== "" });

/** Pending questions, read fresh from T3; only while the thread has pending requests. */
export const useThreadQuestions = (id: string, enabled: boolean) =>
  useQuery({ queryKey: [...keys.questions, id], queryFn: () => api.questions(id), enabled: enabled && id !== "", retry: false });

export const useContinuations = (thread?: string) =>
  useQuery({ queryKey: [...keys.continuations, thread], queryFn: () => api.continuations(thread) });

/** External threads needing input (not task-owned; tasks come from useTasks). */
export const useNeedsInputThreads = (project?: number) =>
  useThreads(project === undefined ? { needsInput: true } : { needsInput: true, project });

/** External threads observed running (not task-owned; tasks come from useTasks). */
export const useRunningThreads = (project?: number) =>
  useThreads(project === undefined ? { running: true } : { running: true, project });

/** External threads settled since `since` (not task-owned; tasks come from useTasks), newest settle first. */
export const useSettledThreads = (since: string, project?: number) =>
  useThreads(
    project === undefined
      ? { settledFrom: since, limit: SETTLED_THREAD_LIMIT }
      : { settledFrom: since, limit: SETTLED_THREAD_LIMIT, project },
  );
const SETTLED_THREAD_LIMIT = 500;

/** Tasks + external threads needing input (app badge, nav count). */
export const useBadge = () => useQuery({ queryKey: keys.badge, queryFn: api.badge });

export const latestNotificationsKey = [...keys.notifications, "latest"];

/** Latest notifications + unread count (bell, toasts). Polled. */
export const useNotifications = () =>
  useQuery({ queryKey: latestNotificationsKey, queryFn: () => api.notifications({ limit: 50 }) });

/** Notifications drawer: current needs input first, then recent task/thread groups. */
export const useNotificationInbox = () =>
  useInfiniteQuery({
    queryKey: [...keys.notifications, "inbox"],
    queryFn: ({ pageParam }) => api.notificationGroups({ limit: 50, cursor: pageParam === "" ? undefined : pageParam }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });

/** Mounted on expansion; history is notification events, not a run lifecycle. */
export const useNotificationHistory = (group: string) =>
  useInfiniteQuery({
    queryKey: [...keys.notifications, "history", group],
    queryFn: ({ pageParam }) => api.notifications({ group, limit: 20, before: pageParam === 0 ? undefined : pageParam }),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextBefore ?? undefined,
  });

export const useSnippets = () => useQuery({ queryKey: keys.snippets, queryFn: api.snippets });

// Changes only on task create or pin; no polling.
export const usePreferences = () =>
  useQuery({ queryKey: keys.preferences, queryFn: api.preferences, refetchInterval: false, staleTime: 30_000 });

export const useStats = (q: StatsQuery) =>
  useQuery({ queryKey: [...keys.stats, q], queryFn: () => api.stats(q), placeholderData: keepPreviousData });

/** Task mutations can change any task, thread, stat or prefill view: refresh them all. */
export function invalidateWork(qc: QueryClient): Promise<void> {
  return Promise.all(
    [
      keys.tasks,
      keys.task,
      keys.threads,
      keys.thread,
      keys.questions,
      keys.continuations,
      keys.stats,
      keys.preferences,
      keys.badge,
      keys.schedules,
      keys.schedule,
    ].map((queryKey) =>
      qc.invalidateQueries({ queryKey }),
    ),
  ).then(() => undefined);
}

export function useInvalidateWork(): () => Promise<void> {
  const qc = useQueryClient();
  return () => invalidateWork(qc);
}
