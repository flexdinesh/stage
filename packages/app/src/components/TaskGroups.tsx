import { useState, type ReactNode } from "react";
import type { Schedule, Task } from "../api/types.ts";
import { isFolded, needsEpisode, parseFold, toggleFold, type GroupKey } from "../lib/dashboard.ts";
import { usePlatform } from "../platform/context.tsx";
import { ScheduleRow } from "./ScheduleList.tsx";
import { TaskRow, TaskRowsSkeleton } from "./TaskList.tsx";
import { DisclosureButton, EmptyState, Marker, Meta, Panel, Row, RowList, SkeletonBar, TextButton, cx, toneClass, type ChipTone } from "./ui.tsx";

export interface GroupMeta {
  title: string;
  /** Stat strip label (laptop) and its phone short form. */
  stat: string;
  short: string;
  tone: ChipTone;
  empty: string;
}

export const GROUP_META: Record<GroupKey, GroupMeta> = {
  needs: {
    title: "Needs input",
    stat: "Needs input",
    short: "need",
    tone: "need",
    empty: "Nothing waiting on you. Tasks waiting for input land here. Session alerts are in Notifications.",
  },
  done: {
    title: "Awaiting review",
    stat: "Review",
    short: "review",
    tone: "done",
    empty: "Finished runs wait here until you complete their tasks.",
  },
  ready: {
    title: "Ready",
    stat: "Ready",
    short: "ready",
    tone: "idle",
    empty: "Nothing ready. Tree tasks whose needs are completed in manual mode, and tree roots, wait here for Start.",
  },
  failed: {
    title: "Failed / interrupted",
    stat: "Failed",
    short: "fail",
    tone: "bad",
    empty: "No failed or interrupted tasks.",
  },
  running: {
    title: "Running",
    stat: "Running",
    short: "run",
    tone: "run",
    empty: "Nothing running. Start a task from the backlog.",
  },
  backlog: {
    title: "Backlog",
    stat: "Backlog",
    short: "backlog",
    tone: "idle",
    empty: "Backlog is empty. Add one with New task.",
  },
  scheduled: {
    title: "Scheduled · next 24h",
    stat: "Scheduled",
    short: "sched",
    tone: "idle",
    empty: "Nothing scheduled in the next 24 hours. Pick At time or Repeat under Start in New task.",
  },
  completed: {
    title: "Completed · last 7 days",
    stat: "Completed",
    short: "done",
    tone: "ok",
    empty: "Nothing completed in the last 7 days.",
  },
};

/** Per-device collapsed groups for one view (`GroupFold` in preference storage). */
export function useGroupFold(storageKey: string) {
  const { storage } = usePlatform();
  const [fold, setFold] = useState(() => parseFold(storage.get(storageKey)));
  return {
    /** Whether `key` renders collapsed; Needs input reopens when it lists a task not seen when collapsed. */
    folded: (key: GroupKey, tasks: readonly Task[]) => isFolded(fold, key, key === "needs" ? tasks.map(needsEpisode) : undefined),
    toggle: (key: GroupKey, tasks: readonly Task[]) => {
      const next = toggleFold(fold, key, key === "needs" ? tasks.map(needsEpisode) : undefined);
      setFold(next);
      storage.set(storageKey, JSON.stringify(next));
    },
  };
}

/** A group's ruled panel: tone square and title (a fold toggle when `onToggle`), its count, actions while open. */
export function GroupPanel({
  info: g,
  count,
  meta,
  collapsed = false,
  onToggle,
  actions,
  children,
}: {
  info: GroupMeta;
  count: number;
  /** Extra mono meta after the count. */
  meta?: string | undefined;
  collapsed?: boolean;
  onToggle?: (() => void) | undefined;
  actions?: ReactNode;
  children: ReactNode;
}) {
  // self-baseline: flex baseline comes from the text, not the square/chevron, so Panel's cap-trimmed h2 centers the caps.
  const label = (
    <span className="inline-flex items-center gap-2 self-baseline">
      <Marker tone={g.tone} />
      <span className="self-baseline">{g.title}</span>
    </span>
  );
  return (
    <Panel
      flush
      collapsed={collapsed}
      title={
        onToggle === undefined ? (
          label
        ) : (
          <DisclosureButton
            open={!collapsed}
            onClick={onToggle}
            // The whole header bar toggles (actions stay above it).
            className="label-caps text-fg after:absolute after:inset-0"
          >
            {label}
          </DisclosureButton>
        )
      }
      meta={
        <span className="tabular">
          {count}
          {meta === undefined ? null : ` · ${meta}`}
        </span>
      }
      {...(collapsed || actions === undefined ? {} : { actions })}
    >
      {children}
    </Panel>
  );
}

/** Loading group: a panel with a bar for its title over task row skeletons. */
export function GroupPanelSkeleton({ rows = 3, showProject = false }: { rows?: number; showProject?: boolean }) {
  return (
    <Panel flush title={<SkeletonBar onPanel className="h-2.5 w-32" />}>
      <TaskRowsSkeleton count={rows} showProject={showProject} />
    </Panel>
  );
}

/**
 * A stat cell's count: the tone square (only while non-zero) and the bold tabular number, Slate at zero. `selected`
 * sets the number in the tone's text color. A null count is loading: a bar holds its place.
 */
export function StatCount({ tone, count, selected = false }: { tone: ChipTone; count: number | null; selected?: boolean }) {
  if (count === null)
    return (
      <span className="skeleton-reveal flex h-6 items-center">
        <SkeletonBar className="h-4 w-4" />
      </span>
    );
  return (
    <span className={cx(toneClass(tone), "flex items-center gap-1.5")}>
      {count > 0 ? <Marker tone={tone} /> : null}
      <span className={cx("tabular text-title font-bold", selected ? "text-(--cf)" : count === 0 ? "text-muted" : "text-fg")}>{count}</span>
    </span>
  );
}

/**
 * Strip of group counts; a cell filters the view to its group. A trailing `completed` cell hides on phone.
 * `counts` null while loading: bars stand in for the numbers and the cells are disabled.
 */
export function StatStrip<K extends string>({
  groups,
  meta,
  counts,
  selected,
  onToggle,
}: {
  groups: readonly K[];
  meta: Record<K, GroupMeta>;
  counts: Record<K, number> | null;
  selected: K | null;
  onToggle: (key: K) => void;
}) {
  // Phone fits six cells; the cell before a hidden Completed closes the row.
  const phoneLast = groups.at(-1) === "completed" ? groups.at(-2) : undefined;
  return (
    <div role="group" aria-label="Filter by state" aria-busy={counts === null} className="mb-4 flex border border-line bg-surface">
      {groups.map((key) => {
        const g = meta[key];
        const on = selected === key;
        const n = counts === null ? null : counts[key];
        return (
          <button
            key={key}
            type="button"
            aria-pressed={on}
            title={g.stat}
            disabled={n === null}
            onClick={() => onToggle(key)}
            className={cx(
              toneClass(g.tone),
              "flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 border-r border-line px-1 py-1.5 whitespace-nowrap max-lg:last-of-type:border-r-0 transition-colors duration-150 lg:flex-none lg:flex-row lg:justify-start lg:gap-2 lg:px-3",
              // Completed stays reachable from its collapsible group.
              key === "completed" && "max-md:hidden",
              key === phoneLast && "max-md:border-r-0",
              on ? "bg-[color-mix(in_srgb,var(--c)_10%,transparent)] shadow-[inset_0_-2px_var(--c)]" : "enabled:hover:bg-panel",
            )}
          >
            <StatCount tone={g.tone} count={n} selected={on} />
            <span className="label-caps text-label lg:hidden">{g.short}</span>
            <span className="label-caps hidden lg:inline">{g.stat}</span>
          </button>
        );
      })}
      <div className="hidden flex-1 items-center justify-end px-3 whitespace-nowrap xl:flex">
        {selected === null ? (
          <Meta small>click a count to filter</Meta>
        ) : (
          <TextButton onClick={() => onToggle(selected)} className="font-mono text-meta-sm text-muted hover:text-fg">
            clear filter
          </TextButton>
        )}
      </div>
    </div>
  );
}

/** Scheduled group: timed backlog tasks and due schedules interleaved, soonest first. */
type ScheduledItem = { kind: "task"; task: Task; at: number } | { kind: "schedule"; schedule: Schedule; at: number };

const ms = (iso: string | null) => (iso === null ? Number.POSITIVE_INFINITY : new Date(iso).getTime());

export function ScheduledRows({
  tasks,
  schedules,
  emptyText,
  showProject,
}: {
  tasks: readonly Task[];
  schedules: readonly Schedule[];
  emptyText: string;
  showProject: boolean;
}) {
  const items = [
    ...tasks.map((task): ScheduledItem => ({ kind: "task", task, at: ms(task.startAt) })),
    ...schedules.map((schedule): ScheduledItem => ({ kind: "schedule", schedule, at: ms(schedule.nextRunAt) })),
  ].toSorted((a, b) => a.at - b.at);
  if (items.length === 0) return <EmptyState inset>{emptyText}</EmptyState>;
  return (
    <RowList>
      {items.map((item) => (
        <Row key={item.kind === "task" ? `task-${item.task.id}` : `schedule-${item.schedule.id}`} bare>
          {item.kind === "task" ? (
            <TaskRow task={item.task} showProject={showProject} showTree />
          ) : (
            <ScheduleRow schedule={item.schedule} showProject={showProject} actions="run" />
          )}
        </Row>
      ))}
    </RowList>
  );
}
