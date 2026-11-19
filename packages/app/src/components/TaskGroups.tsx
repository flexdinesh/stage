import { useState, type ReactNode } from "react";
import type { Schedule, Task } from "../api/types.ts";
import { isFolded, needsEpisode, parseFold, toggleFold, type GroupKey } from "../lib/dashboard.ts";
import { usePlatform } from "../platform/context.tsx";
import { IconChevronDown } from "./icons.tsx";
import { ScheduleRow } from "./ScheduleList.tsx";
import { TaskRow } from "./TaskList.tsx";
import { Panel, cx, toneClass, type ChipTone } from "./ui.tsx";

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
  settled: {
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
  group,
  count,
  meta,
  collapsed = false,
  onToggle,
  actions,
  children,
}: {
  group: GroupKey;
  count: number;
  /** Extra mono meta after the count. */
  meta?: string | undefined;
  collapsed?: boolean;
  onToggle?: (() => void) | undefined;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const g = GROUP_META[group];
  // self-baseline: flex baseline comes from the text, not the square/chevron, so Panel's cap-trimmed h2 centers the caps.
  const label = (
    <span className={cx(toneClass(g.tone), "inline-flex items-center gap-2 self-baseline")}>
      <span aria-hidden className="size-1.5 bg-[var(--c)]" />
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
          <button
            type="button"
            aria-expanded={!collapsed}
            onClick={onToggle}
            // The whole header bar toggles (actions stay above it).
            className="group/fold label-caps inline-flex items-center gap-1.5 text-fg after:absolute after:inset-0"
          >
            <span aria-hidden className={cx("inline-flex text-muted transition-[color,transform] duration-150 group-hover/fold:text-fg", collapsed && "-rotate-90")}>
              <IconChevronDown size={12} />
            </span>
            {label}
          </button>
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

/** Strip of group counts; a cell filters the view to its group. */
export function StatStrip({
  groups,
  counts,
  selected,
  onToggle,
}: {
  groups: readonly GroupKey[];
  counts: Record<GroupKey, number>;
  selected: GroupKey | null;
  onToggle: (key: GroupKey) => void;
}) {
  return (
    <div role="group" aria-label="Filter by state" className="mb-4 flex border border-line bg-surface">
      {groups.map((key) => {
        const g = GROUP_META[key];
        const on = selected === key;
        const n = counts[key];
        return (
          <button
            key={key}
            type="button"
            aria-pressed={on}
            title={g.stat}
            onClick={() => onToggle(key)}
            className={cx(
              toneClass(g.tone),
              "flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 border-r border-line px-1 py-1.5 whitespace-nowrap max-lg:last-of-type:border-r-0 transition-colors duration-150 lg:flex-none lg:flex-row lg:justify-start lg:gap-2 lg:px-3",
              // Phone fits six cells; Completed stays reachable from its collapsible group.
              key === "settled" && "max-md:hidden",
              key === "backlog" && "max-md:border-r-0",
              on ? "bg-[color-mix(in_srgb,var(--c)_10%,transparent)] shadow-[inset_0_-2px_var(--c)]" : "hover:bg-panel",
            )}
          >
            <span className="flex items-center gap-1.5">
              {n > 0 ? <span aria-hidden className="size-1.5 bg-[var(--c)]" /> : null}
              <span className={cx("tabular text-title font-bold", on ? "text-[var(--cf)]" : n === 0 ? "text-muted" : "text-fg")}>{n}</span>
            </span>
            <span className="label-caps text-label lg:hidden">{g.short}</span>
            <span className="label-caps hidden lg:inline">{g.stat}</span>
          </button>
        );
      })}
      <div className="hidden flex-1 items-center justify-end px-3 whitespace-nowrap xl:flex">
        {selected === null ? (
          <span className="font-mono text-meta-sm text-muted">click a count to filter</span>
        ) : (
          <button
            type="button"
            onClick={() => onToggle(selected)}
            className="font-mono text-meta-sm text-muted underline-offset-3 hover:text-fg hover:underline"
          >
            clear filter
          </button>
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
  if (items.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {items.map((item) => (
        <li key={item.kind === "task" ? `task-${item.task.id}` : `schedule-${item.schedule.id}`} className="border-b border-line last:border-b-0">
          {item.kind === "task" ? (
            <TaskRow task={item.task} showProject={showProject} showTree />
          ) : (
            <ScheduleRow schedule={item.schedule} showProject={showProject} actions="run" />
          )}
        </li>
      ))}
    </ul>
  );
}
