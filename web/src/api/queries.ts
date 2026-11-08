import { keepPreviousData, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, type ArchivedFilter } from "./client.ts";
import type { StatsQuery, TaskListQuery, ThreadListQuery } from "./types.ts";

export const keys = {
  t3Status: ["t3Status"],
  catalog: ["catalog"],
  projects: ["projects"],
  tasks: ["tasks"],
  task: ["task"],
  threads: ["threads"],
  chains: ["chains"],
  chain: ["chain"],
  templates: ["templates"],
  stats: ["stats"],
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

export const useChains = () => useQuery({ queryKey: keys.chains, queryFn: api.chains });

export const useChain = (id: number) => useQuery({ queryKey: [...keys.chain, id], queryFn: () => api.chain(id) });

export const useTemplates = () => useQuery({ queryKey: keys.templates, queryFn: api.templates });

export const useStats = (q: StatsQuery) =>
  useQuery({ queryKey: [...keys.stats, q], queryFn: () => api.stats(q), placeholderData: keepPreviousData });

/** Task/chain mutations can change any task, chain, thread or stat view: refresh them all. */
export function invalidateWork(qc: QueryClient): Promise<void> {
  return Promise.all(
    [keys.tasks, keys.task, keys.chains, keys.chain, keys.threads, keys.stats].map((queryKey) =>
      qc.invalidateQueries({ queryKey }),
    ),
  ).then(() => undefined);
}

export function useInvalidateWork(): () => Promise<void> {
  const qc = useQueryClient();
  return () => invalidateWork(qc);
}
