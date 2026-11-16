import { useMemo, useState } from "react";
import type { TaskEvent, TreeDetail } from "../../api/types.ts";
import { clockTime, dateTime, groupByDay } from "../../lib/format.ts";
import { taskStateLabel, taskStateTone } from "../StateBadge.tsx";
import { Button, Panel, cx, toneClass } from "../ui.tsx";

/** Server cap on TreeDetail.events (docs/api.md "Trees"). */
const EVENT_CAP = 50;

type Item = { key: string; at: string; event: TaskEvent | null };

/** What an event says: the state it entered (with T3's detail), or its detail for same-state and creation events. */
function eventText(e: TaskEvent): { lead: string; detail: string } {
  if (e.fromState === null || e.fromState === e.toState) return { lead: e.detail === "" ? taskStateLabel(e.toState) : e.detail, detail: "" };
  return { lead: taskStateLabel(e.toState), detail: e.detail };
}

/**
 * The tree's member events, newest first, grouped by local day (time, tone square of the state entered, `#id`, what
 * happened), ending with the tree's creation when every event fits. `#id` selects that member (`onSelect`).
 */
export function TreeActivity({
  tree,
  onSelect,
  limit = 12,
  className,
}: {
  tree: TreeDetail;
  onSelect?: ((id: number) => void) | undefined;
  limit?: number;
  className?: string | undefined;
}) {
  const [all, setAll] = useState(false);
  const titles = useMemo(() => new Map(tree.tasks.map((t) => [t.id, t.title])), [tree.tasks]);
  const full = tree.events.length >= EVENT_CAP;
  const items: Item[] = useMemo(() => {
    const list: Item[] = tree.events.map((e) => ({ key: `e${e.id}`, at: e.at, event: e }));
    if (!full) list.push({ key: "created", at: tree.createdAt, event: null });
    return list;
  }, [tree.events, tree.createdAt, full]);
  const shown = all ? items : items.slice(0, limit);
  const groups = groupByDay(shown);
  const footer = full || items.length > limit;
  const count = `${tree.events.length}${full ? "+" : ""} event${tree.events.length === 1 ? "" : "s"}`;

  return (
    <Panel title="Activity" meta={count} flush className={className}>
      <ol className={cx(!footer && "[&>li:last-child>ol>li:last-child]:border-b-0")}>
        {groups.map((g) => (
          <li key={g.label}>
            <h3 className="label-caps border-b border-line bg-panel px-3 py-1.5 text-muted">{g.label}</h3>
            <ol>
              {g.items.map((item) => {
                const e = item.event;
                const text = e === null ? { lead: "tree created", detail: "" } : eventText(e);
                return (
                  <li
                    key={item.key}
                    className="grid grid-cols-[40px_6px_auto_minmax(0,1fr)] items-center gap-x-2 border-b border-line px-3 py-1.5 text-small"
                  >
                    <time dateTime={item.at} title={dateTime(item.at)} className="font-mono text-meta text-muted tabular">
                      {clockTime(item.at)}
                    </time>
                    <span aria-hidden className={cx("size-1.5", toneClass(e === null ? "idle" : taskStateTone(e.toState)), "bg-[var(--c)]")} />
                    {e === null ? (
                      <span />
                    ) : onSelect === undefined ? (
                      <span className="font-mono text-meta text-muted tabular" title={titles.get(e.taskId)}>
                        #{e.taskId}
                      </span>
                    ) : (
                      <button
                        type="button"
                        onClick={() => onSelect(e.taskId)}
                        title={titles.get(e.taskId)}
                        className="font-mono text-meta text-muted tabular hover:text-fg hover:underline"
                      >
                        #{e.taskId}
                      </button>
                    )}
                    <span className="flex min-w-0 items-baseline gap-1.5">
                      <span className="shrink-0">{text.lead}</span>
                      {text.detail === "" ? null : (
                        <span className="min-w-0 truncate font-mono text-meta text-muted" title={text.detail}>
                          {text.detail}
                        </span>
                      )}
                    </span>
                  </li>
                );
              })}
            </ol>
          </li>
        ))}
      </ol>
      {footer ? (
        <div className="flex min-h-8 items-center justify-between gap-2 px-3 py-1">
          <span className="font-mono text-meta-sm text-muted">{full ? "Older events are on each task." : ""}</span>
          {items.length > limit ? (
            <Button variant="ghost" small onClick={() => setAll((v) => !v)}>
              {all ? "Show fewer" : `Show all ${items.length}`}
            </Button>
          ) : null}
        </div>
      ) : null}
    </Panel>
  );
}
