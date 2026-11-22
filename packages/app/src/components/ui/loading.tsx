import type { ReactNode } from "react";
import { cx } from "./cx.ts";
import { ErrorText, Muted } from "./feedback.tsx";
import { queryStateView } from "../../lib/queryState.ts";

/**
 * One square placeholder bar. Size it with `className` (height, width); `onPanel` darkens it for Bench Gray
 * grounds (panel header bars). Pulses unless the visitor asks for reduced motion.
 */
export function SkeletonBar({ className, onPanel = false }: { className?: string; onPanel?: boolean }) {
  return <span aria-hidden className={cx("block shrink-0 motion-safe:animate-pulse", onPanel ? "bg-line" : "bg-panel", className)} />;
}

/**
 * A loading placeholder that mirrors the loaded layout. It holds its space but stays invisible for 200ms
 * (`.skeleton-reveal`), so fast loads show nothing; screen readers hear `label` once.
 */
export function Skeleton({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div role="status" aria-busy="true" className={cx("skeleton-reveal", className)}>
      <span className="sr-only">{label}</span>
      {children}
    </div>
  );
}

/** Ruled bar rows: the default loading view, at home in a flush panel or a padded one. */
export function SkeletonRows({ rows = 3, label = "Loading" }: { rows?: number; label?: string }) {
  const widths = ["w-3/5", "w-2/5", "w-1/2", "w-2/3", "w-1/3"];
  return (
    <Skeleton label={label}>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex h-10 items-center border-b border-line px-3 last:border-b-0">
          <SkeletonBar className={cx("h-3.5", widths[i % widths.length])} />
        </div>
      ))}
    </Skeleton>
  );
}

/**
 * Loading / error / empty wrapper for a query result. `stale` dims kept data while a new key loads. With `hasData`,
 * an error (a failed background refetch) shows above the kept view instead of replacing it, so drafts survive.
 */
export function QueryState({
  isPending,
  error,
  hasData,
  empty,
  emptyText = "Nothing here.",
  skeleton,
  stale,
  children,
}: {
  isPending: boolean;
  error: unknown;
  /** Data is cached (`query.data !== undefined`): keep showing it on error. */
  hasData: boolean;
  empty?: boolean;
  emptyText?: string;
  /** Replaces the default ruled rows; mirror the loaded layout. */
  skeleton?: ReactNode;
  /**
   * Previous data on screen while the new query loads (`isPlaceholderData`). Passing it wraps the children
   * in one block (see `Stale`).
   */
  stale?: boolean;
  children: ReactNode;
}) {
  const { view, banner } = queryStateView({ isPending, error, hasData, empty });
  if (view === "pending") return skeleton ?? <SkeletonRows />;
  if (view === "error") return <ErrorText error={error} />;
  const body = view === "empty" ? <Muted>{emptyText}</Muted> : stale === undefined ? children : <Stale stale={stale}>{children}</Stale>;
  // One shape with or without the banner: a changed tree would remount the body and drop its drafts.
  return (
    <>
      {banner ? <ErrorText error={error} /> : null}
      {body}
    </>
  );
}

/** Kept data under a loading key: one block, dimmed and busy until the new data lands. */
export function Stale({ stale, className, children }: { stale: boolean; className?: string; children: ReactNode }) {
  return (
    <div aria-busy={stale} className={cx("transition-opacity duration-150", stale && "opacity-60", className)}>
      {children}
    </div>
  );
}
