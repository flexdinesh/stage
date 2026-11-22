// What a query-backed view shows (components/ui/loading.tsx QueryState). A failed background refetch keeps the
// cached data on screen with the error above it: replacing the view would unmount open drafts and drawers.

export type QueryView = "pending" | "error" | "empty" | "content";

export function queryStateView(q: { isPending: boolean; error: unknown; hasData: boolean; empty?: boolean | undefined }): {
  view: QueryView;
  /** The error shows above kept data. */
  banner: boolean;
} {
  if (q.isPending) return { view: "pending", banner: false };
  const failed = q.error !== null && q.error !== undefined;
  if (failed && !q.hasData) return { view: "error", banner: false };
  return { view: q.empty === true ? "empty" : "content", banner: failed };
}
