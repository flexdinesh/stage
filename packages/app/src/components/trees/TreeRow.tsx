import { Link } from "react-router";
import { useTree } from "../../api/queries.ts";
import type { TreeSummary } from "../../api/types.ts";
import { dateTime, relativeTime, shortDate } from "../../lib/format.ts";
import { closedLabel, nextActionText, rollupLabel, treeClosed, treeMetaParts, treeProjectLabel, treeRollup, type Rollup } from "../../lib/trees.ts";
import { RowCopyLink } from "../StageLinks.tsx";
import { BlockedReason, useStartReadyTasks } from "../LaunchGate.tsx";
import { Button, Chip, DisclosureButton, InlineError, MetaLine, Row, RowList, Skeleton, SkeletonBar, TextLink, buttonClass, cx, errorText, toneClass } from "../ui.tsx";

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
      <MetaLine className="tabular lg:hidden">
        {active}
        {created}
      </MetaLine>
    );
  return (
    <p className="flex flex-col items-end font-mono text-meta whitespace-nowrap text-muted tabular max-lg:hidden">
      {active}
      {created}
    </p>
  );
}

/** Tree row grid: title block, times (laptop), state bar, action. Shared with its skeleton. */
const TREE_ROW_GRID =
  "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-3 py-row md:grid-cols-[minmax(0,1fr)_240px_128px] md:gap-x-5 lg:grid-cols-[minmax(0,1fr)_auto_240px_128px]";

const TREE_TITLE_WIDTHS = ["w-48", "w-36", "w-56"];

/** Loading tree list rows: the tree row grid in bars. */
export function TreeRowsSkeleton({ count = 3 }: { count?: number }) {
  return (
    <Skeleton label="Loading trees">
      <RowList>
        {Array.from({ length: count }, (_, i) => (
          <Row key={i} bare>
            <div className={TREE_ROW_GRID}>
              <div className="flex min-w-0 flex-col gap-1.5 py-0.5">
                <SkeletonBar className={cx("h-4 max-w-full", TREE_TITLE_WIDTHS[i % TREE_TITLE_WIDTHS.length])} />
                <SkeletonBar className="h-3 w-28" />
                <SkeletonBar className="h-3 w-44 max-w-full" />
              </div>
              <div className="flex flex-col items-end gap-1.5 max-lg:hidden">
                <SkeletonBar className="h-3 w-24" />
                <SkeletonBar className="h-3 w-32" />
              </div>
              <div className="order-3 col-span-2 flex min-w-0 flex-col gap-1.5 md:order-none md:col-span-1">
                <SkeletonBar className="h-2 w-full" />
                <div className="flex justify-between gap-2">
                  <SkeletonBar className="h-3 w-20" />
                  <SkeletonBar className="h-3 w-16" />
                </div>
              </div>
              <div className="flex justify-end">
                <SkeletonBar className="h-control-sm w-14" />
              </div>
            </div>
          </Row>
        ))}
      </RowList>
    </Skeleton>
  );
}

/**
 * A tree in a ruled list: title and mono meta, its times, the state bar with its next step, and one action. A done or
 * archived tree carries its chip and only offers Open. With `tasks`, the task count folds the members in place (the
 * `controls` element holds them).
 */
export function TreeRow({ tree, tasks }: { tree: TreeSummary; tasks?: { open: boolean; onToggle: () => void; controls: string } }) {
  const rollup = treeRollup(tree.counts);
  const next = rollup.next;
  const closed = treeClosed(tree);
  const detail = useTree(tree.id, !closed && next.kind === "start");
  const { start, count, reason: startBlock } = useStartReadyTasks(detail.data?.tasks, detail.error);
  const chip = closedLabel(tree, tree.counts);
  const to = `/trees/${tree.id}`;
  const failures = start.data?.errors ?? [];
  const error = errorText(start.error) ?? (failures[0] === undefined ? null : `#${failures[0].taskId}: ${failures[0].error}`);
  const [memberCount, ...meta] = treeMetaParts(tree);

  return (
    <div className={cx("group/row", TREE_ROW_GRID)}>
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <TextLink to={to} className="min-w-0 text-body font-medium break-words">
            {tree.title}
          </TextLink>
          {tree.completedAt === null ? null : (
            <Chip tone="ok" dot title={`Completed ${dateTime(tree.completedAt)}`}>
              completed {relativeTime(tree.completedAt)}
            </Chip>
          )}
          {chip === null ? null : <Chip {...(tree.closedAt === null ? {} : { title: dateTime(tree.closedAt) })}>{chip}</Chip>}
        </div>
        <p className="mt-0.5 font-mono text-meta text-fg [overflow-wrap:anywhere]">{treeProjectLabel(tree)}</p>
        <MetaLine className="mt-0.5 tabular">
          {tasks === undefined ? (
            memberCount
          ) : (
            <DisclosureButton
              open={tasks.open}
              onClick={tasks.onToggle}
              aria-controls={tasks.controls}
              // Starts with the visible text (label in name); aria-expanded says shown or hidden.
              aria-label={`${memberCount} in ${tree.title}`}
              // Inline in the meta line; the hit area reaches past the text without growing the row.
              className="relative align-bottom transition-colors duration-150 hover:text-fg before:absolute before:-inset-x-1 before:-inset-y-2"
            >
              {memberCount}
            </DisclosureButton>
          )}
          {meta}
        </MetaLine>
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
            {tree.counts.completed}/{tree.counts.total} completed
          </span>
        </div>
      </div>

      <div className="flex items-center justify-end gap-1.5">
        {closed ? (
          <Link to={to} className={buttonClass("secondary", true)}>
            Open
          </Link>
        ) : next.kind === "answer" ? (
          <Link to={to} className={buttonClass("primary", true)}>
            View tasks
          </Link>
        ) : next.kind === "start" ? (
          <BlockedReason reason={startBlock ?? undefined} showOnPhone>
            <Button small variant="primary" disabled={startBlock !== null} busy={start.isPending} onClick={() => start.mutate()}>
              Start ready <span className="font-mono tabular">{detail.data === undefined ? next.count : count}</span>
            </Button>
          </BlockedReason>
        ) : (
          <Link to={to} className={buttonClass("secondary", true)}>
            Open
          </Link>
        )}
        <RowCopyLink subject={{ kind: "tree", id: tree.id, title: tree.title }} />
      </div>

      {error === null ? null : (
        <InlineError truncate className="order-4 col-span-2 md:col-span-3">
          {error}
        </InlineError>
      )}
    </div>
  );
}
