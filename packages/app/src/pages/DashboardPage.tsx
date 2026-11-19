import { HARNESS_NAME } from "../lib/harnesses.ts";
import { useState } from "react";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { Link, useSearchParams } from "react-router";
import {
  useProjects,
  useSchedules,
  useT3Status,
  useOpenCodeStatus,
  useHarnesses,
  useTasks,
  useTrees,
} from "../api/queries.ts";
import type { Schedule, Task, TaskState, TreeSummary } from "../api/types.ts";
import { IconChevronDown } from "../components/icons.tsx";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { ScheduleRow } from "../components/ScheduleList.tsx";
import { SettledRows, TaskRow, TaskRows } from "../components/TaskList.tsx";
import { TreeRow } from "../components/trees/TreeRow.tsx";
import { T3Alert } from "../components/T3Setup.tsx";
import { CompleteAllActions, CompleteAllFailures, CompletionLinger, useCompleteAll, useCompletionLinger } from "../components/QuickComplete.tsx";
import { awaitingReview, completedSince, isFolded, needsEpisode, parseFold, splitTreeMembers, toggleFold } from "../lib/dashboard.ts";
import { mergeSettled } from "../lib/settled.ts";
import { AppButton, ErrorText, PageHeader, Panel, QueryState, cx, toneClass, type ChipTone } from "../components/ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";
import { usePlatform } from "../platform/context.tsx";

type GroupKey = "needs" | "trees" | "running" | "done" | "failed" | "ready" | "backlog" | "scheduled" | "settled";

interface Group {
  key: GroupKey;
  title: string;
  stat: string;
  short: string;
  tone: ChipTone;
  states: TaskState[];
  /** Narrows `states` (backlog splits into ready, untimed and timed). */
  filter?: (t: Task) => boolean;
  empty: string;
  /** Not a stat strip cell (no `?state=` filter). */
  noStat?: boolean;
  hideEmpty?: boolean;
}

const GROUPS: Group[] = [
  {
    key: "needs",
    title: "Needs input",
    stat: "Needs input",
    short: "need",
    tone: "need",
    states: ["needs_input"],
    empty: "Nothing waiting on you. Tasks waiting for input land here. Session alerts are in Notifications.",
  },
  {
    key: "done",
    title: "Awaiting review",
    stat: "Review",
    short: "review",
    tone: "done",
    states: ["done", "settled"],
    empty: "Finished runs wait here until you complete their tasks.",
  },
  {
    key: "ready",
    title: "Ready",
    stat: "Ready",
    short: "ready",
    tone: "idle",
    states: ["backlog"],
    filter: (t) => t.readiness === "ready",
    empty: "Nothing ready. Tree tasks whose needs are completed in manual mode, and tree roots, wait here for Start.",
    hideEmpty: true,
  },
  {
    key: "trees",
    title: "Trees",
    stat: "Trees",
    short: "trees",
    tone: "plain",
    states: [],
    empty: "No active trees.",
    noStat: true,
    hideEmpty: true,
  },
  {
    key: "running",
    title: "Running",
    stat: "Running",
    short: "run",
    tone: "run",
    states: ["launching", "running"],
    empty: "Nothing running. Start a task from the backlog.",
  },
  {
    key: "failed",
    title: "Failed / interrupted",
    stat: "Failed",
    short: "fail",
    tone: "bad",
    states: ["failed", "interrupted"],
    empty: "No failed or interrupted tasks.",
    hideEmpty: true,
  },
  {
    key: "backlog",
    title: "Backlog",
    stat: "Backlog",
    short: "backlog",
    tone: "idle",
    states: ["backlog"],
    // Ready tasks count (and list) under Ready only, so the strip partitions backlog.
    filter: (t) => t.startAt === null && t.readiness !== "ready",
    empty: "Backlog is empty. Add one with New task.",
  },
  {
    key: "scheduled",
    title: "Scheduled · next 24h",
    stat: "Scheduled",
    short: "sched",
    tone: "idle",
    states: ["backlog"],
    filter: (t) => t.startAt !== null,
    empty: "Nothing scheduled in the next 24 hours. Pick At time or Repeat under Start in New task.",
    noStat: true,
  },
  {
    key: "settled",
    title: "Completed · last 7 days",
    stat: "Completed",
    short: "settled",
    tone: "ok",
    states: ["settled"],
    empty: "Nothing completed in the last 7 days.",
  },
];

// Settled tasks come from their own windowed query (SETTLED_DAYS); the rest are current totals.
const LIVE_STATES = GROUPS.flatMap((g) => g.states);
const SETTLED_DAYS = 7;

/** Start of the settled window, floored to the hour so the query key stays stable between renders. */
function settledSince(): string {
  const d = new Date(Date.now() - SETTLED_DAYS * 24 * 3600 * 1000);
  d.setMinutes(0, 0, 0);
  return d.toISOString();
}
const ACTIVE_STATES: readonly TaskState[] = ["launching", "running", "needs_input"];
const STAT_GROUPS = GROUPS.filter((g) => g.noStat !== true);
/** Per-device collapsed groups (`GroupFold`). */
const FOLD_KEY = "stage-dashboard-groups";

/** Scheduled group: timed backlog tasks and due schedules interleaved, soonest first. */
type ScheduledItem = { kind: "task"; task: Task; at: number } | { kind: "schedule"; schedule: Schedule; at: number };

const ms = (iso: string | null) => (iso === null ? Number.POSITIVE_INFINITY : new Date(iso).getTime());

function mergeScheduled(tasks: readonly Task[], schedules: readonly Schedule[]): ScheduledItem[] {
  return [
    ...tasks.map((task): ScheduledItem => ({ kind: "task", task, at: ms(task.startAt) })),
    ...schedules.map((schedule): ScheduledItem => ({ kind: "schedule", schedule, at: ms(schedule.nextRunAt) })),
  ].toSorted((a, b) => a.at - b.at);
}

function ScheduledRows({ items, emptyText }: { items: ScheduledItem[]; emptyText: string }) {
  if (items.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {items.map((item) => (
        <li key={item.kind === "task" ? `task-${item.task.id}` : `schedule-${item.schedule.id}`} className="border-b border-line last:border-b-0">
          {item.kind === "task" ? <TaskRow task={item.task} showProject /> : <ScheduleRow schedule={item.schedule} showProject actions="run" />}
        </li>
      ))}
    </ul>
  );
}

const parseGroup = (v: string | null): GroupKey | null => GROUPS.find((g) => g.key === v)?.key ?? null;

function NeedsOpenLink({ tasks }: { tasks: Task[] }) {
  // App links open one thread, so only a lone task gets one.
  const destination = useSessionDestination();
  const href = tasks.length === 1 ? (tasks[0] === undefined ? null : destination(tasks[0])) : null;
  if (href === null || !tasks[0]) return null;
  return (
    <AppButton href={href} small variant="ghost">
      Open in {HARNESS_NAME[tasks[0].harness]}
    </AppButton>
  );
}

/** A group with nothing to list: its count, and why it is empty (its members may be rolled up into tree rows). */
interface EmptyGroup {
  group: Group;
  count: number;
  text: string;
}

function EmptyGroups({ groups }: { groups: EmptyGroup[] }) {
  return (
    <ul className="border border-line bg-surface md:grid md:grid-cols-[max-content_minmax(0,1fr)]">
      {groups.map(({ group: g, count, text }) => (
        <li
          key={g.key}
          className="flex min-w-0 flex-col gap-0.5 border-b border-line px-3 py-2 last:border-b-0 md:col-span-2 md:grid md:grid-cols-subgrid md:items-baseline md:gap-x-6"
        >
          <span className="label-caps inline-flex items-center gap-2 text-fg">
            <span aria-hidden className="size-1.5 bg-line-strong" />
            {g.title}
            <span className="tabular text-muted">{count}</span>
          </span>
          <span className="min-w-0 text-small text-muted">{text}</span>
        </li>
      ))}
    </ul>
  );
}

function StatStrip({
  counts,
  selected,
  onToggle,
}: {
  counts: Record<GroupKey, number>;
  selected: GroupKey | null;
  onToggle: (key: GroupKey) => void;
}) {
  return (
    <div role="group" aria-label="Filter by state" className="mb-4 flex border border-line bg-surface">
      {STAT_GROUPS.map((g) => {
        const on = selected === g.key;
        const n = counts[g.key];
        return (
          <button
            key={g.key}
            type="button"
            aria-pressed={on}
            title={g.stat}
            onClick={() => onToggle(g.key)}
            className={cx(
              toneClass(g.tone),
              "flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 border-r border-line px-1 py-1.5 whitespace-nowrap max-lg:last-of-type:border-r-0 transition-colors duration-150 lg:flex-none lg:flex-row lg:justify-start lg:gap-2 lg:px-3",
              // Phone fits six cells; Settled stays reachable from its collapsible group.
              g.key === "settled" && "max-md:hidden",
              g.key === "backlog" && "max-md:border-r-0",
              on ? "bg-[color-mix(in_srgb,var(--c)_10%,transparent)] shadow-[inset_0_-2px_var(--c)]" : "hover:bg-panel",
            )}
          >
            <span className="flex items-center gap-1.5">
              {n > 0 ? <span aria-hidden className="size-1.5 bg-[var(--c)]" /> : null}
              <span className={cx("tabular text-title font-bold", on ? "text-[var(--cf)]" : n === 0 ? "text-muted" : "text-fg")}>
                {n}
              </span>
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

/** Planning remains available before pairing and during connection failures. */
export function DashboardPage() {
  const t3 = useT3Status();
  const openCode = useOpenCodeStatus();
  const harnesses = useHarnesses();
  // The server's view (a token, or a saved/env T3 URL), as the top-bar control uses: a disconnected T3 stays quiet.
  const t3Configured = harnesses.activeConnection("t3")?.configured ?? false;
  return (
    <>
      {t3Configured ? <T3Alert status={t3.data} /> : openCode.data?.state === "unconfigured" ? <p className="mb-3 text-small"><Link className="underline" to="/settings">Connect a harness</Link> to start tasks in T3 or OpenCode.</p> : null}
      {openCode.data?.state === "unavailable" ? <p className="mb-3 text-small">OpenCode is unavailable. <Link className="underline" to="/settings">Check connection</Link></p> : null}
      {openCode.data?.state === "rejected" ? <p className="mb-3 text-small">OpenCode no longer accepts Stage's sign-in. <Link className="underline" to="/settings">Reconnect</Link></p> : null}
      <CompletionLinger>
        <Dashboard />
      </CompletionLinger>
    </>
  );
}

function Dashboard() {
  const [params, setParams] = useSearchParams();
  const project = params.get("project") ?? "";
  const selected = parseGroup(params.get("state"));
  const projects = useProjects();
  const projectQuery = project === "" ? {} : { project: Number(project) };
  const tasks = useTasks({ state: LIVE_STATES, ...projectQuery });
  const [since] = useState(settledSince);
  const settled = useTasks({ settledFrom: since, ...projectQuery });
  const { storage } = usePlatform();
  const [fold, setFold] = useState(() => parseFold(storage.get(FOLD_KEY)));
  const all = [...new Map([...(tasks.data?.tasks ?? []), ...(settled.data?.tasks ?? [])].map((t) => [t.id, t])).values()];
  // Active trees, plus trees completed inside the settled window; members roll up into their tree's row.
  const trees = useTrees({ settledFrom: since, ...projectQuery });
  // Rows completed here linger in Awaiting review (completed chip, Undo) before moving to Completed.
  const { lingering } = useCompletionLinger();
  const split = splitTreeMembers(all, trees.data?.trees ?? [], lingering);
  const dueSchedules = useSchedules({ dueWithin: "24h", ...projectQuery });
  const due = dueSchedules.data?.schedules ?? [];
  const inGroup = (g: Group) => (t: Task) =>
    g.key === "settled"
      ? completedSince(t, since, lingering)
      : g.key === "done"
        ? awaitingReview(t, lingering)
        : (g.key === "needs" || g.key === "running" || t.acceptanceId === null) && g.states.includes(t.state) && (g.filter?.(t) ?? true);
  // Stat counts include tree members; rows leave out members rolled up into a tree row.
  const byGroup = (g: Group) => all.filter(inGroup(g));
  const rowsOf = (g: Group) => split.rows.filter(inGroup(g));
  const treeRows = (g: Group): TreeSummary[] => (g.key === "trees" ? split.active : g.key === "settled" ? split.settled : []);
  const groupCount = (g: Group) =>
    g.key === "trees" ? split.active.length : byGroup(g).length + (g.key === "scheduled" ? due.length : 0);
  /** Rows the group lists (tasks, external threads, schedules, tree rows). */
  const listed = (g: Group) =>
    rowsOf(g).length + (g.key === "scheduled" ? due.length : 0) + treeRows(g).length;
  // Complete all covers the listed review rows still to complete (not lingering ones, not rows rolled into trees).
  const completeAll = useCompleteAll(split.rows.filter((t) => !lingering.has(t.id) && awaitingReview(t, lingering)));
  /** Members counted here but shown in a tree row. */
  const inTrees = (g: Group) => (g.key === "trees" || g.key === "ready" ? 0 : split.rolledUp.filter(inGroup(g)).length);

  const counts: Record<GroupKey, number> = {
    needs: 0,
    trees: 0,
    running: 0,
    done: 0,
    failed: 0,
    ready: 0,
    backlog: 0,
    scheduled: 0,
    settled: 0,
  };
  for (const g of GROUPS) counts[g.key] = groupCount(g);
  const active = all.filter((t) => ACTIVE_STATES.includes(t.state)).length;
  const projectCount = projects.data?.projects.length;

  function setParam(key: string, value: string | null) {
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value === null || value === "") next.delete(key);
      else next.set(key, value);
      return next;
    });
  }

  // Groups in order; consecutive groups with nothing to list (unfiltered view) collapse into one ruled list.
  const blocks: (Group | EmptyGroup[])[] = [];
  for (const g of GROUPS) {
    if (selected !== null && selected !== g.key) continue;
    const empty = listed(g) === 0;
    if (selected === null && empty && groupCount(g) === 0 && g.hideEmpty === true) continue;
    const prev = blocks.at(-1);
    if (selected === null && empty) {
      const hidden = inTrees(g);
      const entry: EmptyGroup = {
        group: g,
        count: groupCount(g),
        text: hidden === 0 ? g.empty : `${hidden} in tree${hidden === 1 ? "" : "s"}, shown in the Trees group.`,
      };
      if (Array.isArray(prev)) prev.push(entry);
      else blocks.push([entry]);
    } else blocks.push(g);
  }

  const meta = [
    projectCount === undefined ? null : `${projectCount} project${projectCount === 1 ? "" : "s"}`,
    tasks.data === undefined ? null : `${active} active`,
  ]
    .filter((s) => s !== null)
    .join(" · ");

  return (
    <>
      <PageHeader
        title="Dashboard"
        {...(meta === "" ? {} : { meta })}
        actions={<><TrackSessionButton /><ProjectSelect value={project} onChange={(v) => setParam("project", v)} /></>}
      />
      <QueryState isPending={tasks.isPending || settled.isPending} error={tasks.error ?? settled.error}>
        {trees.error === null ? null : (
          <div className="mb-3">
            <ErrorText error={trees.error} />
          </div>
        )}
        <StatStrip
          counts={counts}
          selected={selected}
          onToggle={(key) => setParam("state", selected === key ? null : key)}
        />
        <div className="flex flex-col gap-3.5">
          {blocks.map((block) => {
            if (Array.isArray(block)) return <EmptyGroups key={block.map((e) => e.group.key).join("-")} groups={block} />;
            const g = block;
            const list = rowsOf(g);
            const hidden = inTrees(g);
            // Needs input reopens on its own when a task starts needing input after it was collapsed.
            const episodes = g.key === "needs" ? list.map(needsEpisode) : undefined;
            // Filtering to a group always shows its rows.
            const foldable = selected !== g.key;
            const collapsed = foldable && isFolded(fold, g.key, episodes);
            const toggle = () => {
              const next = toggleFold(fold, g.key, episodes);
              setFold(next);
              storage.set(FOLD_KEY, JSON.stringify(next));
            };
            // self-baseline: flex baseline comes from the text, not the square/chevron (no baseline → bottom edge),
            // so Panel's cap-trimmed h2 centers the caps.
            const label = (
              <span className={cx(toneClass(g.tone), "inline-flex items-center gap-2 self-baseline")}>
                <span aria-hidden className="size-1.5 bg-[var(--c)]" />
                <span className="self-baseline">{g.title}</span>
              </span>
            );
            return (
              <Panel
                key={g.key}
                flush
                collapsed={collapsed}
                title={
                  foldable ? (
                    <button
                      type="button"
                      aria-expanded={!collapsed}
                      onClick={toggle}
                      // The whole header bar toggles (actions stay above it).
                      className="group/fold label-caps inline-flex items-center gap-1.5 text-fg after:absolute after:inset-0"
                    >
                      <span aria-hidden className={cx("inline-flex text-muted transition-[color,transform] duration-150 group-hover/fold:text-fg", collapsed && "-rotate-90")}>
                        <IconChevronDown size={12} />
                      </span>
                      {label}
                    </button>
                  ) : (
                    label
                  )
                }
                meta={
                  <span className="tabular">
                    {groupCount(g)}
                    {hidden === 0 ? null : ` · ${hidden} in trees`}
                  </span>
                }
                {...(collapsed ? {} : g.key === "needs" && list.length > 0 ? { actions: <NeedsOpenLink tasks={list} /> } : {})}
                {...(collapsed ? {} : g.key === "done" ? { actions: <CompleteAllActions state={completeAll} /> } : {})}
              >
                {g.key === "settled" ? (
                  <SettledRows items={mergeSettled(list, split.settled)} emptyText={g.empty} />
                ) : g.key === "trees" ? (
                  <ul>
                    {split.active.map((t) => (
                      <li key={t.id} className="border-b border-line last:border-b-0">
                        <TreeRow tree={t} />
                      </li>
                    ))}
                  </ul>
                ) : g.key === "scheduled" ? (
                  <>
                    <ScheduledRows items={mergeScheduled(list, due)} emptyText={g.empty} />
                    {dueSchedules.error === null ? null : (
                      <div className="border-t border-line p-3">
                        <ErrorText error={dueSchedules.error} />
                      </div>
                    )}
                  </>
                ) : (
                  <>
                    {g.key === "done" ? <CompleteAllFailures state={completeAll} /> : null}
                    <TaskRows
                      tasks={list}
                      emptyText={hidden === 0 ? g.empty : `All ${hidden} are in trees, shown in the Trees group.`}
                      showProject
                      showTree
                    />
                  </>
                )}
              </Panel>
            );
          })}
        </div>
      </QueryState>
    </>
  );
}
