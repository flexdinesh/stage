import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { TreeSummary } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { dateTime, relativeTime, shortDate } from "../../lib/format.ts";
import { closedLabel, nextActionText, rollupLabel, treeClosed, treeMeta, treeProjectLabel, treeRollup, type Rollup } from "../../lib/trees.ts";
import { Button, Chip, buttonClass, cx, errorText, toneClass } from "../ui.tsx";

/** Above this many members the bar draws one stretch per state instead of one cell per task. */
const CELL_LIMIT = 30;

/**
 * Segmented state progress bar: one square cell per member in its state tone, finished work first. Not-started
 * members (blocked, backlog) are hollow. The whole bar carries one accessible label with the counts.
 */
export function TreeStateBar({ rollup, className }: { rollup: Rollup; className?: string }) {
  const label = rollupLabel(rollup);
  const perTask = rollup.counts.total <= CELL_LIMIT;
  const cells = rollup.segments.flatMap((s) =>
    perTask ? Array.from({ length: s.count }, (_, i) => ({ key: `${s.key}-${i}`, s, grow: 1 })) : [{ key: s.key, s, grow: s.count }],
  );
  return (
    <div role="img" aria-label={label} title={label} className={cx("flex h-2 w-full gap-px", className)}>
      {cells.map(({ key, s, grow }) => (
        <span
          key={key}
          style={{ flexGrow: grow }}
          className={cx(
            "min-w-0 basis-0",
            toneClass(s.tone),
            s.hollow ? "border border-line-strong" : s.key === "cancelled" ? "bg-[color-mix(in_srgb,var(--c)_45%,transparent)]" : "bg-[var(--c)]",
          )}
        />
      ))}
    </div>
  );
}

/** Last activity (relative) and creation (date anchor), exact times on hover. */
function TreeWhen({ tree, inline = false }: { tree: TreeSummary; inline?: boolean }) {
  const active = (
    <span title={`Last activity ${dateTime(tree.lastActivityAt)}`} className="text-fg">
      active {relativeTime(tree.lastActivityAt)}
    </span>
  );
  const created = <span title={`Created ${dateTime(tree.createdAt)}`}>created {shortDate(tree.createdAt)}</span>;
  if (inline)
    return (
      <p className="font-mono text-meta text-muted tabular lg:hidden">
        {active} · {created}
      </p>
    );
  return (
    <p className="flex flex-col items-end font-mono text-meta whitespace-nowrap text-muted tabular max-lg:hidden">
      {active}
      {created}
    </p>
  );
}

/**
 * A tree in a ruled list: title and mono meta, its times, the state bar with its next step, and one action. A done or
 * archived tree carries its chip and only offers Open.
 */
export function TreeRow({ tree }: { tree: TreeSummary }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTree(tree.id), onSettled: invalidate });
  const rollup = treeRollup(tree.counts);
  const next = rollup.next;
  const closed = treeClosed(tree);
  const chip = closedLabel(tree, tree.counts);
  const to = `/trees/${tree.id}`;
  const failures = start.data?.errors ?? [];
  const error = errorText(start.error) ?? (failures[0] === undefined ? null : `#${failures[0].taskId}: ${failures[0].error}`);

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-3 py-2.5 md:grid-cols-[minmax(0,1fr)_240px_128px] md:gap-x-5 md:py-2 lg:grid-cols-[minmax(0,1fr)_auto_240px_128px]">
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <Link to={to} className="min-w-0 text-body font-medium break-words hover:underline">
            {tree.title}
          </Link>
          {tree.settledAt === null ? null : (
            <Chip tone="ok" dot title={`Settled ${dateTime(tree.settledAt)}`}>
              settled {relativeTime(tree.settledAt)}
            </Chip>
          )}
          {chip === null ? null : <Chip {...(tree.closedAt === null ? {} : { title: dateTime(tree.closedAt) })}>{chip}</Chip>}
        </div>
        <p className="mt-0.5 font-mono text-meta text-fg [overflow-wrap:anywhere]">{treeProjectLabel(tree)}</p>
        <p className="mt-0.5 font-mono text-meta text-muted tabular">
          {treeMeta(tree)}
        </p>
        <TreeWhen tree={tree} inline />
      </div>

      <TreeWhen tree={tree} />

      <div className="order-3 col-span-2 flex min-w-0 flex-col gap-1.5 md:order-none md:col-span-1">
        <TreeStateBar rollup={rollup} />
        <div className="flex justify-between gap-2 font-mono text-meta-sm text-muted tabular">
          <span className={cx("truncate", next.kind === "answer" && "text-need-fg", next.kind === "retry" && "text-bad")}>
            {nextActionText(next)}
          </span>
          <span className="shrink-0">
            {tree.counts.settled}/{tree.counts.total} settled
          </span>
        </div>
      </div>

      <div className="flex justify-end">
        {closed ? (
          <Link to={to} className={buttonClass("secondary", true)}>
            Open
          </Link>
        ) : next.kind === "answer" ? (
          <Link to={to} className={buttonClass("primary", true)}>
            Answer
          </Link>
        ) : next.kind === "start" ? (
          <Button small variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
            Start ready <span className="font-mono tabular">{next.count}</span>
          </Button>
        ) : (
          <Link to={to} className={buttonClass("secondary", true)}>
            Open
          </Link>
        )}
      </div>

      {error === null ? null : (
        <p className="order-4 col-span-2 truncate font-mono text-meta text-bad md:col-span-3" title={error}>
          {error}
        </p>
      )}
    </div>
  );
}
