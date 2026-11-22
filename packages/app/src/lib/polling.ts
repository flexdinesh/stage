// Poll intervals. Polling is opt-in per query (StageApp sets no default): live views poll, data that changes only
// through this app's own mutations refreshes by invalidation.

export const POLL = {
  /** State that moves on its own while you watch: tasks, trees, notifications, connections. */
  live: 5000,
  /** Slower-moving lists: machines, projects, schedules. */
  slow: 15_000,
} as const;

/**
 * An infinite list polls while only its first page is loaded. A timed refetch re-requests every loaded page in
 * sequence, so after "Load more" the list refreshes on invalidation only.
 */
export function pollFirstPage(pages: number | undefined, ms: number): number | false {
  return (pages ?? 1) > 1 ? false : ms;
}
